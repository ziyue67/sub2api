// Package regional defines the versioned contracts for regional data planes.
// It does not enable regional serving, transport snapshots or settle balances.
package regional

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"
)

const SchemaVersion = 1

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)

// Config is a consumer-side policy, not an application runtime toggle.
// A zero value is unconfigured; callers must validate before regional serving.
// There is deliberately no remote fallback: an expired view must stop admission.
type Config struct {
	RegionID              string `json:"region_id"`
	MaxSnapshotAgeSeconds int64  `json:"max_snapshot_age_seconds"`
	MaxClockSkewSeconds   int64  `json:"max_clock_skew_seconds"`
}

func (c Config) Validate() error {
	if !identifier.MatchString(c.RegionID) {
		return errors.New("invalid regional region_id")
	}
	if c.MaxSnapshotAgeSeconds < 1 || c.MaxSnapshotAgeSeconds > 86400 {
		return errors.New("max_snapshot_age_seconds must be between 1 and 86400")
	}
	if c.MaxClockSkewSeconds < 0 || c.MaxClockSkewSeconds > 60 {
		return errors.New("max_clock_skew_seconds must be between 0 and 60")
	}
	return nil
}

type SnapshotKind string

const (
	SnapshotAuth      SnapshotKind = "auth"
	SnapshotScheduler SnapshotKind = "scheduler"
)

// Revision is monotonic per (region_id, kind), persisted across issuer restarts.
// Zero current revision denotes bootstrap. Accepted revision must never regress.
// Payload is base64 in the envelope JSON, preserving exact bytes on roundtrip.
// PayloadSHA256 covers those decoded bytes; it is NOT authentication.
type Snapshot struct {
	SchemaVersion int          `json:"schema_version"`
	RegionID      string       `json:"region_id"`
	Kind          SnapshotKind `json:"kind"`
	Revision      uint64       `json:"revision,string"`
	IssuedAt      time.Time    `json:"issued_at"`
	ExpiresAt     time.Time    `json:"expires_at"`
	PayloadSHA256 string       `json:"payload_sha256"`
	Payload       []byte       `json:"payload"`
}

// Validate checks an authenticated envelope before activation. The sync layer
// must authenticate the issuer and atomically persist payload + revision.
// Retrying an already-applied revision is handled by that layer without applying
// it again; this method rejects equal revisions as well as older ones.
func (s Snapshot) Validate(c Config, kind SnapshotKind, current uint64, now time.Time) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if s.SchemaVersion != SchemaVersion {
		return errors.New("unsupported regional snapshot schema_version")
	}
	if s.RegionID != c.RegionID {
		return errors.New("snapshot region mismatch")
	}
	if kind != SnapshotAuth && kind != SnapshotScheduler {
		return errors.New("unsupported snapshot kind")
	}
	if s.Kind != kind {
		return errors.New("snapshot kind mismatch")
	}
	if s.Revision == 0 || s.Revision <= current {
		return errors.New("snapshot revision must advance")
	}
	if now.IsZero() || s.IssuedAt.IsZero() || !s.ExpiresAt.After(s.IssuedAt) {
		return errors.New("invalid snapshot timestamps")
	}
	maxAge := time.Duration(c.MaxSnapshotAgeSeconds) * time.Second
	skew := time.Duration(c.MaxClockSkewSeconds) * time.Second
	if s.ExpiresAt.Sub(s.IssuedAt) > maxAge {
		return errors.New("snapshot lifetime exceeds policy")
	}
	if s.IssuedAt.After(now.Add(skew)) {
		return errors.New("snapshot issued in the future")
	}
	// Clock tolerance never extends expiry or the maximum allowed age.
	if !now.Before(s.ExpiresAt) || now.Sub(s.IssuedAt) >= maxAge {
		return errors.New("snapshot expired")
	}
	if !json.Valid(s.Payload) || bytes.Equal(bytes.TrimSpace(s.Payload), []byte("null")) {
		return errors.New("invalid snapshot payload")
	}
	sum := sha256.Sum256(s.Payload)
	if s.PayloadSHA256 != hex.EncodeToString(sum[:]) {
		return errors.New("snapshot payload checksum mismatch")
	}
	return nil
}

