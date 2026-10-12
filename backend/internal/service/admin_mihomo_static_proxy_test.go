package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/stretchr/testify/require"
)

type staticNodeRepo struct {
	ProxyRepository
	items map[string]*Proxy
	fail  string
}

func (r *staticNodeRepo) GetByMihomoNodeID(_ context.Context, name string) (*Proxy, error) {
	return r.items[name], nil
}
func (r *staticNodeRepo) Create(_ context.Context, proxy *Proxy) error {
	if r.fail == proxy.MihomoNodeID {
		return errors.New("fixture database failure")
	}
	proxy.ID = int64(len(r.items) + 1)
	r.items[proxy.MihomoNodeID] = proxy
	return nil
}
func (r *staticNodeRepo) GetByID(_ context.Context, id int64) (*Proxy, error) {
	for _, proxy := range r.items {
		if proxy.ID == id {
			copy := *proxy
			return &copy, nil
		}
	}
	return nil, ErrProxyNotFound
}
func (r *staticNodeRepo) Update(_ context.Context, proxy *Proxy) error {
	r.items[proxy.MihomoNodeID] = proxy
	return nil
}

type staticNodeKernel struct {
	names       []string
	deactivated []string
	err         error
}

func (k *staticNodeKernel) BindStaticNodes(_ context.Context, names []string) ([]mihomo.StaticNodeBinding, error) {
	k.names = names
	if k.err != nil {
		return nil, k.err
	}
	result := make([]mihomo.StaticNodeBinding, len(names))
	for i, name := range names {
		result[i] = mihomo.StaticNodeBinding{Name: name, DisplayName: name, Port: 32000 + i, New: true}
	}
	return result, nil
}
func (k *staticNodeKernel) DeactivateStaticNodes(_ context.Context, names []string) error {
	k.deactivated = append(k.deactivated, names...)
	return nil
}

func TestCreateMihomoStaticProxiesDeduplicatesAndPreservesAccountEndpoint(t *testing.T) {
	repo := &staticNodeRepo{items: map[string]*Proxy{}}
	svc := &adminServiceImpl{proxyRepo: repo}
	kernel := &staticNodeKernel{}
	result, err := svc.CreateMihomoNodeProxies(t.Context(), kernel, []string{"node-one", "DYNAMIC-two", "node-one"})
	require.NoError(t, err)
	require.Len(t, result.Created, 2)
	require.Equal(t, []string{"node-one", "DYNAMIC-two"}, kernel.names)
	require.Equal(t, "http://127.0.0.1:32000", result.Created[0].URL())
	require.Equal(t, FallbackModeNone, result.Created[0].FallbackMode)
	require.Equal(t, "node-one", result.Created[0].MihomoNodeID)
	result, err = svc.CreateMihomoNodeProxies(t.Context(), kernel, []string{"node-one", "DYNAMIC-two"})
	require.NoError(t, err)
	require.Empty(t, result.Created)
	require.Len(t, result.Existing, 2)
	_, err = svc.UpdateProxy(t.Context(), 1, &UpdateProxyInput{Host: "another-exit"})
	require.ErrorContains(t, err, "managed by the node")
	updated, err := svc.UpdateProxy(t.Context(), 1, &UpdateProxyInput{Name: "renamed"})
	require.NoError(t, err)
	require.Equal(t, "renamed", updated.Name)
	require.Equal(t, "http://127.0.0.1:32000", updated.URL())
}

func TestCreateMihomoStaticProxiesRollsBackOnlyFailedNewListeners(t *testing.T) {
	repo := &staticNodeRepo{items: map[string]*Proxy{}, fail: "DYNAMIC-two"}
	svc := &adminServiceImpl{proxyRepo: repo}
	kernel := &staticNodeKernel{}
	result, err := svc.CreateMihomoNodeProxies(t.Context(), kernel, []string{"node-one", "DYNAMIC-two"})
	require.NoError(t, err)
	require.Len(t, result.Created, 1)
	require.Len(t, result.Failed, 1)
	require.Equal(t, []string{"DYNAMIC-two"}, kernel.deactivated)
	require.Contains(t, repo.items, "node-one")
	kernel.err = mihomo.ErrUnknownNode
	_, err = svc.CreateMihomoNodeProxies(t.Context(), kernel, []string{"missing"})
	require.Error(t, err)
	require.Len(t, repo.items, 1)
}
