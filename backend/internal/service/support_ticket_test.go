//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// supportTicketRepoFake records what the service asks for and serves one
// stored ticket; the SQL behaviour is covered by the integration test.
type supportTicketRepoFake struct {
	ticket   *SupportTicket
	messages []SupportTicketMessage
	items    []SupportTicket
	total    int64
	deleted  bool
	err      error

	created  []SupportTicketCreateInput
	added    []SupportTicketMessageInput
	statuses []SupportTicketStatusInput
	filters  []SupportTicketListFilter
	reads    []string
	seen     []int    // message counts passed to MarkRead
	counts   [3]int64 // unread, open, pending
}

func (r *supportTicketRepoFake) CreateTicket(_ context.Context, in SupportTicketCreateInput) (int64, error) {
	r.created = append(r.created, in)
	return 7, r.err
}
func (r *supportTicketRepoFake) ListTickets(_ context.Context, filter SupportTicketListFilter) ([]SupportTicket, int64, error) {
	r.filters = append(r.filters, filter)
	return r.items, r.total, r.err
}
func (r *supportTicketRepoFake) GetTicket(_ context.Context, id, ownerID int64, withUser bool) (*SupportTicket, []SupportTicketMessage, error) {
	if r.ticket == nil || r.ticket.ID != id || (ownerID > 0 && r.ticket.UserID != ownerID) {
		return nil, nil, ErrSupportTicketNotFound
	}
	ticket := *r.ticket
	if withUser {
		ticket.User = &SupportTicketUser{ID: ticket.UserID, Email: "user@example.com"}
	}
	return &ticket, append([]SupportTicketMessage(nil), r.messages...), nil
}
func (r *supportTicketRepoFake) MarkRead(_ context.Context, _ int64, role string, seenMessages int) error {
	r.reads = append(r.reads, role)
	r.seen = append(r.seen, seenMessages)
	return nil
}
func (r *supportTicketRepoFake) AddMessage(_ context.Context, in SupportTicketMessageInput) error {
	r.added = append(r.added, in)
	return r.err
}
func (r *supportTicketRepoFake) SetStatus(_ context.Context, in SupportTicketStatusInput) error {
	r.statuses = append(r.statuses, in)
	return r.err
}
func (r *supportTicketRepoFake) DeleteTicket(context.Context, int64) (bool, error) {
	return r.deleted, r.err
}
func (r *supportTicketRepoFake) CountUnread(context.Context, int64) (int64, error) {
	return r.counts[0], nil
}
func (r *supportTicketRepoFake) CountOpen(context.Context, int64) (int64, error) {
	return r.counts[1], nil
}
func (r *supportTicketRepoFake) CountPending(context.Context) (int64, error) { return r.counts[2], nil }

type supportTicketSettingsFake struct {
	runtime SupportTicketRuntime
	err     error
}

func (f supportTicketSettingsFake) GetSupportTicketRuntime(context.Context) (SupportTicketRuntime, error) {
	return f.runtime, f.err
}

func newSupportTicketTestService(repo *supportTicketRepoFake) *SupportTicketService {
	return &SupportTicketService{repo: repo, settings: supportTicketSettingsFake{runtime: SupportTicketRuntime{
		Enabled: true, Config: SupportTicketConfig{Categories: []string{"API 使用问题", "其他"}, MaxOpenPerUser: 3, Notice: "9–21 点回复"},
	}}}
}

func supportTicketFixture() *supportTicketRepoFake {
	return &supportTicketRepoFake{
		ticket: &SupportTicket{ID: 7, UserID: 42, Title: "报错", Status: SupportTicketStatusReplied, UserUnread: true, AdminUnread: true,
			LastMessageAt: time.Unix(1_790_000_000, 0)},
		messages: []SupportTicketMessage{
			{ID: 1, AuthorRole: SupportTicketRoleUser, AuthorName: "alice", Body: "help"},
			{ID: 2, AuthorRole: SupportTicketRoleAdmin, AuthorName: "root-admin", Body: "on it"},
		},
	}
}

