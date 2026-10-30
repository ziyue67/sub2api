package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const imageArchiveSettingKey = "image_request_archive"
const imageArchiveContextKey = "image_request_archive_capture"
const imageArchiveMemoryLimit int64 = 128 << 20
const imageArchiveCaptureLimit int64 = 64 << 20
const imageArchiveAssetLimit int64 = 32 << 20

type ImageArchiveConfig struct {
	Enabled         bool    `json:"enabled"`
	PreviewOnly     bool    `json:"preview_only"`
	CloseOnBackdrop bool    `json:"close_on_backdrop"`
	AccountIDs      []int64 `json:"account_ids"`
	GroupIDs        []int64 `json:"group_ids"`
	RetentionDays   int     `json:"retention_days"`
	MaxBytes        int64   `json:"max_bytes"`
}

func DefaultImageArchiveConfig() ImageArchiveConfig {
	return ImageArchiveConfig{AccountIDs: []int64{}, GroupIDs: []int64{}, RetentionDays: 30, MaxBytes: 20 << 30}
}

func (v ImageArchiveConfig) Validate() error {
	if v.RetentionDays < 1 || v.RetentionDays > 365 || v.MaxBytes < 1<<30 || v.MaxBytes > 2<<40 {
		return fmt.Errorf("retention_days must be 1–365 and max_bytes must be 1 GiB–2 TiB")
	}
	if len(v.AccountIDs)+len(v.GroupIDs) > 1000 {
		return fmt.Errorf("at most 1000 account/group IDs are allowed")
	}
	for _, ids := range [][]int64{v.AccountIDs, v.GroupIDs} {
		for _, id := range ids {
			if id <= 0 {
				return fmt.Errorf("account/group IDs must be positive")
			}
		}
	}
	return nil
}

func (v ImageArchiveConfig) matches(accountID int64, groupID *int64) bool {
	if !v.Enabled {
		return false
	}
	for _, id := range v.AccountIDs {
		if id == accountID {
			return true
		}
	}
	if groupID != nil {
		for _, id := range v.GroupIDs {
			if id == *groupID {
				return true
			}
		}
	}
	return false
}

type ImageArchiveFile struct {
	ID            int    `json:"id"`
	Role          string `json:"role"`
	MIME          string `json:"mime_type"`
	ByteSize      int64  `json:"byte_size"`
	SHA256        string `json:"sha256"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Thumbnail     bool   `json:"thumbnail"`
	PreviewOnly   bool   `json:"preview_only,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type ImageArchiveRecord struct {
	ID            string             `json:"id"`
	UserID        int64              `json:"user_id"`
	APIKeyID      int64              `json:"api_key_id"`
	GroupID       *int64             `json:"group_id"`
	AccountID     int64              `json:"account_id"`
	UserEmail     string             `json:"user_email"`
	APIKeyName    string             `json:"api_key_name"`
	GroupName     string             `json:"group_name"`
	AccountName   string             `json:"account_name"`
	RequestID     string             `json:"request_id"`
	Model         string             `json:"model"`
	UpstreamModel string             `json:"upstream_model"`
	Endpoint      string             `json:"endpoint"`
	Prompt        string             `json:"prompt"`
	Parameters    map[string]any     `json:"parameters"`
	Files         []ImageArchiveFile `json:"files"`
	Status        string             `json:"status"`
	ErrorCodes    string             `json:"error_codes"`
	ByteSize      int64              `json:"byte_size"`
	CreatedAt     time.Time          `json:"created_at"`
	ExpiresAt     time.Time          `json:"expires_at"`
}

type ImageArchiveFilter struct {
	Page      int
	PageSize  int
	UserID    int64
	APIKeyID  int64
	AccountID int64
	GroupID   int64
	Model     string
	From      *time.Time
	To        *time.Time
}

