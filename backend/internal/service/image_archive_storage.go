package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v4/disk"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const imageArchiveMinFreeBytes uint64 = 2 << 30

func (s *ImageArchiveService) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stop:
			return
		case job := <-s.queue:
			s.process(job)
		}
	}
}

func (s *ImageArchiveService) process(job *ImageArchiveCapture) {
	defer job.release()
	defer func() {
		if recover() != nil {
			s.failed.Add(1)
			s.warn("save_failed")
		}
	}()
	ctx, cancel := context.WithTimeout(s.ctx, 90*time.Second)
	defer cancel()
	// Re-check policy after queuing, so disabling a scope also stops queued saves.
	if !s.config.Load().matches(job.record.AccountID, job.record.GroupID) {
		return
	}
	unlock, err := s.repo.Lock(ctx)
	if err != nil {
		s.failed.Add(1)
		s.warn("storage_busy")
		return
	}
	defer unlock()
	if err := os.MkdirAll(s.root, 0700); err != nil {
		s.failed.Add(1)
		s.warn("storage_unavailable")
		return
	}
	used, err := imageArchiveDirectoryBytes(s.root)
	if err != nil {
		s.failed.Add(1)
		s.warn("storage_unavailable")
		return
	}
	dir := filepath.Join(s.root, job.record.ID)
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(dir)
		}
	}()
	// Result images take priority when reference images would exhaust the quota.
	assets := make([]imageArchiveAsset, 0, len(job.assets))
	for _, asset := range job.assets {
		if asset.role == "result" {
			assets = append(assets, asset)
		}
	}
	for _, asset := range job.assets {
		if asset.role != "result" {
			assets = append(assets, asset)
		}
	}
	for _, asset := range assets {
		if ctx.Err() != nil {
			job.issues = append(job.issues, "save_timeout")
			break
		}
		if used >= s.config.Load().MaxBytes {
			job.issue("capacity_limit")
			s.warn("capacity_limit")
			break
		}
		data := asset.data
		if len(data) == 0 {
			data, err = s.download(ctx, asset.source)
			if err != nil {
				job.issues = append(job.issues, "asset_download_failed")
				continue
			}
		}
		if len(data) == 0 || int64(len(data)) > imageArchiveAssetLimit {
			job.issues = append(job.issues, "asset_size_limit")
			continue
		}
		mimeType, width, height, thumb, err := imageArchiveInspect(data)
		if err != nil {
			job.issues = append(job.issues, "invalid_image")
			continue
		}
		size := int64(len(data) + len(thumb))
		free, spaceErr := s.freeSpace(s.root)
		if spaceErr != nil {
			job.issues = append(job.issues, "disk_check_failed")
			s.warn("disk_check_failed")
			break
		}
		if used+size > s.config.Load().MaxBytes {
			job.issues = append(job.issues, "capacity_limit")
			s.warn("capacity_limit")
			break
		}
		if free < imageArchiveMinFreeBytes+uint64(size) {
			job.issues = append(job.issues, "disk_low")
			s.warn("disk_low")
			break
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			job.issues = append(job.issues, "write_failed")
			break
		}
		file := ImageArchiveFile{ID: len(job.record.Files) + 1, Role: asset.role, MIME: mimeType, ByteSize: int64(len(data)), SHA256: imageArchiveHash(data), Width: width, Height: height, Thumbnail: len(thumb) > 0, RevisedPrompt: asset.revised}
		path := filepath.Join(dir, strconv.Itoa(file.ID))
		if err := os.WriteFile(path, data, 0600); err != nil {
			_ = os.Remove(path)
			job.issues = append(job.issues, "write_failed")
			break
		}
		if len(thumb) > 0 {
			if err := os.WriteFile(path+".jpg", thumb, 0600); err != nil {
				_ = os.Remove(path + ".jpg")
				file.Thumbnail = false
				size = int64(len(data))
				job.issues = append(job.issues, "thumbnail_failed")
			}
		}
		job.record.Files = append(job.record.Files, file)
		used += size
		job.record.ByteSize += size
	}
	if job.outputs == 0 {
		job.issues = append(job.issues, "missing_results")
	}
	results := 0
	for _, f := range job.record.Files {
		if f.Role == "result" {
			results++
		}
	}
	job.record.Status = "saved"
	if len(job.issues) > 0 {
		job.record.Status = "partial"
	}
	if results == 0 {
		job.record.Status = "failed"
		job.issues = append(job.issues, "missing_results")
	}
	job.record.ErrorCodes = imageArchiveErrorCodes(job.issues)
	if err := s.repo.Insert(ctx, &job.record); err != nil {
		// A lost commit acknowledgement does not prove the row was rolled back.
		// Preserve uncertain files; the orphan sweep resolves them after 24 hours.
		checkCtx, checkCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, checkErr := s.repo.Get(checkCtx, job.record.ID)
		checkCancel()
		committed = !errors.Is(checkErr, sql.ErrNoRows)
		s.failed.Add(1)
		s.warn("database_write_failed")
		return
	}
	committed = true
	if job.record.Status == "saved" {
		s.saved.Add(1)
	} else {
		s.failed.Add(1)
		s.warn(job.record.ErrorCodes)
	}
	s.updateDiskStats()
}