func TestSupportTicketDisabledEverywhere(t *testing.T) {
	repo := supportTicketFixture()
	ctx := context.Background()
	for name, settings := range map[string]supportTicketSettingsFake{
		"off":        {runtime: SupportTicketRuntime{Enabled: false}},
		"unreadable": {err: errors.New("db down")},
	} {
		s := &SupportTicketService{repo: repo, settings: settings}
		require.False(t, s.Enabled(ctx), name)
		calls := map[string]func() error{
			"summary": func() error { _, err := s.UserSummary(ctx, 42); return err },
			"list":    func() error { _, err := s.ListForUser(ctx, 42, "", 1, 20); return err },
			"create": func() error {
				_, err := s.Create(ctx, 42, SupportTicketCreateRequest{Title: "t", Body: "b", Category: "其他"})
				return err
			},
			"get":          func() error { _, err := s.GetForUser(ctx, 42, 7); return err },
			"reply":        func() error { _, err := s.ReplyAsUser(ctx, 42, 7, "hi"); return err },
			"close":        func() error { _, err := s.CloseAsUser(ctx, 42, 7); return err },
			"reopen":       func() error { _, err := s.ReopenAsUser(ctx, 42, 7); return err },
			"adminSummary": func() error { _, err := s.AdminSummary(ctx); return err },
			"adminList":    func() error { _, err := s.ListForAdmin(ctx, SupportTicketListFilter{}); return err },
			"adminGet":     func() error { _, err := s.GetForAdmin(ctx, 7); return err },
			"adminReply":   func() error { _, err := s.ReplyAsAdmin(ctx, 1, 7, "hi", ""); return err },
			"adminStatus":  func() error { _, err := s.SetStatusAsAdmin(ctx, 7, SupportTicketStatusClosed); return err },
			"delete":       func() error { return s.Delete(ctx, 7) },
		}
		for call, run := range calls {
			err := run()
			require.Error(t, err, "%s/%s", name, call)
			if name == "off" {
				require.ErrorIs(t, err, ErrSupportTicketDisabled, call)
			}
		}
	}
	require.Empty(t, repo.created, "nothing reaches storage while disabled")
	require.Empty(t, repo.added)
	require.Empty(t, repo.statuses)
	require.Empty(t, repo.reads)
}

func TestSupportTicketCreateValidatesAndCleans(t *testing.T) {
	repo := supportTicketFixture()
	repo.ticket.ID = 7
	s := newSupportTicketTestService(repo)
	ctx := context.Background()

	detail, err := s.Create(ctx, 42, SupportTicketCreateRequest{
		Category: " api 使用问题 ", Title: "  401\n报错\t了  ", Body: "line one\r\nline\x00 two\x07\n",
	})
	require.NoError(t, err)
	require.Len(t, repo.created, 1)
	in := repo.created[0]
	require.Equal(t, int64(42), in.UserID)
	require.Equal(t, "API 使用问题", in.Category, "the configured spelling is stored")
	require.Equal(t, "401 报错 了", in.Title, "titles are one line")
	require.Equal(t, "line one\nline two", in.Body, "control characters are dropped, line breaks kept")
	require.Equal(t, SupportTicketLimits{MaxOpen: 3, MessagesPerMinute: SupportTicketMessagesPerMinute, TicketsPerDay: SupportTicketTicketsPerDay}, in.Limits)
	require.Nil(t, detail.Ticket.User, "users never get the admin view of themselves")
	require.Empty(t, detail.Messages[1].AuthorName, "admin names stay private")

	for name, tc := range map[string]struct {
		req SupportTicketCreateRequest
		err error
	}{
		"no title":     {SupportTicketCreateRequest{Title: " \n ", Body: "b", Category: "其他"}, ErrSupportTicketTitleRequired},
		"long title":   {SupportTicketCreateRequest{Title: strings.Repeat("长", 101), Body: "b", Category: "其他"}, ErrSupportTicketTitleTooLong},
		"no body":      {SupportTicketCreateRequest{Title: "t", Body: "\x00\t ", Category: "其他"}, ErrSupportTicketBodyRequired},
		"long body":    {SupportTicketCreateRequest{Title: "t", Body: strings.Repeat("字", 5001), Category: "其他"}, ErrSupportTicketBodyTooLong},
		"bad category": {SupportTicketCreateRequest{Title: "t", Body: "b", Category: "退款"}, ErrSupportTicketBadCategory},
		"no category":  {SupportTicketCreateRequest{Title: "t", Body: "b"}, ErrSupportTicketBadCategory},
	} {
		_, err := s.Create(ctx, 42, tc.req)
		require.ErrorIs(t, err, tc.err, name)
	}
	require.Len(t, repo.created, 1, "invalid tickets are not stored")
	_, err = s.Create(ctx, 42, SupportTicketCreateRequest{Title: strings.Repeat("长", 100), Body: strings.Repeat("字", 5000), Category: "其他"})
	require.NoError(t, err, "limits are counted in characters, not bytes")

	s.settings = supportTicketSettingsFake{runtime: SupportTicketRuntime{Enabled: true, Config: SupportTicketConfig{MaxOpenPerUser: 3}}}
	_, err = s.Create(ctx, 42, SupportTicketCreateRequest{Title: "t", Body: "b", Category: "anything"})
	require.NoError(t, err)
	require.Empty(t, repo.created[len(repo.created)-1].Category, "without configured categories the field is ignored")
}

