package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	ExcelBPSAutoMoveOn403Key    = "openai_excel_bps_auto_move_on_403"
	ExcelBPS403TargetGroupIDKey = "openai_excel_bps_403_target_group_id"
)

// ExcelBPS403GroupTarget returns an explicitly configured destination. Zero
// means leave all groups; a missing or invalid destination never removes groups.
func (a *Account) ExcelBPS403GroupTarget() (int64, bool) {
	if !a.IsExcelBPSEnabled() || a.Extra[ExcelBPSAutoMoveOn403Key] != true {
		return 0, false
	}
	return excelBPS403GroupID(a.Extra[ExcelBPS403TargetGroupIDKey])
}

func excelBPS403GroupID(value any) (int64, bool) {
	var id int64
	switch value := value.(type) {
	case int:
		id = int64(value)
	case int64:
		id = value
	case float64:
		if math.IsNaN(value) || value < 0 || value >= math.MaxInt64 || math.Trunc(value) != value {
			return 0, false
		}
		id = int64(value)
	case json.Number:
		var err error
		id, err = value.Int64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return id, id >= 0
}

func validateExcelBPS403GroupExtra(extra map[string]any) error {
	invalid := func(message string) error {
		return infraerrors.BadRequest("OPENAI_EXCEL_BPS_INVALID", message)
	}
	if raw, exists := extra[ExcelBPSAutoMoveOn403Key]; exists {
		if _, ok := raw.(bool); !ok {
			return invalid(ExcelBPSAutoMoveOn403Key + " must be a boolean")
		}
	}
	if raw, exists := extra[ExcelBPS403TargetGroupIDKey]; exists && raw != nil {
		if _, ok := excelBPS403GroupID(raw); !ok {
			return invalid(ExcelBPS403TargetGroupIDKey + " must be a nonnegative integer")
		}
	}
	if extra[ExcelBPSAutoMoveOn403Key] == true {
		if _, ok := excelBPS403GroupID(extra[ExcelBPS403TargetGroupIDKey]); !ok {
			return invalid("BPS 403 group action requires an explicit target group, or 0 to leave all groups")
		}
	}
	return nil
}

func (s *adminServiceImpl) validateExcelBPS403GroupSettings(ctx context.Context, account *Account) error {
	if err := validateExcelBPS403GroupExtra(account.Extra); err != nil {
		return err
	}
	if account.Extra[ExcelBPSAutoMoveOn403Key] != true {
		return nil
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || account.IsShadow() || account.IsOpenAIAgentIdentity() || account.IsOpenAIPersonalAccessToken() {
		return infraerrors.BadRequest("OPENAI_EXCEL_BPS_INVALID", "BPS 403 group actions require an OpenAI ChatGPT OAuth account")
	}
	target, _ := excelBPS403GroupID(account.Extra[ExcelBPS403TargetGroupIDKey])
	if target == 0 {
		return nil
	}
	if s.groupRepo == nil {
		return errors.New("group repository not configured")
	}
	group, err := s.groupRepo.GetByIDLite(ctx, target)
	if err != nil {
		return err
	}
	if group == nil || (group.Platform != PlatformOpenAI && group.Platform != PlatformComposite) {
		return infraerrors.BadRequest("OPENAI_EXCEL_BPS_INVALID", "BPS 403 destination must be an OpenAI or composite group")
	}
	return s.ValidateAccountGroupBindings(ctx, []int64{target})
}
