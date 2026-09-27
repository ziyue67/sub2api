package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	observerSetupBalanceGrant     = 99999.0
	observerSetupConcurrencyGrant = 1000
)

// Lock the user before reading its role so concurrent/retried promotions cannot
// create extra groups or grant the same resources twice.
func (s *adminServiceImpl) beginObserverSetup(ctx context.Context, id int64, input *UpdateUserInput) (context.Context, *dbent.Tx, error) {
	if !input.ObserverSetup.enabled() {
		return ctx, nil, nil
	}
	if input.Role != RoleObserver {
		return ctx, nil, infraerrors.BadRequest("OBSERVER_SETUP_REQUIRES_PROMOTION", "Observer setup requires changing the role to observer")
	}
	if input.ObserverSetup.CreateDedicatedGroup && s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return ctx, nil, infraerrors.BadRequest("SIMPLE_MODE_OPERATION_UNSUPPORTED", "Exclusive observer groups are not supported in simple mode")
	}
	if s.entClient == nil {
		return ctx, nil, errors.New("observer setup requires a database transaction")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return ctx, nil, err
	}
	rows, err := tx.Client().QueryContext(ctx, "SELECT id FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE", id)
	if err == nil {
		if !rows.Next() {
			err = rows.Err()
			if err == nil {
				err = ErrUserNotFound
			}
		}
		closeErr := rows.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		_ = tx.Rollback()
		return ctx, nil, err
	}
	return dbent.NewTxContext(ctx, tx), tx, nil
}

func (s *adminServiceImpl) applyObserverSetup(ctx context.Context, user *User, fields *UserUpdateFields, options *ObserverSetupOptions) (int64, error) {
	if options.RevokePublicGroups {
		// Public groups may already be explicitly allowed. Remove those grants too,
		// while preserving the user's existing exclusive-group access.
		allowed := make([]int64, 0, len(user.AllowedGroups))
		for _, id := range user.AllowedGroups {
			group, err := s.groupRepo.GetByIDLite(ctx, id)
			if errors.Is(err, ErrGroupNotFound) {
				continue
			}
			if err != nil {
				return 0, err
			}
			if group.IsExclusive {
				allowed = append(allowed, id)
			}
		}
		user.AllowedGroups = allowed
		user.RestrictPublicGroups = true
		fields.AllowedGroups, fields.RestrictPublicGroups = true, true
	}

	if options.GrantResources {
		if user.Concurrency < 0 || user.Concurrency > (1<<31-1)-observerSetupConcurrencyGrant {
			return 0, infraerrors.BadRequest("INVALID_OBSERVER_CONCURRENCY", "Concurrency cannot be increased by 1000")
		}
		// Zero already means unlimited. Do not turn it into a finite limit.
		if user.Concurrency > 0 {
			user.Concurrency += observerSetupConcurrencyGrant
			fields.Concurrency = true
		}
	}

	if !options.CreateDedicatedGroup {
		return 0, nil
	}
	name := strings.TrimSpace(user.Username)
	if name == "" {
		return 0, infraerrors.BadRequest("OBSERVER_GROUP_USERNAME_REQUIRED", "A username is required to create the observer's dedicated group")
	}
	group, err := s.CreateGroup(ctx, &CreateGroupInput{
		Name: name, Platform: PlatformOpenAI, IsExclusive: true,
		RateMultiplier: 1, SubscriptionType: SubscriptionTypeStandard,
	})
	if err != nil {
		return 0, err
	}
	if !slices.Contains(user.AllowedGroups, group.ID) {
		user.AllowedGroups = append(user.AllowedGroups, group.ID)
	}
	if !slices.Contains(user.ObserverGroupIDs, group.ID) {
		user.ObserverGroupIDs = append(user.ObserverGroupIDs, group.ID)
	}
	fields.AllowedGroups, fields.ObserverGroupIDs = true, true
	return group.ID, nil
}

func (s *adminServiceImpl) recordObserverAdjustment(ctx context.Context, userID, actorID int64, kind string, amount float64) error {
	code, err := GenerateRedeemCode()
	if err != nil {
		return err
	}
	now := time.Now()
	return s.redeemCodeRepo.Create(ctx, &RedeemCode{
		Code: code, Type: kind, Value: amount, Status: StatusUsed,
		UsedBy: &userID, UsedAt: &now,
		Notes: fmt.Sprintf("observer setup: actor_admin_id=%d", actorID),
	})
}