// Amount is an integer in 10^-9 USD units. JSON uses a string to avoid loss of
// precision in JavaScript consumers. Never convert balances through float64.
// Amount is non-negative; rounding/legacy balance conversion belongs to issuer.
type Amount int64

func (a Amount) MarshalJSON() ([]byte, error) {
	if a < 0 {
		return nil, errors.New("regional amount cannot be negative")
	}
	return json.Marshal(strconv.FormatInt(int64(a), 10))
}
func (a *Amount) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("regional amount must be a decimal integer string")
	}
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return errors.New("invalid regional amount")
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return errors.New("invalid regional amount")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return errors.New("regional amount out of range")
	}
	*a = Amount(parsed)
	return nil
}

// Lease is a reservation already debited/held by the authoritative ledger.
// It is scoped to one user, region and accounting currency (USD). It must not
// be reused by another region or returned merely because ExpiresAt elapsed.
type Lease struct {
	BillingMode   string    `json:"billing_mode,omitempty"`
	APIKeyID      int64     `json:"api_key_id,omitempty"`
	AccountID     int64     `json:"account_id,omitempty"`
	SchemaVersion int       `json:"schema_version"`
	LeaseID       string    `json:"lease_id"`
	RegionID      string    `json:"region_id"`
	UserID        int64     `json:"user_id"`
	Granted       Amount    `json:"granted_nano_usd"`
	IssuedAt      time.Time `json:"issued_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

func (l Lease) Validate() error {
	if l.SchemaVersion != SchemaVersion {
		return errors.New("unsupported regional lease schema_version")
	}
	if !identifier.MatchString(l.LeaseID) || !identifier.MatchString(l.RegionID) {
		return errors.New("invalid lease identity")
	}
	if l.UserID <= 0 || l.Granted <= 0 {
		return errors.New("lease requires a user and positive grant")
	}
	if l.IssuedAt.IsZero() || !l.ExpiresAt.After(l.IssuedAt) {
		return errors.New("invalid lease timestamps")
	}
	return nil
}

// UsageEvent is a final charge, not a per-token increment. EventID identifies
// one logical charge globally; retries must retain both ID and exact contents.
// Admission reserves a conservative maximum locally before calling upstream.
// Completion may occur after lease expiry; acceptance is based on AdmittedAt.
type UsageEvent struct {
	SchemaVersion int       `json:"schema_version"`
	EventID       string    `json:"usage_event_id"`
	LeaseID       string    `json:"lease_id"`
	RegionID      string    `json:"region_id"`
	UserID        int64     `json:"user_id"`
	APIKeyID      int64     `json:"api_key_id"`
	AccountID     int64     `json:"account_id"`
	AdmittedAt    time.Time `json:"admitted_at"`
	CompletedAt   time.Time `json:"completed_at"`
	Charge        Amount    `json:"charge_nano_usd"`
}

// ValidateAgainst checks immutable event fields. It does not deduplicate,
// authenticate, reserve or validate aggregate spending; ledger implementations
// must do those operations transactionally and retain collision evidence.
func (e UsageEvent) ValidateAgainst(l Lease) error {
	if err := l.Validate(); err != nil {
		return err
	}
	if e.SchemaVersion != SchemaVersion {
		return errors.New("unsupported regional usage schema_version")
	}
	if !identifier.MatchString(e.EventID) {
		return errors.New("invalid usage_event_id")
	}
	if e.LeaseID != l.LeaseID || e.RegionID != l.RegionID || e.UserID != l.UserID {
		return errors.New("usage event lease mismatch")
	}
	if l.APIKeyID != 0 && e.APIKeyID != l.APIKeyID || l.AccountID != 0 && e.AccountID != l.AccountID {
		return errors.New("usage outside funded scope")
	}
	if e.APIKeyID <= 0 || e.AccountID <= 0 {
		return errors.New("usage event requires API key and account")
	}
	if e.Charge < 0 || e.Charge > l.Granted {
		return errors.New("usage charge outside grant")
	}
	if e.AdmittedAt.Before(l.IssuedAt) || !e.AdmittedAt.Before(l.ExpiresAt) || e.CompletedAt.Before(e.AdmittedAt) {
		return errors.New("invalid usage event timestamps")
	}
	return nil
}
