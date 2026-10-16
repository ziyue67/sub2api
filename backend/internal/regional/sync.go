package regional

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const MaxSyncBytes = 8 << 20

var ErrSyncUnavailable = errors.New("regional sync unavailable")

// Codec uses region-specific AES-256-GCM keys. Keys authenticate both peers;
// they do not prove which peer produced a packet. Provision a distinct key for
// each trusted region. Keep old keys until persisted packets have been rotated.
type Codec struct{ keys map[string]cipher.AEAD }
type packet struct {
	KeyID string `json:"key_id"`
	Nonce []byte `json:"nonce"`
	Data  []byte `json:"data"`
}

func NewCodec(keys map[string][]byte) (*Codec, error) {
	if len(keys) == 0 {
		return nil, errors.New("sync keys required")
	}
	c := &Codec{keys: make(map[string]cipher.AEAD, len(keys))}
	for id, key := range keys {
		if !identifier.MatchString(id) || len(key) != 32 {
			return nil, errors.New("invalid sync key")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		c.keys[id] = aead
	}
	return c, nil
}
func (c *Codec) Seal(keyID string, s Snapshot) ([]byte, error) {
	aead := c.keys[keyID]
	if aead == nil {
		return nil, errors.New("unknown sync key")
	}
	plain, err := json.Marshal(s)
	if err != nil {
		return nil, errors.New("invalid sync snapshot")
	}
	if len(plain) > MaxSyncBytes/2 {
		return nil, errors.New("sync snapshot too large")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	p := packet{KeyID: keyID, Nonce: nonce, Data: aead.Seal(nil, nonce, plain, []byte("regional-sync-v1:"+keyID))}
	return json.Marshal(p)
}
func (c *Codec) Open(wire []byte) (Snapshot, error) {
	var p packet
	if len(wire) > MaxSyncBytes || json.Unmarshal(wire, &p) != nil {
		return Snapshot{}, errors.New("invalid sync packet")
	}
	aead := c.keys[p.KeyID]
	if aead == nil || len(p.Nonce) != aead.NonceSize() {
		return Snapshot{}, errors.New("invalid sync authentication")
	}
	plain, err := aead.Open(nil, p.Nonce, p.Data, []byte("regional-sync-v1:"+p.KeyID))
	if err != nil {
		return Snapshot{}, errors.New("invalid sync authentication")
	}
	if len(plain) > MaxSyncBytes/2 {
		return Snapshot{}, errors.New("sync snapshot too large")
	}
	var s Snapshot
	if json.Unmarshal(plain, &s) != nil {
		return Snapshot{}, errors.New("invalid sync snapshot")
	}
	return s, nil
}

// PayloadValidator must validate the full domain document, including deletions,
// ownership and cross-object references. It must be deterministic and side-effect
// free. Authentication alone never makes arbitrary JSON a valid domain snapshot.
type PayloadValidator func([]byte) error

// Replica holds one (region, kind) stream. Each replica exclusively owns its
// state file; do not share a file across processes/Pods. It persists the encrypted
// snapshot and revision together before making it visible. Missing files require
// explicit bootstrap; normal restarts fail closed if state was lost or corrupted.
type Replica struct {
	publishMu sync.Mutex
	mu        sync.RWMutex
	cfg       Config
	kind      SnapshotKind
	codec     *Codec
	path      string
	validate  PayloadValidator
	now       func() time.Time
	current   *Snapshot
	wire      []byte
	failed    bool
}

func OpenReplica(path string, cfg Config, kind SnapshotKind, codec *Codec, validate PayloadValidator, bootstrap bool) (*Replica, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if path == "" || codec == nil || validate == nil || (kind != SnapshotAuth && kind != SnapshotScheduler) {
		return nil, errors.New("invalid sync replica configuration")
	}
	r := &Replica{cfg: cfg, kind: kind, codec: codec, path: path, validate: validate, now: time.Now}
	wire, err := readBoundedFile(path)
	if errors.Is(err, os.ErrNotExist) && bootstrap {
		return r, nil
	}
	if err != nil {
		return nil, errors.New("cannot restore regional sync state")
	}
	s, err := codec.Open(wire)
	if err != nil {
		return nil, err
	}
	// Validate structure at issuance: expired state must still retain its revision
	// floor after a restart. Current() checks freshness before serving any bytes.
	if err = s.Validate(cfg, kind, 0, s.IssuedAt); err != nil {
		return nil, err
	}
	if err = validate(bytes.Clone(s.Payload)); err != nil {
		return nil, errors.New("invalid persisted sync payload")
	}
	r.current = &s
	r.wire = bytes.Clone(wire)
	return r, nil
}
func readBoundedFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return readBounded(f)
}
func readBounded(reader io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(reader, MaxSyncBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxSyncBytes {
		return nil, errors.New("sync packet too large")
	}
	return b, nil
}

// Accept atomically replaces the entire stream, so an absent object is revoked.
// Identical revision retries are idempotent but never refresh the expiry. Different
// contents at an accepted revision, or earlier revisions, are rejected.
func (r *Replica) Accept(ctx context.Context, wire []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	wire = bytes.Clone(wire)
	s, err := r.codec.Open(wire)
	if err != nil {
		return err
	}
	if err = s.Validate(r.cfg, r.kind, 0, r.now()); err != nil {
		return err
	}
	if err = r.validate(bytes.Clone(s.Payload)); err != nil {
		return errors.New("invalid sync payload")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed {
		return ErrSyncUnavailable
	}
	if r.current != nil {
		if s.Revision < r.current.Revision {
			return errors.New("sync revision rollback")
		}
		if s.Revision == r.current.Revision {
			a, _ := json.Marshal(s)
			b, _ := json.Marshal(r.current)
			if !bytes.Equal(a, b) {
				return errors.New("sync revision conflict")
			}
			return nil
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = s.Validate(r.cfg, r.kind, 0, r.now()); err != nil {
		return err
	}
	if err = writeAtomic(r.path, wire); err != nil {
		// A rename may have succeeded before directory fsync failed. Stop admission
		// until reopening the on-disk state; never serve an older revoked view.
		r.failed = true
		return errors.New("cannot persist regional sync state")
	}
	r.current = &s
	r.wire = wire
	return nil
}
func writeAtomic(path string, wire []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".regional-sync-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = f.Write(wire); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
func (r *Replica) Current() (Snapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.failed || r.current == nil {
		return Snapshot{}, ErrSyncUnavailable
	}
	s := *r.current
	if err := s.Validate(r.cfg, r.kind, 0, r.now()); err != nil {
		return Snapshot{}, ErrSyncUnavailable
	}
	s.Payload = bytes.Clone(s.Payload)
	return s, nil
}

// Publish creates an encrypted full snapshot at a caller-supplied durable
// revision. Database/export code must allocate revisions and capture a consistent
// region-filtered view; this helper never reads the production database.
func (r *Replica) Publish(ctx context.Context, keyID string, revision uint64, issued, expires time.Time, payload []byte) error {
	sum := sha256.Sum256(payload)
	s := Snapshot{SchemaVersion: SchemaVersion, RegionID: r.cfg.RegionID, Kind: r.kind, Revision: revision, IssuedAt: issued, ExpiresAt: expires, Payload: bytes.Clone(payload), PayloadSHA256: hex.EncodeToString(sum[:])}
	wire, err := r.codec.Seal(keyID, s)
	if err != nil {
		return err
	}
	return r.Accept(ctx, wire)
}

// Handler must be mounted behind TLS and client authentication (e.g. mTLS).
// It serves encrypted full replacements only, never redirects or plaintext.
// It is deliberately not registered in the application's public router.
func (r *Replica) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if req.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		r.mu.RLock()
		if r.failed || r.current == nil || r.current.Validate(r.cfg, r.kind, 0, r.now()) != nil {
			r.mu.RUnlock()
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		wire := bytes.Clone(r.wire)
		r.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(wire)
	})
}

// Pull performs one bounded synchronization attempt. Callers schedule retries
// with backoff separately; normal model requests must never invoke Pull.
// The supplied transport configures TLS/mTLS, not redirects or an unlimited timeout.
func (r *Replica) Pull(ctx context.Context, endpoint string, transport http.RoundTripper) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("sync endpoint requires HTTPS without credentials, query or fragment")
	}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errors.New("invalid sync request")
	}
	// Endpoint is trusted deployment configuration, never user input; HTTPS only, no redirects.
	resp, err := client.Do(req) //nolint:gosec // G704: administrative sync endpoint with caller-configured trusted TLS transport.
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("sync request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errors.New("sync endpoint not ready")
	}
	wire, err := readBounded(resp.Body)
	if err != nil {
		return err
	}
	return r.Accept(ctx, wire)
}

// PublishNext allocates the next revision from the persisted stream floor, even
// when the previous view expired. Only one producer process may own the file.
// A failed publish may consume a revision on disk; reopen before further use.
func (r *Replica) PublishNext(ctx context.Context, keyID string, lifetime time.Duration, payload []byte) error {
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	if lifetime <= 0 || lifetime > time.Duration(r.cfg.MaxSnapshotAgeSeconds)*time.Second {
		return errors.New("invalid publication lifetime")
	}
	r.mu.RLock()
	revision := uint64(1)
	if r.current != nil {
		revision = r.current.Revision + 1
	}
	failed := r.failed
	r.mu.RUnlock()
	if failed || revision == 0 {
		return ErrSyncUnavailable
	}
	now := r.now()
	return r.Publish(ctx, keyID, revision, now, now.Add(lifetime), payload)
}
