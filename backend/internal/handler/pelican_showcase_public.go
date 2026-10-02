package handler

import (
	"bytes"
	"compress/gzip"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

const (
	pelicanPublicPath       = "/api/v1/public/pelican-showcase"
	pelicanPublicTTL        = time.Minute
	pelicanPublicTimeout    = 5 * time.Second
	pelicanPublicMaxBytes   = 16 << 20
	pelicanPublicMaxEntries = 64
)

// The public projection deliberately contains no account, prompt, cost, or
// admin-history fields. HTML/SVG is returned as data in JSON, never executed.
type pelicanPublicItem struct {
	ID              int64     `json:"id"`
	GroupID         int64     `json:"group_id"`
	ModelID         string    `json:"model_id"`
	ReasoningEffort string    `json:"reasoning_effort"`
	Status          string    `json:"status"`
	LatencyMs       int64     `json:"latency_ms"`
	GeneratedAt     time.Time `json:"generated_at"`
	ContentURL      string    `json:"content_url"`
	ResponseText    *string   `json:"response_text,omitempty"`
}

type pelicanPublicGroup struct {
	ID       int64               `json:"id"`
	Name     string              `json:"name"`
	Platform string              `json:"platform"`
	Items    []pelicanPublicItem `json:"items"`
}

type pelicanPublicManifest struct {
	SchemaVersion       int                  `json:"schema_version"`
	Enabled             bool                 `json:"enabled"`
	ResultScope         string               `json:"result_scope"`
	PollIntervalSeconds int                  `json:"poll_interval_seconds"`
	MaxItemsPerGroup    int                  `json:"max_items_per_group"`
	RetentionDays       int                  `json:"retention_days"`
	LatestGeneratedAt   *time.Time           `json:"latest_generated_at"`
	Groups              []pelicanPublicGroup `json:"groups"`
}

func publicPelicanItem(item *service.PelicanShowcaseItem) pelicanPublicItem {
	return pelicanPublicItem{
		ID: item.ID, GroupID: item.GroupID, ModelID: item.ModelID,
		ReasoningEffort: item.ReasoningEffort, Status: "success",
		LatencyMs: item.LatencyMs, GeneratedAt: item.GeneratedAt,
		ContentURL: pelicanPublicPath + "/items/" + strconv.FormatInt(item.ID, 10),
	}
}

type pelicanPublicSource interface {
	View(context.Context, time.Time) (*service.PelicanShowcaseView, error)
	Item(context.Context, int64, time.Time) (*service.PelicanShowcaseItem, error)
}

type pelicanPublicBody struct {
	json []byte
	gzip []byte
	etag string
}

func newPelicanPublicBody(value any) (*pelicanPublicBody, error) {
	body, err := json.Marshal(response.Response{Code: 0, Message: "success", Data: value})
	if err != nil {
		return nil, err
	}
	// A weak validator covers both encodings and compression by reverse proxies.
	entry := &pelicanPublicBody{json: body, etag: fmt.Sprintf(`W/"%x"`, sha256.Sum256(body))}
	if len(body) >= 1024 {
		var buf bytes.Buffer
		writer := gzip.NewWriter(&buf)
		if _, err := writer.Write(body); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		if buf.Len() < len(body) {
			entry.gzip = buf.Bytes()
		}
	}
	return entry, nil
}

type pelicanPublicCachedItem struct {
	id   int64
	body *pelicanPublicBody
}

// A single shared manifest bounds the DB work independently of caller count.
// Each generation owns a byte- and count-bounded LRU of serialized item bodies.
// Refreshing the manifest drops the old LRU, so removed/expired items cannot be
// served indefinitely. No cache entries are allocated for arbitrary missing IDs.
type pelicanPublicSnapshot struct {
	body      *pelicanPublicBody
	expiresAt time.Time
	visible   map[int64]struct{}
	mu        sync.Mutex
	items     map[int64]*list.Element
	lru       list.List
	bytes     int
}

func (s *pelicanPublicSnapshot) item(id int64) *pelicanPublicBody {
	s.mu.Lock()
	defer s.mu.Unlock()
	if element := s.items[id]; element != nil {
		item, ok := element.Value.(pelicanPublicCachedItem)
		if !ok {
			return nil
		}
		s.lru.MoveToFront(element)
		return item.body
	}
	return nil
}

func (s *pelicanPublicSnapshot) put(id int64, body *pelicanPublicBody) {
	size := len(body.json) + len(body.gzip)
	if size > pelicanPublicMaxBytes {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[id] != nil {
		return
	}
	for len(s.items) >= pelicanPublicMaxEntries || s.bytes+size > pelicanPublicMaxBytes {
		element := s.lru.Back()
		old, ok := element.Value.(pelicanPublicCachedItem)
		if !ok {
			return
		}
		s.bytes -= len(old.body.json) + len(old.body.gzip)
		delete(s.items, old.id)
		s.lru.Remove(element)
	}
	s.items[id] = s.lru.PushFront(pelicanPublicCachedItem{id: id, body: body})
	s.bytes += size
}

type pelicanPublicCache struct {
	source     pelicanPublicSource
	mu         sync.Mutex
	snapshot   *pelicanPublicSnapshot
	generation uint64
	flights    singleflight.Group
}

func newPelicanPublicCache(source pelicanPublicSource) *pelicanPublicCache {
	return &pelicanPublicCache{source: source}
}

func (p *pelicanPublicCache) invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.generation++
	p.snapshot = nil
}

// One disconnected caller must not cancel a refresh needed by other callers.
// Shared reads still have a hard timeout; individual waiters remain cancellable.
func (p *pelicanPublicCache) shared(ctx context.Context, key string, load func(context.Context) (any, error)) (any, error) {
	result := p.flights.DoChan(key, func() (any, error) {
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pelicanPublicTimeout)
		defer cancel()
		return load(readCtx)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case value := <-result:
		return value.Val, value.Err
	}
}

func (p *pelicanPublicCache) manifest(ctx context.Context) (*pelicanPublicSnapshot, error) {
	p.mu.Lock()
	current, generation := p.snapshot, p.generation
	p.mu.Unlock()
	if current != nil && time.Now().Before(current.expiresAt) {
		return current, nil
	}
	value, err := p.shared(ctx, fmt.Sprintf("manifest:%d", generation), func(ctx context.Context) (any, error) {
		p.mu.Lock()
		current := p.snapshot
		p.mu.Unlock()
		if current != nil && time.Now().Before(current.expiresAt) {
			return current, nil
		}
		now := time.Now()
		view, err := p.source.View(ctx, now)
		if err != nil {
			return nil, err
		}
		manifest := pelicanPublicManifest{
			SchemaVersion: 1, Enabled: view.Enabled, ResultScope: "published_successes",
			PollIntervalSeconds: int(pelicanPublicTTL / time.Second),
			MaxItemsPerGroup:    view.MaxItems, RetentionDays: view.RetentionDays,
			Groups: []pelicanPublicGroup{},
		}
		snapshot := &pelicanPublicSnapshot{
			expiresAt: now.Add(pelicanPublicTTL), visible: make(map[int64]struct{}),
			items: make(map[int64]*list.Element),
		}
		if view.Enabled {
			for _, group := range view.Groups {
				publicGroup := pelicanPublicGroup{ID: group.ID, Name: group.Name, Platform: group.Platform, Items: []pelicanPublicItem{}}
				for _, item := range group.Items {
					publicGroup.Items = append(publicGroup.Items, publicPelicanItem(item))
					snapshot.visible[item.ID] = struct{}{}
					if manifest.LatestGeneratedAt == nil || item.GeneratedAt.After(*manifest.LatestGeneratedAt) {
						generated := item.GeneratedAt
						manifest.LatestGeneratedAt = &generated
					}
				}
				manifest.Groups = append(manifest.Groups, publicGroup)
			}
		}
		snapshot.body, err = newPelicanPublicBody(manifest)
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		if p.generation == generation {
			p.snapshot = snapshot
		}
		p.mu.Unlock()
		return snapshot, nil
	})
	if err != nil {
		return nil, err
	}
	snapshot, ok := value.(*pelicanPublicSnapshot)
	if !ok || snapshot == nil {
		return nil, fmt.Errorf("unexpected pelican manifest cache value %T", value)
	}
	return snapshot, nil
}

func (p *pelicanPublicCache) item(ctx context.Context, snapshot *pelicanPublicSnapshot, id int64) (*pelicanPublicBody, error) {
	if _, visible := snapshot.visible[id]; !visible {
		return nil, service.ErrPelicanShowcaseItemNotFound
	}
	if body := snapshot.item(id); body != nil {
		return body, nil
	}
	value, err := p.shared(ctx, fmt.Sprintf("item:%p:%d", snapshot, id), func(ctx context.Context) (any, error) {
		if body := snapshot.item(id); body != nil {
			return body, nil
		}
		item, err := p.source.Item(ctx, id, time.Now())
		if err != nil {
			return nil, err
		}
		publicItem := publicPelicanItem(item)
		publicItem.ResponseText = &item.ResponseText
		body, err := newPelicanPublicBody(publicItem)
		if err != nil {
			return nil, err
		}
		snapshot.put(id, body)
		return body, nil
	})
	if err != nil {
		return nil, err
	}
	body, ok := value.(*pelicanPublicBody)
	if !ok || body == nil {
		return nil, fmt.Errorf("unexpected pelican item cache value %T", value)
	}
	return body, nil
}

// PublicList GET/HEAD /api/v1/public/pelican-showcase
// Returns the entire retained metadata window, avoiding missed parallel results
// and cursor gaps. Clients fetch bodies only for IDs they have not stored yet.
func (h *PelicanShowcaseHandler) PublicList(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	snapshot, err := h.public.manifest(c.Request.Context())
	if err != nil {
		c.Header("Retry-After", "60")
		response.Error(c, http.StatusServiceUnavailable, "Failed to load pelican showcase; retry later")
		return
	}
	writePelicanPublicBody(c, snapshot.body)
}

// PublicItem GET/HEAD /api/v1/public/pelican-showcase/items/:id
func (h *PelicanShowcaseHandler) PublicItem(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid item id")
		return
	}
	snapshot, err := h.public.manifest(c.Request.Context())
	if err != nil {
		c.Header("Retry-After", "60")
		response.Error(c, http.StatusServiceUnavailable, "Failed to load pelican showcase; retry later")
		return
	}
	body, err := h.public.item(c.Request.Context(), snapshot, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	writePelicanPublicBody(c, body)
}

func writePelicanPublicBody(c *gin.Context, body *pelicanPublicBody) {
	// Every GET/HEAD (including 304) must pass API key auth. Keep the shared
	// in-process snapshot, but never let HTTP/CDN caches serve it anonymously.
	c.Header("Cache-Control", "private, no-cache, must-revalidate")
	c.Header("ETag", body.etag)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Add("Vary", "Accept-Encoding")
	for _, candidate := range strings.Split(c.GetHeader("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(body.etag, "W/") {
			c.Status(http.StatusNotModified)
			return
		}
	}
	payload := body.json
	if len(body.gzip) > 0 && pelicanAcceptsGzip(c.GetHeader("Accept-Encoding")) {
		payload = body.gzip
		c.Header("Content-Encoding", "gzip")
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Length", strconv.Itoa(len(payload)))
	if c.Request.Method == http.MethodHead {
		c.Status(http.StatusOK)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", payload)
}

func pelicanAcceptsGzip(header string) bool {
	wildcard := false
	for _, encoding := range strings.Split(header, ",") {
		parts := strings.Split(encoding, ";")
		name := strings.TrimSpace(parts[0])
		if !strings.EqualFold(name, "gzip") && name != "*" {
			continue
		}
		quality := 1.0
		for _, parameter := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "q") {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err != nil || !(parsed >= 0 && parsed <= 1) {
					return false
				}
				quality = parsed
			}
		}
		if strings.EqualFold(name, "gzip") {
			return quality > 0
		}
		wildcard = quality > 0
	}
	return wildcard
}
