package service

import (
	"context"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrUpstreamChannelValidation = infraerrors.BadRequest("UPSTREAM_CHANNEL_VALIDATION", "渠道参数无效：标识/名称唯一，备注最多500字；绑定最多100个明确账号，必须确认影响和版本")
var ErrUpstreamChannelConflict = infraerrors.Conflict("UPSTREAM_CHANNEL_CONFLICT", "渠道或绑定版本已变化，或渠道仍有账号；请重新读取，先明确解绑后再删除")
var ErrUpstreamChannelNotFound = infraerrors.NotFound("UPSTREAM_CHANNEL_NOT_FOUND", "上游渠道或账号不存在")
var upstreamChannelCode = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type UpstreamChannel struct {
	ID       int64  `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Remark   string `json:"remark"`
	Revision int64  `json:"revision"`
	Members  int64  `json:"members"`
}
type UpstreamChannelMutation struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Remark   string `json:"remark"`
	Revision *int64 `json:"revision"`
	Confirm  bool   `json:"confirm"`
}
type UpstreamChannelPage struct {
	Items    []UpstreamChannel `json:"items"`
	Total    int64             `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}
type UpstreamChannelBinding struct {
	AccountID   int64            `json:"account_id"`
	AccountName string           `json:"account_name"`
	Deleted     bool             `json:"deleted"`
	ChannelID   int64            `json:"channel_id"`
	Revision    int64            `json:"revision"`
	Channel     *UpstreamChannel `json:"channel,omitempty"`
}
type UpstreamChannelBindingsMutation struct {
	ChannelID int64                    `json:"channel_id"`
	Bindings  []UpstreamChannelBinding `json:"bindings"`
	Confirm   bool                     `json:"confirm"`
}
type UpstreamChannelMembers struct {
	Items []UpstreamChannelBinding `json:"items"`
	Total int64                    `json:"total"`
}
type UpstreamChannelRepository interface {
	ListUpstreamChannels(context.Context, string, int, int) (*UpstreamChannelPage, error)
	GetUpstreamChannel(context.Context, int64) (*UpstreamChannel, error)
	CreateUpstreamChannel(context.Context, UpstreamChannelMutation) (*UpstreamChannel, error)
	UpdateUpstreamChannel(context.Context, int64, UpstreamChannelMutation) (*UpstreamChannel, error)
	DeleteUpstreamChannel(context.Context, int64, UpstreamChannelMutation) error
	ReadUpstreamChannelBindings(context.Context, []int64) ([]UpstreamChannelBinding, error)
	BindUpstreamChannelAccounts(context.Context, UpstreamChannelBindingsMutation) ([]UpstreamChannelBinding, error)
	UpstreamChannelMembers(context.Context, int64, int, int) (*UpstreamChannelMembers, error)
}

func (s *ChannelService) UpstreamChannelRepository() (UpstreamChannelRepository, error) {
	r, ok := s.repo.(UpstreamChannelRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("UPSTREAM_CHANNEL_UNAVAILABLE", "上游渠道暂不可用")
	}
	return r, nil
}
func ValidateUpstreamChannelMutation(m *UpstreamChannelMutation, create bool) error {
	m.Name = strings.TrimSpace(m.Name)
	m.Remark = strings.TrimSpace(m.Remark)
	if !utf8.ValidString(m.Name) || !utf8.ValidString(m.Remark) || m.Name == "" || utf8.RuneCountInString(m.Name) > 100 || strings.IndexFunc(m.Name, unicode.IsControl) >= 0 || utf8.RuneCountInString(m.Remark) > 500 || strings.IndexFunc(m.Remark, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
		return ErrUpstreamChannelValidation
	}
	if create {
		if !m.Confirm || !upstreamChannelCode.MatchString(m.Code) {
			return ErrUpstreamChannelValidation
		}
	} else if m.Revision == nil || *m.Revision < 1 || *m.Revision == math.MaxInt64 || !m.Confirm {
		return ErrUpstreamChannelValidation
	}
	return nil
}
func ValidateUpstreamChannelBindings(m UpstreamChannelBindingsMutation) error {
	if !m.Confirm || m.ChannelID < 0 || len(m.Bindings) < 1 || len(m.Bindings) > 100 {
		return ErrUpstreamChannelValidation
	}
	seen := map[int64]bool{}
	for _, b := range m.Bindings {
		if b.AccountID < 1 || b.ChannelID < 0 || b.Revision < 0 || b.Revision == math.MaxInt64 || seen[b.AccountID] {
			return ErrUpstreamChannelValidation
		}
		seen[b.AccountID] = true
	}
	return nil
}

type initialUpstreamChannelKey struct{}

func WithInitialUpstreamChannel(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, initialUpstreamChannelKey{}, id)
}
func InitialUpstreamChannel(ctx context.Context) int64 {
	v, _ := ctx.Value(initialUpstreamChannelKey{}).(int64)
	return v
}
