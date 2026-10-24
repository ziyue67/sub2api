package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageArchivePreviewOnlyStorageAndLegacyCompatibility(t *testing.T) {
	s, repo := archiveTestService(t)
	c, _ := archiveTestContext()
	legacy := archiveTestCapture(s, c)
	archiveObserveImage(t, c)
	legacy.Finish(c, &OpenAIForwardResult{ImageCount: 1})
	s.process(<-s.queue)
	legacySize := repo.records[legacy.record.ID].ByteSize

	// A noisy PNG provides a realistic multi-megabyte, nontrivial input.
	img := image.NewNRGBA(image.Rect(0, 0, 1800, 1200))
	_, err := rand.New(rand.NewSource(7)).Read(img.Pix)
	require.NoError(t, err)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	original := buf.Bytes()
	originalHash := imageArchiveHash(original)
	_, _, _, preview, err := imageArchiveInspect(original, true)
	require.NoError(t, err)
	require.Less(t, len(preview), len(original)/2)

	cfg := *s.config.Load()
	cfg.PreviewOnly = true
	// The compressed image fits; the original input would exceed this quota.
	cfg.MaxBytes = legacySize + int64(len(preview)) + 1024
	s.config.Store(&cfg)
	c, _ = archiveTestContext()
	job := archiveTestCapture(s, c)
	job.add(imageArchiveAsset{role: "result", data: original})
	job.Finish(c, &OpenAIForwardResult{ImageCount: 1})
	s.process(<-s.queue)
	record := repo.records[job.record.ID]
	require.Equal(t, "saved", record.Status)
	require.Len(t, record.Files, 1)
	f := record.Files[0]
	require.True(t, f.PreviewOnly)
	require.True(t, f.Thumbnail)
	require.Equal(t, "image/jpeg", f.MIME)
	require.Equal(t, 1280, f.Width)
	require.Equal(t, 853, f.Height)
	require.Equal(t, int64(len(preview)), f.ByteSize)
	require.Equal(t, f.ByteSize, record.ByteSize)
	require.Equal(t, legacySize+record.ByteSize, s.Stats().StoredBytes)
	require.Equal(t, imageArchiveHash(preview), f.SHA256)
	require.Equal(t, originalHash, imageArchiveHash(original))
	dir := filepath.Join(s.root, record.ID)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1) // No original and no duplicate thumbnail.
	for _, thumbnail := range []bool{false, true} {
		file, mime, err := s.OpenFile(context.Background(), record.ID, f.ID, thumbnail)
		require.NoError(t, err)
		data, err := io.ReadAll(file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		require.Equal(t, "image/jpeg", mime)
		require.Equal(t, preview, data)
	}
	// The current setting must not change how previously stored records are read.
	file, mime, err := s.OpenFile(context.Background(), legacy.record.ID, 1, false)
	require.NoError(t, err)
	data, err := io.ReadAll(file)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.Equal(t, "image/png", mime)
	require.Equal(t, archiveTestImage(t), data)
	require.FileExists(t, filepath.Join(s.root, legacy.record.ID, "1.jpg"))

	// Rollback and deletion also work for records with just one physical file.
	repo.deleteErr = fmt.Errorf("database unavailable")
	require.Error(t, s.Delete(context.Background(), record.ID, 1))
	require.FileExists(t, filepath.Join(dir, "1"))
	require.Len(t, record.Files, 1)
	repo.deleteErr = nil
	require.NoError(t, s.Delete(context.Background(), record.ID, 1))
	require.Empty(t, record.Files)
	require.NotEmpty(t, record.Prompt)
	require.Equal(t, legacySize, s.Stats().StoredBytes)
	require.NoError(t, s.Delete(context.Background(), record.ID, 0))
	require.NoDirExists(t, dir)
	t.Logf("synthetic PNG %d bytes -> preview %d bytes; saved %.1f%%", len(original), len(preview), 100*(1-float64(len(preview))/float64(len(original))))
}

func TestImageArchivePreviewOnlyDoesNotFallBackToOriginal(t *testing.T) {
	s, repo := archiveTestService(t)
	cfg := *s.config.Load()
	cfg.PreviewOnly = true
	s.config.Store(&cfg)
	data := archiveTestImage(t)
	// Valid PNG header with a declared size above the existing decode budget.
	// No large bitmap allocation is needed to exercise the safety limit.
	binary.BigEndian.PutUint32(data[16:20], 5000)
	binary.BigEndian.PutUint32(data[20:24], 5000)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	c, _ := archiveTestContext()
	job := archiveTestCapture(s, c)
	job.add(imageArchiveAsset{role: "result", data: data})
	job.Finish(c, &OpenAIForwardResult{ImageCount: 1})
	s.process(<-s.queue)
	record := repo.records[job.record.ID]
	require.Empty(t, record.Files)
	require.Equal(t, "failed", record.Status)
	require.Contains(t, record.ErrorCodes, "preview_failed")
	require.Zero(t, record.ByteSize)
	require.NoDirExists(t, filepath.Join(s.root, record.ID))
}

func TestImageArchivePreviewTransparencyAndOptionDefaults(t *testing.T) {
	_, _, _, preview, err := imageArchiveInspect(archiveTestImage(t), true)
	require.NoError(t, err)
	img, _, err := image.Decode(bytes.NewReader(preview))
	require.NoError(t, err)
	r, g, b, _ := img.At(0, 0).RGBA()
	require.Greater(t, r, uint32(60000))
	require.Greater(t, g, uint32(60000))
	require.Greater(t, b, uint32(60000))
	var legacy ImageArchiveConfig
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"account_ids":[7],"retention_days":30,"max_bytes":21474836480}`), &legacy))
	require.False(t, legacy.PreviewOnly)
	require.False(t, legacy.CloseOnBackdrop)
	legacy.PreviewOnly, legacy.CloseOnBackdrop = true, true
	raw, err := json.Marshal(legacy)
	require.NoError(t, err)
	var restored ImageArchiveConfig
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.True(t, restored.PreviewOnly)
	require.True(t, restored.CloseOnBackdrop)
}
