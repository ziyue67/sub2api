package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/Wei-Shaw/sub2api/internal/regional/statelock"
)

// RegionalSchedulerAuthority serializes publication and commits the encrypted
// packet and revision together. All repositories passed to build MUST use the
// same repeatable-read transaction, including account edges and raw SQL.
type RegionalSchedulerAuthority interface {
	Restore(context.Context, string, bool) ([]byte, error)
	Publish(context.Context, string, func(context.Context, uint64, AccountRepository, GroupRepository) ([]byte, error)) ([]byte, error)
}

// RegionalSchedulerRuntimeConfig is read from a provisioned secret JSON file.
// One stream per process in this first runtime implementation. No credentials
// or account data are included in status output or logs.
type RegionalSchedulerRuntimeConfig struct {
	Kind                    regional.SnapshotKind `json:"snapshot_kind,omitempty"`
	AuthUserIDs             []int64               `json:"allowed_user_ids,omitempty"`
	AuthLookupKey           []byte                `json:"auth_lookup_key,omitempty"`
	Mode                    string                `json:"mode"`
	Policy                  regional.Config       `json:"policy"`
	Keys                    map[string][]byte     `json:"keys"`
	KeyID                   string                `json:"key_id"`
	AllowedIDs              []int64               `json:"allowed_account_ids"`
	Buckets                 []SchedulerBucket     `json:"buckets"`
	StateFile               string                `json:"state_file"`
	Bootstrap               bool                  `json:"bootstrap"`
	Endpoint                string                `json:"endpoint"`
	Listen                  string                `json:"listen"`
	CAFile                  string                `json:"ca_file"`
	CertFile                string                `json:"cert_file"`
	KeyFile                 string                `json:"key_file"`
	TrustedServerCertSHA256 string                `json:"trusted_server_cert_sha256,omitempty"`
	TrustedClientCertSHA256 []string              `json:"trusted_client_cert_sha256"`
	IntervalSeconds         int                   `json:"interval_seconds"`
	LifetimeSeconds         int                   `json:"lifetime_seconds"`
}

func readRegionalSchedulerConfig(path string, cfg *config.Config) (RegionalSchedulerRuntimeConfig, error) {
	var c RegionalSchedulerRuntimeConfig
	f, err := os.Open(path)
	if err != nil {
		return c, errors.New("cannot open regional scheduler config")
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return c, errors.New("regional scheduler config too large or unreadable")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return c, errors.New("invalid regional scheduler config")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("trailing regional scheduler config")
	}
	if err := c.validate(cfg); err != nil {
		return c, err
	}
	return c, nil
}
func (c RegionalSchedulerRuntimeConfig) validate(cfg *config.Config) error {
	if c.Kind != "" && c.Kind != regional.SnapshotScheduler && c.Kind != regional.SnapshotAuth {
		return errors.New("invalid regional snapshot kind")
	}
	if c.Kind == regional.SnapshotAuth {
		if len(c.AuthLookupKey) != 32 || len(c.AllowedIDs) != 0 || len(c.Buckets) != 0 {
			return errors.New("auth stream requires lookup key and no scheduler assignment")
		}
		seenUsers := map[int64]bool{}
		for _, id := range c.AuthUserIDs {
			if id <= 0 || seenUsers[id] {
				return errors.New("invalid regional user assignment")
			}
			seenUsers[id] = true
		}
	} else if len(c.AuthUserIDs) != 0 || len(c.AuthLookupKey) != 0 {
		return errors.New("auth assignment on scheduler stream")
	}
	if err := c.Policy.Validate(); err != nil {
		return err
	}
	if c.Mode != "publisher" && c.Mode != "subscriber" {
		return errors.New("regional mode must be publisher or subscriber")
	}
	if cfg == nil || (c.Mode == "subscriber" && cfg.Runtime.Role != config.RuntimeRoleGateway) || (c.Mode == "publisher" && !cfg.RunsBackgroundJobs()) {
		return errors.New("regional mode incompatible with runtime role")
	}
	if c.IntervalSeconds < 1 || c.IntervalSeconds > 300 || c.LifetimeSeconds <= c.IntervalSeconds || int64(c.LifetimeSeconds) > c.Policy.MaxSnapshotAgeSeconds {
		return errors.New("invalid regional scheduler interval or lifetime")
	}
	if c.Listen == "" || c.CAFile == "" || c.CertFile == "" || c.KeyFile == "" || len(c.Keys[c.KeyID]) != 32 || len(c.TrustedClientCertSHA256) == 0 {
		return errors.New("regional TLS and encryption configuration required")
	}
	for _, pin := range c.TrustedClientCertSHA256 {
		if !validCertificatePin(pin) {
			return errors.New("invalid trusted client certificate fingerprint")
		}
	}
	if c.Mode == "subscriber" && !validCertificatePin(c.TrustedServerCertSHA256) {
		return errors.New("subscriber requires publisher certificate fingerprint")
	}
	seen := map[int64]bool{}
	for _, id := range c.AllowedIDs {
		if id <= 0 || seen[id] {
			return errors.New("invalid regional account assignment")
		}
		seen[id] = true
	}
	doc := RegionalSchedulerDocument{Version: 1, Buckets: make([]RegionalSchedulerBucket, 0, len(c.Buckets))}
	for _, b := range c.Buckets {
		doc.Buckets = append(doc.Buckets, RegionalSchedulerBucket{Bucket: b, Accounts: []*Account{}})
	}
	payload, _ := json.Marshal(doc)
	if err := RegionalSchedulerValidator(c.AllowedIDs)(payload); err != nil {
		return err
	}
	if c.Mode == "subscriber" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || c.StateFile == "" {
			return errors.New("regional subscriber requires state file and HTTPS endpoint")
		}
	}
	return nil
}

