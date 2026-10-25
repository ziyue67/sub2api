//go:build integration

package repository

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Only scheduling is controlled here. Management writes, version fencing, hit
// counts, account penalties and mail eligibility all use the real repository.
type moderationMailPendingHit struct {
	hit     service.ModerationHit
	release chan struct{}
}

type moderationMailFencingRepo struct {
	*contentModerationRepository
	ctx     context.Context
	pending chan moderationMailPendingHit
}

func (r *moderationMailFencingRepo) ApplyModerationHit(ctx context.Context, hit service.ModerationHit) (service.ModerationHitResult, error) {
	pending := moderationMailPendingHit{hit: hit, release: make(chan struct{})}
	select {
	case r.pending <- pending:
	case <-r.ctx.Done():
		return service.ModerationHitResult{}, r.ctx.Err()
	case <-ctx.Done():
		return service.ModerationHitResult{}, ctx.Err()
	}
	select {
	case <-pending.release:
		return r.contentModerationRepository.ApplyModerationHit(ctx, hit)
	case <-r.ctx.Done():
		return service.ModerationHitResult{}, r.ctx.Err()
	case <-ctx.Done():
		return service.ModerationHitResult{}, ctx.Err()
	}
}

// Immutable, in-memory configuration keeps all mail and audit traffic on the
// local fixtures. User management state is never held or changed in this map.
type moderationMailFencingSettings struct {
	service.SettingRepository
	values map[string]string
}

func (s *moderationMailFencingSettings) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}
func (s *moderationMailFencingSettings) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = s.values[key]
	}
	return values, nil
}

