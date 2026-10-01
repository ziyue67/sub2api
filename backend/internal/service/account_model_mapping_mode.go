package service

import infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

const OpenAIModelMappingModeKey = "model_mapping_mode"

// Legacy/explicit mappings remain allowlists. Alias mode is an explicit
// account policy, never inferred from a small mapping or an import marker.
func (a *Account) IsOpenAIModelMappingAliases() bool {
	return a != nil && a.IsOpenAIOAuth() && !a.IsShadow() && a.GetCredential(OpenAIModelMappingModeKey) == "aliases"
}

func ValidateModelMappingMode(credentials map[string]any) error {
	raw, exists := credentials[OpenAIModelMappingModeKey]
	if !exists || raw == nil {
		return nil
	}
	mode, ok := raw.(string)
	if !ok || (mode != "" && mode != "whitelist" && mode != "aliases") {
		return infraerrors.BadRequest("INVALID_MODEL_MAPPING_MODE", "model_mapping_mode must be whitelist or aliases")
	}
	return nil
}