type ImageArchiveRepository interface {
	Insert(context.Context, *ImageArchiveRecord) error
	Get(context.Context, string) (*ImageArchiveRecord, error)
	List(context.Context, ImageArchiveFilter) ([]ImageArchiveRecord, int64, error)
	Expired(context.Context, time.Time, int) ([]string, error)
	Delete(context.Context, string) error
	UpdateFiles(context.Context, string, []ImageArchiveFile, int64) error
	// Serializes filesystem writes/cleanup across server processes sharing this volume.
	Lock(context.Context) (func(), error)
}

type ImageArchiveStats struct {
	Config           ImageArchiveConfig `json:"config"`
	StoredBytes      int64              `json:"stored_bytes"`
	DiskFreeBytes    uint64             `json:"disk_free_bytes"`
	MemoryBytes      int64              `json:"memory_bytes"`
	Active           int64              `json:"active"`
	Saved            int64              `json:"saved"`
	Failed           int64              `json:"failed"`
	Dropped          int64              `json:"dropped"`
	Warning          string             `json:"warning"`
	WarningAt        *time.Time         `json:"warning_at"`
	StorageAvailable bool               `json:"storage_available"`
}

type ImageArchiveService struct {
	repo        ImageArchiveRepository
	settings    SettingRepository
	root        string
	config      atomic.Pointer[ImageArchiveConfig]
	configMu    sync.Mutex
	queue       chan *ImageArchiveCapture
	queueMu     sync.RWMutex
	slots       chan struct{}
	memory      atomic.Int64
	active      atomic.Int64
	saved       atomic.Int64
	failed      atomic.Int64
	dropped     atomic.Int64
	mu          sync.RWMutex
	storedBytes int64
	diskFree    uint64
	warning     string
	warningAt   *time.Time
	available   bool
	stop        chan struct{}
	cancel      context.CancelFunc
	ctx         context.Context
	wg          sync.WaitGroup
	stopOnce    sync.Once
	// download and freeSpace are replaceable in focused tests.
	download  func(context.Context, string) ([]byte, error)
	freeSpace func(string) (uint64, error)
}

func NewImageArchiveService(repo ImageArchiveRepository, settings SettingRepository) *ImageArchiveService {
	return newImageArchiveService(repo, settings, "data/image-archive")
}

func newImageArchiveService(repo ImageArchiveRepository, settings SettingRepository, root string) *ImageArchiveService {
	ctx, cancel := context.WithCancel(context.Background())
	s := &ImageArchiveService{repo: repo, settings: settings, root: root, queue: make(chan *ImageArchiveCapture, 8), slots: make(chan struct{}, 8), ctx: ctx, cancel: cancel, stop: make(chan struct{})}
	cfg := DefaultImageArchiveConfig()
	s.config.Store(&cfg)
	s.download = downloadImageArchiveAsset
	s.freeSpace = imageArchiveFreeSpace
	return s
}

func (s *ImageArchiveService) Start() {
	s.refreshConfig()
	s.wg.Add(3)
	for range 2 {
		go s.worker()
	}
	go func() {
		defer s.wg.Done()
		s.maintain()
		refresh := time.NewTicker(15 * time.Second)
		cleanup := time.NewTicker(time.Hour)
		defer refresh.Stop()
		defer cleanup.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-refresh.C:
				s.refreshConfig()
			case <-cleanup.C:
				s.maintain()
			}
		}
	}()
}

func (s *ImageArchiveService) Stop() {
	s.stopOnce.Do(func() {
		s.queueMu.Lock()
		close(s.stop)
		s.cancel()
		s.queueMu.Unlock()
	})
	s.wg.Wait()
	for {
		select {
		case job := <-s.queue:
			job.release()
		default:
			return
		}
	}
}

func (s *ImageArchiveService) refreshConfig() {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
	defer cancel()
	raw, err := s.settings.GetValue(ctx, imageArchiveSettingKey)
	if errors.Is(err, ErrSettingNotFound) {
		cfg := DefaultImageArchiveConfig()
		s.config.Store(&cfg)
		return
	}
	var cfg ImageArchiveConfig
	if err != nil || json.Unmarshal([]byte(raw), &cfg) != nil || cfg.Validate() != nil {
		// Fail closed on a broken settings read. Never archive an uncertain scope.
		cfg = DefaultImageArchiveConfig()
		s.config.Store(&cfg)
		s.warn("settings_unavailable")
		return
	}
	s.config.Store(&cfg)
}