func validCertificatePin(pin string) bool {
	if len(pin) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(pin)
	return err == nil && len(decoded) == sha256.Size
}
func verifyCertificatePin(certificates []*x509.Certificate, pin string) error {
	if len(certificates) == 0 || !validCertificatePin(pin) {
		return errors.New("untrusted regional peer certificate")
	}
	sum := sha256.Sum256(certificates[0].Raw)
	expected, _ := hex.DecodeString(pin)
	if subtle.ConstantTimeCompare(sum[:], expected) != 1 {
		return errors.New("untrusted regional peer certificate")
	}
	return nil
}
func verifyAnyCertificatePin(certificates []*x509.Certificate, pins []string) error {
	if len(certificates) == 0 {
		return errors.New("missing regional peer certificate")
	}
	sum := sha256.Sum256(certificates[0].Raw)
	for _, pin := range pins {
		expected, err := hex.DecodeString(pin)
		if err == nil && subtle.ConstantTimeCompare(sum[:], expected) == 1 {
			return nil
		}
	}
	return errors.New("untrusted regional peer certificate")
}

type regionalSchedulerStatus struct {
	Mode        string    `json:"mode"`
	Ready       bool      `json:"ready"`
	Attempts    uint64    `json:"attempts"`
	Failures    uint64    `json:"failures"`
	LastSuccess time.Time `json:"last_success"`
	Revision    uint64    `json:"revision,string"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type regionalSchedulerRuntime struct {
	cfg           RegionalSchedulerRuntimeConfig
	codec         *regional.Codec
	replica       *regional.Replica
	authority     RegionalSchedulerAuthority
	authAuthority RegionalAuthAuthority
	appConfig     *config.Config
	mu            sync.RWMutex
	wire          []byte
	status        regionalSchedulerStatus
	cancel        context.CancelFunc
	done          chan struct{}
	server        *http.Server
	transport     *http.Transport
	stateLock     *statelock.Lock
	stopOnce      sync.Once
}

func startRegionalSchedulerRuntime(cfg *config.Config, authority RegionalSchedulerAuthority) (*regionalSchedulerRuntime, error) {
	return startRegionalRuntime(cfg, authority, nil, cfg.Runtime.RegionalSchedulerFile, regional.SnapshotScheduler)
}
func startRegionalAuthRuntime(cfg *config.Config, authority RegionalAuthAuthority) (*regionalSchedulerRuntime, error) {
	return startRegionalRuntime(cfg, nil, authority, cfg.Runtime.RegionalAuthFile, regional.SnapshotAuth)
}
func (c RegionalSchedulerRuntimeConfig) snapshotKind() regional.SnapshotKind {
	if c.Kind == "" {
		return regional.SnapshotScheduler
	}
	return c.Kind
}
func (c RegionalSchedulerRuntimeConfig) validator() regional.PayloadValidator {
	if c.snapshotKind() == regional.SnapshotAuth {
		return RegionalAuthValidator(c.AuthUserIDs)
	}
	return RegionalSchedulerValidator(c.AllowedIDs)
}
func startRegionalRuntime(cfg *config.Config, authority RegionalSchedulerAuthority, authAuthority RegionalAuthAuthority, path string, kind regional.SnapshotKind) (*regionalSchedulerRuntime, error) {
	c, err := readRegionalSchedulerConfig(path, cfg)
	if err != nil {
		return nil, err
	}
	if c.snapshotKind() != kind {
		return nil, errors.New("regional runtime snapshot kind mismatch")
	}
	codec, err := regional.NewCodec(c.Keys)
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, errors.New("cannot load regional TLS identity")
	}
	ca, err := os.ReadFile(c.CAFile)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("cannot load regional TLS CA")
	}
	r := &regionalSchedulerRuntime{cfg: c, codec: codec, authority: authority, authAuthority: authAuthority, appConfig: cfg, done: make(chan struct{}), status: regionalSchedulerStatus{Mode: c.Mode}}
	r.transport = &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}, VerifyConnection: func(state tls.ConnectionState) error {
		return verifyCertificatePin(state.PeerCertificates, c.TrustedServerCertSHA256)
	}}}
	if c.Mode == "subscriber" {
		r.stateLock = statelock.New(c.StateFile + ".lock")
		locked, lockErr := r.stateLock.TryLock()
		if lockErr != nil || !locked {
			return nil, errors.New("regional subscriber state already owned or inaccessible")
		}
		r.replica, err = regional.OpenReplica(c.StateFile, c.Policy, kind, codec, c.validator(), c.Bootstrap)
		if err != nil {
			_ = r.stateLock.Unlock()
			return nil, err
		}
	} else {
		if (kind == regional.SnapshotScheduler && authority == nil) || (kind == regional.SnapshotAuth && authAuthority == nil) {
			return nil, errors.New("regional publisher authority required")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		var wire []byte
		var loadErr error
		if kind == regional.SnapshotAuth {
			wire, loadErr = authAuthority.Restore(ctx, c.Policy.RegionID, c.Bootstrap)
		} else {
			wire, loadErr = authority.Restore(ctx, c.Policy.RegionID, c.Bootstrap)
		}
		cancel()
		if loadErr != nil {
			return nil, errors.New("cannot restore regional publisher")
		}
		if len(wire) > 0 {
			// Keep expired state for observability; it is never served. The durable DB
			// revision remains authoritative even if assignments change on restart.
			snapshot, openErr := codec.Open(wire)
			if openErr != nil || snapshot.Validate(c.Policy, kind, 0, snapshot.IssuedAt) != nil {
				return nil, errors.New("invalid regional publisher state")
			}
			if err := c.validator()(snapshot.Payload); err != nil {
				return nil, errors.New("persisted publisher snapshot outside regional assignment")
			}
			r.wire = wire
		}
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		if r.stateLock != nil {
			_ = r.stateLock.Unlock()
		}
		return nil, errors.New("cannot bind regional listener")
	}
	mux := http.NewServeMux()
	route := "/internal/regional/" + string(kind)
	mux.HandleFunc("GET "+route+"/status", r.serveStatus)
	if c.Mode == "publisher" {
		mux.HandleFunc("GET "+route, r.serveSnapshot)
	}
	r.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, VerifyConnection: func(state tls.ConnectionState) error {
		return verifyAnyCertificatePin(state.PeerCertificates, c.TrustedClientCertSHA256)
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go func() {
		if err := r.server.Serve(tls.NewListener(listener, r.server.TLSConfig)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("regional snapshot listener stopped", "kind", kind)
			cancel()
		}
	}()
	go r.run(ctx)
	return r, nil
}

func (r *regionalSchedulerRuntime) tick(ctx context.Context) error {
	if r.replica != nil {
		return r.replica.Pull(ctx, r.cfg.Endpoint, r.transport)
	}
	var wire []byte
	var err error
	if r.cfg.snapshotKind() == regional.SnapshotAuth {
		wire, err = r.authAuthority.Publish(ctx, r.cfg.Policy.RegionID, func(ctx context.Context, revision uint64, source RegionalAuthSource) ([]byte, error) {
			payload, err := ExportRegionalAuth(ctx, source, r.cfg.AuthUserIDs, r.cfg.AuthLookupKey)
			if err != nil {
				return nil, err
			}
			return r.sealSnapshot(payload, revision)
		})
	} else {
		wire, err = r.authority.Publish(ctx, r.cfg.Policy.RegionID, func(ctx context.Context, revision uint64, accounts AccountRepository, groups GroupRepository) ([]byte, error) {
			exporter := NewSchedulerSnapshotService(nil, nil, accounts, groups, r.appConfig)
			payload, err := exporter.ExportRegionalScheduler(ctx, r.cfg.Buckets, r.cfg.AllowedIDs)
			if err != nil {
				return nil, err
			}
			return r.sealSnapshot(payload, revision)
		})
	}
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.wire = wire
	r.mu.Unlock()
	return nil
}
func (r *regionalSchedulerRuntime) sealSnapshot(payload []byte, revision uint64) ([]byte, error) {
	sum := sha256.Sum256(payload)
	now := time.Now()
	snapshot := regional.Snapshot{SchemaVersion: regional.SchemaVersion, RegionID: r.cfg.Policy.RegionID, Kind: r.cfg.snapshotKind(), Revision: revision, IssuedAt: now, ExpiresAt: now.Add(time.Duration(r.cfg.LifetimeSeconds) * time.Second), Payload: payload, PayloadSHA256: hex.EncodeToString(sum[:])}
	if err := snapshot.Validate(r.cfg.Policy, r.cfg.snapshotKind(), 0, now); err != nil {
		return nil, err
	}
	return r.codec.Seal(r.cfg.KeyID, snapshot)
}
func (r *regionalSchedulerRuntime) run(ctx context.Context) {
	defer close(r.done)
	interval := time.Duration(r.cfg.IntervalSeconds) * time.Second
	delay := interval
	for {
		attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := r.tick(attempt)
		cancel()
		r.mu.Lock()
		r.status.Attempts++
		if err != nil {
			r.status.Failures++
		} else {
			r.status.LastSuccess = time.Now()
		}
		r.mu.Unlock()
		if err != nil {
			delay = min(delay*2, 5*time.Minute)
			slog.Warn("regional snapshot synchronization failed", "kind", r.cfg.snapshotKind(), "mode", r.cfg.Mode, "region", r.cfg.Policy.RegionID)
		} else {
			delay = interval
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (r *regionalSchedulerRuntime) snapshot() (regional.Snapshot, []byte, error) {
	if r.replica != nil {
		s, err := r.replica.Current()
		return s, nil, err
	}
	r.mu.RLock()
	wire := append([]byte(nil), r.wire...)
	r.mu.RUnlock()
	s, err := r.codec.Open(wire)
	if err == nil {
		err = s.Validate(r.cfg.Policy, r.cfg.snapshotKind(), 0, time.Now())
	}
	return s, wire, err
}
func (r *regionalSchedulerRuntime) serveSnapshot(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, wire, err := r.snapshot()
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(wire)
}
func (r *regionalSchedulerRuntime) serveStatus(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	r.mu.RLock()
	status := r.status
	r.mu.RUnlock()
	s, _, err := r.snapshot()
	status.Ready = err == nil
	status.Revision = s.Revision
	status.ExpiresAt = s.ExpiresAt
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(status)
}
func (r *regionalSchedulerRuntime) stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		r.cancel()
		_ = r.server.Close()
		<-r.done
		r.transport.CloseIdleConnections()
		if r.stateLock != nil {
			_ = r.stateLock.Unlock()
		}
	})
}
