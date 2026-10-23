//go:build integration

package repository

import "github.com/Wei-Shaw/sub2api/internal/service"

func (s *APIKeyRepoSuite) TestAdditionalGroupReplacementAndPrimaryRemoval() {
	user := s.mustCreateUser("replace-multi@test.invalid")
	first := s.mustCreateGroup("multi-first")
	second := s.mustCreateGroup("multi-second")
	third := s.mustCreateGroup("multi-third")
	key := &service.APIKey{UserID: user.ID, Key: "sk-multi-ctes", Name: "multi", Status: service.StatusActive, GroupID: &first.ID, GroupIDs: []int64{first.ID, second.ID}}
	s.Require().NoError(s.repo.Create(s.ctx, key))
	affected, err := s.repo.UpdateGroupIDByUserAndGroup(s.ctx, user.ID, second.ID, third.ID)
	s.Require().NoError(err)
	s.Equal(int64(1), affected)
	got, err := s.repo.GetByKeyForAuth(s.ctx, key.Key)
	s.Require().NoError(err)
	s.Equal([]int64{first.ID, third.ID}, got.GroupIDs)
	_, err = s.repo.ClearGroupIDByGroupID(s.ctx, first.ID)
	s.Require().NoError(err)
	got, err = s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err)
	s.Require().NotNil(got.GroupID)
	s.Equal(third.ID, *got.GroupID)
	s.Equal([]int64{third.ID}, got.GroupIDs)
}
