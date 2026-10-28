package repository

import (
	"context"
	"fmt"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Group removal affects only the target account's existing memberships. A
// reusable preset can legitimately name a group a future target has not joined;
// it may not name deleted/nonexistent groups or an observer's ungranted group.
func validateQualityGroupReferences(ctx context.Context, q planRowQuerier, cfg *service.PelicanTestConfig) error {
	if cfg == nil || cfg.Quality == nil {
		return nil
	}
	policy := cfg.Quality
	ids := map[int64]bool{}
	if policy.Action == "remove_groups" || policy.Action == service.QualityActionIsolateModels {
		for _, id := range policy.RemoveGroupIDs {
			ids[id] = true
		}
	}
	ordered := []int64{}
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, id := range ordered {
		if !service.ObserverCanManageGroup(ctx, id) {
			return service.ErrObserverScope
		}
		var exists bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM groups WHERE id=$1 AND deleted_at IS NULL)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("quality target group %d does not exist", id)
		}
	}
	return nil
}
