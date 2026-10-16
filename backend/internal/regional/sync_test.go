package regional

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testCodec(t *testing.T) *Codec {
	t.Helper()
	c, err := NewCodec(map[string][]byte{"key1": bytes.Repeat([]byte{1}, 32), "key2": bytes.Repeat([]byte{2}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func testPayload(b []byte) error {
	var p struct {
		IDs []int `json:"ids"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return err
	}
	if p.IDs == nil {
		return errors.New("ids required")
	}
	return nil
}
func replicaFixture(t *testing.T) (*Replica, *Codec, time.Time) {
	t.Helper()
	codec := testCodec(t)
	cfg := Config{RegionID: "us", MaxSnapshotAgeSeconds: 60, MaxClockSkewSeconds: 2}
	r, err := OpenReplica(filepath.Join(t.TempDir(), "state"), cfg, SnapshotScheduler, codec, testPayload, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	return r, codec, now
}
func wireFixture(t *testing.T, c *Codec, n time.Time, revision uint64, payload string) []byte {
	t.Helper()
	hash := sha256.Sum256([]byte(payload))
	s := Snapshot{SchemaVersion: 1, RegionID: "us", Kind: SnapshotScheduler, Revision: revision, IssuedAt: n, ExpiresAt: n.Add(time.Minute), Payload: []byte(payload), PayloadSHA256: hex.EncodeToString(hash[:])}
	b, err := c.Seal("key1", s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestSyncRestoreAndRevocation(t *testing.T) {
	r, c, n := replicaFixture(t)
	ctx := context.Background()
	first := wireFixture(t, c, n, 1, `{"ids":[1,2]}`)
	if err := r.Accept(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := r.Accept(ctx, wireFixture(t, c, n, 2, `{"ids":[2]}`)); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenReplica(r.path, r.cfg, r.kind, c, testPayload, false)
	if err != nil {
		t.Fatal(err)
	}
	restored.now = r.now
	s, err := restored.Current()
	if err != nil || string(s.Payload) != `{"ids":[2]}` {
		t.Fatalf("revocation lost: %v", err)
	}
	s.Payload[0] = 'x'
	if _, err := restored.Current(); err != nil {
		t.Fatal("caller mutated stored snapshot")
	}
	if err := restored.Accept(ctx, first); err == nil {
		t.Fatal("rollback accepted after restart")
	}
	restored.now = func() time.Time { return n.Add(time.Minute) }
	if _, err := restored.Current(); err == nil {
		t.Fatal("expired snapshot served")
	}
	// Expired state is restored for its revision floor, never served or reset.
	expired, err := OpenReplica(r.path, r.cfg, r.kind, c, testPayload, false)
	if err != nil {
		t.Fatal(err)
	}
	expired.now = restored.now
	if _, err := expired.Current(); err == nil {
		t.Fatal("restart extended expiry")
	}
	freshOld := wireFixture(t, c, n.Add(time.Minute), 1, `{"ids":[1,2]}`)
	if err := expired.Accept(ctx, freshOld); err == nil {
		t.Fatal("expired floor discarded")
	}
}
func TestSyncRetryConflictAndInvalidInput(t *testing.T) {
	r, c, n := replicaFixture(t)
	ctx := context.Background()
	wire := wireFixture(t, c, n, 2, `{"ids":[2]}`)
	if err := r.Accept(ctx, wire); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(r.path)
	if err := r.Accept(ctx, wireFixture(t, c, n, 2, `{"ids":[2]}`)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(r.path)
	if !bytes.Equal(before, after) {
		t.Fatal("retry rewrote state")
	}
	for _, bad := range [][]byte{
		wireFixture(t, c, n, 2, `{"ids":[3]}`),
		wireFixture(t, c, n, 3, `{"other":[]}`),
		[]byte(`{}`),
		bytes.Repeat([]byte{'x'}, MaxSyncBytes+1),
	} {
		if err := r.Accept(ctx, bad); err == nil {
			t.Fatal("invalid packet accepted")
		}
	}
	s, _ := r.Current()
	if s.Revision != 2 {
		t.Fatal("failed update changed state")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := r.Accept(cancelled, wire); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r.now = func() time.Time { return n.Add(time.Minute) }
	if err := r.Accept(ctx, wire); err == nil {
		t.Fatal("retry refreshed expiry")
	}
}
func TestSyncEncryptionAndKeyRotation(t *testing.T) {
	r, c, n := replicaFixture(t)
	wire := wireFixture(t, c, n, 1, `{"ids":[12345]}`)
	if bytes.Contains(wire, []byte("12345")) {
		t.Fatal("plaintext leaked")
	}
	var p packet
	if err := json.Unmarshal(wire, &p); err != nil {
		t.Fatal(err)
	}
	p.Data[0] ^= 1
	bad, _ := json.Marshal(p)
	if _, err := c.Open(bad); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	wrong, _ := NewCodec(map[string][]byte{"key1": bytes.Repeat([]byte{3}, 32)})
	if _, err := wrong.Open(wire); err == nil {
		t.Fatal("wrong key accepted")
	}
	s, err := c.Open(wire)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := c.Seal("key2", s)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Accept(context.Background(), rotated); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReplica(r.path, r.cfg, r.kind, wrong, testPayload, false); err == nil {
		t.Fatal("unreadable state ignored")
	}
	if _, err := NewCodec(map[string][]byte{"short": {1}}); err == nil {
		t.Fatal("short key accepted")
	}
}
func TestSyncMissingCorruptAndPersistenceFailure(t *testing.T) {
	r, c, n := replicaFixture(t)
	if _, err := OpenReplica(r.path, r.cfg, r.kind, c, testPayload, false); err == nil {
		t.Fatal("missing state treated as bootstrap")
	}
	if err := r.Accept(context.Background(), wireFixture(t, c, n, 1, `{"ids":[1]}`)); err != nil {
		t.Fatal(err)
	}
	// Replace parent with a missing directory to force a write failure.
	r.path = filepath.Join(t.TempDir(), "absent", "state")
	if err := r.Accept(context.Background(), wireFixture(t, c, n, 2, `{"ids":[]}`)); err == nil {
		t.Fatal("write failure ignored")
	}
	if _, err := r.Current(); err == nil {
		t.Fatal("served old revoked data after write failure")
	}
	r.path = filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(r.path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReplica(r.path, r.cfg, r.kind, c, testPayload, true); err == nil {
		t.Fatal("bootstrap bypassed corrupt file")
	}
}
func TestSyncHTTPSFullReplacementAndRedirect(t *testing.T) {
	source, _, _ := replicaFixture(t)
	source.now = time.Now
	now := time.Now()
	if err := source.Publish(context.Background(), "key1", 1, now, now.Add(time.Minute), []byte(`{"ids":[1]}`)); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(source.Handler())
	defer server.Close()
	target, _, _ := replicaFixture(t)
	target.now = time.Now
	if err := target.Pull(context.Background(), server.URL, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	if err := source.Publish(context.Background(), "key2", 2, now, now.Add(time.Minute), []byte(`{"ids":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err := target.Pull(context.Background(), server.URL, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	s, err := target.Current()
	if err != nil || string(s.Payload) != `{"ids":[]}` {
		t.Fatalf("deletion not synchronized: %v", err)
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	if err := target.Pull(context.Background(), redirect.URL, redirect.Client().Transport); err == nil {
		t.Fatal("redirect followed")
	}
	if err := target.Pull(context.Background(), "http://localhost", nil); err == nil {
		t.Fatal("plaintext allowed")
	}
	stat, err := os.Stat(target.path)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("state permissions: %v", err)
	}
}
func TestSyncConcurrentUpdatesKeepHighestRevision(t *testing.T) {
	r, c, n := replicaFixture(t)
	var wg sync.WaitGroup
	for rev := uint64(1); rev <= 20; rev++ {
		wire := wireFixture(t, c, n, rev, `{"ids":[]}`)
		wg.Add(1)
		go func() { defer wg.Done(); _ = r.Accept(context.Background(), wire) }()
	}
	wg.Wait()
	s, err := r.Current()
	if err != nil || s.Revision != 20 {
		t.Fatalf("lost monotonic update: %v %v", s.Revision, err)
	}
}

func TestSyncPublishNextSurvivesRestartAndConcurrentPublish(t *testing.T) {
	r, c, n := replicaFixture(t)
	ctx := context.Background()
	if err := r.PublishNext(ctx, "key1", time.Minute, []byte(`{"ids":[]}`)); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenReplica(r.path, r.cfg, r.kind, c, testPayload, false)
	if err != nil {
		t.Fatal(err)
	}
	restored.now = func() time.Time { return n.Add(time.Minute) }
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := restored.PublishNext(ctx, "key1", time.Minute, []byte(`{"ids":[]}`)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	s, err := restored.Current()
	if err != nil || s.Revision != 11 {
		t.Fatalf("durable sequence: %d %v", s.Revision, err)
	}
	if err := restored.PublishNext(ctx, "key1", 2*time.Minute, []byte(`{"ids":[]}`)); err == nil {
		t.Fatal("overlong lifetime accepted")
	}
}
