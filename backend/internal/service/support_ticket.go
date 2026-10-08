package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	SupportTicketStatusPending    = "pending"
	SupportTicketStatusProcessing = "processing"
	SupportTicketStatusReplied    = "replied"
	SupportTicketStatusClosed     = "closed"
	// SupportTicketStatusOpen is a list filter for every status except closed.
	SupportTicketStatusOpen = "open"

	SupportTicketRoleUser  = "user"
	SupportTicketRoleAdmin = "admin"

	SupportTicketTitleMaxRunes   = 100
	SupportTicketBodyMaxRunes    = 5000
	SupportTicketKeywordMaxRunes = 100
	// SupportTicketMaxUserMessages caps what a user can add to one ticket.
	SupportTicketMaxUserMessages = 200

	// Flood limits for users; admins are not limited.
	SupportTicketMessagesPerMinute = 5
	SupportTicketTicketsPerDay     = 10

	supportTicketDefaultPageSize = 20
	supportTicketMaxPageSize     = 100
)

var (
	ErrSupportTicketDisabled    = infraerrors.Forbidden("SUPPORT_TICKET_DISABLED", "support tickets are disabled")
	ErrSupportTicketNotFound    = infraerrors.NotFound("SUPPORT_TICKET_NOT_FOUND", "support ticket not found")
	ErrSupportTicketClosed      = infraerrors.Conflict("SUPPORT_TICKET_CLOSED", "the ticket is closed; reopen it first")
	ErrSupportTicketNotClosed   = infraerrors.Conflict("SUPPORT_TICKET_NOT_CLOSED", "only a closed ticket can be reopened")
	ErrSupportTicketTooManyOpen = infraerrors.Conflict("SUPPORT_TICKET_TOO_MANY_OPEN", "too many unclosed tickets")
	ErrSupportTicketTooFast     = infraerrors.TooManyRequests("SUPPORT_TICKET_TOO_FAST", "messages are sent too quickly")
	ErrSupportTicketDailyLimit  = infraerrors.TooManyRequests("SUPPORT_TICKET_DAILY_LIMIT", "too many tickets created today")
	ErrSupportTicketFull        = infraerrors.Conflict("SUPPORT_TICKET_FULL", "this ticket has too many messages; open a new one")

	ErrSupportTicketTitleRequired = infraerrors.BadRequest("SUPPORT_TICKET_TITLE_REQUIRED", "the title is required")
	ErrSupportTicketTitleTooLong  = infraerrors.BadRequest("SUPPORT_TICKET_TITLE_TOO_LONG", "the title is too long")
	ErrSupportTicketBodyRequired  = infraerrors.BadRequest("SUPPORT_TICKET_BODY_REQUIRED", "the message is required")
	ErrSupportTicketBodyTooLong   = infraerrors.BadRequest("SUPPORT_TICKET_BODY_TOO_LONG", "the message is too long")
	ErrSupportTicketBadCategory   = infraerrors.BadRequest("SUPPORT_TICKET_CATEGORY_INVALID", "choose one of the listed categories")
	ErrSupportTicketBadStatus     = infraerrors.BadRequest("SUPPORT_TICKET_STATUS_INVALID", "invalid ticket status")
)

// SupportTicket is one conversation. User is filled for admin views only.
type SupportTicket struct {
	ID              int64              `json:"id"`
	UserID          int64              `json:"user_id"`
	Category        string             `json:"category"`
	Title           string             `json:"title"`
	Status          string             `json:"status"`
	MessageCount    int                `json:"message_count"`
	LastMessageAt   time.Time          `json:"last_message_at"`
	LastMessageRole string             `json:"last_message_role"`
	UserUnread      bool               `json:"user_unread"`
	AdminUnread     bool               `json:"admin_unread"`
	ClosedAt        *time.Time         `json:"closed_at,omitempty"`
	ClosedByRole    string             `json:"closed_by_role,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	User            *SupportTicketUser `json:"user,omitempty"`
}

// SupportTicketUser is what an admin sees about the ticket's author.
type SupportTicketUser struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Username  string    `json:"username"`
	Status    string    `json:"status"`
	Balance   float64   `json:"balance"`
	Deleted   bool      `json:"deleted"`
	CreatedAt time.Time `json:"created_at"`
}

// SupportTicketMessage is one entry of the conversation. AuthorName names the
// admin who replied and is only sent to admins.
type SupportTicketMessage struct {
	ID         int64     `json:"id"`
	AuthorRole string    `json:"author_role"`
	AuthorName string    `json:"author_name,omitempty"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
}

