package service

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// accountProxyLaneSessionContextKey carries the stable session identity from
// the scheduler into the slot-acquisition helper.  It is deliberately an
// unexported key type so unrelated middleware cannot accidentally collide.
type accountProxyLaneSessionContextKey struct{}
type accountProxyLaneIDContextKey struct{}

// WithAccountProxyLaneSession annotates a request context with the session key
// used for deterministic lane selection.  Empty keys are valid and simply
// select a deterministic non-sticky bucket.
func WithAccountProxyLaneSession(ctx context.Context, sessionKey string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, accountProxyLaneSessionContextKey{}, sessionKey)
}

// AccountProxyLaneSessionFromContext returns the scheduler's lane session key.
func AccountProxyLaneSessionFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(accountProxyLaneSessionContextKey{}).(string)
	return value
}

// WithAccountProxyLaneID annotates an upstream request context with the exact
// egress lane selected by the scheduler.  The value is deliberately carried
// on the request rather than inferred from proxyURL: two lanes may reference
// the same proxy and still require independent connection pools.
func WithAccountProxyLaneID(ctx context.Context, laneID int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if laneID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, accountProxyLaneIDContextKey{}, laneID)
}

// AccountProxyLaneIDFromContext returns the selected lane ID, or zero for a
// legacy account request that has no lane-specific routing metadata.
func AccountProxyLaneIDFromContext(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	laneID, _ := ctx.Value(accountProxyLaneIDContextKey{}).(int64)
	if laneID <= 0 {
		return 0
	}
	return laneID
}

// WithSelectedAccountProxyLane adds the selected lane to an upstream context.
// It is safe to call at every provider boundary and is a no-op for legacy
// accounts or requests that have not acquired a lane yet.
func WithSelectedAccountProxyLane(ctx context.Context, account *Account) context.Context {
	if account == nil || account.SelectedProxyLane == nil {
		return ctx
	}
	return WithAccountProxyLaneID(ctx, account.SelectedProxyLane.ID)
}

// Account proxy lane transport values.  Keep these strings in sync with the
// CHECK constraint in migrations/233_add_account_proxy_lanes.sql.
const (
	AccountProxyLaneTransportProxy  = "proxy"
	AccountProxyLaneTransportDirect = "direct"

	AccountProxyLaneStatusActive   = "active"
	AccountProxyLaneStatusPaused   = "paused"
	AccountProxyLaneStatusError    = "error"
	AccountProxyLaneStatusDisabled = "disabled"
)

// ErrNoSchedulableAccountProxyLane is returned when every configured lane is
// disabled, paused, cooling down, or has a zero weight.
var ErrNoSchedulableAccountProxyLane = errors.New("no schedulable account proxy lane")
var ErrAccountProxyLaneNotFound = infraerrors.NotFound("ACCOUNT_PROXY_LANE_NOT_FOUND", "account proxy lane not found")

