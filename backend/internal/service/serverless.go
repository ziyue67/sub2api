package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/serverless"
)

type serverlessStore struct{ s *SettingService }

func (s serverlessStore) Load(ctx context.Context) (serverless.Config, error) {
	c := serverless.Config{Pods: []serverless.PodPolicy{}, Regions: []serverless.Region{}}
	raw, err := s.s.settingRepo.GetValue(ctx, serverless.SettingKey)
	if errors.Is(err, ErrSettingNotFound) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(raw), &c)
	return c, err
}
func (s serverlessStore) Save(ctx context.Context, c serverless.Config) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.s.settingRepo.Set(ctx, serverless.SettingKey, string(raw))
}
func (s *SettingService) ServerlessStore() serverless.Store { return serverlessStore{s: s} }
func (s *SettingService) ServerlessVersion() string         { return s.version }
