package regional

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func snapshotFixture() (Config, Snapshot, time.Time) {
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	payload := json.RawMessage(`{"test_revision":1}`)
	hash := sha256.Sum256(payload)
	return Config{RegionID: "us", MaxSnapshotAgeSeconds: 60, MaxClockSkewSeconds: 2}, Snapshot{
		SchemaVersion: SchemaVersion, RegionID: "us", Kind: SnapshotAuth, Revision: 2,
		IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(59 * time.Second),
		Payload: payload, PayloadSHA256: hex.EncodeToString(hash[:]),
	}, now
}

func TestSnapshotRejectsUnsafeActivation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config, *Snapshot, *time.Time)
	}{
		{"wrong region", func(c *Config, s *Snapshot, n *time.Time) { s.RegionID = "asia" }},
		{"wrong kind", func(c *Config, s *Snapshot, n *time.Time) { s.Kind = SnapshotScheduler }},
		{"unknown schema", func(c *Config, s *Snapshot, n *time.Time) { s.SchemaVersion++ }},
		{"equal revision", func(c *Config, s *Snapshot, n *time.Time) { s.Revision = 1 }},
		{"older revision", func(c *Config, s *Snapshot, n *time.Time) { s.Revision = 0 }},
		{"future issuer", func(c *Config, s *Snapshot, n *time.Time) { s.IssuedAt = n.Add(3 * time.Second) }},
		{"at expiry", func(c *Config, s *Snapshot, n *time.Time) { *n = s.ExpiresAt }},
		{"overlong lifetime", func(c *Config, s *Snapshot, n *time.Time) { s.ExpiresAt = s.ExpiresAt.Add(time.Second) }},
		{"invalid times", func(c *Config, s *Snapshot, n *time.Time) { s.ExpiresAt = s.IssuedAt }},
		{"zero clock", func(c *Config, s *Snapshot, n *time.Time) { *n = time.Time{} }},
		{"tampered payload", func(c *Config, s *Snapshot, n *time.Time) { s.Payload = json.RawMessage(`{"test_revision":99}`) }},
		{"invalid JSON", func(c *Config, s *Snapshot, n *time.Time) { s.Payload = json.RawMessage(`{`) }},
		{"null payload", func(c *Config, s *Snapshot, n *time.Time) {
			s.Payload = json.RawMessage(" null ")
			h := sha256.Sum256(s.Payload)
			s.PayloadSHA256 = hex.EncodeToString(h[:])
		}},
		{"unconfigured", func(c *Config, s *Snapshot, n *time.Time) { *c = Config{} }},
		{"negative skew", func(c *Config, s *Snapshot, n *time.Time) { c.MaxClockSkewSeconds = -1 }},
		{"unbounded age", func(c *Config, s *Snapshot, n *time.Time) { c.MaxSnapshotAgeSeconds = 86401 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, s, n := snapshotFixture()
			tc.mutate(&c, &s, &n)
			if err := s.Validate(c, SnapshotAuth, 1, n); err == nil {
				t.Fatal("unsafe snapshot accepted")
			}
		})
	}
}
func TestSnapshotBootstrapAndClockTolerance(t *testing.T) {
	c, s, n := snapshotFixture()
	if err := s.Validate(c, SnapshotAuth, 0, n); err != nil {
		t.Fatal(err)
	}
	s.IssuedAt = n.Add(2 * time.Second)
	s.ExpiresAt = s.IssuedAt.Add(60 * time.Second)
	if err := s.Validate(c, SnapshotAuth, 1, n); err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(c, SnapshotAuth, 1, s.ExpiresAt); err == nil {
		t.Fatal("clock skew extended expiry")
	}
	if err := s.Validate(c, SnapshotKind("unknown"), 1, n); err == nil {
		t.Fatal("unknown kind accepted")
	}
}
func TestAmountsPreserveIntegerPrecision(t *testing.T) {
	const max Amount = 9223372036854775807
	b, err := json.Marshal(max)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"9223372036854775807"` {
		t.Fatalf("lost precision: %s", b)
	}
	var a Amount
	if err := json.Unmarshal(b, &a); err != nil || a != max {
		t.Fatalf("roundtrip: %v %v", a, err)
	}
	for _, bad := range []string{`1`, `null`, `""`, `"-1"`, `"+1"`, `"01"`, `"1.0"`, `"1e2"`, `" 1"`, `"9223372036854775808"`} {
		t.Run(bad, func(t *testing.T) {
			a := Amount(7)
			if err := json.Unmarshal([]byte(bad), &a); err == nil {
				t.Fatal("invalid amount accepted")
			}
			if a != 7 {
				t.Fatal("failed decode mutated amount")
			}
		})
	}
	if _, err := json.Marshal(Amount(-1)); err == nil {
		t.Fatal("negative amount marshaled")
	}
	if err := json.Unmarshal([]byte(`"0"`), &a); err != nil || a != 0 {
		t.Fatalf("zero: %v", err)
	}
}
func leaseFixture() (Lease, UsageEvent) {
	_, _, n := snapshotFixture()
	l := Lease{SchemaVersion: SchemaVersion, LeaseID: "lease-1", RegionID: "us", UserID: 1, Granted: 1000, IssuedAt: n, ExpiresAt: n.Add(time.Minute)}
	e := UsageEvent{SchemaVersion: SchemaVersion, EventID: "event-1", LeaseID: l.LeaseID, RegionID: l.RegionID, UserID: l.UserID, APIKeyID: 2, AccountID: 3, AdmittedAt: n, CompletedAt: n.Add(2 * time.Minute), Charge: 500}
	return l, e
}
func TestUsageAllowsCompletionAfterExpiry(t *testing.T) {
	l, e := leaseFixture()
	if err := e.ValidateAgainst(l); err != nil {
		t.Fatal(err)
	}
	e.Charge = 0
	if err := e.ValidateAgainst(l); err != nil {
		t.Fatal(err)
	}
}
func TestUsageRejectsInvalidChargeAndLease(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Lease, *UsageEvent)
	}{
		{"region", func(l *Lease, e *UsageEvent) { e.RegionID = "asia" }},
		{"lease", func(l *Lease, e *UsageEvent) { e.LeaseID = "lease-2" }},
		{"user", func(l *Lease, e *UsageEvent) { e.UserID++ }},
		{"key", func(l *Lease, e *UsageEvent) { e.APIKeyID = 0 }},
		{"account", func(l *Lease, e *UsageEvent) { e.AccountID = 0 }},
		{"schema", func(l *Lease, e *UsageEvent) { e.SchemaVersion++ }},
		{"missing event", func(l *Lease, e *UsageEvent) { e.EventID = "" }},
		{"negative", func(l *Lease, e *UsageEvent) { e.Charge = -1 }},
		{"over grant", func(l *Lease, e *UsageEvent) { e.Charge = l.Granted + 1 }},
		{"late admission", func(l *Lease, e *UsageEvent) { e.AdmittedAt = l.ExpiresAt }},
		{"early admission", func(l *Lease, e *UsageEvent) { e.AdmittedAt = l.IssuedAt.Add(-time.Second) }},
		{"completion before admission", func(l *Lease, e *UsageEvent) { e.CompletedAt = e.AdmittedAt.Add(-time.Second) }},
		{"zero grant", func(l *Lease, e *UsageEvent) { l.Granted = 0 }},
		{"invalid lease schema", func(l *Lease, e *UsageEvent) { l.SchemaVersion++ }},
		{"invalid lease time", func(l *Lease, e *UsageEvent) { l.ExpiresAt = l.IssuedAt }},
		{"invalid region ID", func(l *Lease, e *UsageEvent) { l.RegionID = "/us" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, e := leaseFixture()
			tc.mutate(&l, &e)
			if err := e.ValidateAgainst(l); err == nil {
				t.Fatal("invalid usage accepted")
			}
		})
	}
}

func TestSnapshotWireRoundtripPreservesDigest(t *testing.T) {
	c, s, n := snapshotFixture()
	s.Payload = []byte("{ \"label\": \"<test>&\" }")
	sum := sha256.Sum256(s.Payload)
	s.PayloadSHA256 = hex.EncodeToString(sum[:])
	wire, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(c, SnapshotAuth, 0, n); err != nil {
		t.Fatal(err)
	}
}