// ValidateSelectedAccountProxyLane verifies the request-local egress before a
// forwarding layer turns the lane into a proxy URL.  Scheduler metadata is
// intentionally compact and may carry only ProxyID; treating that payload as
// a valid proxy lane would make AccountProxyURL return an empty string, which
// the HTTP transport interprets as a direct connection.  A direct lane is the
// only valid lane with no hydrated Proxy object.
//
// The function deliberately leaves legacy accounts (no configured lanes and
// no selected lane) untouched.  Once a lane is selected, however, a proxy
// transport must have the exact referenced Proxy object hydrated and a
// direct transport must not retain any proxy relation.
func ValidateSelectedAccountProxyLane(account *Account) error {
	if account == nil || account.SelectedProxyLane == nil {
		return nil
	}
	lane := account.SelectedProxyLane
	if lane.ID <= 0 {
		return fmt.Errorf("%w: selected lane id is invalid", ErrNoSchedulableAccountProxyLane)
	}
	if lane.AccountID > 0 && account.ID > 0 && lane.AccountID != account.ID {
		return fmt.Errorf("%w: selected lane %d belongs to account %d, request account is %d", ErrNoSchedulableAccountProxyLane, lane.ID, lane.AccountID, account.ID)
	}

	switch normalizeAccountProxyLaneTransport(lane.Transport) {
	case AccountProxyLaneTransportDirect:
		if lane.ProxyID != nil || lane.Proxy != nil {
			return fmt.Errorf("%w: direct lane %d carries a proxy relation", ErrNoSchedulableAccountProxyLane, lane.ID)
		}
	case AccountProxyLaneTransportProxy:
		if lane.ProxyID == nil || *lane.ProxyID <= 0 {
			return fmt.Errorf("%w: proxy lane %d has no positive proxy_id", ErrNoSchedulableAccountProxyLane, lane.ID)
		}
		if lane.Proxy == nil {
			return fmt.Errorf("%w: proxy lane %d proxy is not hydrated", ErrNoSchedulableAccountProxyLane, lane.ID)
		}
		if lane.Proxy.ID != *lane.ProxyID {
			return fmt.Errorf("%w: proxy lane %d relation mismatch (proxy_id=%d object_id=%d)", ErrNoSchedulableAccountProxyLane, lane.ID, *lane.ProxyID, lane.Proxy.ID)
		}
	default:
		return fmt.Errorf("%w: selected lane %d has unsupported transport %q", ErrNoSchedulableAccountProxyLane, lane.ID, lane.Transport)
	}
	return nil
}

// AccountProxyLane is the service/domain representation of one account egress.
// Proxy is optional because repositories may load lane rows without eagerly
// loading the referenced proxy; ProxyID remains the authoritative relation.
type AccountProxyLane struct {
	ID            int64
	AccountID     int64
	ProxyID       *int64
	Name          string
	Transport     string
	Concurrency   int
	Weight        int
	Priority      int
	Status        string
	Schedulable   bool
	CooldownUntil *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time

	Proxy *Proxy
}

// HasProxyLanes reports whether the account opted into lane scheduling.  An
// empty slice deliberately means "legacy single proxy" rather than "direct".
func (a *Account) HasProxyLanes() bool {
	return a != nil && len(a.ProxyLanes) > 0
}

// EffectiveProxyLanes returns configured lanes, or a synthetic legacy lane
// when the account has no lane rows.  The synthetic lane keeps all existing
// callers on the account.Proxy/account.Concurrency path while allowing new
// scheduling code to share one representation.
func (a *Account) EffectiveProxyLanes() []AccountProxyLane {
	if a == nil {
		return nil
	}
	if len(a.ProxyLanes) > 0 {
		lanes := make([]AccountProxyLane, len(a.ProxyLanes))
		copy(lanes, a.ProxyLanes)
		return lanes
	}
	legacy := AccountProxyLane{
		ID:          a.ID,
		AccountID:   a.ID,
		ProxyID:     a.ProxyID,
		Name:        "legacy",
		Transport:   AccountProxyLaneTransportProxy,
		Concurrency: a.Concurrency,
		Weight:      1,
		Priority:    a.Priority,
		Status:      AccountProxyLaneStatusActive,
		Schedulable: a.Schedulable,
		Proxy:       a.Proxy,
	}
	if a.ProxyID == nil {
		legacy.Transport = AccountProxyLaneTransportDirect
	}
	return []AccountProxyLane{legacy}
}