func (s *ImageArchiveService) UpdateConfig(ctx context.Context, cfg ImageArchiveConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if cfg.AccountIDs == nil {
		cfg.AccountIDs = []int64{}
	}
	if cfg.GroupIDs == nil {
		cfg.GroupIDs = []int64{}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := s.settings.Set(ctx, imageArchiveSettingKey, string(raw)); err != nil {
		return err
	}
	s.config.Store(&cfg)
	return nil
}

func (s *ImageArchiveService) Stats() ImageArchiveStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ImageArchiveStats{Config: *s.config.Load(), StoredBytes: s.storedBytes, DiskFreeBytes: s.diskFree, MemoryBytes: s.memory.Load(), Active: s.active.Load(), Saved: s.saved.Load(), Failed: s.failed.Load(), Dropped: s.dropped.Load(), Warning: s.warning, WarningAt: s.warningAt, StorageAvailable: s.available}
}

func (s *ImageArchiveService) warn(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.warning = code
	s.warningAt = &now
}

type imageArchiveAsset struct {
	role, source, revised string
	data                  []byte
}

// A capture belongs to one selected-account attempt, never to the whole failover loop.
type ImageArchiveCapture struct {
	service  *ImageArchiveService
	record   ImageArchiveRecord
	assets   []imageArchiveAsset
	seen     map[[32]byte]bool
	bytes    int64
	issues   []string
	outputs  int
	released sync.Once
}

// Begin never performs I/O or waits for capacity. IDs must come from authenticated context.
func (s *ImageArchiveService) Begin(c *gin.Context, userID, keyID int64, groupID *int64, accountID int64, req *OpenAIImagesRequest) *ImageArchiveCapture {
	if s == nil || req == nil || !s.config.Load().matches(accountID, groupID) {
		return nil
	}
	select {
	case <-s.stop:
		return nil
	default:
	}
	select {
	case s.slots <- struct{}{}:
	default:
		s.dropped.Add(1)
		s.warn("queue_full")
		return nil
	}
	cfg := *s.config.Load()
	job := &ImageArchiveCapture{
		service: s, seen: make(map[[32]byte]bool),
		record: ImageArchiveRecord{
			ID: uuid.NewString(), UserID: userID, APIKeyID: keyID, AccountID: accountID,
			CreatedAt: time.Now(), Parameters: map[string]any{}, Files: []ImageArchiveFile{},
		},
	}
	job.record.Model = job.smallString(req.Model)
	job.record.Endpoint = job.smallString(req.Endpoint)
	for name, value := range map[string]any{
		"size": req.Size, "quality": req.Quality, "n": req.N, "stream": req.Stream,
		"output_format": req.OutputFormat, "response_format": req.ResponseFormat,
		"background": req.Background, "style": req.Style, "moderation": req.Moderation,
		"input_fidelity": req.InputFidelity, "output_compression": req.OutputCompression, "partial_images": req.PartialImages,
	} {
		job.parameter(name, value)
	}
	if groupID != nil {
		id := *groupID
		job.record.GroupID = &id
	}
	job.record.ExpiresAt = job.record.CreatedAt.Add(time.Duration(cfg.RetentionDays) * 24 * time.Hour)
	s.active.Add(1)
	if req.clientModel != "" {
		job.record.Model = job.smallString(req.clientModel)
	}
	prompt := req.Prompt
	if !req.Multipart && len(req.Body) > 0 {
		if raw := gjson.GetBytes(req.Body, "prompt"); raw.Type == gjson.String {
			prompt = raw.String()
		}
		for _, name := range []string{"aspect_ratio", "resolution", "image_resolution", "seed", "steps", "guidance_scale"} {
			value := gjson.GetBytes(req.Body, name)
			if value.Exists() && !value.IsObject() && !value.IsArray() && len(value.Raw) <= 1024 {
				job.parameter(name, value.Value())
			}
		}
	}
	if len(prompt) <= 1<<20 && job.reserve(int64(len(prompt))) {
		job.record.Prompt = strings.Clone(prompt)
	} else {
		job.issues = append(job.issues, "prompt_limit")
	}
	for _, upload := range req.Uploads {
		job.add(imageArchiveAsset{role: "reference", data: upload.Data})
	}
	for _, src := range req.InputImageURLs {
		job.add(imageArchiveAsset{role: "reference", source: src})
	}
	if req.MaskUpload != nil {
		job.add(imageArchiveAsset{role: "mask", data: req.MaskUpload.Data})
	}
	if req.MaskImageURL != "" {
		job.add(imageArchiveAsset{role: "mask", source: req.MaskImageURL})
	}
	// Compatible providers also accept references on /generations, notably Seedream.
	if !req.Multipart && len(req.Body) > 0 {
		for _, field := range []string{"reference_images", "image", "images"} {
			job.addJSONReferences(gjson.GetBytes(req.Body, field), 0)
		}
		job.addJSONReference(gjson.GetBytes(req.Body, "mask"), "mask")
	}
	c.Set(imageArchiveContextKey, job)
	return job
}