func TestSupportTicketLimitErrorsCarryTheLimit(t *testing.T) {
	ctx := context.Background()
	for err, max := range map[*infraerrors.ApplicationError]string{
		ErrSupportTicketTooManyOpen: "3",
		ErrSupportTicketTooFast:     "5",
		ErrSupportTicketDailyLimit:  "10",
		ErrSupportTicketFull:        "200",
	} {
		repo := supportTicketFixture()
		repo.err = err
		s := newSupportTicketTestService(repo)
		_, got := s.Create(ctx, 42, SupportTicketCreateRequest{Title: "t", Body: "b", Category: "其他"})
		if err == ErrSupportTicketFull {
			_, got = s.ReplyAsUser(ctx, 42, 7, "b")
		}
		require.ErrorIs(t, got, err)
		var appErr *infraerrors.ApplicationError
		require.True(t, errors.As(got, &appErr))
		require.Equal(t, max, appErr.Metadata["max"], appErr.Reason)
	}
}

func TestSupportTicketOpeningClearsOnlyTheReadersMark(t *testing.T) {
	repo := supportTicketFixture()
	s := newSupportTicketTestService(repo)
	ctx := context.Background()

	_, err := s.GetForUser(ctx, 99, 7)
	require.ErrorIs(t, err, ErrSupportTicketNotFound, "someone else's ticket does not exist for you")

	detail, err := s.GetForUser(ctx, 42, 7)
	require.NoError(t, err)
	require.Equal(t, []string{SupportTicketRoleUser}, repo.reads)
	require.Equal(t, []int{len(detail.Messages)}, repo.seen, "only the messages the reader got count as read")
	require.False(t, detail.Ticket.UserUnread)
	require.True(t, detail.Ticket.AdminUnread, "the user reading does not touch the admin side")
	require.Nil(t, detail.Ticket.User)
	require.Empty(t, detail.Messages[1].AuthorName)

	repo.ticket.UserUnread = false
	_, err = s.GetForUser(ctx, 42, 7)
	require.NoError(t, err)
	require.Len(t, repo.reads, 1, "an already read ticket is not written again")

	detail, err = s.GetForAdmin(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, []string{SupportTicketRoleUser, SupportTicketRoleAdmin}, repo.reads)
	require.False(t, detail.Ticket.AdminUnread)
	require.Equal(t, "user@example.com", detail.Ticket.User.Email, "admins see the author")
	require.Equal(t, "root-admin", detail.Messages[1].AuthorName, "admins see who answered")
}

func TestSupportTicketReplies(t *testing.T) {
	repo := supportTicketFixture()
	s := newSupportTicketTestService(repo)
	ctx := context.Background()

	_, err := s.ReplyAsUser(ctx, 42, 7, "  more info  ")
	require.NoError(t, err)
	require.Equal(t, SupportTicketMessageInput{
		TicketID: 7, OwnerID: 42, AuthorRole: SupportTicketRoleUser, AuthorUserID: 42, Body: "more info",
		NextStatus: SupportTicketStatusPending, Limits: SupportTicketLimits{MaxOpen: 3, MessagesPerMinute: 5, TicketsPerDay: 10},
	}, repo.added[0])

	for status, want := range map[string]string{
		"":                            SupportTicketStatusReplied,
		SupportTicketStatusProcessing: SupportTicketStatusProcessing,
		SupportTicketStatusClosed:     SupportTicketStatusClosed,
	} {
		_, err = s.ReplyAsAdmin(ctx, 1, 7, "fixed", status)
		require.NoError(t, err)
		last := repo.added[len(repo.added)-1]
		require.Equal(t, want, last.NextStatus)
		require.Equal(t, SupportTicketRoleAdmin, last.AuthorRole)
		require.Zero(t, last.OwnerID, "admins answer any ticket")
		require.Equal(t, SupportTicketLimits{}, last.Limits, "admins are not rate limited")
	}
	added := len(repo.added)
	_, err = s.ReplyAsAdmin(ctx, 1, 7, "fixed", SupportTicketStatusPending)
	require.ErrorIs(t, err, ErrSupportTicketBadStatus)
	_, err = s.ReplyAsUser(ctx, 42, 7, " ")
	require.ErrorIs(t, err, ErrSupportTicketBodyRequired)
	require.Len(t, repo.added, added)
}

