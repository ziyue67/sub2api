package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type supportTicketRepository struct{ db *sql.DB }

func NewSupportTicketRepository(db *sql.DB) service.SupportTicketRepository {
	return &supportTicketRepository{db: db}
}

const supportTicketColumns = `t.id, t.user_id, t.category, t.title, t.status, t.message_count, t.last_message_at,
	t.last_message_role, t.user_unread, t.admin_unread, t.closed_at, t.closed_by_role, t.created_at, t.updated_at,
	COALESCE(u.email, ''), COALESCE(u.username, ''), COALESCE(u.status, ''), COALESCE(u.balance, 0)::float8,
	(u.id IS NULL OR u.deleted_at IS NOT NULL), u.created_at`

func scanSupportTicket(row interface{ Scan(...any) error }, withUser bool) (*service.SupportTicket, error) {
	var ticket service.SupportTicket
	var user service.SupportTicketUser
	var closedAt sql.NullTime
	var userCreatedAt sql.NullTime
	if err := row.Scan(&ticket.ID, &ticket.UserID, &ticket.Category, &ticket.Title, &ticket.Status, &ticket.MessageCount,
		&ticket.LastMessageAt, &ticket.LastMessageRole, &ticket.UserUnread, &ticket.AdminUnread, &closedAt,
		&ticket.ClosedByRole, &ticket.CreatedAt, &ticket.UpdatedAt,
		&user.Email, &user.Username, &user.Status, &user.Balance, &user.Deleted, &userCreatedAt); err != nil {
		return nil, err
	}
	if closedAt.Valid {
		value := closedAt.Time
		ticket.ClosedAt = &value
	}
	if withUser {
		user.ID = ticket.UserID
		if userCreatedAt.Valid {
			user.CreatedAt = userCreatedAt.Time
		}
		ticket.User = &user
	}
	return &ticket, nil
}

