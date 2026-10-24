package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type archiveTestRepository struct {
	records   map[string]*ImageArchiveRecord
	expired   []string
	insertErr error
	deleteErr error
}

func (r *archiveTestRepository) Insert(_ context.Context, v *ImageArchiveRecord) error {
	if r.insertErr != nil {
		return r.insertErr
	}
	r.records[v.ID] = v
	return nil
}
func (r *archiveTestRepository) Get(_ context.Context, id string) (*ImageArchiveRecord, error) {
	if v := r.records[id]; v != nil {
		return v, nil
	}
	return nil, sql.ErrNoRows
}
func (r *archiveTestRepository) List(context.Context, ImageArchiveFilter) ([]ImageArchiveRecord, int64, error) {
	return nil, 0, nil
}
func (r *archiveTestRepository) Expired(context.Context, time.Time, int) ([]string, error) {
	return r.expired, nil
}
func (r *archiveTestRepository) Delete(_ context.Context, id string) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	delete(r.records, id)
	r.expired = nil
	return nil
}
func (r *archiveTestRepository) Lock(context.Context) (func(), error) { return func() {}, nil }

func archiveTestImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 9, 7))
	img.Set(1, 1, color.White)
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}
func archiveTestService(t *testing.T) (*ImageArchiveService, *archiveTestRepository) {
	t.Helper()
	repo := &archiveTestRepository{records: map[string]*ImageArchiveRecord{}}
	s := newImageArchiveService(repo, nil, t.TempDir())
	cfg := DefaultImageArchiveConfig()
	cfg.Enabled = true
	cfg.AccountIDs = []int64{7}
	s.config.Store(&cfg)
	s.freeSpace = func(string) (uint64, error) { return 10 << 30, nil }
	t.Cleanup(s.Stop)
	return s, repo
}
func archiveTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	return c, rec
}
func archiveTestCapture(s *ImageArchiveService, c *gin.Context) *ImageArchiveCapture {
	return s.Begin(c, 12, 34, nil, 7, &OpenAIImagesRequest{Model: "gpt-image-2", Prompt: "  保留 原样 <script>  ", N: 1, Endpoint: openAIImagesGenerationsEndpoint})
}
func archiveObserveImage(t *testing.T, c *gin.Context) {
	t.Helper()
	ObserveImageArchiveJSON(c, []byte(fmt.Sprintf(`{"data":[{"b64_json":%q,"revised_prompt":"上游提示词"}]}`, base64.StdEncoding.EncodeToString(archiveTestImage(t)))))
}

func TestImageArchiveScopeAndAttemptIsolation(t *testing.T) {
	s, repo := archiveTestService(t)
	c, _ := archiveTestContext()
	cfg := *s.config.Load()
	require.False(t, cfg.matches(99, nil))
	groupID := int64(8)
	cfg.GroupIDs = []int64{8}
	require.True(t, cfg.matches(99, &groupID))
	require.Nil(t, s.Begin(c, 12, 34, nil, 99, &OpenAIImagesRequest{}))
	first := archiveTestCapture(s, c)
	first.Finish(c, nil)
	require.Zero(t, s.active.Load())
	second := archiveTestCapture(s, c)
	archiveObserveImage(t, c)
	second.Finish(c, &OpenAIForwardResult{RequestID: "paid-request", UpstreamModel: "mapped-model", ImageCount: 1})
	s.process(<-s.queue)
	require.Len(t, repo.records, 1)
	v := repo.records[second.record.ID]
	require.NotNil(t, v)
	require.Equal(t, int64(12), v.UserID)
	require.Equal(t, int64(34), v.APIKeyID)
	require.Equal(t, int64(7), v.AccountID)
	require.Equal(t, "paid-request", v.RequestID)
	require.Equal(t, "mapped-model", v.UpstreamModel)
	require.Equal(t, "  保留 原样 <script>  ", v.Prompt)
	require.Equal(t, "saved", v.Status)
	require.Len(t, v.Files, 1)
	require.True(t, v.Files[0].Thumbnail)
	require.WithinDuration(t, v.CreatedAt.Add(30*24*time.Hour), v.ExpiresAt, time.Second)
	file, mimeType, err := s.OpenFile(context.Background(), v.ID, 1, false)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	require.NoError(t, err)
	require.Equal(t, archiveTestImage(t), data)
	require.Equal(t, "image/png", mimeType)
	require.Zero(t, s.active.Load())
	require.Zero(t, s.memory.Load())
}