// ApplySelectedProxyLane projects a lane onto the request-scoped account
// object.  Existing forwarding code historically reads ProxyID, Proxy and
// Concurrency directly; projecting these fields keeps all providers on the
// same lane without widening every HTTP client interface.  The persisted
// account row is never mutated because scheduler/repository code passes value
// copies into a request.
func (a *Account) ApplySelectedProxyLane(lane *AccountProxyLane) {
	if a == nil || lane == nil {
		return
	}
	copyLane := *lane
	// Cache/database payloads may contain user-entered enum casing or a blank
	// transport (the latter means the SQL default, proxy).  Normalize before
	// projecting fields; otherwise a value such as "DIRECT" would be treated
	// as an unknown transport and could accidentally inherit the account's
	// legacy proxy through the fallback fields.
	copyLane.Transport = normalizeAccountProxyLaneTransport(copyLane.Transport)
	// Scheduler metadata intentionally omits proxy credentials.  If the
	// request-local lane came from that compact payload, recover the fully
	// hydrated proxy object by matching the lane in the destination account
	// before projecting fields.  Never silently reuse a different proxy.
	if copyLane.Proxy == nil && copyLane.ProxyID != nil && copyLane.ID > 0 {
		for i := range a.ProxyLanes {
			candidate := &a.ProxyLanes[i]
			// Lane identity is the only safe join key.  Proxy IDs are not
			// sufficient: a stale lane binding can point at a proxy that has
			// since been mounted by another lane, and borrowing that lane's
			// hydrated object would silently switch the request's egress.
			if candidate.ID == copyLane.ID {
				if candidate.Proxy != nil {
					copyLane.Proxy = candidate.Proxy
				}
				// Keep the freshest persisted lane settings while borrowing only
				// the hydrated proxy pointer.
				break
			}
		}
		// Only the synthetic legacy lane (whose ID is the account ID) may
		// hydrate from the account-level proxy field.  Once a real lane ID is in
		// play, even an equal proxy ID must not resurrect a stale lane after its
		// row was deleted or reassigned.
		if copyLane.Proxy == nil && copyLane.ID == a.ID && !a.HasProxyLanes() && a.Proxy != nil && a.Proxy.ID == *copyLane.ProxyID {
			copyLane.Proxy = a.Proxy
		}
	}
	// Scheduler metadata intentionally strips proxy credentials.  Prefer the
	// matching lane's hydrated Proxy object, then the account's legacy Proxy if
	// its ID matches.  Never reuse a different proxy: doing so silently sends a
	// request over the wrong egress.
	if copyLane.Transport == AccountProxyLaneTransportProxy && copyLane.Proxy == nil && copyLane.ProxyID != nil {
		for i := range a.ProxyLanes {
			candidate := &a.ProxyLanes[i]
			if candidate.ID == copyLane.ID && candidate.Proxy != nil {
				copyLane.Proxy = candidate.Proxy
				if copyLane.ProxyID == nil {
					copyLane.ProxyID = candidate.ProxyID
				}
				break
			}
		}
		if copyLane.Proxy == nil && copyLane.ID == a.ID && !a.HasProxyLanes() && a.Proxy != nil && a.Proxy.ID == *copyLane.ProxyID {
			copyLane.Proxy = a.Proxy
		}
	}
	a.SelectedProxyLane = &copyLane
	a.Concurrency = copyLane.Concurrency
	if copyLane.Transport == AccountProxyLaneTransportDirect {
		a.ProxyID = nil
		a.Proxy = nil
		return
	}
	a.ProxyID = copyLane.ProxyID
	if copyLane.Proxy != nil {
		a.Proxy = copyLane.Proxy
	} else {
		// A scheduler metadata cache may intentionally carry only ProxyID.  A
		// real lane without its matching hydrated Proxy must fail closed: keeping
		// the account-level proxy here would silently send the request through a
		// different lane (or resurrect a deleted/reassigned proxy).
		a.Proxy = nil
	}
}

// PreserveSelectedProxyLane carries a request-scoped lane choice across an
// account refresh (profit-control recheck, credential refresh, or scheduler
// hydration).  Matching by lane ID prevents a refreshed snapshot from
// accidentally switching the request to the account's legacy proxy.
func PreserveSelectedProxyLane(original, refreshed *Account) *Account {
	if refreshed == nil || original == nil || original.SelectedProxyLane == nil {
		return refreshed
	}
	lane := original.SelectedProxyLaneOrNil()
	if lane == nil {
		return refreshed
	}
	// PreserveSelectedProxyLane is used while a scheduler snapshot is being
	// refreshed, before the admission namespace is known.  Applying the lane
	// keeps transport affinity, but ApplySelectedProxyLane also projects the
	// lane cap onto Account.Concurrency.  Restoring the refreshed account's
	// aggregate value here is required for legacy-cache admission and for wait
	// plans built immediately after this helper returns; lane-capable callers
	// set the lane max explicitly once they choose the lane namespace.
	aggregateConcurrency := refreshed.Concurrency
	refreshed.ApplySelectedProxyLane(lane)
	refreshed.Concurrency = aggregateConcurrency
	return refreshed
}

