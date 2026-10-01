package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/reauthruntime"
)

func (s *OpenAIOAuthReauthService) configureWorker(cfg *config.Config, info BuildInfo) {
	token := strings.TrimSpace(os.Getenv("OPENAI_REAUTH_WORKER_TOKEN"))
	if token != "" {
		s.workerToken = token
		return
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return
	}
	s.workerToken = hex.EncodeToString(buf)
	dir := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if dir == "" {
		if st, err := os.Stat("/app/data"); err == nil && st.IsDir() {
			dir = "/app/data"
		} else {
			dir = "./data"
		}
	}
	host := "127.0.0.1"
	port := 8080
	if cfg != nil {
		port = cfg.Server.Port
		switch cfg.Server.Host {
		case "", "0.0.0.0":
		case "::":
			host = "::1"
		default:
			host = cfg.Server.Host
		}
	}
	s.worker = reauthruntime.New(filepath.Join(dir, "credential-worker"), info.Version, "http://"+net.JoinHostPort(host, strconv.Itoa(port)), s.workerToken)
}

// WorkerAuthentication verifies the private protocol without exposing the key.
func (s *OpenAIOAuthReauthService) WorkerAuthentication(provided string) (configured, valid bool) {
	token := strings.TrimSpace(os.Getenv("OPENAI_REAUTH_WORKER_TOKEN"))
	if s != nil && s.workerToken != "" {
		token = s.workerToken
	}
	configured = len(token) >= 32
	return configured, configured && provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

func (s *OpenAIOAuthReauthService) WorkerStatus() reauthruntime.Status {
	if s == nil {
		return reauthruntime.Status{Mode: "managed", State: "unavailable", Reason: "worker_start_failed"}
	}
	if s.worker != nil {
		status := s.worker.Status()
		if status.State == "running" && s.workerLastSeen.Load() < status.StartedAt {
			status.State = "preparing"
			if time.Since(time.Unix(0, status.StartedAt)) > time.Minute {
				status.State = "unavailable"
				status.Reason = "api_unreachable"
			}
		}
		return status
	}
	configured, _ := s.WorkerAuthentication("")
	if !configured {
		return reauthruntime.Status{Mode: "external", State: "unavailable", Reason: "external_not_configured"}
	}
	last := s.workerLastSeen.Load()
	if last == 0 || time.Since(time.Unix(0, last)) > 26*time.Minute {
		return reauthruntime.Status{Mode: "external", State: "unavailable", Reason: "external_offline"}
	}
	return reauthruntime.Status{Mode: "external", State: "running"}
}

func (s *OpenAIOAuthReauthService) EnsureWorker() {
	if s != nil && s.worker != nil {
		s.worker.Ensure()
	}
}
func (s *OpenAIOAuthReauthService) stopWorker() {
	if s != nil && s.worker != nil {
		s.worker.Stop()
	}
}

func (s *OpenAIOAuthReauthService) checkWorkerMode(mode string) error {
	// Constructors used by embedders/tests do not enable a runtime implicitly.
	if s.worker == nil {
		return nil
	}
	if mode != OpenAIOAuthReauthModePasswordTOTP {
		return infraerrors.ServiceUnavailable("OPENAI_REAUTH_MODE_UNAVAILABLE", "Email OTP re-login is unavailable in the built-in runtime")
	}
	s.EnsureWorker()
	if s.WorkerStatus().State == "unavailable" {
		return infraerrors.ServiceUnavailable("OPENAI_REAUTH_WORKER_UNAVAILABLE", "Re-login service is unavailable; see Credential Operations status")
	}
	return nil
}