func TestSupportTicketStatusChanges(t *testing.T) {
	repo := supportTicketFixture()
	s := newSupportTicketTestService(repo)
	ctx := context.Background()

	_, err := s.CloseAsUser(ctx, 42, 7)
	require.NoError(t, err)
	_, err = s.ReopenAsUser(ctx, 42, 7)
	require.NoError(t, err)
	require.Equal(t, SupportTicketStatusInput{TicketID: 7, OwnerID: 42, ActorRole: SupportTicketRoleUser, Status: SupportTicketStatusClosed,
		Limits: SupportTicketLimits{MaxOpen: 3, MessagesPerMinute: 5, TicketsPerDay: 10}}, repo.statuses[0])
	require.Equal(t, SupportTicketStatusPending, repo.statuses[1].Status)
	require.Equal(t, 3, repo.statuses[1].Limits.MaxOpen, "reopening counts against the open limit")

	for _, status := range []string{SupportTicketStatusProcessing, SupportTicketStatusClosed, SupportTicketStatusPending} {
		_, err = s.SetStatusAsAdmin(ctx, 7, status)
		require.NoError(t, err)
		last := repo.statuses[len(repo.statuses)-1]
		require.Equal(t, SupportTicketRoleAdmin, last.ActorRole)
		require.Zero(t, last.Limits.MaxOpen, "admins reopen regardless of the user's limit")
	}
	_, err = s.SetStatusAsAdmin(ctx, 7, SupportTicketStatusReplied)
	require.ErrorIs(t, err, ErrSupportTicketBadStatus, "replied only follows a message")

	require.ErrorIs(t, s.Delete(ctx, 7), ErrSupportTicketNotFound)
	repo.deleted = true
	require.NoError(t, s.Delete(ctx, 7))
}

func TestSupportTicketListing(t *testing.T) {
	repo := supportTicketFixture()
	s := newSupportTicketTestService(repo)
	ctx := context.Background()

	page, err := s.ListForUser(ctx, 42, "", 0, 500)
	require.NoError(t, err)
	require.Equal(t, SupportTicketListFilter{UserID: 42, Page: 1, PageSize: 100}, repo.filters[0])
	require.NotNil(t, page.Items, "an empty page is a list, not null")
	_, err = s.ListForUser(ctx, 42, "", 2, 0)
	require.NoError(t, err)
	require.Equal(t, 20, repo.filters[1].PageSize)
	_, err = s.ListForUser(ctx, 42, "deleted", 1, 20)
	require.ErrorIs(t, err, ErrSupportTicketBadStatus)

	_, err = s.ListForAdmin(ctx, SupportTicketListFilter{UserID: 42, Keyword: " #12\n" + strings.Repeat("k", 200), Category: " 其他 "})
	require.NoError(t, err)
	last := repo.filters[len(repo.filters)-1]
	require.Zero(t, last.UserID, "admins list every user's tickets")
	require.Equal(t, "其他", last.Category)
	require.Equal(t, SupportTicketKeywordMaxRunes, len([]rune(last.Keyword)))
	require.True(t, strings.HasPrefix(last.Keyword, "#12 k"))
	require.True(t, last.PendingFirst)
	for status, pendingFirst := range map[string]bool{SupportTicketStatusOpen: true, SupportTicketStatusClosed: false, SupportTicketStatusPending: false} {
		_, err = s.ListForAdmin(ctx, SupportTicketListFilter{Status: status})
		require.NoError(t, err)
		require.Equal(t, pendingFirst, repo.filters[len(repo.filters)-1].PendingFirst, status)
	}
}

