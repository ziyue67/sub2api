//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/stretchr/testify/require"
)

type nodeCheckProber struct {
	exit    *ProxyExitInfo
	latency int64
	err     error
	urls    []string
}

func (p *nodeCheckProber) ProbeProxy(_ context.Context, proxyURL string) (*ProxyExitInfo, int64, error) {
	p.urls = append(p.urls, proxyURL)
	if p.err != nil {
		return nil, 0, p.err
	}
	return p.exit, p.latency, nil
}

type nodeCheckLatencyCache struct {
	mu     sync.Mutex
	stored map[int64]*ProxyLatencyInfo
	sets   int
}

func (c *nodeCheckLatencyCache) GetProxyLatencies(_ context.Context, ids []int64) (map[int64]*ProxyLatencyInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := map[int64]*ProxyLatencyInfo{}
	for _, id := range ids {
		if info := c.stored[id]; info != nil {
			result[id] = info
		}
	}
	return result, nil
}

func (c *nodeCheckLatencyCache) SetProxyLatency(_ context.Context, id int64, info *ProxyLatencyInfo) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stored == nil {
		c.stored = map[int64]*ProxyLatencyInfo{}
	}
	c.stored[id] = info
	c.sets++
	return nil
}

type nodeCheckProxyRepo struct {
	ProxyRepository
	proxy *Proxy
}

func (r *nodeCheckProxyRepo) GetByID(context.Context, int64) (*Proxy, error) { return r.proxy, nil }

type fakeMihomoKernel struct {
	proxyURL string
	err      error
	skip     bool
	probed   []string
	recorded map[string]mihomo.NodeCheck
}

func (k *fakeMihomoKernel) ProbeNode(ctx context.Context, name string, check func(ctx context.Context, proxyURL string)) error {
	k.probed = append(k.probed, name)
	if k.err != nil {
		return k.err
	}
	if !k.skip {
		check(ctx, k.proxyURL)
	}
	return nil
}

func (k *fakeMihomoKernel) RecordNodeCheck(name string, check mihomo.NodeCheck) {
	if k.recorded == nil {
		k.recorded = map[string]mihomo.NodeCheck{}
	}
	k.recorded[name] = check
}

// rejectingConnectProxy stands in for a node: every AI target CONNECT is
// refused, so quality checks complete offline with failed target items.
func rejectingConnectProxy(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var connects atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			connects.Add(1)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://"), &connects
}

func testExit() *ProxyExitInfo {
	return &ProxyExitInfo{IP: "203.0.113.9", City: "Tokyo", Region: "Tokyo", Country: "Japan", CountryCode: "JP"}
}

func TestTestMihomoNodeRecordsResultOutsideProxyCache(t *testing.T) {
	prober := &nodeCheckProber{exit: testExit(), latency: 42}
	cache := &nodeCheckLatencyCache{}
	svc := &adminServiceImpl{proxyProber: prober, proxyLatencyCache: cache}
	kernel := &fakeMihomoKernel{proxyURL: "http://probe:one-time@127.0.0.1:1"}

	result, err := svc.TestMihomoNode(context.Background(), kernel, "node-one")
	require.NoError(t, err)
	require.True(t, result.Success)
	require.EqualValues(t, 42, result.LatencyMs)
	require.Equal(t, "Tokyo", result.City)
	require.Equal(t, []string{kernel.proxyURL}, prober.urls, "the test runs through the node's isolated listener")
	require.Zero(t, cache.sets, "node checks never touch the static proxy latency cache")

	check := kernel.recorded["node-one"]
	require.Equal(t, "success", check.LatencyStatus)
	require.EqualValues(t, 42, *check.LatencyMs)
	require.Equal(t, "Proxy is accessible", check.LatencyMessage)
	require.Equal(t, "203.0.113.9", check.IPAddress)
	require.Equal(t, "JP", check.CountryCode)
	require.Equal(t, "Tokyo", check.City)
	require.Positive(t, check.CheckedAt)
	require.Nil(t, check.QualityChecked, "a connection test carries no quality grade")
}

func TestTestMihomoNodeRecordsFailedConnection(t *testing.T) {
	svc := &adminServiceImpl{proxyProber: &nodeCheckProber{err: errors.New("proxy connection failed: EOF")}}
	kernel := &fakeMihomoKernel{proxyURL: "http://probe:one-time@127.0.0.1:1"}

	result, err := svc.TestMihomoNode(context.Background(), kernel, "DYNAMIC-one")
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, "proxy connection failed: EOF", result.Message)
	check := kernel.recorded["DYNAMIC-one"]
	require.Equal(t, "failed", check.LatencyStatus)
	require.Equal(t, "proxy connection failed: EOF", check.LatencyMessage)
	require.Nil(t, check.LatencyMs)
	require.Empty(t, check.IPAddress)
}

