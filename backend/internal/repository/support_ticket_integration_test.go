//go:build integration

package repository

import (
	"context"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestSupportTicketRepositoryLifecycle(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewSupportTicketRepository(integrationDB)
	alice := mustCreateUser(t, client, &service.User{Email: "st-alice-" + uuid.NewString() + "@example.com", Username: "alice", Balance: 12.5})
	bob := mustCreateUser(t, client, &service.User{Email: "st-bob-" + uuid.NewString() + "@example.com"})
	admin := mustCreateUser(t, client, &service.User{Email: "st-admin-" + uuid.NewString() + "@example.com", Username: "root", Role: service.RoleAdmin})
	t.Cleanup(func() {
		// Tickets and messages go with their users.
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id = ANY($1)`, pq.Array([]int64{alice.ID, bob.ID, admin.ID}))
		require.NoError(t, err)
	})
	limits := service.SupportTicketLimits{MaxOpen: 2, MessagesPerMinute: 4, TicketsPerDay: 3}
	create := func(userID int64, title string, limits service.SupportTicketLimits) (int64, error) {
		return repo.CreateTicket(ctx, service.SupportTicketCreateInput{UserID: userID, Category: "其他", Title: title, Body: "hello", Limits: limits})
	}

	first, err := create(alice.ID, "first", limits)
	require.NoError(t, err)
	second, err := create(alice.ID, "second", limits)
	require.NoError(t, err)
	_, err = create(alice.ID, "third", limits)
	require.ErrorIs(t, err, service.ErrSupportTicketTooManyOpen)

	ticket, messages, err := repo.GetTicket(ctx, first, alice.ID, false)
	require.NoError(t, err)
	require.Equal(t, service.SupportTicketStatusPending, ticket.Status)
	require.Equal(t, 1, ticket.MessageCount)
	require.Equal(t, "其他", ticket.Category)
	require.True(t, ticket.AdminUnread)
	require.False(t, ticket.UserUnread)
	require.Equal(t, service.SupportTicketRoleUser, ticket.LastMessageRole)
	require.Nil(t, ticket.User)
	require.Len(t, messages, 1)
	require.Equal(t, "hello", messages[0].Body)
	_, _, err = repo.GetTicket(ctx, first, bob.ID, false)
	require.ErrorIs(t, err, service.ErrSupportTicketNotFound, "another user's ticket does not exist for bob")
	ticket, _, err = repo.GetTicket(ctx, first, 0, true)
	require.NoError(t, err)
	require.Equal(t, &service.SupportTicketUser{ID: alice.ID, Email: alice.Email, Username: "alice", Status: service.StatusActive,
		Balance: 12.5, CreatedAt: ticket.User.CreatedAt}, ticket.User)
	require.False(t, ticket.User.CreatedAt.IsZero())

	// An admin answer flips the unread marks and waits for the user.
	require.NoError(t, repo.AddMessage(ctx, service.SupportTicketMessageInput{
		TicketID: first, AuthorRole: service.SupportTicketRoleAdmin, AuthorUserID: admin.ID, Body: "on it", NextStatus: service.SupportTicketStatusReplied,
	}))
	ticket, messages, err = repo.GetTicket(ctx, first, 0, true)
	require.NoError(t, err)
	require.Equal(t, service.SupportTicketStatusReplied, ticket.Status)
	require.True(t, ticket.UserUnread)
	require.False(t, ticket.AdminUnread)
	require.Equal(t, 2, ticket.MessageCount)
	require.Equal(t, service.SupportTicketRoleAdmin, ticket.LastMessageRole)
	require.Equal(t, "root", messages[1].AuthorName)
	unread, err := repo.CountUnread(ctx, alice.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), unread)
	// A reader who loaded fewer messages than there are now keeps the mark.
	require.NoError(t, repo.MarkRead(ctx, first, service.SupportTicketRoleUser, 1))
	unread, err = repo.CountUnread(ctx, alice.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), unread)
	require.NoError(t, repo.MarkRead(ctx, first, service.SupportTicketRoleUser, len(messages)))
	unread, err = repo.CountUnread(ctx, alice.ID)
	require.NoError(t, err)
	require.Zero(t, unread)

	// The user's answer puts it back in the admin queue.
	userReply := func(ticketID, ownerID int64) error {
		return repo.AddMessage(ctx, service.SupportTicketMessageInput{TicketID: ticketID, OwnerID: ownerID, AuthorRole: service.SupportTicketRoleUser,
			AuthorUserID: ownerID, Body: "more", NextStatus: service.SupportTicketStatusPending, Limits: limits})
	}
	require.NoError(t, userReply(first, alice.ID))
	ticket, _, err = repo.GetTicket(ctx, first, alice.ID, false)
	require.NoError(t, err)
	require.Equal(t, service.SupportTicketStatusPending, ticket.Status)
	require.True(t, ticket.AdminUnread)
	require.False(t, ticket.UserUnread)
	require.ErrorIs(t, userReply(first, bob.ID), service.ErrSupportTicketNotFound)

	// Alice wrote three messages within the minute; the limit is four.
	require.NoError(t, userReply(first, alice.ID))
	require.ErrorIs(t, userReply(first, alice.ID), service.ErrSupportTicketTooFast)
	noRate := service.SupportTicketLimits{MaxOpen: 2, TicketsPerDay: 3}

	setStatus := func(ticketID, ownerID int64, role, status string, limits service.SupportTicketLimits) error {
		return repo.SetStatus(ctx, service.SupportTicketStatusInput{TicketID: ticketID, OwnerID: ownerID, ActorRole: role, Status: status, Limits: limits})
	}
	require.NoError(t, setStatus(second, alice.ID, service.SupportTicketRoleUser, service.SupportTicketStatusClosed, noRate))
	require.NoError(t, setStatus(second, alice.ID, service.SupportTicketRoleUser, service.SupportTicketStatusClosed, noRate), "closing twice is harmless")
	ticket, _, err = repo.GetTicket(ctx, second, alice.ID, false)
	require.NoError(t, err)
	require.Equal(t, service.SupportTicketStatusClosed, ticket.Status)
	require.Equal(t, service.SupportTicketRoleUser, ticket.ClosedByRole)
	require.NotNil(t, ticket.ClosedAt)
	require.ErrorIs(t, repo.AddMessage(ctx, service.SupportTicketMessageInput{TicketID: second, AuthorRole: service.SupportTicketRoleAdmin,
		AuthorUserID: admin.ID, Body: "late", NextStatus: service.SupportTicketStatusReplied}), service.ErrSupportTicketClosed)
	require.ErrorIs(t, setStatus(second, 0, service.SupportTicketRoleAdmin, service.SupportTicketStatusProcessing, service.SupportTicketLimits{}), service.ErrSupportTicketClosed)

	third, err := create(alice.ID, "third", noRate)
	require.NoError(t, err, "closing freed a slot")
	require.ErrorIs(t, setStatus(second, alice.ID, service.SupportTicketRoleUser, service.SupportTicketStatusPending, noRate),
		service.ErrSupportTicketTooManyOpen, "reopening counts against the limit")
	require.ErrorIs(t, setStatus(first, alice.ID, service.SupportTicketRoleUser, service.SupportTicketStatusPending, noRate), service.ErrSupportTicketNotClosed)
	require.NoError(t, setStatus(second, 0, service.SupportTicketRoleAdmin, service.SupportTicketStatusPending, service.SupportTicketLimits{}), "admins reopen past the limit")
	ticket, _, err = repo.GetTicket(ctx, second, 0, true)
	require.NoError(t, err)
	require.Equal(t, service.SupportTicketStatusPending, ticket.Status)
	require.Nil(t, ticket.ClosedAt)
	require.Empty(t, ticket.ClosedByRole)
	require.NoError(t, setStatus(third, 0, service.SupportTicketRoleAdmin, service.SupportTicketStatusProcessing, service.SupportTicketLimits{}))

	// Bob: daily limit, and a title with LIKE wildcards.
	bobLimits := service.SupportTicketLimits{TicketsPerDay: 1}
	bobTicket, err := create(bob.ID, "100% down_now", bobLimits)
	require.NoError(t, err)
	_, err = create(bob.ID, "again", bobLimits)
	require.ErrorIs(t, err, service.ErrSupportTicketDailyLimit)

	open, err := repo.CountOpen(ctx, alice.ID)
	require.NoError(t, err)
	require.Equal(t, int64(3), open)
	pending, err := repo.CountPending(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(3), pending, "first, second and bob's ticket")

	list := func(filter service.SupportTicketListFilter) ([]int64, int64) {
		if filter.Page == 0 {
			filter.Page, filter.PageSize = 1, 50
		}
		items, total, err := repo.ListTickets(ctx, filter)
		require.NoError(t, err)
		ids := make([]int64, 0, len(items))
		for _, item := range items {
			require.Equal(t, filter.UserID == 0, item.User != nil, "only admin listings carry the author")
			ids = append(ids, item.ID)
		}
		return ids, total
	}
	ids, total := list(service.SupportTicketListFilter{UserID: alice.ID, Page: 1, PageSize: 2})
	require.Equal(t, int64(3), total)
	require.Equal(t, []int64{third, first}, ids, "latest activity first")
	ids, _ = list(service.SupportTicketListFilter{UserID: alice.ID, Page: 2, PageSize: 2})
	require.Equal(t, []int64{second}, ids)
	ids, _ = list(service.SupportTicketListFilter{Status: service.SupportTicketStatusOpen, PendingFirst: true})
	require.Equal(t, []int64{bobTicket, first, second, third}, ids, "pending first, then by activity")
	ids, _ = list(service.SupportTicketListFilter{Status: service.SupportTicketStatusProcessing})
	require.Equal(t, []int64{third}, ids)
	ids, _ = list(service.SupportTicketListFilter{Keyword: "st-alice-"})
	require.ElementsMatch(t, []int64{first, second, third}, ids, "search by author email")
	ids, _ = list(service.SupportTicketListFilter{Keyword: "%"})
	require.Equal(t, []int64{bobTicket}, ids, "LIKE wildcards are literal")
	ids, _ = list(service.SupportTicketListFilter{Keyword: "#" + strconv.FormatInt(second, 10)})
	require.Equal(t, []int64{second}, ids, "search by ticket number")
	ids, _ = list(service.SupportTicketListFilter{UserID: bob.ID, Category: "其他"})
	require.Equal(t, []int64{bobTicket}, ids)
	ids, _ = list(service.SupportTicketListFilter{UserID: bob.ID, Category: "退款"})
	require.Empty(t, ids)

	// A full ticket stops the user but not the admin.
	_, err = integrationDB.ExecContext(ctx, `UPDATE support_tickets SET message_count = $2 WHERE id = $1`, bobTicket, service.SupportTicketMaxUserMessages)
	require.NoError(t, err)
	require.ErrorIs(t, repo.AddMessage(ctx, service.SupportTicketMessageInput{TicketID: bobTicket, OwnerID: bob.ID, AuthorRole: service.SupportTicketRoleUser,
		AuthorUserID: bob.ID, Body: "x", NextStatus: service.SupportTicketStatusPending}), service.ErrSupportTicketFull)
	require.NoError(t, repo.AddMessage(ctx, service.SupportTicketMessageInput{TicketID: bobTicket, AuthorRole: service.SupportTicketRoleAdmin,
		AuthorUserID: admin.ID, Body: "closing this one", NextStatus: service.SupportTicketStatusClosed}))
	ticket, _, err = repo.GetTicket(ctx, bobTicket, 0, true)
	require.NoError(t, err)
	require.Equal(t, service.SupportTicketStatusClosed, ticket.Status)
	require.Equal(t, service.SupportTicketRoleAdmin, ticket.ClosedByRole)

	// A soft-deleted author is flagged for admins.
	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET deleted_at = NOW() WHERE id = $1`, bob.ID)
	require.NoError(t, err)
	ticket, _, err = repo.GetTicket(ctx, bobTicket, 0, true)
	require.NoError(t, err)
	require.True(t, ticket.User.Deleted)

	deleted, err := repo.DeleteTicket(ctx, third)
	require.NoError(t, err)
	require.True(t, deleted)
	_, _, err = repo.GetTicket(ctx, third, 0, true)
	require.ErrorIs(t, err, service.ErrSupportTicketNotFound)
	var leftover int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM support_ticket_messages WHERE ticket_id = $1`, third).Scan(&leftover))
	require.Zero(t, leftover, "messages go with their ticket")
	deleted, err = repo.DeleteTicket(ctx, third)
	require.NoError(t, err)
	require.False(t, deleted)
}
