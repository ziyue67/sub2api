//go:build unit

package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/stretchr/testify/require"
)

type runtimeAuthorityStub struct {
	mu       sync.Mutex
	revision uint64
	wire     []byte
	fail     bool
}

func (a *runtimeAuthorityStub) Restore(context.Context, string, bool) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]byte(nil), a.wire...), nil
}
func (a *runtimeAuthorityStub) Publish(ctx context.Context, _ string, build func(context.Context, uint64, AccountRepository, GroupRepository) ([]byte, error)) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail {
		return nil, errors.New("authority unavailable")
	}
	repo := &regionalExportRepo{accounts: []Account{{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "secret-test-only"}}}}
	wire, err := build(ctx, a.revision+1, repo, nil)
	if err == nil {
		a.revision++
		a.wire = wire
	}
	return wire, err
}
func runtimeTestConfig(t *testing.T) RegionalSchedulerRuntimeConfig {
	t.Helper()
	dir := t.TempDir()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "regional-test-ca"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	require.NoError(t, err)
	leafPin := sha256.Sum256(der)
	pin := hex.EncodeToString(leafPin[:])
	c := RegionalSchedulerRuntimeConfig{Mode: "publisher", Policy: regional.Config{RegionID: "us", MaxSnapshotAgeSeconds: 60}, Keys: map[string][]byte{"test": bytes.Repeat([]byte{7}, 32)}, KeyID: "test", TrustedClientCertSHA256: []string{pin}, TrustedServerCertSHA256: pin, AllowedIDs: []int64{1}, Buckets: []SchedulerBucket{{Platform: PlatformOpenAI, Mode: SchedulerModeSingle}}, Listen: "127.0.0.1:0", CAFile: filepath.Join(dir, "ca.pem"), CertFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem"), IntervalSeconds: 1, LifetimeSeconds: 30}
	require.NoError(t, os.WriteFile(c.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600))
	require.NoError(t, os.WriteFile(c.CertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600))
	require.NoError(t, os.WriteFile(c.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600))
	leaf.SerialNumber = big.NewInt(3)
	otherDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherDER}), 0600))
	return c
}
func writeRuntimeTestConfig(t *testing.T, c RegionalSchedulerRuntimeConfig) *config.Config {
	t.Helper()
	b, err := json.Marshal(c)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "runtime.json")
	require.NoError(t, os.WriteFile(path, b, 0600))
	role := config.RuntimeRoleFull
	if c.Mode == "subscriber" {
		role = config.RuntimeRoleGateway
	}
	return &config.Config{Runtime: config.RuntimeConfig{Role: role, RegionalSchedulerFile: path}}
}

// runtimeTestConfig belongs to the unit-tagged runtime suite. Keep callers here
// so default-tag compilation (including lint) never depends on that fixture.
func TestRegionalSchedulerPublisherRejectsReaderMode(t *testing.T) {
	c := runtimeTestConfig(t)
	cfg := &config.Config{Runtime: config.RuntimeConfig{Role: config.RuntimeRoleFull, SchedulerSnapshotMode: config.SchedulerSnapshotModeReader}}
	require.ErrorContains(t, c.validate(cfg), "regional mode incompatible")
	cfg.Runtime.SchedulerSnapshotMode = config.SchedulerSnapshotModeWriter
	require.NoError(t, c.validate(cfg))
}

