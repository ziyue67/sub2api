package repository

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/regional/control"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/regional/slots"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// The provisioned membership describes the complete primary deployment epoch.
// Revoking its old Redis identity is required before any regional grant.
type RegionalConcurrencyRuntimeConfig struct {
	Cutover          *RegionalCutoverConfig
	Mode             string
	Region           string
	Instance         string
	Epoch            string
	PrimaryInstances []string
	LegacyRedisUser  string
	Listen           string
	Endpoint         string
	CAFile           string
	CertFile         string
	KeyFile          string
	ServerPin        string
	Scope            control.Scope
	Targets          []RegionalSlotTarget
	LifetimeSeconds  int
}

func readRegionalConcurrencyConfig(path string) (RegionalConcurrencyRuntimeConfig, error) {
	var c RegionalConcurrencyRuntimeConfig
	f, err := os.Open(path)
	if err != nil {
		return c, errors.New("cannot open regional concurrency config")
	}
	defer func() { _ = f.Close() }()
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, errors.New("invalid regional concurrency config")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("trailing regional concurrency config")
	}
	if c.Mode != "primary" && c.Mode != "regional" {
		return c, errors.New("regional concurrency mode must be primary or regional")
	}
	if c.Instance == "" || len(c.Instance) > 128 || c.Epoch == "" || len(c.Epoch) > 128 {
		return c, errors.New("regional concurrency instance and epoch required")
	}
	if c.Mode == "regional" && c.Region == "" {
		return c, errors.New("regional concurrency region required")
	}
	if c.Mode == "primary" && (len(c.PrimaryInstances) == 0 || c.LegacyRedisUser == "") {
		return c, errors.New("regional grants require expected primary instances and revoked legacy Redis user")
	}
	seen := map[string]bool{}
	for _, id := range c.PrimaryInstances {
		if id == "" || len(id) > 128 || seen[id] {
			return c, errors.New("invalid primary membership")
		}
		seen[id] = true
	}
	if c.Mode == "primary" && !seen[c.Instance] {
		return c, errors.New("current primary absent from epoch membership")
	}
	return c, nil
}
func provideRegionalConcurrencyCache(base service.ConcurrencyCache, rdb *redis.Client, db *sql.DB, cfg *config.Config) (service.ConcurrencyCache, error) {
	c, err := readRegionalConcurrencyConfig(cfg.Runtime.RegionalConcurrencyFile)
	if err != nil {
		return nil, err
	}
	if c.Mode == "primary" && cfg.Runtime.Role == config.RuntimeRoleGateway {
		return nil, errors.New("primary concurrency authority requires full runtime role")
	}
	if c.Mode == "regional" && c.Endpoint == "" {
		return nil, errors.New("regional concurrency requires broker endpoint")
	}
	if db == nil {
		return nil, errors.New("regional concurrency database required")
	}
	var p *slots.Primary
	var l *slots.Local
	if c.Mode == "primary" {
		p = NewRegionalSlotAuthority(db)
		p.GrantGuard = regionalGrantGuard(rdb, c)
	} else {
		if cfg.Runtime.Role != config.RuntimeRoleGateway || cfg.Runtime.RegionalAuthFile == "" || cfg.Runtime.RegionalSchedulerFile == "" {
			return nil, errors.New("regional slots require gateway local authentication and scheduler")
		}
		l = &slots.Local{DB: db, Region: c.Region}
	}
	value, err := NewRegionalConcurrencyCache(base, p, l)
	if err != nil {
		return nil, err
	}
	cache, ok := value.(*regionalConcurrencyCache)
	if !ok {
		return nil, errors.New("regional concurrency provider type mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = db.ExecContext(ctx, "INSERT INTO regional_runtime_members(epoch,instance_id,role) VALUES($1,$2,$3) ON CONFLICT(epoch,instance_id) DO UPDATE SET heartbeat_at=clock_timestamp(),role=EXCLUDED.role", c.Epoch, c.Instance, c.Mode); err != nil {
		return nil, err
	}
	cache.runtimeConfig = &c
	if err := cache.prepareControl(); err != nil {
		return nil, err
	}
	return cache, nil
}

// TTL expiry does not prove an old request stopped. Require identity revocation,
// disconnection and complete drained namespaces before issuing regional grants.
func regionalGrantGuard(rdb *redis.Client, c RegionalConcurrencyRuntimeConfig) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		if err := verifyRegionalCutover(ctx, tx, c); err != nil {
			return err
		}
		return regionalLegacyGuard(rdb, c)(ctx, tx)
	}
}
func regionalLegacyGuard(rdb *redis.Client, c RegionalConcurrencyRuntimeConfig) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		user, err := rdb.Do(ctx, "ACL", "GETUSER", c.LegacyRedisUser).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return errors.New("legacy Redis revocation cannot be verified")
		}
		if user != nil {
			off := false
			switch data := user.(type) {
			case []any:
				for i := 0; i+1 < len(data); i += 2 {
					if data[i] == "flags" {
						if flags, ok := data[i+1].([]any); ok {
							for _, flag := range flags {
								if flag == "off" {
									off = true
								}
							}
						}
					}
				}
			case map[any]any:
				if flags, ok := data["flags"].([]any); ok {
					for _, flag := range flags {
						if flag == "off" {
							off = true
						}
					}
				}
			}
			if !off {
				return errors.New("legacy Redis admission identity still enabled or unrecognized")
			}
		}
		clients, err := rdb.ClientList(ctx).Result()
		if err != nil {
			return err
		}
		for _, line := range strings.Split(clients, string(rune(10))) {
			for _, field := range strings.Fields(line) {
				if field == "user="+c.LegacyRedisUser {
					return errors.New("legacy Redis clients remain connected")
				}
			}
		}
		for _, pattern := range []string{accountSlotKeyPrefix + "*", userSlotKeyPrefix + "*", apiKeySlotKeyPrefix + "*", liveAccountSlotKeyPrefix + "*", liveUserSlotKeyPrefix + "*", liveAPIKeySlotKeyPrefix + "*"} {
			var cursor uint64
			for {
				keys, next, err := rdb.Scan(ctx, cursor, pattern, 100).Result()
				if err != nil {
					return err
				}
				for _, key := range keys {
					n, err := rdb.ZCard(ctx, key).Result()
					if err != nil {
						return err
					}
					if n != 0 {
						return errors.New("legacy concurrency namespace is not drained")
					}
				}
				cursor = next
				if cursor == 0 {
					break
				}
			}
		}
		for _, id := range c.PrimaryInstances {
			var ready bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_runtime_members WHERE epoch=$1 AND instance_id=$2 AND role='primary' AND heartbeat_at>clock_timestamp()-interval '30 seconds')", c.Epoch, id).Scan(&ready); err != nil {
				return err
			}
			if !ready {
				return errors.New("primary epoch membership is incomplete or stale")
			}
		}
		return nil
	}
}

