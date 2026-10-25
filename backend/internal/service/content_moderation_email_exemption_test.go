//go:build unit

package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type moderationMailRepo struct {
	*moderationRecoveryRepo
	emailUpdates int
	beforeRead   func()
}

func (r *moderationMailRepo) GetModerationUser(ctx context.Context, id int64, since time.Time, exclude bool) (*ModerationUserState, error) {
	if r.beforeRead != nil {
		r.beforeRead()
	}
	return r.moderationRecoveryRepo.GetModerationUser(ctx, id, since, exclude)
}
func (r *moderationMailRepo) UpdateLogEmailSent(context.Context, int64, bool) error {
	r.emailUpdates++
	return nil
}

func moderationMailFixture(t *testing.T, path string) (*ContentModerationService, *moderationMailRepo, *fakeSMTPServer) {
	t.Helper()
	smtp, port := startFakeSMTPServer(t, false, true)
	settings := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeySMTPHost: "localhost", SettingKeySMTPPort: strconv.Itoa(port),
		SettingKeySMTPUsername: "fixture", SettingKeySMTPPassword: "fixture",
		SettingKeySMTPFrom: "sender@example.test", SettingKeyRiskControlEnabled: "true",
	}}
	repo := &moderationMailRepo{moderationRecoveryRepo: &moderationRecoveryRepo{
		contentModerationTestRepo: &contentModerationTestRepo{},
		state:                     ModerationUserState{Version: 2},
		result:                    ModerationHitResult{Accepted: true, Count: 1, BanApplied: true, OwnedDisable: true},
	}}
	email := NewEmailService(settings, nil)
	if path != "built-in" {
		NewNotificationEmailService(settings, email)
		if path == "fallback" {
			for _, event := range []string{NotificationEmailEventContentModerationViolation, NotificationEmailEventContentModerationDisabled, NotificationEmailEventCyberPolicyNotice} {
				settings.values[notificationEmailTemplateKey(event, "en")] = "invalid JSON fixture"
			}
		}
	}
	return &ContentModerationService{repo: repo, emailService: email, settingRepo: settings}, repo, smtp
}

func moderationMailLog() *ContentModerationLog {
	id, version := int64(42), int64(2)
	return &ContentModerationLog{UserID: &id, UserEmail: "user@example.test", moderationVersion: &version, Flagged: true, Action: ContentModerationActionKeywordBlock, CreatedAt: time.Now()}
}

func moderationMailSend(svc *ContentModerationService, event string, log *ContentModerationLog) error {
	switch event {
	case "violation":
		return svc.sendViolationEmail(context.Background(), defaultContentModerationConfig(), log)
	case "disabled":
		return svc.sendAccountDisabledEmail(context.Background(), defaultContentModerationConfig(), log)
	default:
		return svc.sendCyberPolicyEmail(context.Background(), log)
	}
}

func TestModerationEmailExemptionActualSendPaths(t *testing.T) {
	for _, path := range []string{"built-in", "template", "fallback"} {
		for _, event := range []string{"violation", "disabled", "cyber"} {
			t.Run(path+"/"+event, func(t *testing.T) {
				svc, repo, smtp := moderationMailFixture(t, path)
				repo.state.EmailExempt = true
				log := moderationMailLog()
				err := moderationMailSend(svc, event, log)
				require.ErrorIs(t, err, errModerationEmailExempt)
				require.False(t, shouldFallbackNotificationEmail(err))
				require.Zero(t, smtp.conns.Load())
				require.False(t, log.EmailSent)
				require.Zero(t, repo.emailUpdates)
				// Revoking only the email flag restores the next actual send.
				off := false
				cache := &contentModerationTestAuthCacheInvalidator{}
				svc.authCacheInvalidator = cache
				state, err := svc.ManageModerationUser(context.Background(), 42, ModerationUserAction{Action: "exemption", Version: 2, ActorID: 7, Reason: "resume notices", Confirm: true, EmailExempt: &off})
				require.NoError(t, err)
				require.False(t, state.EmailExempt)
				require.Equal(t, []int64{42}, cache.userIDs)
				require.NoError(t, moderationMailSend(svc, event, log))
				require.EqualValues(t, 1, smtp.conns.Load())
				// sawCommand uppercases entire lines, including mailbox local parts.
				// Compare raw RCPT commands to preserve the exact recipient and reject extras.
				smtp.mu.Lock()
				commands := append([]string(nil), smtp.commands...)
				smtp.mu.Unlock()
				var recipients []string
				for _, command := range commands {
					if strings.HasPrefix(strings.ToUpper(command), "RCPT TO:") {
						recipients = append(recipients, command)
					}
				}
				require.Equal(t, []string{"RCPT TO:<user@example.test>"}, recipients)
				require.True(t, smtp.sawCommand("DATA"))
			})
		}
	}
}

