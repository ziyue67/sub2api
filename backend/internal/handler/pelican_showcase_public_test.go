//go:build unit

package handler

import (
	"bytes"
	"compress/gzip"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type publicPelicanSourceStub struct {
	view          *service.PelicanShowcaseView
	item          *service.PelicanShowcaseItem
	viewErr       error
	itemErr       error
	apiDisabled   bool
	settingsErr   error
	settingsReads atomic.Int64
	viewReads     atomic.Int64
	itemReads     atomic.Int64
	started       chan struct{}
	release       chan struct{}
}

func (s *publicPelicanSourceStub) Settings(context.Context) (service.PelicanShowcaseRuntime, error) {
	s.settingsReads.Add(1)
	return service.PelicanShowcaseRuntime{Enabled: s.view.Enabled, APIEnabled: !s.apiDisabled}, s.settingsErr
}

func (s *publicPelicanSourceStub) View(ctx context.Context, _ time.Time) (*service.PelicanShowcaseView, error) {
	s.viewReads.Add(1)
	if s.started != nil {
		s.started <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.view, s.viewErr
}

func (s *publicPelicanSourceStub) Item(context.Context, int64, time.Time) (*service.PelicanShowcaseItem, error) {
	s.itemReads.Add(1)
	return s.item, s.itemErr
}

func newPublicPelicanFixture() (*PelicanShowcaseHandler, *publicPelicanSourceStub) {
	item := &service.PelicanShowcaseItem{ID: 7, GroupID: 3, ModelID: "test-model", ReasoningEffort: "high",
		LatencyMs: 1200, GeneratedAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
		ResponseText: "<!doctype html><html><body>" + strings.Repeat("<svg>pelican</svg>", 300) + "</body></html>"}
	source := &publicPelicanSourceStub{item: item, view: &service.PelicanShowcaseView{Enabled: true, MaxItems: 20, RetentionDays: 7,
		Groups: []*service.PelicanShowcaseGroup{{ID: 3, Name: "Public group", Platform: "openai", Items: []*service.PelicanShowcaseItem{item}},
			{ID: 4, Name: "Empty group", Items: []*service.PelicanShowcaseItem{}}}}}
	return &PelicanShowcaseHandler{public: newPelicanPublicCache(source)}, source
}

func publicPelicanRequest(h *PelicanShowcaseHandler, method, id string, headers map[string]string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	path := pelicanPublicPath
	if id != "" {
		path += "/items/" + id
		c.Params = gin.Params{{Key: "id", Value: id}}
	}
	c.Request = httptest.NewRequest(method, path, nil)
	for key, value := range headers {
		c.Request.Header.Set(key, value)
	}
	if id == "" {
		h.PublicList(c)
	} else {
		h.PublicItem(c)
	}
	c.Writer.WriteHeaderNow() // Normally flushed by gin.Engine after the handler returns.
	return w
}

func TestPelicanPublicManifestAndBody(t *testing.T) {
	h, source := newPublicPelicanFixture()
	w := publicPelicanRequest(h, http.MethodGet, "", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var manifest struct {
		Data pelicanPublicManifest `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &manifest))
	require.Equal(t, 1, manifest.Data.SchemaVersion)
	require.Equal(t, "published_successes", manifest.Data.ResultScope)
	require.Equal(t, 60, manifest.Data.PollIntervalSeconds)
	require.Len(t, manifest.Data.Groups, 2)
	require.NotNil(t, manifest.Data.Groups[1].Items)
	require.Equal(t, pelicanPublicPath+"/items/7", manifest.Data.Groups[0].Items[0].ContentURL)
	require.Equal(t, source.item.GeneratedAt, *manifest.Data.LatestGeneratedAt)
	for _, forbidden := range []string{"response_text", "<svg", "account_id", "prompt", "cost", "source_result_id"} {
		require.NotContains(t, w.Body.String(), forbidden)
	}
	require.EqualValues(t, 0, source.itemReads.Load())
	item := publicPelicanRequest(h, http.MethodGet, "7", nil)
	require.Equal(t, http.StatusOK, item.Code)
	require.Equal(t, "application/json; charset=utf-8", item.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", item.Header().Get("X-Content-Type-Options"))
	var detail struct {
		Data pelicanPublicItem `json:"data"`
	}
	require.NoError(t, json.Unmarshal(item.Body.Bytes(), &detail))
	require.Equal(t, source.item.ResponseText, *detail.Data.ResponseText)
	require.Equal(t, "success", detail.Data.Status)
	publicPelicanRequest(h, http.MethodGet, "", nil)
	publicPelicanRequest(h, http.MethodGet, "7", nil)
	require.EqualValues(t, 1, source.viewReads.Load())
	require.EqualValues(t, 1, source.itemReads.Load())
}

func TestPelicanPublicConditionalReadsAndCompression(t *testing.T) {
	h, source := newPublicPelicanFixture()
	for _, id := range []string{"", "7"} {
		first := publicPelicanRequest(h, http.MethodGet, id, nil)
		etag := first.Header().Get("ETag")
		require.NotEmpty(t, etag)
		for _, candidate := range []string{etag, strings.TrimPrefix(etag, "W/"), `"other", ` + etag, "*"} {
			cached := publicPelicanRequest(h, http.MethodGet, id, map[string]string{"If-None-Match": candidate})
			require.Equal(t, http.StatusNotModified, cached.Code, candidate)
			require.Empty(t, cached.Body.String())
			require.Equal(t, etag, cached.Header().Get("ETag"))
		}
		unmatched := publicPelicanRequest(h, http.MethodGet, id, map[string]string{"If-None-Match": `"wrong"`})
		require.Equal(t, http.StatusOK, unmatched.Code)
		head := publicPelicanRequest(h, http.MethodHead, id, nil)
		require.Equal(t, http.StatusOK, head.Code)
		require.Empty(t, head.Body.String())
		require.Equal(t, first.Header().Get("Content-Length"), head.Header().Get("Content-Length"))
	}
	plain := publicPelicanRequest(h, http.MethodGet, "7", nil)
	compressed := publicPelicanRequest(h, http.MethodGet, "7", map[string]string{"Accept-Encoding": "br, gzip"})
	require.Equal(t, "gzip", compressed.Header().Get("Content-Encoding"))
	require.Contains(t, compressed.Header().Values("Vary"), "Accept-Encoding")
	require.Equal(t, plain.Header().Get("ETag"), compressed.Header().Get("ETag"))
	zr, err := gzip.NewReader(bytes.NewReader(compressed.Body.Bytes()))
	require.NoError(t, err)
	defer zr.Close()
	decoded, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.Equal(t, plain.Body.Bytes(), decoded)
	require.Less(t, compressed.Body.Len(), plain.Body.Len()/2)
	require.EqualValues(t, 1, source.viewReads.Load())
	require.EqualValues(t, 1, source.itemReads.Load())
	for header, expected := range map[string]bool{"": false, "br": false, "gzip": true, "gzip;q=0": false, "gzip;q=0, *;q=1": false, "*;q=1, gzip;q=0": false, "gzip; q=0.5": true, "gzip;q=bad": false, "gzip;q=NaN": false, "gzip;q=2": false} {
		require.Equal(t, expected, pelicanAcceptsGzip(header), header)
	}
}

func TestPelicanPublicVisibilityAndRefresh(t *testing.T) {
	h, source := newPublicPelicanFixture()
	first := publicPelicanRequest(h, http.MethodGet, "", nil)
	publicPelicanRequest(h, http.MethodGet, "7", nil)
	for _, id := range []string{"0", "-1", "abc", "9223372036854775808"} {
		require.Equal(t, http.StatusBadRequest, publicPelicanRequest(h, http.MethodGet, id, nil).Code)
	}
	missing := publicPelicanRequest(h, http.MethodGet, "999", nil)
	require.Equal(t, http.StatusNotFound, missing.Code)
	require.Equal(t, "no-store", missing.Header().Get("Cache-Control"))
	require.EqualValues(t, 1, source.itemReads.Load(), "unknown IDs never reach the DB")

	// The server snapshot stays shared, but HTTP caches must revalidate auth.
	h.public.snapshot.expiresAt = time.Now().Add(10 * time.Second)
	remaining := publicPelicanRequest(h, http.MethodGet, "", nil)
	require.Equal(t, "private, no-cache, must-revalidate", remaining.Header().Get("Cache-Control"))

	// Unchanged refreshes preserve the validator; no wall-clock field churn.
	h.public.snapshot.expiresAt = time.Now().Add(-time.Second)
	unchanged := publicPelicanRequest(h, http.MethodGet, "", map[string]string{"If-None-Match": first.Header().Get("ETag")})
	require.Equal(t, http.StatusNotModified, unchanged.Code)
	require.EqualValues(t, 2, source.viewReads.Load())

	// Removing a snapshot changes the manifest, and makes its cached body 404.
	source.view.Groups[0].Items = nil
	h.public.invalidate()
	changed := publicPelicanRequest(h, http.MethodGet, "", map[string]string{"If-None-Match": first.Header().Get("ETag")})
	require.Equal(t, http.StatusOK, changed.Code)
	require.NotEqual(t, first.Header().Get("ETag"), changed.Header().Get("ETag"))
	require.Equal(t, http.StatusNotFound, publicPelicanRequest(h, http.MethodGet, "7", nil).Code)

	source.view.Enabled = false
	h.public.invalidate()
	disabled := publicPelicanRequest(h, http.MethodGet, "", nil)
	require.Contains(t, disabled.Body.String(), `"enabled":false`)
	require.Contains(t, disabled.Body.String(), `"groups":[]`)
	require.NotContains(t, disabled.Body.String(), "Public group")
	require.Equal(t, http.StatusNotFound, publicPelicanRequest(h, http.MethodGet, "7", nil).Code)
}

func TestPelicanPublicErrorsAreNotCached(t *testing.T) {
	h, source := newPublicPelicanFixture()
	source.viewErr = errors.New("database unavailable")
	failed := publicPelicanRequest(h, http.MethodGet, "", nil)
	require.Equal(t, http.StatusServiceUnavailable, failed.Code)
	require.Equal(t, "no-store", failed.Header().Get("Cache-Control"))
	require.Equal(t, "60", failed.Header().Get("Retry-After"))
	require.Empty(t, failed.Header().Get("ETag"))
	require.NotContains(t, failed.Body.String(), "database")
	source.viewErr = nil
	require.Equal(t, http.StatusOK, publicPelicanRequest(h, http.MethodGet, "", nil).Code)
	source.itemErr = service.ErrPelicanShowcaseItemNotFound
	require.Equal(t, http.StatusNotFound, publicPelicanRequest(h, http.MethodGet, "7", nil).Code)
	source.itemErr = nil
	require.Equal(t, http.StatusOK, publicPelicanRequest(h, http.MethodGet, "7", nil).Code)
}

func TestPelicanPublicAPISwitchRejectsCachedAndConditionalReads(t *testing.T) {
	h, source := newPublicPelicanFixture()
	etags := map[string]string{}
	for _, id := range []string{"", "7"} {
		warm := publicPelicanRequest(h, http.MethodGet, id, nil)
		require.Equal(t, http.StatusOK, warm.Code)
		etags[id] = warm.Header().Get("ETag")
	}
	require.NotNil(t, h.public.snapshot.item(7), "warm both caches before toggling the API switch")
	source.apiDisabled = true
	for _, id := range []string{"", "7"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, validator := range []string{"", etags[id], "*"} {
				w := publicPelicanRequest(h, method, id, map[string]string{"If-None-Match": validator})
				require.Equal(t, http.StatusForbidden, w.Code, "%s id=%s validator=%s", method, id, validator)
				require.Contains(t, w.Body.String(), `"reason":"PELICAN_SHOWCASE_API_DISABLED"`)
				require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
				require.Empty(t, w.Header().Get("ETag"))
				require.NotContains(t, w.Body.String(), source.item.ResponseText)
			}
		}
	}
	require.EqualValues(t, 1, source.viewReads.Load(), "denied reads cannot consult the manifest cache or source")
	require.EqualValues(t, 1, source.itemReads.Load(), "denied reads cannot consult cached item bodies or source")
	require.EqualValues(t, 14, source.settingsReads.Load(), "every request rechecks the runtime switch")
	source.apiDisabled = false
	require.Equal(t, http.StatusNotModified, publicPelicanRequest(h, http.MethodGet, "7", map[string]string{"If-None-Match": etags["7"]}).Code)
}

func TestPelicanPublicSettingsReadFailureRejectsCachedReads(t *testing.T) {
	h, source := newPublicPelicanFixture()
	for _, id := range []string{"", "7"} {
		require.Equal(t, http.StatusOK, publicPelicanRequest(h, http.MethodGet, id, nil).Code)
	}
	source.settingsErr = errors.New("settings database unavailable")
	for _, id := range []string{"", "7"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := publicPelicanRequest(h, method, id, map[string]string{"If-None-Match": "*"})
			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			require.Contains(t, w.Body.String(), `"reason":"PELICAN_SHOWCASE_API_UNAVAILABLE"`)
			require.NotContains(t, w.Body.String(), "database")
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.Equal(t, "60", w.Header().Get("Retry-After"))
			require.Empty(t, w.Header().Get("ETag"))
		}
	}
	require.EqualValues(t, 1, source.viewReads.Load())
	require.EqualValues(t, 1, source.itemReads.Load())
}

func TestPelicanPublicConcurrentReadersShareLoads(t *testing.T) {
	h, source := newPublicPelicanFixture()
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := publicPelicanRequest(h, http.MethodGet, "7", nil)
			if w.Code != http.StatusOK {
				t.Errorf("concurrent read returned %d", w.Code)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, source.viewReads.Load())
	require.EqualValues(t, 1, source.itemReads.Load())
}

func TestPelicanPublicCancelledReaderDoesNotCancelSharedRefresh(t *testing.T) {
	h, source := newPublicPelicanFixture()
	source.started, source.release = make(chan struct{}, 2), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := h.public.manifest(ctx); first <- err }()
	<-source.started
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)
	close(source.release)
	_, err := h.public.manifest(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, source.viewReads.Load())
}

func TestPelicanPublicInvalidateDuringRefreshDoesNotRepopulateCache(t *testing.T) {
	h, source := newPublicPelicanFixture()
	source.started, source.release = make(chan struct{}, 2), make(chan struct{})
	finished := make(chan error, 1)
	go func() { _, err := h.public.manifest(context.Background()); finished <- err }()
	<-source.started
	h.public.invalidate()
	close(source.release)
	require.NoError(t, <-finished)
	require.Nil(t, h.public.snapshot)
	_, err := h.public.manifest(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 2, source.viewReads.Load())
}

func TestPelicanPublicBodyCacheHasMemoryAndEntryLimits(t *testing.T) {
	snapshot := &pelicanPublicSnapshot{items: make(map[int64]*list.Element)}
	for id := int64(1); id <= 100; id++ {
		snapshot.put(id, &pelicanPublicBody{json: []byte(strconv.FormatInt(id, 10))})
	}
	require.Len(t, snapshot.items, pelicanPublicMaxEntries)
	require.Nil(t, snapshot.item(1))
	require.NotNil(t, snapshot.item(100))
	snapshot.put(101, &pelicanPublicBody{json: make([]byte, pelicanPublicMaxBytes)})
	require.Len(t, snapshot.items, 1)
	require.Equal(t, pelicanPublicMaxBytes, snapshot.bytes)
	snapshot.put(102, &pelicanPublicBody{json: make([]byte, pelicanPublicMaxBytes+1)})
	require.Len(t, snapshot.items, 1)
	require.Nil(t, snapshot.item(102))
}