func TestImageArchiveMultipartReferenceAndMask(t *testing.T) {
	s, _ := archiveTestService(t)
	var body bytes.Buffer
	// Exercise the existing multipart parser, so masks and reference files use the actual request shape.
	c, _ := archiveTestContext()
	c.Request.URL.Path = "/v1/images/edits"
	bodyBytes, contentType := archiveMultipartRequest(t, archiveTestImage(t))
	_, _ = body.Write(bodyBytes)
	c.Request.Header.Set("Content-Type", contentType)
	gateway := newOpenAIImagesTestService(nil)
	parsed, err := gateway.ParseOpenAIImagesRequest(c, body.Bytes())
	require.NoError(t, err)
	job := s.Begin(c, 1, 2, nil, 7, parsed)
	require.NotNil(t, job)
	archiveObserveImage(t, c)
	job.Finish(c, &OpenAIForwardResult{ImageCount: 1})
	s.process(<-s.queue)
	require.Equal(t, "saved", job.record.Status)
	require.Len(t, job.record.Files, 3)
	require.Equal(t, []string{"result", "reference", "mask"}, []string{job.record.Files[0].Role, job.record.Files[1].Role, job.record.Files[2].Role})
}

func archiveMultipartRequest(t *testing.T, data []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	require.NoError(t, writer.WriteField("model", "gpt-image-2"))
	require.NoError(t, writer.WriteField("prompt", "参考图编辑"))
	for _, name := range []string{"image", "mask"} {
		part, err := writer.CreateFormFile(name, name+".png")
		require.NoError(t, err)
		_, err = part.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buf.Bytes(), writer.FormDataContentType()
}

func TestImageArchiveSSEContinuesAfterClientDisconnect(t *testing.T) {
	s, _ := archiveTestService(t)
	c, rec := archiveTestContext()
	job := archiveTestCapture(s, c)
	c.Writer = &archiveDisconnectedWriter{ResponseWriter: c.Writer}
	encoded := base64.StdEncoding.EncodeToString(archiveTestImage(t))
	body := fmt.Sprintf("data: {\"type\":\"image_generation.partial_image\",\"b64_json\":%q}\n\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":%q}\n\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":%q}\n\ndata: [DONE]\n\n", encoded, encoded, encoded)
	gateway := newOpenAIImagesTestService(nil)
	_, _, _, _, err := gateway.handleOpenAIImagesStreamingResponse(&http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, c, time.Now(), nil)
	require.NoError(t, err)
	require.Empty(t, rec.Body.String())
	require.Equal(t, 1, job.outputs)
	job.Finish(c, &OpenAIForwardResult{ImageCount: 1})
	s.process(<-s.queue)
	require.Equal(t, "saved", job.record.Status)
}

type archiveDisconnectedWriter struct{ gin.ResponseWriter }