// SelectedProxyLaneOrNil returns a defensive copy for callers that need to
// carry lane metadata across a refreshed account snapshot.
func (a *Account) SelectedProxyLaneOrNil() *AccountProxyLane {
	if a == nil || a.SelectedProxyLane == nil {
		return nil
	}
	copyLane := *a.SelectedProxyLane
	return &copyLane
}

// AccountProxyURL resolves the request-scoped egress URL.  Forwarders should
// use this instead of reading Proxy/ProxyID directly: a direct lane must not
// inherit the account's legacy proxy, and a lane whose proxy relation was not
// hydrated must never accidentally reuse a different proxy.
func AccountProxyURL(account *Account) string {
	if account == nil {
		return ""
	}
	if lane := account.SelectedProxyLane; lane != nil {
		if normalizeAccountProxyLaneTransport(lane.Transport) == AccountProxyLaneTransportDirect {
			return ""
		}
		if lane.Proxy == nil || lane.ProxyID == nil || lane.Proxy.ID != *lane.ProxyID {
			return ""
		}
		return lane.Proxy.URL()
	}
	// Preserve the legacy contract: repositories/tests historically populated
	// ProxyID while leaving Proxy.ID at its zero value.  The selected-lane path
	// above is strict; legacy fallback only requires both fields to be present.
	if account.ProxyID == nil || account.Proxy == nil {
		return ""
	}
	return account.Proxy.URL()
}

// Validate checks the lane's persisted invariants.  AccountID is validated
// here as well so a lane cannot silently be attached to account 0.
func (l AccountProxyLane) Validate() error {
	if l.AccountID <= 0 {
		return errors.New("account proxy lane account_id must be positive")
	}
	return l.validateFields()
}

// ValidateForAccount validates a lane received in an account-scoped payload.
// A zero AccountID is accepted for create payloads (the repository supplies
// the parent account); a non-zero value must match accountID.
func (l AccountProxyLane) ValidateForAccount(accountID int64) error {
	if accountID <= 0 {
		return errors.New("account id must be positive")
	}
	if l.AccountID != 0 && l.AccountID != accountID {
		return fmt.Errorf("account proxy lane account_id %d does not match account %d", l.AccountID, accountID)
	}
	return l.validateFields()
}

func (l AccountProxyLane) validateFields() error {
	if name := strings.TrimSpace(l.Name); name == "" {
		return errors.New("account proxy lane name is required")
	} else if utf8.RuneCountInString(name) > 100 {
		return errors.New("account proxy lane name must be at most 100 characters")
	}

	transport := normalizeAccountProxyLaneTransport(l.Transport)
	if transport != AccountProxyLaneTransportProxy && transport != AccountProxyLaneTransportDirect {
		return fmt.Errorf("unsupported account proxy lane transport %q", l.Transport)
	}
	if l.Concurrency < 0 {
		return errors.New("account proxy lane concurrency cannot be negative")
	}
	if l.Weight < 0 {
		return errors.New("account proxy lane weight cannot be negative")
	}
	if l.Priority < 0 {
		return errors.New("account proxy lane priority cannot be negative")
	}
	status := strings.ToLower(strings.TrimSpace(l.Status))
	if status == "" {
		status = AccountProxyLaneStatusActive
	}
	switch status {
	case AccountProxyLaneStatusActive, AccountProxyLaneStatusPaused, AccountProxyLaneStatusError, AccountProxyLaneStatusDisabled:
	default:
		return fmt.Errorf("unsupported account proxy lane status %q", l.Status)
	}
	if transport == AccountProxyLaneTransportDirect && l.ProxyID != nil {
		return errors.New("direct account proxy lane must not have proxy_id")
	}
	if transport == AccountProxyLaneTransportProxy {
		if l.ProxyID == nil || *l.ProxyID <= 0 {
			return errors.New("proxy account proxy lane requires a positive proxy_id")
		}
	}
	return nil
}

