package service

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

const openAIIPUnauthorizedWindow = 5 * time.Second

// This is the specific upstream policy message observed with HTTP 401. Do not
// turn generic authentication failures or client-supplied text into this policy.
func isOpenAIIPUnauthorizedResponse(status int, body []byte) bool {
	return status == http.StatusUnauthorized && strings.EqualFold(
		strings.TrimSpace(gjson.GetBytes(body, "error.message").String()),
		"Your IP is not authorized to make this request.",
	)
}

type openAIIPUnauthorizedFailure struct {
	at        time.Time
	requestID string
}

// Observations are process-local and reset on restart. Multiple instances count
// independently; the existing database/runtime block still owns the cooldown.
// No background goroutine, request bodies, tokens, or IP addresses are retained.
type openAIIPUnauthorizedStreak struct {
	mu        sync.Mutex
	previous  map[int64]openAIIPUnauthorizedFailure
	nextSweep time.Time
}

func (s *openAIIPUnauthorizedStreak) observe(accountID int64, requestID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.previous == nil {
		s.previous = make(map[int64]openAIIPUnauthorizedFailure)
	}
	if !now.Before(s.nextSweep) {
		for id, failure := range s.previous {
			if now.Sub(failure.at) > openAIIPUnauthorizedWindow {
				delete(s.previous, id)
			}
		}
		s.nextSweep = now.Add(openAIIPUnauthorizedWindow)
	}
	previous, exists := s.previous[accountID]
	// Some paths can report one upstream error more than once. A repeated
	// upstream request ID must neither advance the count nor extend its window.
	if exists && requestID != "" && requestID == previous.requestID {
		return false
	}
	s.previous[accountID] = openAIIPUnauthorizedFailure{at: now, requestID: requestID}
	elapsed := now.Sub(previous.at)
	return exists && elapsed >= 0 && elapsed <= openAIIPUnauthorizedWindow
}

func (s *RateLimitService) resetOpenAIIPUnauthorizedStreak(account *Account) {
	if s == nil || account == nil || account.Platform != PlatformOpenAI {
		return
	}
	id := account.ID
	if account.IsShadow() && account.ParentAccountID != nil {
		id = *account.ParentAccountID
	}
	s.openAIIPUnauthorized.mu.Lock()
	delete(s.openAIIPUnauthorized.previous, id)
	s.openAIIPUnauthorized.mu.Unlock()
}
