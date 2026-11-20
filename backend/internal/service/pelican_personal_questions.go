package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	PelicanPromptModePersonal = "personal_random"
	pelicanPersonalSource     = "personal:svg-motion"
	pelicanPersonalVersion    = "personal-svg-v1"
	maxPelicanPersonalItems   = 50
)

var (
	errPelicanPersonalInvalid = infraerrors.BadRequest("PELICAN_PERSONAL_INVALID", "Invalid personal SVG question bank")
	errPelicanPersonalEmpty   = infraerrors.BadRequest("PELICAN_PERSONAL_EMPTY", "No enabled questions in your personal SVG bank")
	errPelicanPersonalRound   = infraerrors.BadRequest("PELICAN_PERSONAL_ROUND_INVALID", "Invalid personal SVG round; prepare a new round")
)

type PelicanPersonalQuestion struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Prompt  string `json:"prompt"`
	Enabled bool   `json:"enabled"`
}

func pelicanPersonalSettingKey(owner int64) string {
	return "pelican_personal_questions:" + strconv.FormatInt(owner, 10)
}

func (s *SettingService) GetPelicanPersonalQuestions(ctx context.Context, owner int64) ([]PelicanPersonalQuestion, error) {
	if owner <= 0 {
		return nil, errPelicanPersonalInvalid
	}
	items := make([]PelicanPersonalQuestion, 0)
	raw, err := s.settingRepo.GetValue(ctx, pelicanPersonalSettingKey(owner))
	if errors.Is(err, ErrSettingNotFound) {
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("decode personal SVG bank: %w", err)
	}
	return items, nil
}

func (s *SettingService) SavePelicanPersonalQuestions(ctx context.Context, owner int64, items []PelicanPersonalQuestion) ([]PelicanPersonalQuestion, error) {
	if owner <= 0 || items == nil || len(items) > maxPelicanPersonalItems {
		return nil, errPelicanPersonalInvalid
	}
	saved := make([]PelicanPersonalQuestion, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		item.Title, item.Prompt = strings.TrimSpace(item.Title), strings.TrimSpace(item.Prompt)
		if item.Title == "" || item.Prompt == "" || utf8.RuneCountInString(item.Title) > 80 || utf8.RuneCountInString(item.Prompt) > 4000 {
			return nil, errPelicanPersonalInvalid
		}
		if item.ID == "" {
			id, err := uuid.NewRandom()
			if err != nil {
				return nil, err
			}
			item.ID = id.String()
		}
		id, err := uuid.Parse(item.ID)
		if err != nil || id.String() != item.ID || seen[item.ID] {
			return nil, errPelicanPersonalInvalid
		}
		seen[item.ID] = true
		saved = append(saved, item)
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, pelicanPersonalSettingKey(owner), string(raw)); err != nil {
		return nil, err
	}
	return saved, nil
}

func (s *SettingService) NewPelicanPersonalRound(ctx context.Context, owner int64, previousID string) (*PelicanTestConfig, error) {
	items, err := s.GetPelicanPersonalQuestions(ctx, owner)
	if err != nil {
		return nil, err
	}
	enabled := make([]PelicanPersonalQuestion, 0, len(items))
	for _, item := range items {
		if item.Enabled {
			enabled = append(enabled, item)
		}
	}
	if len(enabled) == 0 {
		return nil, errPelicanPersonalEmpty
	}
	candidates := enabled
	if len(enabled) > 1 {
		candidates = make([]PelicanPersonalQuestion, 0, len(enabled))
		for _, item := range enabled {
			if item.ID != previousID {
				candidates = append(candidates, item)
			}
		}
	}
	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
	if err != nil {
		return nil, err
	}
	round, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}
	picked := candidates[index.Int64()]
	question := &PelicanQuestion{
		ID: picked.ID, Title: picked.Title, Prompt: picked.Prompt, Criteria: []string{},
		Version: pelicanPersonalVersion, Source: pelicanPersonalSource, RoundID: "manual:" + round.String(),
	}
	key := s.pelicanPersonalSigningKey()
	if len(key) == 0 {
		return nil, fmt.Errorf("personal SVG round signing is unavailable")
	}
	question.Proof = hex.EncodeToString(signPelicanPersonalQuestion(key, owner, *question))
	return personalPelicanConfig(question), nil
}

func (s *SettingService) ResolvePelicanPersonalRound(owner int64, question *PelicanQuestion) (*PelicanTestConfig, error) {
	key := s.pelicanPersonalSigningKey()
	if len(key) == 0 || owner <= 0 || question == nil || question.Source != pelicanPersonalSource ||
		question.Version != pelicanPersonalVersion || len(question.Proof) != sha256.Size*2 {
		return nil, errPelicanPersonalRound
	}
	actual, err := hex.DecodeString(question.Proof)
	if err != nil || !hmac.Equal(actual, signPelicanPersonalQuestion(key, owner, *question)) {
		return nil, errPelicanPersonalRound
	}
	return personalPelicanConfig(question), nil
}

func (s *SettingService) pelicanPersonalSigningKey() []byte {
	if s.cfg == nil || strings.TrimSpace(s.cfg.JWT.Secret) == "" {
		return nil
	}
	key := sha256.Sum256([]byte("sub2api/pelican-personal-round/v1\x00" + s.cfg.JWT.Secret))
	return key[:]
}

// Bind the frozen prompt to its administrator so later edits cannot mutate the round.
func signPelicanPersonalQuestion(key []byte, owner int64, question PelicanQuestion) []byte {
	question.Proof = ""
	payload, _ := json.Marshal(question) // PelicanQuestion contains only JSON-safe fields.
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strconv.FormatInt(owner, 10) + ":"))
	mac.Write(payload)
	return mac.Sum(nil)
}

func personalPelicanConfig(question *PelicanQuestion) *PelicanTestConfig {
	cfg := &PelicanTestConfig{QuestionKind: "pelican", PromptMode: PelicanPromptModePersonal, Prompt: question.Prompt, PelicanQuestion: question}
	cfg.ActualPrompt = intelligenceTestPrompt(cfg)
	return cfg
}
