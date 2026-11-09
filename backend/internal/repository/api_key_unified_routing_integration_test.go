//go:build integration

package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (s *APIKeyRepoSuite) TestUnifiedRoutingRoundTripAndGroupChanges() {
	user := s.mustCreateUser("unified-routing@test.invalid")
	a, b, c := s.mustCreateGroup("unified-a"), s.mustCreateGroup("unified-b"), s.mustCreateGroup("unified-c")
	p := &domain.APIKeyGroupRouting{Strategy: service.APIKeyRoutingPriority, Failover: true, Routes: []domain.APIKeyGroupRoute{
		{GroupID: a.ID, Priority: 1, Weight: 1, Enabled: true},
		{GroupID: b.ID, Priority: 2, Weight: 3, Enabled: true, CooldownSeconds: 60},
	}}
	k := &service.APIKey{UserID: user.ID, Key: "sk-unified-integration", Name: "unified", Status: service.StatusActive, GroupID: &a.ID, GroupIDs: []int64{a.ID, b.ID}, GroupRoutes: p.Routes, GroupRouting: p}
	s.Require().NoError(s.repo.Create(s.ctx, k))
	got, err := s.repo.GetByKeyForAuth(s.ctx, k.Key)
	s.Require().NoError(err)
	s.Equal(p, got.GroupRouting)
	s.Require().NoError(s.repo.IncrementRateLimitUsage(s.ctx, k.ID, 0.25))
	got.GroupRouting.Failover = false
	s.Require().NoError(s.repo.Update(s.ctx, got, service.APIKeyUpdateFields{GroupRouting: true}))
	got, err = s.repo.GetByID(s.ctx, k.ID)
	s.Require().NoError(err)
	s.False(got.GroupRouting.Failover)
	s.Equal(0.25, got.Usage5h)
	_, err = s.repo.UpdateGroupIDByUserAndGroup(s.ctx, user.ID, b.ID, c.ID)
	s.Require().NoError(err)
	got, err = s.repo.GetByKeyForAuth(s.ctx, k.Key)
	s.Require().NoError(err)
	s.Equal([]int64{a.ID, c.ID}, got.ConfiguredGroupIDs())
	s.Equal(c.ID, got.GroupRouting.Routes[1].GroupID)
	_, err = s.repo.ClearGroupIDByGroupID(s.ctx, c.ID)
	s.Require().NoError(err)
	got, err = s.repo.GetByKeyForAuth(s.ctx, k.Key)
	s.Require().NoError(err)
	s.Equal([]int64{a.ID}, got.ConfiguredGroupIDs())
	s.Len(got.GroupRoutes, 1)
}

func (s *APIKeyRepoSuite) TestUnifiedRoutingCascadeDoesNotLeaveDeletedCandidate() {
	user := s.mustCreateUser("unified-delete@test.invalid")
	a, b := s.mustCreateGroup("unified-keep"), s.mustCreateGroup("unified-delete")
	p := &domain.APIKeyGroupRouting{Strategy: service.APIKeyRoutingLowestRate, Routes: []domain.APIKeyGroupRoute{{GroupID: a.ID, Priority: 1, Weight: 1, Enabled: true}, {GroupID: b.ID, Priority: 1, Weight: 1, Enabled: true}}}
	k := &service.APIKey{UserID: user.ID, Key: "sk-unified-delete", Name: "unified", Status: service.StatusActive, GroupID: &a.ID, GroupIDs: []int64{a.ID, b.ID}, GroupRoutes: p.Routes, GroupRouting: p}
	s.Require().NoError(s.repo.Create(s.ctx, k))
	_, err := newGroupRepositoryWithSQL(s.client, s.repo.sql).DeleteCascade(s.ctx, b.ID)
	s.Require().NoError(err)
	got, err := s.repo.GetByID(s.ctx, k.ID)
	s.Require().NoError(err)
	s.Equal([]int64{a.ID}, got.ConfiguredGroupIDs())
	s.Len(got.GroupRoutes, 1)
}
