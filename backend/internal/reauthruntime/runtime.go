// Package reauthruntime installs and supervises the release-matched login
// runtime. Account credentials remain in the authenticated worker protocol.
package reauthruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const maxArchive = 256 << 20
const maxExtracted = 768 << 20

type Status struct {
	StartedAt int64  `json:"-"`
	Mode      string `json:"mode"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
}

type Manager struct {
	mu                            sync.Mutex
	status                        Status
	root, version, baseURL, token string
	ctx                           context.Context
	cancel                        context.CancelFunc
	done                          chan struct{}
	started                       bool
	retryAt                       time.Time
	client                        *http.Client
}

func New(root, version, baseURL, token string) *Manager {
	root, _ = filepath.Abs(root)
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{root: root, version: strings.TrimPrefix(version, "v"), baseURL: baseURL, token: token,
		ctx: ctx, cancel: cancel, client: &http.Client{Timeout: 5 * time.Minute},
		status: Status{Mode: "managed", State: "idle"}}
}

func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.status }
func (m *Manager) set(state, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state == "running" {
		m.status.StartedAt = time.Now().UnixNano()
	}
	m.status.State = state
	m.status.Reason = reason
}

// Ensure never performs downloads on the request goroutine and retries failed
// preparation at most once per minute.
func (m *Manager) Ensure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.ctx.Err() != nil || time.Now().Before(m.retryAt) {
		return
	}
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		m.status.State = "unavailable"
		m.status.Reason = "unsupported_platform"
		return
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`).MatchString(m.version) {
		m.status.State = "unavailable"
		m.status.Reason = "release_required"
		return
	}
	m.started = true
	m.status.State = "preparing"
	m.status.Reason = ""
	m.done = make(chan struct{})
	go m.run()
}

func (m *Manager) Stop() {
	m.cancel()
	m.mu.Lock()
	done := m.done
	m.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (m *Manager) run() {
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.started = false
		m.retryAt = time.Now().Add(time.Minute)
		close(m.done)
	}()
	prepareCtx, prepareCancel := context.WithTimeout(m.ctx, 10*time.Minute)
	dir, err := m.prepare(prepareCtx)
	prepareCancel()
	if err != nil {
		m.set("unavailable", "runtime_install_failed")
		return
	}
	cmd := exec.CommandContext(m.ctx, filepath.Join(dir, "python"), filepath.Join(dir, "worker.py"))
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "PYTHONDONTWRITEBYTECODE=1", "PYTHONUNBUFFERED=1",
		"SUB2API_BASE_URL=" + m.baseURL, "OPENAI_REAUTH_WORKER_TOKEN=" + m.token,
		"OPENAI_REAUTH_WORKER_ID=managed-" + randomID(), "TOSUB2_ROOT=" + filepath.Join(dir, "tosub2"),
		"TOSUB2_PYTHON=" + filepath.Join(dir, "python"), "NODE_EXECUTABLE=" + filepath.Join(dir, "node")}
	// The managed child has a minimal environment. Explicitly pass the bounded
	// concurrency setting; Python validates it before claiming anything.
	if concurrency := os.Getenv("OPENAI_REAUTH_CONCURRENCY"); concurrency != "" {
		cmd.Env = append(cmd.Env, "OPENAI_REAUTH_CONCURRENCY="+concurrency)
	}
	cmd.Dir = dir
	// Worker/external runner output must never reach the application journal.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		m.set("unavailable", "worker_start_failed")
		return
	}
	m.set("running", "")
	_ = cmd.Wait()
	if m.ctx.Err() != nil {
		m.set("stopped", "")
	} else {
		m.set("unavailable", "worker_exited")
	}
}

func randomID() string { return fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano()) }

func (m *Manager) prepare(ctx context.Context) (string, error) {
	dir := filepath.Join(m.root, m.version+"-linux-"+runtime.GOARCH)
	if info, err := os.Stat(filepath.Join(dir, "ready")); err == nil && info.Mode().IsRegular() {
		return dir, nil
	}
	if err := os.MkdirAll(m.root, 0700); err != nil {
		return "", err
	}
	// A release digest from the owner repository is required; never execute an
	// unverified download or follow an arbitrary manifest download URL.
	name := "sub2api-reauth_" + m.version + "_linux_" + runtime.GOARCH + ".tar.gz"
	url := "https://api.github.com/repos/ranxi2001/sub2api/releases/tags/v" + m.version
	body, err := m.get(ctx, url, 4<<20)
	if err != nil {
		return "", err
	}
	var release struct {
		Assets []struct {
			Name   string
			Digest string
		}
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", err
	}
	var digest string
	for _, a := range release.Assets {
		if a.Name == name {
			digest = strings.TrimPrefix(a.Digest, "sha256:")
		}
	}
	if len(digest) != 64 {
		return "", errors.New("missing runtime digest")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", errors.New("invalid runtime digest")
	}
	archive, err := m.get(ctx, "https://github.com/ranxi2001/sub2api/releases/download/v"+m.version+"/"+name, maxArchive)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(archive)
	if hex.EncodeToString(hash[:]) != digest {
		return "", errors.New("runtime checksum mismatch")
	}
	tmp, err := os.MkdirTemp(m.root, ".prepare-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := extract(archive, tmp); err != nil {
		return "", err
	}
	check := exec.CommandContext(ctx, filepath.Join(tmp, "python"), filepath.Join(tmp, "worker.py"), "--check")
	check.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "OPENAI_REAUTH_WORKER_TOKEN=" + m.token,
		"TOSUB2_ROOT=" + filepath.Join(tmp, "tosub2"), "NODE_EXECUTABLE=" + filepath.Join(tmp, "node"),
		"PYTHONDONTWRITEBYTECODE=1"}
	configureProcess(check)
	if err := check.Run(); err != nil {
		return "", errors.New("runtime self-check failed")
	}
	if err := os.WriteFile(filepath.Join(tmp, "ready"), []byte(digest), 0600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Another instance sharing the data directory can finish first.
		if _, otherErr := os.Stat(filepath.Join(dir, "ready")); otherErr != nil {
			return "", err
		}
	}
	return dir, nil
}

func (m *Manager) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Sub2API-Reauth")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("incomplete or oversized download")
	}
	return data, nil
}