func (r *supportTicketRepository) CreateTicket(ctx context.Context, in service.SupportTicketCreateInput) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockSupportTicketUser(ctx, tx, in.UserID); err != nil {
		return 0, err
	}
	if in.Limits.MaxOpen > 0 {
		open, err := countSupportTickets(ctx, tx, `user_id = $1 AND status <> 'closed'`, in.UserID)
		if err != nil {
			return 0, err
		}
		if open >= int64(in.Limits.MaxOpen) {
			return 0, service.ErrSupportTicketTooManyOpen
		}
	}
	if in.Limits.TicketsPerDay > 0 {
		created, err := countSupportTickets(ctx, tx, `user_id = $1 AND created_at > NOW() - INTERVAL '1 day'`, in.UserID)
		if err != nil {
			return 0, err
		}
		if created >= int64(in.Limits.TicketsPerDay) {
			return 0, service.ErrSupportTicketDailyLimit
		}
	}
	if err := checkSupportTicketMessageRate(ctx, tx, in.UserID, in.Limits); err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO support_tickets
		(user_id, category, title, status, message_count, last_message_at, last_message_role, user_unread, admin_unread)
		VALUES ($1, $2, $3, 'pending', 1, NOW(), 'user', FALSE, TRUE) RETURNING id`,
		in.UserID, in.Category, in.Title).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO support_ticket_messages (ticket_id, author_role, author_user_id, body)
		VALUES ($1, 'user', $2, $3)`, id, in.UserID, in.Body); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (r *supportTicketRepository) ListTickets(ctx context.Context, filter service.SupportTicketListFilter) ([]service.SupportTicket, int64, error) {
	where := []string{"TRUE"}
	args := []any{}
	arg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if filter.UserID > 0 {
		where = append(where, "t.user_id = "+arg(filter.UserID))
	}
	switch filter.Status {
	case "":
	case service.SupportTicketStatusOpen:
		where = append(where, "t.status <> 'closed'")
	default:
		where = append(where, "t.status = "+arg(filter.Status))
	}
	if filter.Category != "" {
		where = append(where, "t.category = "+arg(filter.Category))
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := arg("%" + escapeLikeWildcards(keyword) + "%")
		conditions := []string{
			"t.title ILIKE " + pattern + ` ESCAPE '\'`,
			"u.email ILIKE " + pattern + ` ESCAPE '\'`,
			"u.username ILIKE " + pattern + ` ESCAPE '\'`,
		}
		if id, err := strconv.ParseInt(strings.TrimPrefix(keyword, "#"), 10, 64); err == nil && id > 0 {
			conditions = append(conditions, "t.id = "+arg(id))
		}
		where = append(where, "("+strings.Join(conditions, " OR ")+")")
	}
	from := ` FROM support_tickets t LEFT JOIN users u ON u.id = t.user_id WHERE ` + strings.Join(where, " AND ")

	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := ` ORDER BY t.last_message_at DESC, t.id DESC`
	if filter.PendingFirst {
		order = ` ORDER BY (t.status = 'pending') DESC, t.last_message_at DESC, t.id DESC`
	}
	query := `SELECT ` + supportTicketColumns + from + order +
		` LIMIT ` + arg(filter.PageSize) + ` OFFSET ` + arg((filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := []service.SupportTicket{}
	for rows.Next() {
		ticket, err := scanSupportTicket(rows, filter.UserID == 0)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *ticket)
	}
	return items, total, rows.Err()
}

func (r *supportTicketRepository) GetTicket(ctx context.Context, id, ownerID int64, withUser bool) (*service.SupportTicket, []service.SupportTicketMessage, error) {
	query := `SELECT ` + supportTicketColumns + ` FROM support_tickets t LEFT JOIN users u ON u.id = t.user_id WHERE t.id = $1`
	args := []any{id}
	if ownerID > 0 {
		query += ` AND t.user_id = $2`
		args = append(args, ownerID)
	}
	ticket, err := scanSupportTicket(r.db.QueryRowContext(ctx, query, args...), withUser)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, service.ErrSupportTicketNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT m.id, m.author_role, COALESCE(NULLIF(a.username, ''), a.email, ''), m.body, m.created_at
		FROM support_ticket_messages m LEFT JOIN users a ON a.id = m.author_user_id
		WHERE m.ticket_id = $1 ORDER BY m.id`, id)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	messages := []service.SupportTicketMessage{}
	for rows.Next() {
		var message service.SupportTicketMessage
		if err := rows.Scan(&message.ID, &message.AuthorRole, &message.AuthorName, &message.Body, &message.CreatedAt); err != nil {
			return nil, nil, err
		}
		messages = append(messages, message)
	}
	return ticket, messages, rows.Err()
}

func (r *supportTicketRepository) MarkRead(ctx context.Context, id int64, role string, seenMessages int) error {
	column := "user_unread"
	if role == service.SupportTicketRoleAdmin {
		column = "admin_unread"
	}
	_, err := r.db.ExecContext(ctx, `UPDATE support_tickets SET `+column+` = FALSE
		WHERE id = $1 AND `+column+` AND message_count = $2`, id, seenMessages)
	return err
}

func (r *supportTicketRepository) AddMessage(ctx context.Context, in service.SupportTicketMessageInput) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	isUser := in.AuthorRole == service.SupportTicketRoleUser
	if isUser {
		if err := lockSupportTicketUser(ctx, tx, in.AuthorUserID); err != nil {
			return err
		}
	}
	status, _, messageCount, err := lockSupportTicketRow(ctx, tx, in.TicketID, in.OwnerID)
	if err != nil {
		return err
	}
	if status == service.SupportTicketStatusClosed {
		return service.ErrSupportTicketClosed
	}
	if isUser {
		// Admins can still answer a full ticket.
		if messageCount >= service.SupportTicketMaxUserMessages {
			return service.ErrSupportTicketFull
		}
		if err := checkSupportTicketMessageRate(ctx, tx, in.AuthorUserID, in.Limits); err != nil {
			return err
		}
	}
	var authorID any
	if in.AuthorUserID > 0 {
		authorID = in.AuthorUserID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO support_ticket_messages (ticket_id, author_role, author_user_id, body)
		VALUES ($1, $2, $3, $4)`, in.TicketID, in.AuthorRole, authorID, in.Body); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE support_tickets SET
		status = $2::text, message_count = message_count + 1, last_message_at = NOW(), last_message_role = $3::text,
		user_unread = ($3::text = 'admin'), admin_unread = ($3::text = 'user'),
		closed_at = CASE WHEN $2::text = 'closed' THEN NOW() ELSE NULL END,
		closed_by_role = CASE WHEN $2::text = 'closed' THEN $3::text ELSE '' END,
		updated_at = NOW()
		WHERE id = $1`, in.TicketID, in.NextStatus, in.AuthorRole); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *supportTicketRepository) SetStatus(ctx context.Context, in service.SupportTicketStatusInput) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	reopening := in.Status == service.SupportTicketStatusPending
	if reopening && in.OwnerID > 0 {
		// Same lock as ticket creation, so reopening cannot race past the open limit.
		if err := lockSupportTicketUser(ctx, tx, in.OwnerID); err != nil {
			return err
		}
	}
	status, ownerID, _, err := lockSupportTicketRow(ctx, tx, in.TicketID, in.OwnerID)
	if err != nil {
		return err
	}
	switch in.Status {
	case service.SupportTicketStatusClosed:
		if status == service.SupportTicketStatusClosed {
			return nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE support_tickets SET status = 'closed', closed_at = NOW(), closed_by_role = $2, updated_at = NOW()
			WHERE id = $1`, in.TicketID, in.ActorRole)
	case service.SupportTicketStatusPending:
		if status != service.SupportTicketStatusClosed {
			return service.ErrSupportTicketNotClosed
		}
		if in.Limits.MaxOpen > 0 {
			open, err := countSupportTickets(ctx, tx, `user_id = $1 AND status <> 'closed'`, ownerID)
			if err != nil {
				return err
			}
			if open >= int64(in.Limits.MaxOpen) {
				return service.ErrSupportTicketTooManyOpen
			}
		}
		// A user reopening asks for attention; an admin reopening already has it.
		_, err = tx.ExecContext(ctx, `UPDATE support_tickets SET status = 'pending', closed_at = NULL, closed_by_role = '',
			admin_unread = admin_unread OR $2 = 'user', updated_at = NOW() WHERE id = $1`, in.TicketID, in.ActorRole)
	case service.SupportTicketStatusProcessing:
		if status == service.SupportTicketStatusClosed {
			return service.ErrSupportTicketClosed
		}
		_, err = tx.ExecContext(ctx, `UPDATE support_tickets SET status = 'processing', updated_at = NOW() WHERE id = $1`, in.TicketID)
	default:
		return service.ErrSupportTicketBadStatus
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *supportTicketRepository) DeleteTicket(ctx context.Context, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM support_tickets WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (r *supportTicketRepository) CountUnread(ctx context.Context, userID int64) (int64, error) {
	return countSupportTickets(ctx, r.db, `user_id = $1 AND user_unread`, userID)
}

func (r *supportTicketRepository) CountOpen(ctx context.Context, userID int64) (int64, error) {
	return countSupportTickets(ctx, r.db, `user_id = $1 AND status <> 'closed'`, userID)
}

func (r *supportTicketRepository) CountPending(ctx context.Context) (int64, error) {
	return countSupportTickets(ctx, r.db, `status = 'pending'`)
}

type supportTicketQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func countSupportTickets(ctx context.Context, q supportTicketQuerier, where string, args ...any) (int64, error) {
	var count int64
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM support_tickets WHERE `+where, args...).Scan(&count)
	return count, err
}

// lockSupportTicketUser serializes one user's ticket writes so the open-ticket
// and flood limits cannot be raced past by parallel requests.
func lockSupportTicketUser(ctx context.Context, tx *sql.Tx, userID int64) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockHash(fmt.Sprintf("support_ticket:user:%d", userID)))
	return err
}

// lockSupportTicketRow locks the ticket for a write. With ownerID set, another
// user's ticket is reported as missing.
func lockSupportTicketRow(ctx context.Context, tx *sql.Tx, id, ownerID int64) (status string, owner int64, messageCount int, err error) {
	query := `SELECT status, user_id, message_count FROM support_tickets WHERE id = $1`
	args := []any{id}
	if ownerID > 0 {
		query += ` AND user_id = $2`
		args = append(args, ownerID)
	}
	err = tx.QueryRowContext(ctx, query+` FOR UPDATE`, args...).Scan(&status, &owner, &messageCount)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, 0, service.ErrSupportTicketNotFound
	}
	return status, owner, messageCount, err
}

func checkSupportTicketMessageRate(ctx context.Context, tx *sql.Tx, userID int64, limits service.SupportTicketLimits) error {
	if limits.MessagesPerMinute <= 0 {
		return nil
	}
	var recent int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM support_ticket_messages
		WHERE author_user_id = $1 AND author_role = 'user' AND created_at > NOW() - INTERVAL '1 minute'`, userID).Scan(&recent); err != nil {
		return err
	}
	if recent >= int64(limits.MessagesPerMinute) {
		return service.ErrSupportTicketTooFast
	}
	return nil
}
