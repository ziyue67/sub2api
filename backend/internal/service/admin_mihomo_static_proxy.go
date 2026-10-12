package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type MihomoStaticNodeBinder interface {
	BindStaticNodes(ctx context.Context, names []string) ([]mihomo.StaticNodeBinding, error)
	DeactivateStaticNodes(ctx context.Context, names []string) error
}

// Only the admin node-conversion path needs this exact lookup; ordinary proxy
// repository clients remain independent of Mihomo metadata.
type mihomoNodeProxyLookup interface {
	GetByMihomoNodeID(ctx context.Context, name string) (*Proxy, error)
}

type MihomoNodeProxyImportFailure struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type MihomoNodeProxyImportResult struct {
	Created  []Proxy                        `json:"created"`
	Existing []Proxy                        `json:"existing"`
	Failed   []MihomoNodeProxyImportFailure `json:"failed"`
}

// CreateMihomoNodeProxies registers fixed local HTTP exits as ordinary static
// proxies. The underlying Mihomo listener is tied to the stable node identity,
// never to the rotating group or to a source-supplied server address.
func (s *adminServiceImpl) CreateMihomoNodeProxies(ctx context.Context, kernel MihomoStaticNodeBinder, names []string) (*MihomoNodeProxyImportResult, error) {
	if len(names) == 0 || len(names) > 100 {
		return nil, infraerrors.BadRequest("MIHOMO_STATIC_SELECTION_INVALID", "select between 1 and 100 nodes")
	}
	s.mihomoProxyMu.Lock()
	defer s.mihomoProxyMu.Unlock()
	lookup, ok := s.proxyRepo.(mihomoNodeProxyLookup)
	if !ok {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "MIHOMO_STATIC_LOOKUP_UNAVAILABLE", "node proxy lookup is unavailable")
	}
	unique := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	previous := make(map[string]*Proxy, len(names))
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		unique = append(unique, name)
		proxy, err := lookup.GetByMihomoNodeID(ctx, name)
		if err != nil {
			return nil, err
		}
		previous[name] = proxy
	}
	if len(unique) == 0 {
		return nil, infraerrors.BadRequest("MIHOMO_STATIC_SELECTION_INVALID", "select at least one node")
	}
	bindings, err := kernel.BindStaticNodes(ctx, unique)
	if err != nil {
		if errors.Is(err, mihomo.ErrUnknownNode) {
			return nil, infraerrors.NotFound("MIHOMO_NODE_NOT_FOUND", "node no longer exists; refresh the list")
		}
		return nil, infraerrors.Conflict("MIHOMO_STATIC_BIND_FAILED", err.Error())
	}
	result := &MihomoNodeProxyImportResult{Created: []Proxy{}, Existing: []Proxy{}, Failed: []MihomoNodeProxyImportFailure{}}
	toDeactivate := make([]string, 0)
	for _, binding := range bindings {
		if existing := previous[binding.Name]; existing != nil {
			if existing.Protocol != "http" || existing.Host != "127.0.0.1" || existing.Port != binding.Port {
				result.Failed = append(result.Failed, MihomoNodeProxyImportFailure{Name: binding.Name, Reason: "saved proxy endpoint does not match its node binding"})
				if binding.New {
					toDeactivate = append(toDeactivate, binding.Name)
				}
				continue
			}
			result.Existing = append(result.Existing, *existing)
			continue
		}
		label := []rune(strings.TrimSpace(binding.DisplayName))
		if len(label) > 90 {
			label = label[:90]
		}
		if len(label) == 0 {
			label = []rune(binding.Name)
		}
		created, createErr := s.CreateProxy(ctx, &CreateProxyInput{
			Name: "Mihomo · " + string(label), Protocol: "http", Host: "127.0.0.1", Port: binding.Port,
			MihomoNodeID: binding.Name, FallbackMode: FallbackModeNone, ExpiryWarnDays: 7,
		})
		if createErr != nil {
			result.Failed = append(result.Failed, MihomoNodeProxyImportFailure{Name: binding.Name, Reason: "could not register static proxy"})
			if binding.New {
				toDeactivate = append(toDeactivate, binding.Name)
			}
			continue
		}
		result.Created = append(result.Created, *created)
	}
	if len(toDeactivate) > 0 {
		// A failed database write must not leave an unused local listener alive.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if err := kernel.DeactivateStaticNodes(cleanup, toDeactivate); err != nil {
			return result, infraerrors.New(http.StatusInternalServerError, "MIHOMO_STATIC_ROLLBACK_FAILED", "could not deactivate an unregistered static listener")
		}
	}
	return result, nil
}