func imageArchiveErrorCodes(codes []string) string {
	unique := []string{}
	seen := map[string]bool{}
	for _, c := range codes {
		if !seen[c] {
			seen[c] = true
			unique = append(unique, c)
		}
	}
	return strings.Join(unique, ",")
}

func imageArchiveInspect(data []byte) (string, int, int, []byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return "", 0, 0, nil, fmt.Errorf("invalid raster image")
	}
	mimeType := map[string]string{"jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif", "webp": "image/webp"}[format]
	if mimeType == "" {
		return "", 0, 0, nil, fmt.Errorf("unsupported image")
	}
	// Keep the original even when decoding it would exceed the thumbnail memory budget.
	if cfg.Width > 20_000_000/cfg.Height {
		return mimeType, cfg.Width, cfg.Height, nil, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, nil, err
	}
	w, h := cfg.Width, cfg.Height
	if w > 384 || h > 384 {
		if w >= h {
			h = max(1, h*384/w)
			w = 384
		} else {
			w = max(1, w*384/h)
			h = 384
		}
	}
	preview := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.ApproxBiLinear.Scale(preview, preview.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, preview, &jpeg.Options{Quality: 78}); err != nil {
		return mimeType, cfg.Width, cfg.Height, nil, nil
	}
	return mimeType, cfg.Width, cfg.Height, out.Bytes(), nil
}

// downloadImageArchiveAsset never forwards API credentials. DNS is checked at dial time,
// including every redirect, to prevent private-network reads and DNS rebinding.
func downloadImageArchiveAsset(ctx context.Context, source string) ([]byte, error) {
	if strings.HasPrefix(source, "data:") {
		header, payload, ok := strings.Cut(source, ",")
		if !ok || !strings.HasSuffix(header, ";base64") || int64(len(payload)) > imageArchiveAssetLimit*4/3+4 {
			return nil, fmt.Errorf("invalid data URI")
		}
		return base64.StdEncoding.DecodeString(payload)
	}
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return nil, fmt.Errorf("invalid image URL")
	}
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("image host lookup failed")
			}
			for _, ip := range ips {
				if !imageArchivePublicIP(ip) {
					return nil, fmt.Errorf("private image host")
				}
			}
			var lastErr error
			for _, ip := range ips {
				conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.User != nil || (req.URL.Port() != "" && req.URL.Port() != "443") {
			return fmt.Errorf("image redirect rejected")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("image download failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > imageArchiveAssetLimit {
		return nil, fmt.Errorf("image download rejected")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, imageArchiveAssetLimit+1))
	if err != nil || int64(len(data)) > imageArchiveAssetLimit {
		return nil, fmt.Errorf("image download too large")
	}
	return data, nil
}

func imageArchivePublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16"} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}

func imageArchiveDirectoryBytes(root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive symlink rejected")
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func imageArchiveFreeSpace(path string) (uint64, error) {
	info, err := disk.Usage(path)
	if err != nil {
		return 0, err
	}
	return info.Free, nil
}

func (s *ImageArchiveService) updateDiskStats() {
	used, err := imageArchiveDirectoryBytes(s.root)
	free, spaceErr := s.freeSpace(s.root)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.available = err == nil && spaceErr == nil
	s.storedBytes = used
	s.diskFree = free
}

func (s *ImageArchiveService) maintain() {
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	unlock, err := s.repo.Lock(ctx)
	if err != nil {
		s.warn("cleanup_busy")
		return
	}
	defer unlock()
	if err := os.MkdirAll(s.root, 0700); err != nil {
		s.warn("storage_unavailable")
		return
	}
	for ctx.Err() == nil {
		ids, err := s.repo.Expired(ctx, time.Now(), 200)
		if err != nil {
			s.warn("cleanup_failed")
			return
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if !imageArchiveValidID(id) {
				s.warn("cleanup_failed")
				return
			}
			if err := os.RemoveAll(filepath.Join(s.root, id)); err != nil {
				s.warn("cleanup_failed")
				return
			}
			if err := s.repo.Delete(ctx, id); err != nil {
				s.warn("cleanup_failed")
				return
			}
		}
	}
	// Recover directories left by a crash between filesystem writes and DB insertion.
	entries, err := os.ReadDir(s.root)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() || !imageArchiveValidID(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil || time.Since(info.ModTime()) < 24*time.Hour {
				continue
			}
			_, err = s.repo.Get(ctx, e.Name())
			if errors.Is(err, sql.ErrNoRows) {
				if err := os.RemoveAll(filepath.Join(s.root, e.Name())); err != nil {
					s.warn("cleanup_failed")
				}
			}
		}
	}
	s.updateDiskStats()
}