type SupportTicketDetail struct {
	Ticket   SupportTicket          `json:"ticket"`
	Messages []SupportTicketMessage `json:"messages"`
}

type SupportTicketPage struct {
	Items    []SupportTicket `json:"items"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

// SupportTicketUserSummary feeds the user's menu badge and ticket form.
type SupportTicketUserSummary struct {
	UnreadCount int64    `json:"unread_count"`
	OpenCount   int64    `json:"open_count"`
	MaxOpen     int      `json:"max_open"`
	Categories  []string `json:"categories"`
	Notice      string   `json:"notice"`
}

type SupportTicketAdminSummary struct {
	PendingCount int64    `json:"pending_count"`
	Categories   []string `json:"categories"`
}

type SupportTicketCreateRequest struct {
	Category string `json:"category"`
	Title    string `json:"title"`
	Body     string `json:"body"`
}

// SupportTicketListFilter selects tickets. UserID 0 means every user (admins).
type SupportTicketListFilter struct {
	UserID       int64
	Status       string
	Category     string
	Keyword      string
	PendingFirst bool
	Page         int
	PageSize     int
}

// SupportTicketLimits are enforced inside the write transaction; zero disables one.
type SupportTicketLimits struct {
	MaxOpen           int
	MessagesPerMinute int
	TicketsPerDay     int
}

type SupportTicketCreateInput struct {
	UserID   int64
	Category string
	Title    string
	Body     string
	Limits   SupportTicketLimits
}

// SupportTicketMessageInput adds a message. OwnerID scopes a user's write to
// their own ticket; admins pass 0.
type SupportTicketMessageInput struct {
	TicketID     int64
	OwnerID      int64
	AuthorRole   string
	AuthorUserID int64
	Body         string
	NextStatus   string
	Limits       SupportTicketLimits
}

// SupportTicketStatusInput changes the status without a message. Reopening
// (closed → pending) checks Limits.MaxOpen against the ticket's owner.
type SupportTicketStatusInput struct {
	TicketID  int64
	OwnerID   int64
	ActorRole string
	Status    string
	Limits    SupportTicketLimits
}

type SupportTicketRepository interface {
	CreateTicket(ctx context.Context, in SupportTicketCreateInput) (int64, error)
	ListTickets(ctx context.Context, filter SupportTicketListFilter) ([]SupportTicket, int64, error)
	// GetTicket returns ErrSupportTicketNotFound when the ticket is missing or,
	// with ownerID set, belongs to someone else.
	GetTicket(ctx context.Context, id, ownerID int64, withUser bool) (*SupportTicket, []SupportTicketMessage, error)
	// MarkRead clears the reader's unread mark unless a message arrived after
	// the reader loaded seenMessages of them.
	MarkRead(ctx context.Context, id int64, role string, seenMessages int) error
	AddMessage(ctx context.Context, in SupportTicketMessageInput) error
	SetStatus(ctx context.Context, in SupportTicketStatusInput) error
	DeleteTicket(ctx context.Context, id int64) (bool, error)
	CountUnread(ctx context.Context, userID int64) (int64, error)
	CountOpen(ctx context.Context, userID int64) (int64, error)
	CountPending(ctx context.Context) (int64, error)
}

type supportTicketSettings interface {
	GetSupportTicketRuntime(ctx context.Context) (SupportTicketRuntime, error)
}

type SupportTicketService struct {
	repo     SupportTicketRepository
	settings supportTicketSettings
}

func NewSupportTicketService(repo SupportTicketRepository, settings *SettingService) *SupportTicketService {
	return &SupportTicketService{repo: repo, settings: settings}
}

// Enabled reports the feature switch; unreadable settings count as disabled.
func (s *SupportTicketService) Enabled(ctx context.Context) bool {
	runtime, err := s.runtime(ctx)
	return err == nil && runtime.Enabled
}

func (s *SupportTicketService) runtime(ctx context.Context) (SupportTicketRuntime, error) {
	if s == nil || s.settings == nil {
		return SupportTicketRuntime{}, errSupportTicketSettingsUnavailable
	}
	return s.settings.GetSupportTicketRuntime(ctx)
}

func (s *SupportTicketService) config(ctx context.Context) (SupportTicketConfig, error) {
	runtime, err := s.runtime(ctx)
	if err != nil {
		return SupportTicketConfig{}, err
	}
	if !runtime.Enabled {
		return SupportTicketConfig{}, ErrSupportTicketDisabled
	}
	return runtime.Config, nil
}

func supportTicketUserLimits(cfg SupportTicketConfig) SupportTicketLimits {
	return SupportTicketLimits{
		MaxOpen:           cfg.MaxOpenPerUser,
		MessagesPerMinute: SupportTicketMessagesPerMinute,
		TicketsPerDay:     SupportTicketTicketsPerDay,
	}
}

// ---- users ----

func (s *SupportTicketService) UserSummary(ctx context.Context, userID int64) (*SupportTicketUserSummary, error) {
	cfg, err := s.config(ctx)
	if err != nil {
		return nil, err
	}
	unread, err := s.repo.CountUnread(ctx, userID)
	if err != nil {
		return nil, err
	}
	open, err := s.repo.CountOpen(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &SupportTicketUserSummary{
		UnreadCount: unread, OpenCount: open, MaxOpen: cfg.MaxOpenPerUser,
		Categories: cfg.Categories, Notice: cfg.Notice,
	}, nil
}

func (s *SupportTicketService) ListForUser(ctx context.Context, userID int64, status string, page, pageSize int) (*SupportTicketPage, error) {
	if _, err := s.config(ctx); err != nil {
		return nil, err
	}
	return s.list(ctx, SupportTicketListFilter{UserID: userID, Status: status, Page: page, PageSize: pageSize})
}

func (s *SupportTicketService) Create(ctx context.Context, userID int64, req SupportTicketCreateRequest) (*SupportTicketDetail, error) {
	cfg, err := s.config(ctx)
	if err != nil {
		return nil, err
	}
	title, err := normalizeSupportTicketTitle(req.Title)
	if err != nil {
		return nil, err
	}
	body, err := normalizeSupportTicketBody(req.Body)
	if err != nil {
		return nil, err
	}
	category, err := matchSupportTicketCategory(req.Category, cfg.Categories)
	if err != nil {
		return nil, err
	}
	id, err := s.repo.CreateTicket(ctx, SupportTicketCreateInput{
		UserID: userID, Category: category, Title: title, Body: body, Limits: supportTicketUserLimits(cfg),
	})
	if err != nil {
		return nil, supportTicketLimitError(err, cfg)
	}
	return s.detail(ctx, id, userID, false)
}

// GetForUser opens the user's own ticket and clears its unread mark.
func (s *SupportTicketService) GetForUser(ctx context.Context, userID, id int64) (*SupportTicketDetail, error) {
	if _, err := s.config(ctx); err != nil {
		return nil, err
	}
	return s.open(ctx, id, userID, false)
}

func (s *SupportTicketService) ReplyAsUser(ctx context.Context, userID, id int64, rawBody string) (*SupportTicketDetail, error) {
	cfg, err := s.config(ctx)
	if err != nil {
		return nil, err
	}
	body, err := normalizeSupportTicketBody(rawBody)
	if err != nil {
		return nil, err
	}
	if err := s.repo.AddMessage(ctx, SupportTicketMessageInput{
		TicketID: id, OwnerID: userID, AuthorRole: SupportTicketRoleUser, AuthorUserID: userID,
		Body: body, NextStatus: SupportTicketStatusPending, Limits: supportTicketUserLimits(cfg),
	}); err != nil {
		return nil, supportTicketLimitError(err, cfg)
	}
	return s.detail(ctx, id, userID, false)
}

func (s *SupportTicketService) CloseAsUser(ctx context.Context, userID, id int64) (*SupportTicketDetail, error) {
	return s.setUserStatus(ctx, userID, id, SupportTicketStatusClosed)
}

func (s *SupportTicketService) ReopenAsUser(ctx context.Context, userID, id int64) (*SupportTicketDetail, error) {
	return s.setUserStatus(ctx, userID, id, SupportTicketStatusPending)
}

func (s *SupportTicketService) setUserStatus(ctx context.Context, userID, id int64, status string) (*SupportTicketDetail, error) {
	cfg, err := s.config(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SetStatus(ctx, SupportTicketStatusInput{
		TicketID: id, OwnerID: userID, ActorRole: SupportTicketRoleUser, Status: status, Limits: supportTicketUserLimits(cfg),
	}); err != nil {
		return nil, supportTicketLimitError(err, cfg)
	}
	return s.detail(ctx, id, userID, false)
}

// ---- admins ----

func (s *SupportTicketService) AdminSummary(ctx context.Context) (*SupportTicketAdminSummary, error) {
	cfg, err := s.config(ctx)
	if err != nil {
		return nil, err
	}
	pending, err := s.repo.CountPending(ctx)
	if err != nil {
		return nil, err
	}
	return &SupportTicketAdminSummary{PendingCount: pending, Categories: cfg.Categories}, nil
}

func (s *SupportTicketService) ListForAdmin(ctx context.Context, filter SupportTicketListFilter) (*SupportTicketPage, error) {
	if _, err := s.config(ctx); err != nil {
		return nil, err
	}
	filter.UserID = 0
	filter.Category = strings.TrimSpace(filter.Category)
	filter.Keyword = cleanSupportTicketLine(filter.Keyword)
	if utf8.RuneCountInString(filter.Keyword) > SupportTicketKeywordMaxRunes {
		filter.Keyword = string([]rune(filter.Keyword)[:SupportTicketKeywordMaxRunes])
	}
	filter.PendingFirst = filter.Status == "" || filter.Status == SupportTicketStatusOpen
	return s.list(ctx, filter)
}

// GetForAdmin opens any ticket with its author and clears the admin unread mark.
func (s *SupportTicketService) GetForAdmin(ctx context.Context, id int64) (*SupportTicketDetail, error) {
	if _, err := s.config(ctx); err != nil {
		return nil, err
	}
	return s.open(ctx, id, 0, true)
}

// ReplyAsAdmin answers a ticket and moves it to nextStatus: replied (the
// default), processing or closed.
func (s *SupportTicketService) ReplyAsAdmin(ctx context.Context, adminID, id int64, rawBody, nextStatus string) (*SupportTicketDetail, error) {
	if _, err := s.config(ctx); err != nil {
		return nil, err
	}
	body, err := normalizeSupportTicketBody(rawBody)
	if err != nil {
		return nil, err
	}
	switch nextStatus {
	case "":
		nextStatus = SupportTicketStatusReplied
	case SupportTicketStatusReplied, SupportTicketStatusProcessing, SupportTicketStatusClosed:
	default:
		return nil, ErrSupportTicketBadStatus
	}
	if err := s.repo.AddMessage(ctx, SupportTicketMessageInput{
		TicketID: id, AuthorRole: SupportTicketRoleAdmin, AuthorUserID: adminID, Body: body, NextStatus: nextStatus,
	}); err != nil {
		return nil, err
	}
	return s.detail(ctx, id, 0, true)
}

// SetStatusAsAdmin marks a ticket processing, closes it, or reopens a closed
// one as pending. Admin reopening ignores the user's open-ticket limit.
func (s *SupportTicketService) SetStatusAsAdmin(ctx context.Context, id int64, status string) (*SupportTicketDetail, error) {
	if _, err := s.config(ctx); err != nil {
		return nil, err
	}
	switch status {
	case SupportTicketStatusProcessing, SupportTicketStatusClosed, SupportTicketStatusPending:
	default:
		return nil, ErrSupportTicketBadStatus
	}
	if err := s.repo.SetStatus(ctx, SupportTicketStatusInput{TicketID: id, ActorRole: SupportTicketRoleAdmin, Status: status}); err != nil {
		return nil, err
	}
	return s.detail(ctx, id, 0, true)
}

func (s *SupportTicketService) Delete(ctx context.Context, id int64) error {
	if _, err := s.config(ctx); err != nil {
		return err
	}
	deleted, err := s.repo.DeleteTicket(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrSupportTicketNotFound
	}
	return nil
}

// ---- helpers ----

func (s *SupportTicketService) list(ctx context.Context, filter SupportTicketListFilter) (*SupportTicketPage, error) {
	switch filter.Status {
	case "", SupportTicketStatusOpen, SupportTicketStatusPending, SupportTicketStatusProcessing, SupportTicketStatusReplied, SupportTicketStatusClosed:
	default:
		return nil, ErrSupportTicketBadStatus
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = supportTicketDefaultPageSize
	}
	if filter.PageSize > supportTicketMaxPageSize {
		filter.PageSize = supportTicketMaxPageSize
	}
	items, total, err := s.repo.ListTickets(ctx, filter)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []SupportTicket{}
	}
	return &SupportTicketPage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

// open loads a ticket for its reader and clears that side's unread mark.
func (s *SupportTicketService) open(ctx context.Context, id, ownerID int64, admin bool) (*SupportTicketDetail, error) {
	detail, err := s.detail(ctx, id, ownerID, admin)
	if err != nil {
		return nil, err
	}
	role, unread := SupportTicketRoleUser, &detail.Ticket.UserUnread
	if admin {
		role, unread = SupportTicketRoleAdmin, &detail.Ticket.AdminUnread
	}
	if *unread {
		if err := s.repo.MarkRead(ctx, id, role, len(detail.Messages)); err != nil {
			return nil, err
		}
		*unread = false
	}
	return detail, nil
}

func (s *SupportTicketService) detail(ctx context.Context, id, ownerID int64, admin bool) (*SupportTicketDetail, error) {
	ticket, messages, err := s.repo.GetTicket(ctx, id, ownerID, admin)
	if err != nil {
		return nil, err
	}
	if messages == nil {
		messages = []SupportTicketMessage{}
	}
	if !admin {
		ticket.User = nil
		for i := range messages {
			messages[i].AuthorName = ""
		}
	}
	return &SupportTicketDetail{Ticket: *ticket, Messages: messages}, nil
}

// supportTicketLimitError adds the configured limit to the open-ticket error so
// the UI can say how many tickets are allowed.
func supportTicketLimitError(err error, cfg SupportTicketConfig) error {
	if errors.Is(err, ErrSupportTicketTooManyOpen) {
		return ErrSupportTicketTooManyOpen.WithMetadata(map[string]string{"max": strconv.Itoa(cfg.MaxOpenPerUser)})
	}
	if errors.Is(err, ErrSupportTicketTooFast) {
		return ErrSupportTicketTooFast.WithMetadata(map[string]string{"max": strconv.Itoa(SupportTicketMessagesPerMinute)})
	}
	if errors.Is(err, ErrSupportTicketDailyLimit) {
		return ErrSupportTicketDailyLimit.WithMetadata(map[string]string{"max": strconv.Itoa(SupportTicketTicketsPerDay)})
	}
	if errors.Is(err, ErrSupportTicketFull) {
		return ErrSupportTicketFull.WithMetadata(map[string]string{"max": strconv.Itoa(SupportTicketMaxUserMessages)})
	}
	return err
}

func normalizeSupportTicketTitle(raw string) (string, error) {
	title := cleanSupportTicketLine(raw)
	if title == "" {
		return "", ErrSupportTicketTitleRequired
	}
	if utf8.RuneCountInString(title) > SupportTicketTitleMaxRunes {
		return "", ErrSupportTicketTitleTooLong
	}
	return title, nil
}

func normalizeSupportTicketBody(raw string) (string, error) {
	body := cleanSupportTicketText(raw)
	if body == "" {
		return "", ErrSupportTicketBodyRequired
	}
	if utf8.RuneCountInString(body) > SupportTicketBodyMaxRunes {
		return "", ErrSupportTicketBodyTooLong
	}
	return body, nil
}

// matchSupportTicketCategory returns the configured spelling of the chosen
// category. Without configured categories the field is ignored.
func matchSupportTicketCategory(raw string, categories []string) (string, error) {
	if len(categories) == 0 {
		return "", nil
	}
	chosen := strings.ToLower(cleanSupportTicketLine(raw))
	for _, category := range categories {
		if strings.ToLower(category) == chosen {
			return category, nil
		}
	}
	return "", ErrSupportTicketBadCategory
}