type RegionalSlotTarget struct {
	Resource slots.Resource
	Capacity int
}

func (c *regionalConcurrencyCache) RunRegionalRuntime(ctx context.Context) {
	if c.runtimeConfig == nil {
		return
	}
	if c.controlServer != nil {
		go func() {
			err := c.controlServer.Serve(tls.NewListener(c.controlListener, c.controlServer.TLSConfig))
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("regional control listener stopped")
				c.runtimeFailed.Store(true)
			}
		}()
	}
	defer func() {
		if c.controlServer != nil {
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = c.controlServer.Shutdown(shutdown)
		}
		if c.brokerClient != nil {
			c.brokerClient.Close()
		}
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		cycle, cancel := context.WithTimeout(ctx, 20*time.Second)
		if c.broker != nil {
			for _, target := range c.runtimeConfig.Targets {
				if err := c.broker.EnsureSlots(cycle, target.Resource, target.Capacity, time.Duration(c.runtimeConfig.LifetimeSeconds)*time.Second); err != nil {
					slog.Warn("regional slot target not ready")
				}
			}
			if err := c.broker.RunOnce(cycle); err != nil {
				slog.Warn("regional broker has unconfirmed operations")
			}
		}
		if c.runtimeFailed.Load() {
			cancel()
			return
		}
		_, _ = c.database().ExecContext(cycle, "UPDATE regional_runtime_members SET heartbeat_at=clock_timestamp() WHERE epoch=$1 AND instance_id=$2", c.runtimeConfig.Epoch, c.runtimeConfig.Instance)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (c *regionalConcurrencyCache) prepareControl() error {
	cfg := c.runtimeConfig
	if cfg.Listen == "" && cfg.Endpoint == "" {
		return nil
	}
	pem, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return errors.New("regional control CA unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return errors.New("invalid regional control CA")
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return errors.New("regional control identity unavailable")
	}
	if c.primary != nil {
		if cfg.Listen == "" || cfg.Endpoint != "" {
			return errors.New("primary requires control listen address")
		}
		handler, err := control.NewSlotsHandler(cfg.Scope, c.primary)
		if err != nil {
			return err
		}
		listener, err := net.Listen("tcp", cfg.Listen)
		if err != nil {
			return errors.New("regional control listener unavailable")
		}
		c.controlListener = listener
		slotsHandler := handler
		handler = c.withFundsControl(slotsHandler)
		c.controlServer = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}}
	} else {
		if cfg.Endpoint == "" || cfg.Listen != "" || len(cfg.Targets) == 0 || cfg.LifetimeSeconds < 30 || cfg.LifetimeSeconds > 3600 {
			return errors.New("regional broker endpoint and bounded targets required")
		}
		seen := map[slots.Resource]bool{}
		for _, target := range cfg.Targets {
			if err := target.Resource.Validate(); err != nil {
				return err
			}
			if target.Capacity <= 0 || seen[target.Resource] {
				return errors.New("invalid regional broker target")
			}
			seen[target.Resource] = true
		}
		client, err := control.NewClient(cfg.Endpoint, roots, cert, cfg.ServerPin)
		if err != nil {
			return err
		}
		c.brokerClient = client
		c.broker = &control.Broker{DB: c.local.DB, Slots: c.local, Remote: client}
	}
	return nil
}
