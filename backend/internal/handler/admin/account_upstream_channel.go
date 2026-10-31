package admin

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (h *AccountHandler) SetUpstreamChannelService(s *service.ChannelService) { h.upstreamChannels = s }
func (h *AccountHandler) enrichUpstreamChannelBindings(ctx context.Context, items []AccountWithConcurrency) error {
	if h.upstreamChannels == nil {
		return nil
	}
	if _, observer := service.ObserverGroupIDs(ctx); observer {
		return nil
	}
	repo, err := h.upstreamChannels.UpstreamChannelRepository()
	if err != nil {
		return err
	}
	for start := 0; start < len(items); start += 100 {
		end := min(start+100, len(items))
		ids := make([]int64, 0, end-start)
		byID := map[int64]int{}
		for i := start; i < end; i++ {
			if items[i].Account != nil {
				ids = append(ids, items[i].ID)
				byID[items[i].ID] = i
			}
		}
		if len(ids) == 0 {
			continue
		}
		bindings, e := repo.ReadUpstreamChannelBindings(ctx, ids)
		if e != nil {
			return e
		}
		for i := range bindings {
			if index, ok := byID[bindings[i].AccountID]; ok {
				b := bindings[i]
				items[index].UpstreamChannelBinding = &b
			}
		}
	}
	return nil
}