// Loopback SMTP sink: accepts fixtures only, records complete DATA messages and
// never relays mail. No production credentials, transport or SMTP service.
func startModerationMailFencingSMTP(t *testing.T) (int, *atomic.Int64) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	delivered := new(atomic.Int64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := textproto.NewReader(bufio.NewReader(conn))
				reply := func(line string) { _, _ = fmt.Fprint(conn, line+"\r\n") }
				reply("220 localhost fixture")
				for {
					line, err := reader.ReadLine()
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						reply("250-localhost\r\n250 AUTH PLAIN")
					case strings.HasPrefix(line, "AUTH"):
						reply("235 fixture authenticated")
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						reply("250 ok")
					case line == "DATA":
						reply("354 fixture data")
						if _, err := reader.ReadDotBytes(); err != nil {
							return
						}
						delivered.Add(1)
						reply("250 accepted")
					case line == "QUIT":
						reply("221 bye")
						return
					default:
						reply("500 unsupported fixture command")
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	return listener.Addr().(*net.TCPAddr).Port, delivered
}

func TestModerationMailManagementSaveFencesQueuedTasks(t *testing.T) {
	for _, mode := range []string{service.ContentModerationModePreBlock, service.ContentModerationModeObserve} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var id int64
			email := fmt.Sprintf("mail-fence-%s-%d@example.test", mode, time.Now().UnixNano())
			require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES($1,'unused-fixture') RETURNING id`, email).Scan(&id))
			t.Cleanup(func() {
				for _, query := range []string{`DELETE FROM content_moderation_logs WHERE user_id=$1`, `DELETE FROM content_moderation_user_actions WHERE user_id=$1`, `DELETE FROM users WHERE id=$1`} {
					_, err := integrationDB.ExecContext(context.Background(), query, id)
					require.NoError(t, err)
				}
			})
			auditCalls := new(atomic.Int64)
			audit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				auditCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"results":[{"category_scores":{"violence":1}}]}`))
			}))
			defer audit.Close()
			port, delivered := startModerationMailFencingSMTP(t)
			cfg := service.ContentModerationConfig{
				Engine: service.ContentModerationEngineOpenAI, Enabled: true, Mode: mode,
				BaseURL: audit.URL, APIKeys: []string{"offline-fixture"}, AllGroups: true, SampleRate: 100,
				KeywordBlockingMode: service.ContentModerationKeywordModeKeywordAndAPI, BlockedKeywords: []string{"blocked-fixture"},
				AutoBanEnabled: true, BanThreshold: 1, EmailOnHit: true, WorkerCount: 1,
			}
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			settings := &moderationMailFencingSettings{values: map[string]string{
				service.SettingKeyRiskControlEnabled: "true", service.SettingKeyContentModerationConfig: string(raw),
				service.SettingKeySMTPHost: "127.0.0.1", service.SettingKeySMTPPort: strconv.Itoa(port),
				service.SettingKeySMTPUsername: "fixture", service.SettingKeySMTPPassword: "fixture", service.SettingKeySMTPFrom: "sender@example.test",
			}}
			repo := &moderationMailFencingRepo{contentModerationRepository: &contentModerationRepository{db: integrationDB}, ctx: ctx, pending: make(chan moderationMailPendingHit, 1)}
			svc := service.NewContentModerationService(settings, repo, nil, nil, nil, nil, nil, service.NewEmailService(settings, nil))
			state, err := svc.GetModerationUser(ctx, id)
			require.NoError(t, err)
			require.Zero(t, state.Version)
			require.False(t, state.EmailExempt)

			enqueue := func(requestID string) moderationMailPendingHit {
				decision, err := svc.Check(ctx, service.ContentModerationCheckInput{
					UserID: id, UserEmail: email, RequestID: requestID,
					Protocol: service.ContentModerationProtocolOpenAIChat,
					Body:     []byte(`{"messages":[{"role":"user","content":"blocked-fixture"}]}`),
				})
				require.NoError(t, err)
				require.Equal(t, mode == service.ContentModerationModePreBlock, decision.Blocked)
				select {
				case pending := <-repo.pending:
					require.Equal(t, requestID, pending.hit.RequestID)
					return pending
				case <-time.After(10 * time.Second):
					t.Fatal("queued task did not reach real hit repository")
					return moderationMailPendingHit{}
				}
			}
			waitProcessed := func(total int64) {
				require.Eventually(t, func() bool {
					status, err := svc.GetStatus(ctx)
					return err == nil && status.Processed == total && status.ActiveWorkers == 0
				}, 10*time.Second, 10*time.Millisecond)
			}
			saveEmail := func(before *service.ModerationUserState, exempt bool) *service.ModerationUserState {
				after, err := svc.ManageModerationUser(ctx, id, service.ModerationUserAction{Action: "exemption", Version: before.Version, ActorID: 7, Reason: "mail flag only", Confirm: true, EmailExempt: &exempt})
				require.NoError(t, err)
				require.Equal(t, before.Version+1, after.Version)
				require.Equal(t, exempt, after.EmailExempt)
				require.Equal(t, before.CurrentCount, after.CurrentCount)
				require.Equal(t, before.Status, after.Status)
				require.False(t, after.AutoBanExempt)
				require.False(t, after.KeywordExempt)
				require.False(t, after.APIAuditExempt)
				return after
			}
			assertLog := func(requestID string, count int, banned, sent bool) {
				var actualCount int
				var actualBanned, actualSent bool
				require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT violation_count,auto_banned,email_sent FROM content_moderation_logs WHERE user_id=$1 AND request_id=$2`, id, requestID).Scan(&actualCount, &actualBanned, &actualSent))
				require.Equal(t, count, actualCount)
				require.Equal(t, banned, actualBanned)
				require.Equal(t, sent, actualSent)
			}

			old := enqueue("before-mail-save")
			require.Equal(t, state.Version, old.hit.Version)
			state = saveEmail(state, true) // Real service -> repository transaction, version 0 -> 1.
			close(old.release)
			waitProcessed(1)
			state, err = svc.GetModerationUser(ctx, id)
			require.NoError(t, err)
			require.Zero(t, state.CurrentCount, "the old task must not bypass the pre-existing version fence")
			require.Equal(t, service.StatusActive, state.Status)
			require.False(t, state.OwnedDisable)
			require.EqualValues(t, 1, state.Version)
			assertLog("before-mail-save", 0, false, false) // Evidence remains, hit/penalty/notification rejected.
			require.Zero(t, delivered.Load())

			current := enqueue("after-mail-save")
			require.Equal(t, state.Version, current.hit.Version)
			close(current.release)
			waitProcessed(2)
			state, err = svc.GetModerationUser(ctx, id)
			require.NoError(t, err)
			require.Equal(t, 1, state.CurrentCount)
			require.Equal(t, service.StatusDisabled, state.Status)
			require.True(t, state.OwnedDisable)
			require.EqualValues(t, 2, state.Version, "the fresh hit applies the original auto-ban version increment")
			assertLog("after-mail-save", 1, true, false)
			require.Zero(t, delivered.Load(), "accepted current-version penalties do not imply an email exemption bypass")

			state = saveEmail(state, false) // Revocation is another real save; it does not unban or reset.
			require.Equal(t, service.StatusDisabled, state.Status)
			// Restore the moderation-owned disable explicitly so a future authenticated request can run.
			beforeUnban := state.Version
			state, err = svc.ManageModerationUser(ctx, id, service.ModerationUserAction{Action: "unban", Version: beforeUnban, ActorID: 7, Reason: "restore separately", Confirm: true})
			require.NoError(t, err)
			require.Equal(t, beforeUnban+1, state.Version)
			require.Equal(t, 1, state.CurrentCount)
			require.Equal(t, service.StatusActive, state.Status)
			require.False(t, state.EmailExempt)
			future := enqueue("after-mail-revocation")
			require.Equal(t, state.Version, future.hit.Version)
			close(future.release)
			waitProcessed(3)
			state, err = svc.GetModerationUser(ctx, id)
			require.NoError(t, err)
			require.Equal(t, 2, state.CurrentCount)
			require.True(t, state.OwnedDisable)
			require.Equal(t, service.StatusDisabled, state.Status)
			require.Equal(t, future.hit.Version+1, state.Version)
			assertLog("after-mail-revocation", 2, true, true)
			require.EqualValues(t, 2, delivered.Load(), "fresh revoked-version task sends violation and account-disable notices")
			assertLog("before-mail-save", 0, false, false)
			assertLog("after-mail-save", 1, true, false)
			wantAuditCalls := int64(0)
			if mode == service.ContentModerationModeObserve {
				wantAuditCalls = 3
			}
			require.Equal(t, wantAuditCalls, auditCalls.Load())
		})
	}
}