func imageArchiveValidID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

// OpenFile takes only database-backed IDs, never a caller-supplied filesystem path.
func (s *ImageArchiveService) OpenFile(ctx context.Context, id string, fileID int, thumb bool) (*os.File, string, error) {
	if !imageArchiveValidID(id) || fileID <= 0 {
		return nil, "", os.ErrNotExist
	}
	record, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if !record.ExpiresAt.After(time.Now()) {
		return nil, "", os.ErrNotExist
	}
	for _, f := range record.Files {
		if f.ID == fileID {
			name := strconv.Itoa(fileID)
			mimeType := f.MIME
			if thumb {
				if !f.Thumbnail {
					return nil, "", os.ErrNotExist
				}
				name += ".jpg"
				mimeType = "image/jpeg"
			}
			path := filepath.Join(s.root, id, name)
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				return nil, "", os.ErrNotExist
			}
			file, err := os.Open(path)
			return file, mimeType, err
		}
	}
	return nil, "", os.ErrNotExist
}

func (s *ImageArchiveService) List(ctx context.Context, filter ImageArchiveFilter) ([]ImageArchiveRecord, int64, error) {
	return s.repo.List(ctx, filter)
}
func (s *ImageArchiveService) Get(ctx context.Context, id string) (*ImageArchiveRecord, error) {
	if !imageArchiveValidID(id) {
		return nil, sql.ErrNoRows
	}
	return s.repo.Get(ctx, id)
}

// Delete removes a whole archive when fileID is zero, or one saved image otherwise.
// Files are staged on the same volume before the DB change. Failed DB writes restore
// the files; successful writes leave any interrupted cleanup to the orphan sweep.
func (s *ImageArchiveService) Delete(ctx context.Context, id string, fileID int) error {
	if !imageArchiveValidID(id) || fileID < 0 {
		return sql.ErrNoRows
	}
	unlock, err := s.repo.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	defer s.updateDiskStats()
	record, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.root, id)
	staged := filepath.Join(s.root, uuid.NewString())
	type move struct{ from, to string }
	moves := []move{}
	committed := false
	defer func() {
		if !committed {
			for i := len(moves) - 1; i >= 0; i-- {
				if err := os.Rename(moves[i].to, moves[i].from); err != nil {
					s.warn("delete_restore_failed")
					return
				}
			}
		}
		if err := os.RemoveAll(staged); err != nil {
			s.warn("delete_cleanup_failed")
		}
	}()
	stage := func(from, to string, directory bool) error {
		info, err := os.Lstat(from)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
			return fmt.Errorf("invalid archive file")
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		moves = append(moves, move{from, to})
		return nil
	}
	if fileID == 0 {
		if err := stage(dir, staged, true); err != nil {
			return err
		}
		if err := s.repo.Delete(ctx, id); err != nil {
			return err
		}
	} else {
		remaining := make([]ImageArchiveFile, 0, len(record.Files))
		found := false
		for _, file := range record.Files {
			if file.ID == fileID {
				found = true
			} else {
				remaining = append(remaining, file)
			}
		}
		if !found {
			return sql.ErrNoRows
		}
		if info, err := os.Lstat(dir); err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("invalid archive directory")
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Mkdir(staged, 0700); err != nil {
			return err
		}
		name := strconv.Itoa(fileID)
		for _, suffix := range []string{"", ".jpg"} {
			if err := stage(filepath.Join(dir, name+suffix), filepath.Join(staged, name+suffix), false); err != nil {
				return err
			}
		}
		size, err := imageArchiveDirectoryBytes(dir)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := s.repo.UpdateFiles(ctx, id, remaining, size); err != nil {
			return err
		}
	}
	committed = true
	return nil
}