// Normalize returns a copy with user-entered enum/name whitespace normalized.
// It deliberately does not fill IDs or silently repair an invalid proxy/direct
// combination; callers should validate the result before persisting it.
func (l AccountProxyLane) Normalize() AccountProxyLane {
	l.Name = strings.TrimSpace(l.Name)
	l.Transport = normalizeAccountProxyLaneTransport(l.Transport)
	l.Status = strings.ToLower(strings.TrimSpace(l.Status))
	if l.Status == "" {
		l.Status = AccountProxyLaneStatusActive
	}
	return l
}

func normalizeAccountProxyLaneTransport(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		// The SQL/Ent default is proxy.  Treat a zero-value service object the
		// same way so callers can construct a lane incrementally.
		return AccountProxyLaneTransportProxy
	}
	return value
}

// IsSchedulableAt reports whether this lane may receive a request at now.
// The proxy pointer, when loaded, is also checked for status/expiry; a nil
// pointer does not make a valid proxy lane unusable because lazy repositories
// commonly load only ProxyID.
func (l AccountProxyLane) IsSchedulableAt(now time.Time) bool {
	if strings.ToLower(strings.TrimSpace(l.Status)) != AccountProxyLaneStatusActive || !l.Schedulable {
		return false
	}
	if l.CooldownUntil != nil && l.CooldownUntil.After(now) {
		return false
	}
	if l.Proxy != nil && (!l.Proxy.IsActive() || l.Proxy.IsExpired(now)) {
		return false
	}
	return l.Weight > 0 && l.Concurrency >= 0
}

// FilterSchedulableAccountProxyLanes returns a stable, priority-ordered copy
// of lanes that can be selected at now.  Lower priority values win, matching
// the account scheduler's existing priority convention.  Within one priority
// the original order is retained for deterministic tie handling.
func FilterSchedulableAccountProxyLanes(lanes []AccountProxyLane, now time.Time) []AccountProxyLane {
	filtered := make([]AccountProxyLane, 0, len(lanes))
	for _, lane := range lanes {
		if lane.IsSchedulableAt(now) {
			filtered = append(filtered, lane)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Priority < filtered[j].Priority
	})
	return filtered
}

// AccountProxyLaneSelectionOptions controls deterministic lane selection.
// StickyKey should contain the caller's stable session key.  Empty keys are
// still deterministic (the first weighted bucket is selected), which keeps
// the function pure and lets the caller explicitly opt into stickiness.
type AccountProxyLaneSelectionOptions struct {
	StickyKey string
	Now       time.Time
}

// SelectAccountProxyLane selects one eligible lane.  It first restricts the
// candidate set to the best (lowest) priority and then uses a deterministic
// weighted hash.  Consequently all requests in one session stay on the same
// egress while independent sessions spread according to Weight.
func SelectAccountProxyLane(lanes []AccountProxyLane, opts AccountProxyLaneSelectionOptions) (AccountProxyLane, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	candidates := FilterSchedulableAccountProxyLanes(lanes, now)
	if len(candidates) == 0 {
		return AccountProxyLane{}, ErrNoSchedulableAccountProxyLane
	}

	bestPriority := candidates[0].Priority
	best := candidates[:0]
	for _, lane := range candidates {
		if lane.Priority == bestPriority {
			best = append(best, lane)
		}
	}
	if len(best) == 1 {
		return best[0], nil
	}

	var totalWeight uint64
	for _, lane := range best {
		if lane.Weight > 0 {
			totalWeight += uint64(lane.Weight)
		}
	}
	if totalWeight == 0 {
		return AccountProxyLane{}, ErrNoSchedulableAccountProxyLane
	}

	// Canonicalize the weighted set before hashing/cumulative selection.  The
	// repository normally returns priority,id order, but cache rebuilds and
	// callers that construct an in-memory account are not required to preserve
	// that order.  Sorting by the persisted lane identity keeps a session on the
	// same egress when rows are merely reordered; the remaining fields are
	// deterministic tie-breakers for unsaved/invalid duplicate IDs.
	canonical := append([]AccountProxyLane(nil), best...)
	sort.SliceStable(canonical, func(i, j int) bool {
		left, right := canonical[i], canonical[j]
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		if left.Transport != right.Transport {
			return left.Transport < right.Transport
		}
		leftProxy, rightProxy := int64(0), int64(0)
		if left.ProxyID != nil {
			leftProxy = *left.ProxyID
		}
		if right.ProxyID != nil {
			rightProxy = *right.ProxyID
		}
		return leftProxy < rightProxy
	})

	// FNV-1a is fast, stable across processes, and already used by the service
	// for other deterministic sticky decisions.  Running the bucket over the
	// canonical sequence means a source-slice reorder does not silently remap
	// every session to a different weighted bucket.
	h := fnv.New64a()
	_, _ = h.Write([]byte(opts.StickyKey))
	bucket := h.Sum64() % totalWeight
	var cumulative uint64
	for _, lane := range canonical {
		if lane.Weight <= 0 {
			continue
		}
		cumulative += uint64(lane.Weight)
		if bucket < cumulative {
			return lane, nil
		}
	}
	// Defensive fallback for impossible uint64 overflow/rounding conditions.
	return canonical[len(canonical)-1], nil
}