func TestSupportTicketSummaries(t *testing.T) {
	repo := supportTicketFixture()
	repo.counts = [3]int64{2, 3, 9}
	s := newSupportTicketTestService(repo)
	ctx := context.Background()

	summary, err := s.UserSummary(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, &SupportTicketUserSummary{UnreadCount: 2, OpenCount: 3, MaxOpen: 3, Categories: []string{"API 使用问题", "其他"}, Notice: "9–21 点回复"}, summary)
	admin, err := s.AdminSummary(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(9), admin.PendingCount)
	require.Equal(t, []string{"API 使用问题", "其他"}, admin.Categories)
}

func TestSupportTicketConfigNormalization(t *testing.T) {
	cfg, err := NormalizeSupportTicketConfig(SupportTicketConfig{
		Categories: []string{" 充值 ", "", "充值", "Billing", "billing", "API\n报错"},
		Notice:     "  工作时间\r\n9–21 点\x00  ",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"充值", "Billing", "API 报错"}, cfg.Categories, "trimmed, deduplicated ignoring case, one line")
	require.Equal(t, SupportTicketDefaultMaxOpen, cfg.MaxOpenPerUser, "zero takes the default")
	require.Equal(t, "工作时间\n9–21 点", cfg.Notice)

	tooMany := make([]string, SupportTicketMaxCategories+1)
	for i := range tooMany {
		tooMany[i] = strings.Repeat("c", i+1)
	}
	for name, bad := range map[string]SupportTicketConfig{
		"too many categories": {Categories: tooMany},
		"long category":       {Categories: []string{strings.Repeat("类", SupportTicketCategoryMaxRunes+1)}},
		"open limit":          {MaxOpenPerUser: SupportTicketMaxOpenLimit + 1},
		"negative limit":      {MaxOpenPerUser: -1},
		"long notice":         {Notice: strings.Repeat("字", SupportTicketNoticeMaxRunes+1)},
	} {
		_, err := NormalizeSupportTicketConfig(bad)
		require.Error(t, err, name)
	}

	defaults, err := parseSupportTicketConfig("")
	require.NoError(t, err)
	require.Equal(t, DefaultSupportTicketConfig(), defaults)
	require.Len(t, defaults.Categories, 5)
	empty, err := parseSupportTicketConfig(`{"categories":[],"max_open_per_user":0}`)
	require.NoError(t, err)
	require.Empty(t, empty.Categories, "an admin can remove every category")
	require.NotNil(t, empty.Categories, "it is stored as an empty list")
	require.Equal(t, SupportTicketDefaultMaxOpen, empty.MaxOpenPerUser)
	_, err = parseSupportTicketConfig(`{"categories":`)
	require.Error(t, err, "corrupt JSON fails closed")
}

func TestSupportTicketRuntimeReadsSwitchAndConfig(t *testing.T) {
	repo := &settingRepoValuesFake{values: map[string]string{
		SettingKeySupportTicketEnabled: "true",
		SettingKeySupportTicketConfig:  `{"categories":["其他"],"max_open_per_user":2,"notice":"n"}`,
	}}
	s := &SettingService{settingRepo: repo}
	runtime, err := s.GetSupportTicketRuntime(context.Background())
	require.NoError(t, err)
	require.Equal(t, SupportTicketRuntime{Enabled: true, Config: SupportTicketConfig{Categories: []string{"其他"}, MaxOpenPerUser: 2, Notice: "n"}}, runtime)

	repo.values[SettingKeySupportTicketEnabled] = "1"
	runtime, err = s.GetSupportTicketRuntime(context.Background())
	require.NoError(t, err)
	require.False(t, runtime.Enabled, "only the literal true enables it")

	repo.values[SettingKeySupportTicketConfig] = "{"
	_, err = s.GetSupportTicketRuntime(context.Background())
	require.Error(t, err)
	_, err = (*SettingService)(nil).GetSupportTicketRuntime(context.Background())
	require.Error(t, err)
}

// settingRepoValuesFake serves settings from a map.
type settingRepoValuesFake struct {
	SettingRepository
	values map[string]string
}

func (r *settingRepoValuesFake) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}
