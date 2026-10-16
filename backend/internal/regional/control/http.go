// Package control exposes authenticated background ledger operations. It never
// proxies model requests and is not registered on the public application router.
package control

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
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/Wei-Shaw/sub2api/internal/regional/slots"
)

const Prefix = "/internal/regional/control"
const maxBody = 256 << 10

type Scope struct {
	Region            string
	CertificateSHA256 string
	UserIDs           []int64
	AccountIDs        []int64
	APIKeyIDs         []int64
	MaximumGrant      regional.Amount
	MaximumSlots      int
	MaximumLifetime   time.Duration
}

func (s Scope) Validate() error {
	if err := (regional.Config{RegionID: s.Region, MaxSnapshotAgeSeconds: 60}).Validate(); err != nil {
		return err
	}
	pin, err := hex.DecodeString(s.CertificateSHA256)
	if err != nil || len(pin) != sha256.Size || s.CertificateSHA256 != hex.EncodeToString(pin) {
		return errors.New("control peer pin required")
	}
	if s.MaximumGrant <= 0 || s.MaximumSlots <= 0 || s.MaximumLifetime < time.Second || s.MaximumLifetime > time.Hour {
		return errors.New("invalid control grant policy")
	}
	if _, err := ledger.USD(s.MaximumGrant); err != nil {
		return err
	}
	for _, ids := range [][]int64{s.UserIDs, s.AccountIDs, s.APIKeyIDs} {
		seen := map[int64]bool{}
		for _, id := range ids {
			if id <= 0 || seen[id] {
				return errors.New("invalid control assignment")
			}
			seen[id] = true
		}
	}
	return nil
}
func contains(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
func (s Scope) resource(r slots.Resource) bool {
	switch r.Kind {
	case "account":
		return contains(s.AccountIDs, r.ID)
	case "user":
		return contains(s.UserIDs, r.ID)
	case "api_key":
		return contains(s.APIKeyIDs, r.ID)
	}
	return false
}
func (s Scope) authenticate(r *http.Request) bool {
	if r.TLS == nil || r.TLS.Version < tls.VersionTLS13 || len(r.TLS.PeerCertificates) == 0 || len(r.TLS.VerifiedChains) == 0 {
		return false
	}
	sum := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	pin, _ := hex.DecodeString(s.CertificateSHA256)
	return subtle.ConstantTimeCompare(sum[:], pin) == 1
}

// Handler requires a listener using TLS1.3, RequireAndVerifyClientCert and a
// private CA. Each handler instance has ONE fixed region/certificate assignment.
// A valid CA certificate alone never grants permission to issue/settle leases.
type Handler struct {
	Scope  Scope
	Quota  *ledger.Primary
	Slots  *slots.Primary
	Verify ledger.VerifyEvent
}

func NewHandler(scope Scope, quota *ledger.Primary, capacity *slots.Primary, verify ledger.VerifyEvent) (http.Handler, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if quota == nil || quota.DB == nil || capacity == nil || capacity.DB == nil || capacity.Limit == nil || verify == nil {
		return nil, errors.New("control authorities and settlement verifier required")
	}
	scope.UserIDs = append([]int64(nil), scope.UserIDs...)
	scope.AccountIDs = append([]int64(nil), scope.AccountIDs...)
	scope.APIKeyIDs = append([]int64(nil), scope.APIKeyIDs...)
	return &Handler{Scope: scope, Quota: quota, Slots: capacity, Verify: verify}, nil
}

// NewSlotsHandler exposes only slot operations until a settlement verifier is wired.
func NewSlotsHandler(scope Scope, capacity *slots.Primary) (http.Handler, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if capacity == nil || capacity.DB == nil || capacity.Limit == nil || capacity.GrantGuard == nil {
		return nil, errors.New("guarded slot authority required")
	}
	scope.UserIDs = append([]int64(nil), scope.UserIDs...)
	scope.AccountIDs = append([]int64(nil), scope.AccountIDs...)
	scope.APIKeyIDs = append([]int64(nil), scope.APIKeyIDs...)
	return &Handler{Scope: scope, Slots: capacity}, nil
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	defer func() { _ = r.Body.Close() }()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return errors.New("trailing control data")
	}
	return nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.Scope.authenticate(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.URL.Path, Prefix+"/quota/") && (h.Quota == nil || h.Verify == nil) {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var err error
	switch r.URL.Path {
	case Prefix + "/quota/issue":
		var lease regional.Lease
		if decode(w, r, &lease) != nil {
			w.WriteHeader(400)
			return
		}
		if lease.RegionID != h.Scope.Region || !contains(h.Scope.UserIDs, lease.UserID) || (lease.APIKeyID != 0 && !contains(h.Scope.APIKeyIDs, lease.APIKeyID)) || (lease.AccountID != 0 && !contains(h.Scope.AccountIDs, lease.AccountID)) || lease.Granted > h.Scope.MaximumGrant || lease.ExpiresAt.Sub(lease.IssuedAt) > h.Scope.MaximumLifetime {
			w.WriteHeader(403)
			return
		}
		err = h.Quota.Issue(ctx, lease)
	case Prefix + "/quota/settle":
		var event ledger.Event
		if decode(w, r, &event) != nil {
			w.WriteHeader(400)
			return
		}
		if event.Usage.RegionID != h.Scope.Region || !contains(h.Scope.UserIDs, event.Usage.UserID) || !contains(h.Scope.APIKeyIDs, event.Usage.APIKeyID) || !contains(h.Scope.AccountIDs, event.Usage.AccountID) {
			w.WriteHeader(403)
			return
		}
		_, err = h.Quota.Settle(ctx, h.Scope.Region, event, h.Verify)
	case Prefix + "/quota/close":
		var receipt ledger.DrainReceipt
		if decode(w, r, &receipt) != nil {
			w.WriteHeader(400)
			return
		}
		if receipt.Region != h.Scope.Region {
			w.WriteHeader(403)
			return
		}
		err = h.Quota.Close(ctx, h.Scope.Region, receipt)
	case Prefix + "/slots/issue", Prefix + "/slots/renew":
		var grant slots.Grant
		if decode(w, r, &grant) != nil {
			w.WriteHeader(400)
			return
		}
		if grant.Region != h.Scope.Region || !h.Scope.resource(grant.Resource) || grant.Slots > h.Scope.MaximumSlots || time.Until(grant.ExpiresAt) > h.Scope.MaximumLifetime {
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == Prefix+"/slots/issue" {
			err = h.Slots.Issue(ctx, grant)
		} else {
			err = h.Slots.Renew(ctx, grant)
		}
	case Prefix + "/slots/drain":
		var request struct {
			LeaseID string `json:"lease_id"`
		}
		if decode(w, r, &request) != nil {
			w.WriteHeader(400)
			return
		}
		err = h.Slots.BeginDrain(ctx, h.Scope.Region, request.LeaseID)
	case Prefix + "/slots/close":
		var receipt slots.DrainReceipt
		if decode(w, r, &receipt) != nil {
			w.WriteHeader(400)
			return
		}
		if receipt.Region != h.Scope.Region {
			w.WriteHeader(403)
			return
		}
		err = h.Slots.Close(ctx, h.Scope.Region, receipt)
	default:
		w.WriteHeader(404)
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, slots.ErrExpiredUnissued), errors.Is(err, ledger.ErrExpiredUnissued):
			w.WriteHeader(http.StatusGone)
		case errors.Is(err, ledger.ErrConflict), errors.Is(err, slots.ErrConflict):
			w.WriteHeader(409)
		case errors.Is(err, ledger.ErrExhausted), errors.Is(err, slots.ErrUnavailable):
			w.WriteHeader(429)
		default:
			w.WriteHeader(503)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Client is used by background workers only. Redirects and environment proxies
// are disabled so region credentials cannot be forwarded to a different host.
type Client struct {
	base   string
	client *http.Client
}

func NewClient(endpoint string, roots *x509.CertPool, identity tls.Certificate, serverPin string) (*Client, error) {
	u, err := url.Parse(endpoint)
	pin, pinErr := hex.DecodeString(serverPin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || roots == nil || len(identity.Certificate) == 0 || pinErr != nil || len(pin) != sha256.Size || serverPin != hex.EncodeToString(pin) {
		return nil, errors.New("invalid control TLS endpoint")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{identity}, VerifyConnection: func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("control server identity missing")
		}
		sum := sha256.Sum256(state.PeerCertificates[0].Raw)
		if subtle.ConstantTimeCompare(sum[:], pin) != 1 {
			return errors.New("control server pin mismatch")
		}
		return nil
	}}}
	return &Client{base: strings.TrimRight(endpoint, "/"), client: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("control redirect forbidden") }}}, nil
}
func (c *Client) post(ctx context.Context, path string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(raw) > maxBody {
		return errors.New("control request exceeds limit")
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+Prefix+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(r)
	if err != nil {
		return errors.New("regional control transport unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusGone {
		return slots.ErrExpiredUnissued
	}
	if response.StatusCode != http.StatusNoContent {
		return errors.New("regional control operation was not confirmed")
	}
	return nil
}
func (c *Client) Settle(ctx context.Context, e ledger.Event) error {
	return c.post(ctx, "/quota/settle", e)
}
func (c *Client) IssueQuota(ctx context.Context, l regional.Lease) error {
	return c.post(ctx, "/quota/issue", l)
}
func (c *Client) CloseQuota(ctx context.Context, r ledger.DrainReceipt) error {
	return c.post(ctx, "/quota/close", r)
}
func (c *Client) IssueSlots(ctx context.Context, g slots.Grant) error {
	return c.post(ctx, "/slots/issue", g)
}
func (c *Client) RenewSlots(ctx context.Context, g slots.Grant) error {
	return c.post(ctx, "/slots/renew", g)
}
func (c *Client) DrainSlots(ctx context.Context, id string) error {
	return c.post(ctx, "/slots/drain", struct {
		LeaseID string `json:"lease_id"`
	}{id})
}
func (c *Client) CloseSlots(ctx context.Context, r slots.DrainReceipt) error {
	return c.post(ctx, "/slots/close", r)
}
func (c *Client) Close() { c.client.CloseIdleConnections() }
