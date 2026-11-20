//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type personalQuestionSettingsRepo struct {
	SettingRepository
	values map[string]string
}

func (r *personalQuestionSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func (r *personalQuestionSettingsRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func TestPelicanPersonalQuestionBank(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.JWT.Secret = "unit-test-only"
	repo := &personalQuestionSettingsRepo{values: map[string]string{}}
	s := NewSettingService(repo, cfg)
	items, err := s.GetPelicanPersonalQuestions(ctx, 11)
	require.NoError(t, err)
	require.NotNil(t, items)
	_, err = s.NewPelicanPersonalRound(ctx, 11, "")
	require.ErrorIs(t, err, errPelicanPersonalEmpty)
	items, err = s.SavePelicanPersonalQuestions(ctx, 11, []PelicanPersonalQuestion{
		{Title: " Rain ", Prompt: " Draw rain ", Enabled: true},
		{Title: "Clock", Prompt: "Draw a clock", Enabled: true},
		{Title: "Disabled", Prompt: "Do not draw this", Enabled: false},
	})
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.Equal(t, "Rain", items[0].Title)
	require.Equal(t, "Draw rain", items[0].Prompt)

	// A new service instance reads the same saved bank. Another admin gets an empty bank.
	s = NewSettingService(repo, cfg)
	loaded, err := s.GetPelicanPersonalQuestions(ctx, 11)
	require.NoError(t, err)
	require.Equal(t, items, loaded)
	other, err := s.GetPelicanPersonalQuestions(ctx, 12)
	require.NoError(t, err)
	require.Empty(t, other)

	first, err := s.NewPelicanPersonalRound(ctx, 11, "")
	require.NoError(t, err)
	require.Contains(t, []string{items[0].ID, items[1].ID}, first.PelicanQuestion.ID)
	second, err := s.NewPelicanPersonalRound(ctx, 11, first.PelicanQuestion.ID)
	require.NoError(t, err)
	require.Contains(t, []string{items[0].ID, items[1].ID}, second.PelicanQuestion.ID)
	require.NotEqual(t, first.PelicanQuestion.ID, second.PelicanQuestion.ID)
	require.NotEqual(t, first.PelicanQuestion.RoundID, second.PelicanQuestion.RoundID)
	require.Equal(t, first.Prompt+"\n\n"+PelicanDeliveryContract, first.ActualPrompt)

	for i := range items {
		items[i].Enabled = i == 0
		items[i].Prompt += " edited"
	}
	_, err = s.SavePelicanPersonalQuestions(ctx, 11, items)
	require.NoError(t, err)
	same, err := s.NewPelicanPersonalRound(ctx, 11, items[0].ID)
	require.NoError(t, err)
	require.Equal(t, items[0].ID, same.PelicanQuestion.ID)
	require.Equal(t, items[0].Prompt, same.Prompt)
	frozen, err := s.ResolvePelicanPersonalRound(11, first.PelicanQuestion)
	require.NoError(t, err)
	require.Equal(t, first.ActualPrompt, frozen.ActualPrompt)
	_, err = s.ResolvePelicanPersonalRound(12, first.PelicanQuestion)
	require.ErrorIs(t, err, errPelicanPersonalRound)
	tampered := *first.PelicanQuestion
	tampered.Prompt = "Changed in transit"
	_, err = s.ResolvePelicanPersonalRound(11, &tampered)
	require.ErrorIs(t, err, errPelicanPersonalRound)
	require.Error(t, validatePelicanPromptMode(first), "manual-only mode must not enter a scheduled plan")
}

func TestPelicanPersonalQuestionValidation(t *testing.T) {
	ctx := context.Background()
	repo := &personalQuestionSettingsRepo{values: map[string]string{}}
	s := NewSettingService(repo, &config.Config{})
	id := "95f2a858-aa9a-4b6f-b53b-ab191cffb73a"
	for _, items := range [][]PelicanPersonalQuestion{
		nil,
		make([]PelicanPersonalQuestion, maxPelicanPersonalItems+1),
		{{Title: " ", Prompt: "draw"}},
		{{Title: "Title", Prompt: strings.Repeat("x", 4001)}},
		{{ID: "invalid-id", Title: "Title", Prompt: "draw"}},
		{{ID: id, Title: "Title", Prompt: "draw"}, {ID: id, Title: "Title", Prompt: "draw"}},
	} {
		_, err := s.SavePelicanPersonalQuestions(ctx, 11, items)
		require.ErrorIs(t, err, errPelicanPersonalInvalid)
	}
	require.Empty(t, repo.values)
}
