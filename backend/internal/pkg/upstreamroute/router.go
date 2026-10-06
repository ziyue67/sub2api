// Package upstreamroute selects an operator-managed egress for an upstream host.
package upstreamroute

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
)

type Config struct {
	Enabled bool     `mapstructure:"enabled"`
	Regions []Region `mapstructure:"regions"`
	Rules   []Rule   `mapstructure:"rules"`
}
type Region struct {
	ID          string `mapstructure:"id"`
	ProxyURLEnv string `mapstructure:"proxy_url_env"`
}
type Rule struct {
	Domain     string  `mapstructure:"domain"`
	Region     string  `mapstructure:"region"`
	AccountIDs []int64 `mapstructure:"account_ids"`
}
type route struct {
	host, region, proxy string
	wildcard            bool
	accounts            map[int64]bool
}

// Router is immutable after construction, including resolved proxy secrets.
type Router struct{ routes []route }

var regionID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)
var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)
var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// New resolves credentials at startup. No error includes a proxy environment
// value or an error from parsing that value. Disabled policies need no secrets.
func New(c Config) (*Router, error) {
	if len(c.Regions) > 64 || len(c.Rules) > 256 {
		return nil, errors.New("maximum 64 upstream regions and 256 domain rules")
	}
	regions := make(map[string]string, len(c.Regions))
	for i, r := range c.Regions {
		if !regionID.MatchString(r.ID) || !envName.MatchString(r.ProxyURLEnv) {
			return nil, fmt.Errorf("upstream region %d requires a valid ID and proxy_url_env name", i)
		}
		if _, exists := regions[r.ID]; exists {
			return nil, errors.New("duplicate upstream region ID")
		}
		regions[r.ID] = ""
		if !c.Enabled {
			continue
		}
		raw, ok := os.LookupEnv(r.ProxyURLEnv)
		if !ok || strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("upstream region %s proxy environment is empty", r.ID)
		}
		_, p, err := proxyurl.Parse(raw)
		if err != nil || p == nil || p.Opaque != "" || p.Path != "" || p.RawQuery != "" || p.ForceQuery || p.Fragment != "" {
			return nil, fmt.Errorf("upstream region %s requires a valid HTTP(S) or SOCKS5 proxy origin", r.ID)
		}
		if p.Port() != "" {
			port, err := strconv.Atoi(p.Port())
			if err != nil || port < 1 || port > 65535 {
				return nil, fmt.Errorf("upstream region %s has an invalid proxy port", r.ID)
			}
		}
		p.Scheme = strings.ToLower(p.Scheme)
		regions[r.ID] = p.String()
	}
	result := &Router{}
	seen := make(map[string]map[int64]bool)
	for i, rule := range c.Rules {
		domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(rule.Domain)), ".")
		wildcard := strings.HasPrefix(domain, "*.")
		host := strings.TrimPrefix(domain, "*.")
		if !validHost(host) || (wildcard && (net.ParseIP(host) != nil || !strings.Contains(host, "."))) || len(rule.AccountIDs) > 256 {
			return nil, fmt.Errorf("upstream domain rule %d is invalid or repeated", i)
		}
		proxy, ok := regions[rule.Region]
		if !ok {
			return nil, fmt.Errorf("upstream domain rule %d references an unknown region", i)
		}
		if seen[domain] == nil {
			seen[domain] = make(map[int64]bool)
		}
		accounts := make(map[int64]bool, len(rule.AccountIDs))
		if len(rule.AccountIDs) == 0 {
			accounts[0] = true
		}
		for _, id := range rule.AccountIDs {
			if id <= 0 || accounts[id] {
				return nil, fmt.Errorf("upstream domain rule %d has an invalid or repeated account ID", i)
			}
			accounts[id] = true
		}
		for id := range accounts {
			if seen[domain][id] {
				return nil, fmt.Errorf("upstream domain rule %d repeats a domain and account ID", i)
			}
			seen[domain][id] = true
		}
		result.routes = append(result.routes, route{host: host, region: rule.Region, proxy: proxy, wildcard: wildcard, accounts: accounts})
	}
	if !c.Enabled {
		return nil, nil
	}
	if len(result.routes) == 0 {
		return nil, errors.New("enabled upstream routing requires domain rules")
	}
	// Exact hosts beat suffix rules; longest suffix wins independently of order.
	sort.Slice(result.routes, func(i, j int) bool {
		a, b := result.routes[i], result.routes[j]
		if a.wildcard != b.wildcard {
			return !a.wildcard
		}
		if a.host == b.host {
			return !a.accounts[0] && b.accounts[0]
		}
		return len(a.host) > len(b.host)
	})
	return result, nil
}
func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// Match uses the outgoing URL, never client headers, IP location or model name.
// A miss retains the direct path; a match must never silently fall back to it.
func (r *Router) Match(target *url.URL) (proxy, region string) {
	return r.MatchForAccount(target, 0)
}

// MatchForAccount applies account-scoped rules only to the selected upstream
// account. A missing account ID can match global rules but not scoped rules.
func (r *Router) MatchForAccount(target *url.URL, accountID int64) (proxy, region string) {
	if r == nil || target == nil || (target.Scheme != "http" && target.Scheme != "https" && target.Scheme != "ws" && target.Scheme != "wss") {
		return "", ""
	}
	host := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	for _, rule := range r.routes {
		if !rule.accounts[0] && !rule.accounts[accountID] {
			continue
		}
		if (!rule.wildcard && host == rule.host) || (rule.wildcard && strings.HasSuffix(host, "."+rule.host)) {
			return rule.proxy, rule.region
		}
	}
	return "", ""
}

type transportError struct{ cause error }

func (e *transportError) Error() string { return "regional upstream transport failed" }
func (e *transportError) Unwrap() error { return e.cause }

// TransportError preserves cancellation classification without printing nested
// net/http errors that may contain proxy credentials.
func TransportError(err error) error {
	if err == nil {
		return nil
	}
	return &transportError{cause: err}
}