func (j *ImageArchiveCapture) smallString(value string) string {
	if len(value) > 4096 || !j.reserve(int64(len(value))) {
		j.issue("parameter_limit")
		return ""
	}
	return strings.Clone(value)
}

func (j *ImageArchiveCapture) parameter(name string, value any) {
	if text, ok := value.(string); ok {
		value = j.smallString(text)
	}
	if number, ok := value.(*int); ok {
		if number == nil {
			value = nil
		} else {
			value = *number
		}
	}
	j.record.Parameters[name] = value
}

func (j *ImageArchiveCapture) addJSONReferences(value gjson.Result, depth int) {
	if depth > 4 || len(j.assets) >= 64 {
		return
	}
	if value.IsArray() {
		for _, child := range value.Array() {
			j.addJSONReferences(child, depth+1)
			if len(j.assets) >= 64 {
				break
			}
		}
		return
	}
	j.addJSONReference(value, "reference")
}

func (j *ImageArchiveCapture) addJSONReference(value gjson.Result, role string) {
	if value.Type == gjson.String {
		j.add(imageArchiveAsset{role: role, source: value.String()})
		return
	}
	for _, key := range []string{"url", "image_url", "image_url.url"} {
		src := value.Get(key)
		if src.Type == gjson.String {
			j.add(imageArchiveAsset{role: role, source: src.String()})
			return
		}
	}
}

func (j *ImageArchiveCapture) reserve(n int64) bool {
	if n < 0 || j.bytes+n > imageArchiveCaptureLimit {
		return false
	}
	for {
		old := j.service.memory.Load()
		if old+n > imageArchiveMemoryLimit {
			return false
		}
		if j.service.memory.CompareAndSwap(old, old+n) {
			j.bytes += n
			return true
		}
	}
}

func (j *ImageArchiveCapture) add(a imageArchiveAsset) {
	if a.source == "" && len(a.data) == 0 {
		return
	}
	n := int64(len(a.source) + len(a.data) + len(a.revised))
	if len(j.assets) >= 64 || n > imageArchiveAssetLimit*4/3+4096 {
		j.issue("capture_limit")
		return
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(a.role))
	_, _ = hash.Write([]byte(a.source))
	_, _ = hash.Write(a.data)
	var key [32]byte
	copy(key[:], hash.Sum(nil))
	if j.seen[key] {
		return
	}
	if !j.reserve(n) {
		j.issue("capture_limit")
		return
	}
	j.seen[key] = true
	a.source = strings.Clone(a.source)
	a.revised = strings.Clone(a.revised)
	a.data = append([]byte(nil), a.data...)
	j.assets = append(j.assets, a)
	if a.role == "result" {
		j.outputs++
	}
}