func TestModerationEmailExemptionRechecksAndReadErrorsNeverFallback(t *testing.T) {
	for _, path := range []string{"built-in", "template", "fallback"} {
		for _, event := range []string{"violation", "disabled", "cyber"} {
			for _, fail := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/error%t", path, event, fail), func(t *testing.T) {
					svc, repo, smtp := moderationMailFixture(t, path)
					repo.beforeRead = func() {
						if repo.readCount == 1 {
							if fail {
								repo.getErr = context.DeadlineExceeded
							} else {
								repo.state.EmailExempt = true
							}
						}
					}
					err := moderationMailSend(svc, event, moderationMailLog())
					if fail {
						require.ErrorIs(t, err, context.DeadlineExceeded)
					} else {
						require.ErrorIs(t, err, errModerationEmailExempt)
					}
					require.False(t, shouldFallbackNotificationEmail(err))
					require.Zero(t, smtp.conns.Load())
					require.Equal(t, 2, repo.readCount)
				})
			}
		}
	}
}

func TestModerationEmailExemptionKeepsGlobalSwitchAndHistory(t *testing.T) {
	svc, repo, smtp := moderationMailFixture(t, "template")
	cfg := defaultContentModerationConfig()
	cfg.EmailOnHit = false
	log := moderationMailLog()
	svc.sendFlaggedNotificationSideEffects(context.Background(), cfg, log, false)
	require.Zero(t, smtp.conns.Load())
	require.False(t, log.EmailSent)
	cfg.EmailOnHit = true
	repo.state.EmailExempt = true
	svc.persistContentModerationLog(context.Background(), cfg, log, "fixture", false, true)
	require.False(t, log.EmailSent)
	require.Zero(t, repo.emailUpdates)
	require.Len(t, repo.hits, 1)
	require.True(t, log.AutoBanned)
	history := repo.snapshotLogs()
	require.Len(t, history, 1)
	require.False(t, history[0].EmailSent)
	repo.state.EmailExempt = false
	future := moderationMailLog()
	svc.sendFlaggedNotificationSideEffects(context.Background(), cfg, future, false)
	require.True(t, future.EmailSent)
	require.EqualValues(t, 1, smtp.conns.Load())
	require.False(t, repo.snapshotLogs()[0].EmailSent, "old evidence must not become current status")
}

func TestModerationEmailExemptionQueuedRecordAndObserveSameVersionReadLatest(t *testing.T) {
	for _, mode := range []string{ContentModerationModePreBlock, ContentModerationModeObserve} {
		t.Run(mode, func(t *testing.T) {
			svc, _, calls := exemptionService(t, mode, ContentModerationKeywordModeKeywordAndAPI, ModerationUserState{Version: 2})
			mailSvc, repo, smtp := moderationMailFixture(t, "template")
			svc.repo, svc.emailService = repo, mailSvc.emailService
			input := exemptionInput(true)
			input.UserEmail = "user@example.test"
			_, err := svc.Check(context.Background(), input)
			require.NoError(t, err)
			require.Len(t, svc.asyncQueue, 1)
			task := <-svc.asyncQueue
			// Deliberately isolate same-version mail eligibility here. This state
			// assignment is NOT a management save: real saves increment version
			// and reject old queued hits. The repository integration regression
			// TestModerationMailManagementSaveFencesQueuedTasks covers that path.
			repo.state.EmailExempt = true
			snapshot, err := svc.loadRuntimeSnapshot(context.Background())
			require.NoError(t, err)
			if task.log != nil {
				svc.persistContentModerationLog(context.Background(), task.config, task.log, task.inputHash, task.recordHash, task.applySideEffects)
			} else {
				delay := 1
				svc.checkSync(context.Background(), task.input, snapshot.config, task.content, task.inputHash, &delay, false)
			}
			require.Equal(t, map[string]int{ContentModerationModePreBlock: 0, ContentModerationModeObserve: 1}[mode], *calls)
			require.Len(t, repo.hits, 1)
			require.Len(t, repo.snapshotLogs(), 1)
			require.Zero(t, smtp.conns.Load())
			require.Zero(t, repo.emailUpdates)
		})
	}
}

func TestModerationEmailExemptionCyberStillCountsAndPersists(t *testing.T) {
	svc, repo, smtp := moderationMailFixture(t, "template")
	repo.state.EmailExempt = true
	version := int64(2)
	svc.RecordCyberPolicyEvent(context.Background(), CyberPolicyRecordInput{UserID: 42, UserEmail: "user@example.test", ModerationVersion: &version, RequestID: "cyber-mail-fixture"})
	logs := repo.snapshotLogs()
	require.Len(t, logs, 1)
	require.Equal(t, ContentModerationActionCyberPolicy, logs[0].Action)
	require.Len(t, repo.hits, 1)
	require.True(t, logs[0].AutoBanned)
	require.False(t, logs[0].EmailSent)
	require.Zero(t, repo.emailUpdates)
	require.Zero(t, smtp.conns.Load())
}

func TestModerationEmailExemptionDoesNotSuppressUnrelatedEmails(t *testing.T) {
	svc, repo, smtp := moderationMailFixture(t, "template")
	repo.state.EmailExempt = true
	for _, event := range []string{NotificationEmailEventAuthVerifyCode, NotificationEmailEventAuthPasswordReset, NotificationEmailEventBalanceLow} {
		require.NoError(t, svc.emailService.notificationEmailService.Send(context.Background(), NotificationEmailSendInput{Event: event, UserID: 42, RecipientEmail: "user@example.test"}))
	}
	require.EqualValues(t, 3, smtp.conns.Load())
	require.Zero(t, repo.readCount)
}