func (w *archiveDisconnectedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestImageArchiveStorageFailuresPreserveResponse(t *testing.T) {
	for _, reason := range []string{"capacity_limit", "disk_low", "disk_check_failed", "database_write_failed", "asset_download_failed"} {
		t.Run(reason, func(t *testing.T) {
			s, repo := archiveTestService(t)
			c, rec := archiveTestContext()
			job := archiveTestCapture(s, c)
			archiveObserveImage(t, c)
			if reason == "capacity_limit" {
				cfg := *s.config.Load()
				cfg.MaxBytes = 1
				s.config.Store(&cfg)
			}
			if reason == "disk_low" {
				s.freeSpace = func(string) (uint64, error) { return 1, nil }
			}
			if reason == "disk_check_failed" {
				s.freeSpace = func(string) (uint64, error) { return 0, fmt.Errorf("disk unavailable") }
			}
			if reason == "database_write_failed" {
				repo.insertErr = fmt.Errorf("database unavailable")
			}
			if reason == "asset_download_failed" {
				s.download = func(context.Context, string) ([]byte, error) { return nil, fmt.Errorf("upstream unavailable") }
			}
			c.Data(200, "application/json", []byte(`{"data":"customer response"}`))
			before := rec.Body.String()
			job.Finish(c, &OpenAIForwardResult{ImageCount: 1})
			s.process(<-s.queue)
			require.Equal(t, before, rec.Body.String())
			require.Equal(t, 200, rec.Code)
			require.Contains(t, s.Stats().Warning, reason)
			require.Zero(t, s.memory.Load())
			require.Zero(t, s.active.Load())
			if reason == "database_write_failed" {
				_, err := os.Stat(filepath.Join(s.root, job.record.ID))
				require.True(t, os.IsNotExist(err))
			} else {
				require.Equal(t, "failed", job.record.Status)
			}
		})
	}
}

func TestImageArchiveURLAssetAndQueueBudget(t *testing.T) {
	s, _ := archiveTestService(t)
	c, _ := archiveTestContext()
	job := archiveTestCapture(s, c)
	s.download = func(_ context.Context, source string) ([]byte, error) {
		require.Equal(t, "https://cdn.example/image?signed=secret", source)
		return archiveTestImage(t), nil
	}
	ObserveImageArchiveJSON(c, []byte(`{"data":[{"url":"https://cdn.example/image?signed=secret"}]}`))
	job.Finish(c, &OpenAIForwardResult{ImageCount: 1})
	s.process(<-s.queue)
	require.Equal(t, "saved", job.record.Status)
	for range cap(s.slots) {
		pending := archiveTestCapture(s, c)
		require.NotNil(t, pending)
		t.Cleanup(pending.release)
	}
	require.Nil(t, archiveTestCapture(s, c))
	require.Equal(t, int64(1), s.dropped.Load())
	before := s.memory.Load()
	over := &ImageArchiveCapture{service: s}
	require.False(t, over.reserve(imageArchiveMemoryLimit+1))
	require.Equal(t, before, s.memory.Load())
}

func TestImageArchiveDisabledWhileQueuedAndExpiry(t *testing.T) {
	s, repo := archiveTestService(t)
	c, _ := archiveTestContext()
	job := archiveTestCapture(s, c)
	archiveObserveImage(t, c)
	job.Finish(c, &OpenAIForwardResult{ImageCount: 1})
	cfg := *s.config.Load()
	cfg.Enabled = false
	s.config.Store(&cfg)
	s.process(<-s.queue)
	require.Empty(t, repo.records)
	cfg.Enabled = true
	s.config.Store(&cfg)
	job = archiveTestCapture(s, c)
	archiveObserveImage(t, c)
	job.Finish(c, &OpenAIForwardResult{ImageCount: 1})
	s.process(<-s.queue)
	job.record.ExpiresAt = time.Now().Add(-time.Hour)
	repo.expired = []string{job.record.ID}
	_, _, err := s.OpenFile(context.Background(), job.record.ID, 1, false)
	require.ErrorIs(t, err, os.ErrNotExist)
	orphan := filepath.Join(s.root, uuid.NewString())
	require.NoError(t, os.Mkdir(orphan, 0700))
	require.NoError(t, os.Chtimes(orphan, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour)))
	s.maintain()
	require.Empty(t, repo.records)
	_, err = os.Stat(filepath.Join(s.root, job.record.ID))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(orphan)
	require.True(t, os.IsNotExist(err))
	for _, id := range []string{"../../etc/passwd", "{00000000-0000-0000-0000-000000000000}", "not-a-uuid"} {
		_, _, err := s.OpenFile(context.Background(), id, 1, false)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestImageArchiveDownloadRejectsPrivateAndNonImages(t *testing.T) {
	for _, src := range []string{"file:///etc/passwd", "http://example.com/i.png", "https://127.0.0.1/image", "https://user:password@example.com/image", "https://example.com:8443/image", "data:image/png,hello"} {
		_, err := downloadImageArchiveAsset(context.Background(), src)
		require.Error(t, err, src)
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.100.100.200", "::1", "::ffff:192.168.1.1", "64:ff9b::a00:1", "2002:a00:1::"} {
		require.False(t, imageArchivePublicIP(netip.MustParseAddr(ip)), ip)
	}
	require.True(t, imageArchivePublicIP(netip.MustParseAddr("8.8.8.8")))
	_, _, _, _, err := imageArchiveInspect([]byte("<svg onload='alert(1)'/>"), false)
	require.Error(t, err)
}

func TestImageArchiveSeedreamReferenceInputs(t *testing.T) {
	s, _ := archiveTestService(t)
	c, _ := archiveTestContext()
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(archiveTestImage(t))
	job := s.Begin(c, 1, 2, nil, 7, &OpenAIImagesRequest{Model: "seedream-5.0-lite", Prompt: "trimmed", Body: []byte(fmt.Sprintf(`{"prompt":"  原始提示词  ","aspect_ratio":"16:9","resolution":"2K","reference_images":[%q,{"url":%q}]}`, dataURI, dataURI))})
	require.Equal(t, "  原始提示词  ", job.record.Prompt)
	require.Equal(t, "16:9", job.record.Parameters["aspect_ratio"])
	require.Equal(t, "2K", job.record.Parameters["resolution"])
	require.Len(t, job.assets, 1)
	archiveObserveImage(t, c)
	job.Finish(c, &OpenAIForwardResult{ImageCount: 1})
	s.process(<-s.queue)
	require.Equal(t, "saved", job.record.Status)
	require.Len(t, job.record.Files, 2)
}

func TestImageArchiveStopConcurrentWithFinish(t *testing.T) {
	s, _ := archiveTestService(t)
	var wg sync.WaitGroup
	for range 8 {
		c, _ := archiveTestContext()
		job := archiveTestCapture(s, c)
		archiveObserveImage(t, c)
		wg.Add(1)
		go func() { defer wg.Done(); job.Finish(c, &OpenAIForwardResult{ImageCount: 1}) }()
	}
	s.Stop()
	wg.Wait()
	require.Zero(t, s.memory.Load())
	require.Zero(t, s.active.Load())
	require.Empty(t, s.queue)
}

func (r *archiveTestRepository) UpdateFiles(_ context.Context, id string, files []ImageArchiveFile, size int64) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	r.records[id].Files = files
	r.records[id].ByteSize = size
	return nil
}

func TestImageArchiveDeleteFilesAndRecord(t *testing.T) {
	s, repo := archiveTestService(t)
	c, _ := archiveTestContext()
	job := archiveTestCapture(s, c)
	archiveObserveImage(t, c)
	job.add(imageArchiveAsset{role: "reference", data: archiveTestImage(t)})
	job.Finish(c, &OpenAIForwardResult{ImageCount: 1})
	s.process(<-s.queue)
	record := repo.records[job.record.ID]
	require.Len(t, record.Files, 2)
	dir := filepath.Join(s.root, record.ID)
	originalSize := record.ByteSize

	// A database failure restores the original and thumbnail and keeps metadata.
	repo.deleteErr = fmt.Errorf("database unavailable")
	require.Error(t, s.Delete(context.Background(), record.ID, 1))
	require.FileExists(t, filepath.Join(dir, "1"))
	require.FileExists(t, filepath.Join(dir, "1.jpg"))
	require.Len(t, record.Files, 2)
	require.Equal(t, originalSize, record.ByteSize)
	repo.deleteErr = nil

	require.NoError(t, s.Delete(context.Background(), record.ID, 1))
	require.NoFileExists(t, filepath.Join(dir, "1"))
	require.NoFileExists(t, filepath.Join(dir, "1.jpg"))
	require.FileExists(t, filepath.Join(dir, "2"))
	require.Len(t, record.Files, 1)
	require.Equal(t, 2, record.Files[0].ID) // IDs stay stable after a deletion.
	require.Less(t, record.ByteSize, originalSize)
	require.Equal(t, record.ByteSize, s.Stats().StoredBytes)
	_, _, err := s.OpenFile(context.Background(), record.ID, 1, false)
	require.Error(t, err)
	require.ErrorIs(t, s.Delete(context.Background(), record.ID, 999), sql.ErrNoRows)
	require.ErrorIs(t, s.Delete(context.Background(), "../outside", 0), sql.ErrNoRows)

	repo.deleteErr = fmt.Errorf("database unavailable")
	require.Error(t, s.Delete(context.Background(), record.ID, 0))
	require.DirExists(t, dir)
	require.Contains(t, repo.records, record.ID)
	repo.deleteErr = nil
	require.NoError(t, s.Delete(context.Background(), record.ID, 0))
	require.NoDirExists(t, dir)
	require.NotContains(t, repo.records, record.ID)
	require.Zero(t, s.Stats().StoredBytes)
	entries, err := os.ReadDir(s.root)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestImageArchiveDeleteLastImageKeepsPrompt(t *testing.T) {
	s, repo := archiveTestService(t)
	c, _ := archiveTestContext()
	job := archiveTestCapture(s, c)
	archiveObserveImage(t, c)
	job.Finish(c, &OpenAIForwardResult{ImageCount: 1})
	s.process(<-s.queue)
	require.NoError(t, s.Delete(context.Background(), job.record.ID, 1))
	record := repo.records[job.record.ID]
	require.Empty(t, record.Files)
	require.NotEmpty(t, record.Prompt)
	require.Zero(t, record.ByteSize)
	require.Zero(t, s.Stats().StoredBytes)
}