// SelectAccountProxyLaneForSession is a concise convenience wrapper for the
// common sticky-session call site.
func SelectAccountProxyLaneForSession(lanes []AccountProxyLane, sessionKey string, now time.Time) (AccountProxyLane, error) {
	return SelectAccountProxyLane(lanes, AccountProxyLaneSelectionOptions{
		StickyKey: sessionKey,
		Now:       now,
	})
}

// OrderedAccountProxyLanesForSession returns the preferred lane first,
// followed by other eligible lanes in stable priority/id order.  Schedulers
// use this order to spill over when a sticky lane is temporarily full while
// retaining deterministic affinity whenever capacity is available.
func OrderedAccountProxyLanesForSession(lanes []AccountProxyLane, sessionKey string, now time.Time) ([]AccountProxyLane, error) {
	eligible := FilterSchedulableAccountProxyLanes(lanes, now)
	if len(eligible) == 0 {
		return nil, ErrNoSchedulableAccountProxyLane
	}
	if strings.TrimSpace(sessionKey) == "" {
		// Keep the pure selector deterministic. Callers that need per-request
		// spreading should provide their generated request ID as the key.
		sessionKey = "default"
	}
	preferred, err := SelectAccountProxyLane(eligible, AccountProxyLaneSelectionOptions{StickyKey: sessionKey, Now: now})
	if err != nil {
		return nil, err
	}
	ordered := make([]AccountProxyLane, 0, len(eligible))
	ordered = append(ordered, preferred)
	for _, lane := range eligible {
		if lane.ID == preferred.ID {
			continue
		}
		ordered = append(ordered, lane)
	}
	return ordered, nil
}

// ValidateAccountProxyLanes validates an account-scoped lane collection.  It
// also enforces unique non-empty names and duplicate proxy prevention before
// the database's unique indexes are reached, yielding a useful API error.
func ValidateAccountProxyLanes(accountID int64, lanes []AccountProxyLane) error {
	if accountID <= 0 {
		return errors.New("account id must be positive")
	}
	names := make(map[string]struct{}, len(lanes))
	proxies := make(map[int64]struct{}, len(lanes))
	for i, lane := range lanes {
		if err := lane.ValidateForAccount(accountID); err != nil {
			return fmt.Errorf("lane[%d]: %w", i, err)
		}
		name := strings.ToLower(strings.TrimSpace(lane.Name))
		if _, exists := names[name]; exists {
			return fmt.Errorf("lane[%d]: duplicate lane name %q", i, lane.Name)
		}
		names[name] = struct{}{}
		if lane.ProxyID != nil && normalizeAccountProxyLaneTransport(lane.Transport) == AccountProxyLaneTransportProxy {
			if _, exists := proxies[*lane.ProxyID]; exists {
				return fmt.Errorf("lane[%d]: proxy_id %d is already mounted on this account", i, *lane.ProxyID)
			}
			proxies[*lane.ProxyID] = struct{}{}
		}
	}
	return nil
}