func TestCheckMihomoNodeQualityRoutesTargetsThroughNode(t *testing.T) {
	address, connects := rejectingConnectProxy(t)
	kernel := &fakeMihomoKernel{proxyURL: "http://probe:one-time@" + address}
	cache := &nodeCheckLatencyCache{}
	svc := &adminServiceImpl{proxyProber: &nodeCheckProber{exit: testExit(), latency: 35}, proxyLatencyCache: cache}
	pooled, err := httpclient.GetClient(httpclient.Options{ProxyURL: kernel.proxyURL, Timeout: proxyQualityRequestTimeout, ResponseHeaderTimeout: proxyQualityResponseHeaderTimeout})
	require.NoError(t, err)

	result, err := svc.CheckMihomoNodeQuality(context.Background(), kernel, "node-one")
	require.NoError(t, err)
	require.EqualValues(t, len(proxyQualityTargets), connects.Load(), "every target request goes through the node")
	require.Len(t, result.Items, len(proxyQualityTargets)+1)
	require.Equal(t, "pass", result.Items[0].Status)
	require.Equal(t, len(proxyQualityTargets), result.FailedCount)
	require.Equal(t, 100-22*len(proxyQualityTargets), result.Score)
	require.Equal(t, "F", result.Grade)
	require.Zero(t, result.ProxyID)
	require.Zero(t, cache.sets)

	check := kernel.recorded["node-one"]
	require.Equal(t, "success", check.LatencyStatus, "base connectivity decides the latency status")
	require.EqualValues(t, 35, *check.LatencyMs)
	require.Equal(t, result.Summary, check.LatencyMessage)
	require.Equal(t, "Tokyo", check.City, "the exit location is kept even though the result omits it")
	require.Equal(t, "failed", check.QualityStatus)
	require.Equal(t, result.Score, *check.QualityScore)
	require.Equal(t, "F", check.QualityGrade)
	require.Equal(t, result.CheckedAt, *check.QualityChecked)

	fresh, err := httpclient.GetClient(httpclient.Options{ProxyURL: kernel.proxyURL, Timeout: proxyQualityRequestTimeout, ResponseHeaderTimeout: proxyQualityResponseHeaderTimeout})
	require.NoError(t, err)
	require.NotSame(t, pooled, fresh, "clients for the short-lived listener are not kept")
	httpclient.EvictProxyClients(kernel.proxyURL)
}

func TestMihomoNodeCheckErrorsAreMappedAndNotRecorded(t *testing.T) {
	svc := &adminServiceImpl{proxyProber: &nodeCheckProber{exit: testExit()}}
	for _, tc := range []struct {
		name   string
		kernel *fakeMihomoKernel
		code   int
		reason string
	}{
		{"unknown node", &fakeMihomoKernel{err: mihomo.ErrUnknownNode}, http.StatusNotFound, "MIHOMO_NODE_NOT_FOUND"},
		{"kernel unavailable", &fakeMihomoKernel{err: errors.New("install the kernel first")}, http.StatusConflict, "MIHOMO_NODE_CHECK_UNAVAILABLE"},
		{"check skipped", &fakeMihomoKernel{skip: true}, http.StatusConflict, "MIHOMO_NODE_CHECK_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.TestMihomoNode(context.Background(), tc.kernel, "node-one")
			require.Equal(t, tc.code, infraerrors.Code(err))
			require.Equal(t, tc.reason, infraerrors.Reason(err))
			_, err = svc.CheckMihomoNodeQuality(context.Background(), tc.kernel, "node-one")
			require.Equal(t, tc.code, infraerrors.Code(err))
			require.Empty(t, tc.kernel.recorded)
		})
	}
}

func TestStaticProxyChecksStillWriteLatencyCache(t *testing.T) {
	address, connects := rejectingConnectProxy(t)
	host, port, found := strings.Cut(address, ":")
	require.True(t, found)
	portNumber, err := strconv.Atoi(port)
	require.NoError(t, err)
	proxy := &Proxy{ID: 7, Protocol: "http", Host: host, Port: portNumber}
	cache := &nodeCheckLatencyCache{}
	prober := &nodeCheckProber{exit: testExit(), latency: 51}
	svc := &adminServiceImpl{proxyRepo: &nodeCheckProxyRepo{proxy: proxy}, proxyProber: prober, proxyLatencyCache: cache}

	tested, err := svc.TestProxy(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, tested.Success)
	info := cache.stored[7]
	require.True(t, info.Success)
	require.EqualValues(t, 51, *info.LatencyMs)
	require.Equal(t, "Proxy is accessible", info.Message)
	require.Equal(t, "Tokyo", info.City)
	require.Nil(t, info.QualityCheckedAt)

	quality, err := svc.CheckProxyQuality(context.Background(), 7)
	require.NoError(t, err)
	require.EqualValues(t, 7, quality.ProxyID)
	require.EqualValues(t, len(proxyQualityTargets), connects.Load())
	info = cache.stored[7]
	require.Equal(t, quality.Grade, info.QualityGrade)
	require.Equal(t, quality.CheckedAt, *info.QualityCheckedAt)
	require.Equal(t, "Tokyo", info.City)

	prober.err = errors.New("proxy connection failed: EOF")
	failed, err := svc.TestProxy(context.Background(), 7)
	require.NoError(t, err)
	require.False(t, failed.Success)
	info = cache.stored[7]
	require.False(t, info.Success)
	require.Nil(t, info.LatencyMs)
	require.Equal(t, quality.Grade, info.QualityGrade, "a failed connection test keeps the cached quality grade")
	require.Equal(t, []string{proxy.URL(), proxy.URL(), proxy.URL()}, prober.urls)

	svc.proxyProber = nil
	unconfigured, err := svc.TestProxy(context.Background(), 7)
	require.NoError(t, err)
	require.False(t, unconfigured.Success, "a missing prober is reported instead of panicking")
}