func (j *ImageArchiveCapture) issue(code string) {
	for _, existing := range j.issues {
		if existing == code {
			return
		}
	}
	if len(j.issues) < 32 {
		j.issues = append(j.issues, code)
	}
}

// ObserveImageArchiveJSON copies only final image assets. Partial SSE previews are excluded.
func ObserveImageArchiveJSON(c *gin.Context, body []byte) {
	value, ok := c.Get(imageArchiveContextKey)
	if !ok {
		return
	}
	job, _ := value.(*ImageArchiveCapture)
	if job == nil {
		return
	}
	root := gjson.ParseBytes(body)
	typ := root.Get("type").String()
	if typ != "" && !strings.HasSuffix(typ, ".completed") {
		return
	}
	items := root.Get("data").Array()
	if len(items) == 0 && strings.HasSuffix(typ, ".completed") {
		items = []gjson.Result{root}
	}
	for _, item := range items {
		src := item.Get("b64_json").String()
		if src != "" {
			src = "data:;base64," + src
		} else {
			src = item.Get("url").String()
		}
		job.add(imageArchiveAsset{role: "result", source: src, revised: item.Get("revised_prompt").String()})
	}
}

// Finish is non-blocking and must run even when the client disconnected or forwarding returned a partial error.
func (j *ImageArchiveCapture) Finish(c *gin.Context, result *OpenAIForwardResult) {
	j.FinishWithError(c, result, nil)
}

// FinishWithError is non-blocking and preserves a failed request record when the
// upstream returned no image. This lets the owner inspect a charged/failed
// request instead of losing it at the archive boundary.
func (j *ImageArchiveCapture) FinishWithError(c *gin.Context, result *OpenAIForwardResult, forwardErr error) {
	if j == nil {
		return
	}
	c.Set(imageArchiveContextKey, (*ImageArchiveCapture)(nil))
	if forwardErr != nil {
		j.issue("forward_failed")
	}
	if result == nil && forwardErr == nil && j.outputs == 0 {
		j.release()
		return
	}
	count := j.outputs
	if result != nil && result.ImageCount > count {
		count = result.ImageCount
	}
	j.parameter("_image_count", count)
	generation := "failed"
	if count > 0 {
		generation = "succeeded"
	}
	j.parameter("_generation_status", generation)
	if forwardErr != nil && count == 0 {
		// Both Images handlers skip RecordUsage for a failed zero-image attempt.
		// This describes this attempt only, never a later successful retry.
		j.parameter("_billing_outcome", "not_submitted_v1")
	}
	j.parameter("_delivery_status", "unknown")
	if count > 0 {
		j.parameter("_delivery_status", "returned")
		if forwardErr != nil || (c.Request != nil && c.Request.Context().Err() != nil) {
			j.parameter("_delivery_status", "interrupted")
		}
	}
	j.parameter("_client_request_id", c.Writer.Header().Get("X-Client-Request-ID"))
	if result != nil && result.ImageCount > 0 && c.Request != nil {
		j.parameter("_billing_request_id", resolveUsageBillingRequestID(c.Request.Context(), result.RequestID))
	}
	if result != nil {
		j.record.RequestID = j.smallString(result.RequestID)
		j.record.UpstreamModel = j.smallString(result.UpstreamModel)
		if result.ImageCount > j.outputs {
			j.issues = append(j.issues, "missing_results")
		}
	}
	j.service.queueMu.RLock()
	defer j.service.queueMu.RUnlock()
	select {
	case <-j.service.stop:
		j.release()
		return
	default:
	}
	select {
	case j.service.queue <- j:
	default:
		j.service.dropped.Add(1)
		j.service.warn("queue_full")
		j.release()
	}
}

func (j *ImageArchiveCapture) release() {
	j.released.Do(func() { j.service.memory.Add(-j.bytes); j.service.active.Add(-1); <-j.service.slots })
}

func imageArchiveHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
