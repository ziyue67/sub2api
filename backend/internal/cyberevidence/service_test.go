package cyberevidence

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type testCipher struct{ fail bool }

func (c testCipher) Encrypt(s string) (string, error) {
	if c.fail {
		return "", errors.New("secret must never escape")
	}
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func (c testCipher) Decrypt(s string) (string, error) {
	b, e := base64.StdEncoding.DecodeString(s)
	return string(b), e
}

type memoryStore struct {
	mu       sync.Mutex
	calls    int
	failures int
	status   string
	stored   Prepared
	config   Config
	record   Record
	gate     chan struct{}
	started  chan struct{}
	once     sync.Once
}

func (m *memoryStore) LoadConfig(context.Context) (Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config, nil
}
func (m *memoryStore) SaveConfig(_ context.Context, c Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = c
	return nil
}
func (m *memoryStore) Save(ctx context.Context, id int64, p Prepared, days int) error {
	if m.started != nil {
		m.once.Do(func() { close(m.started) })
	}
	if m.gate != nil {
		select {
		case <-m.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls <= m.failures {
		return errors.New("sensitive database error must not escape")
	}
	m.stored = p
	m.status = "saved"
	return nil
}
func (m *memoryStore) Status(_ context.Context, _ int64, s string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = s
	return nil
}
func (m *memoryStore) Get(context.Context, int64) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.record
	r.Status = m.status
	r.Ciphertext = m.stored.Ciphertext
	return r, nil
}
func (m *memoryStore) Delete(context.Context, int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stored = Prepared{}
	m.status = "deleted"
	return nil
}
func (m *memoryStore) Cleanup(context.Context, int) (int64, error) { return 0, nil }
func (m *memoryStore) state() (int, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls, m.status
}
func eventually(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not complete")
}

func TestServiceEncryptionRequiredAndNoPlaintextFallback(t *testing.T) {
	for _, tc := range []struct {
		ready  bool
		cipher Cipher
		want   string
	}{{false, testCipher{}, "encryption_unavailable"}, {true, testCipher{fail: true}, "encryption_failed"}, {true, nil, "encryption_unavailable"}} {
		m := &memoryStore{config: DefaultConfig()}
		s := New(m, tc.cipher, tc.ready)
		p := s.Prepare([]byte(`{"input":"synthetic context"}`), "synthetic upstream reason")
		if p.Status != tc.want || p.Ciphertext != "" {
			t.Fatalf("unsafe preparation: %s", p.Status)
		}
		s.Enqueue(1, p)
		s.Close()
		if n, _ := m.state(); n != 0 {
			t.Fatal("plaintext fallback reached store")
		}
	}
}
func TestServiceBoundedRetryAndFailureStatus(t *testing.T) {
	for _, failures := range []int{2, 99} {
		m := &memoryStore{config: DefaultConfig(), failures: failures}
		s := New(m, testCipher{}, true)
		p := s.Prepare([]byte(`{"messages":[{"role":"user","content":"synthetic context"}]}`), "synthetic upstream reason")
		s.Enqueue(4, p)
		eventually(t, func() bool {
			n, status := m.state()
			return n == 3 && (status == "saved" || status == "storage_failed")
		})
		s.Close()
		n, status := m.state()
		if n != 3 {
			t.Fatal("retry unbounded")
		}
		if failures > 3 && (status != "storage_failed" || s.Stats().Failed != 1) {
			t.Fatal("failure invisible")
		}
		if failures == 2 {
			r, e := s.Get(context.Background(), 4)
			if e != nil || r.Payload == nil || !strings.Contains(r.Payload.Input, "synthetic context") || strings.Contains(r.Payload.Input, "synthetic upstream") {
				t.Fatal("payload/reason split lost")
			}
		}
	}
}
func TestServiceQueueBackpressure(t *testing.T) {
	m := &memoryStore{config: DefaultConfig(), gate: make(chan struct{}), started: make(chan struct{})}
	s := New(m, testCipher{}, true)
	p := s.Prepare([]byte(`{"input":"synthetic"}`), "reason")
	s.Enqueue(1, p)
	<-m.started
	for i := 2; i < 100; i++ {
		s.Enqueue(int64(i), p)
	}
	if s.Stats().Dropped == 0 || s.Stats().QueueDepth > 64 {
		t.Fatal("queue unbounded")
	}
	close(m.gate)
	s.Close()
}
func TestServiceHistoricalContentAndDelete(t *testing.T) {
	m := &memoryStore{config: DefaultConfig(), status: "historical_unavailable"}
	s := New(m, testCipher{}, true)
	defer s.Close()
	r, e := s.Get(context.Background(), 1)
	if e != nil || r.Payload != nil || r.Status != "historical_unavailable" {
		t.Fatal("historical content invented")
	}
	if e = s.Delete(context.Background(), 1); e != nil {
		t.Fatal(e)
	}
	r, _ = s.Get(context.Background(), 1)
	if r.Payload != nil || r.Status != "deleted" {
		t.Fatal("delete failed")
	}
	if s.UpdateConfig(context.Background(), Config{Enabled: true, RetentionDays: 31}) == nil {
		t.Fatal("unbounded retention")
	}
}