func TestRegionalRuntimeMTLSPullRestoreAndAdmission(t *testing.T) {
	c := runtimeTestConfig(t)
	authority := &runtimeAuthorityStub{}
	pub, err := startRegionalSchedulerRuntime(writeRuntimeTestConfig(t, c), authority)
	require.NoError(t, err)
	defer pub.stop()
	require.Eventually(t, func() bool { _, _, err := pub.snapshot(); return err == nil }, time.Second*5, time.Millisecond*10)
	// Real TLS listener with production TLS policy and production handlers.
	server := httptest.NewUnstartedServer(pub.server.Handler)
	server.TLS = pub.server.TLSConfig.Clone()
	server.StartTLS()
	defer server.Close()
	unauth := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pub.transport.TLSClientConfig.RootCAs}}, Timeout: time.Second}
	_, err = unauth.Get(server.URL + "/internal/regional/scheduler")
	require.Error(t, err)
	otherCert, err := tls.LoadX509KeyPair(filepath.Join(filepath.Dir(c.CertFile), "other.pem"), c.KeyFile)
	require.NoError(t, err)
	foreignTransport := pub.transport.Clone()
	foreignTransport.TLSClientConfig.Certificates = []tls.Certificate{otherCert}
	defer foreignTransport.CloseIdleConnections()
	_, err = (&http.Client{Transport: foreignTransport, Timeout: time.Second}).Get(server.URL + "/internal/regional/scheduler")
	require.Error(t, err, "same CA without an allowed fingerprint must be rejected")
	wrongPublisherTransport := pub.transport.Clone()
	wrongPublisherTransport.TLSClientConfig.VerifyConnection = func(state tls.ConnectionState) error {
		return verifyCertificatePin(state.PeerCertificates, strings.Repeat("0", 64))
	}
	defer wrongPublisherTransport.CloseIdleConnections()
	_, err = (&http.Client{Transport: wrongPublisherTransport, Timeout: time.Second}).Get(server.URL + "/internal/regional/scheduler")
	require.Error(t, err, "unexpected publisher fingerprint must be rejected")
	c.Mode = "subscriber"
	c.Endpoint = server.URL + "/internal/regional/scheduler"
	c.StateFile = filepath.Join(t.TempDir(), "state")
	c.Bootstrap = true
	cfg := writeRuntimeTestConfig(t, c)
	svc, err := ProvideSchedulerSnapshotService(nil, nil, nil, nil, cfg, nil)
	require.NoError(t, err)
	defer svc.Stop()
	require.Eventually(t, func() bool {
		accounts, _, err := svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
		return err == nil && len(accounts) == 1
	}, time.Second*5, time.Millisecond*10)
	// Duplicate ownership cannot overwrite an accepted revision floor.
	_, err = startRegionalSchedulerRuntime(cfg, nil)
	require.Error(t, err)
	wire, err := os.ReadFile(c.StateFile)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "secret-test-only")
	client := &http.Client{Transport: svc.regionalRuntime.transport, Timeout: time.Second}
	resp, err := client.Get(server.URL + "/internal/regional/scheduler/status")
	require.NoError(t, err)
	var status regionalSchedulerStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	_ = resp.Body.Close()
	require.True(t, status.Ready)
	require.Positive(t, status.Attempts)
	require.Positive(t, status.Revision)
	svc.Stop()
	server.Close()
	pub.stop()
	// Restore succeeds without a reachable source; only local candidate reads run.
	c.Bootstrap = false
	restored, err := ProvideSchedulerSnapshotService(nil, nil, nil, nil, writeRuntimeTestConfig(t, c), nil)
	require.NoError(t, err)
	defer restored.Stop()
	accounts, _, err := restored.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	// Publisher restart advances the shared authority revision, even with no file.
	authority.mu.Lock()
	previous := authority.revision
	authority.mu.Unlock()
	c.Mode = "publisher"
	restarted, err := startRegionalSchedulerRuntime(writeRuntimeTestConfig(t, c), authority)
	require.NoError(t, err)
	defer restarted.stop()
	require.Eventually(t, func() bool { s, _, err := restarted.snapshot(); return err == nil && s.Revision > previous }, time.Second*5, time.Millisecond*10)
}

func TestRegionalRuntimeFailsClosedAndPreservesExpiry(t *testing.T) {
	c := runtimeTestConfig(t)
	authority := &runtimeAuthorityStub{}
	pub, err := startRegionalSchedulerRuntime(writeRuntimeTestConfig(t, c), authority)
	require.NoError(t, err)
	defer pub.stop()
	require.Eventually(t, func() bool { _, _, err := pub.snapshot(); return err == nil }, time.Second*5, time.Millisecond*10)
	authority.mu.Lock()
	authority.fail = true
	authority.mu.Unlock()
	before, _, err := pub.snapshot()
	require.NoError(t, err)
	require.Error(t, pub.tick(context.Background()))
	after, _, err := pub.snapshot()
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
	require.Equal(t, before.ExpiresAt, after.ExpiresAt)
	c.Mode = "subscriber"
	c.Endpoint = "https://127.0.0.1:1/internal/regional/scheduler"
	c.StateFile = filepath.Join(t.TempDir(), "state")
	_, err = startRegionalSchedulerRuntime(writeRuntimeTestConfig(t, c), nil)
	require.Error(t, err)
	c.Bootstrap = true
	sub, err := ProvideSchedulerSnapshotService(nil, nil, nil, nil, writeRuntimeTestConfig(t, c), nil)
	require.NoError(t, err)
	_, _, err = sub.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
	require.Error(t, err)
	sub.Stop()
	require.NoError(t, os.WriteFile(c.StateFile, []byte("broken"), 0600))
	_, err = startRegionalSchedulerRuntime(writeRuntimeTestConfig(t, c), nil)
	require.Error(t, err)
}

func TestRegionalRuntimeConfigurationValidation(t *testing.T) {
	base := runtimeTestConfig(t)
	for _, mutate := range []func(*RegionalSchedulerRuntimeConfig){
		func(c *RegionalSchedulerRuntimeConfig) { c.Mode = "typo" },
		func(c *RegionalSchedulerRuntimeConfig) { c.LifetimeSeconds = c.IntervalSeconds },
		func(c *RegionalSchedulerRuntimeConfig) { c.AllowedIDs = []int64{1, 1} },
		func(c *RegionalSchedulerRuntimeConfig) { c.Buckets = append(c.Buckets, c.Buckets[0]) },
		func(c *RegionalSchedulerRuntimeConfig) {
			c.Mode = "subscriber"
			c.Endpoint = "http://example.com"
			c.StateFile = "state"
		},
	} {
		c := base
		mutate(&c)
		_, err := startRegionalSchedulerRuntime(writeRuntimeTestConfig(t, c), nil)
		require.Error(t, err)
	}
}
