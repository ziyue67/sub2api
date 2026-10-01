package service

import (
	"maps"
	"strings"
	"unicode"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type OAuthModelMappingRule struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func defaultOAuthModelMappings(platform string) []OAuthModelMappingRule {
	if platform == PlatformOpenAI {
		return []OAuthModelMappingRule{{From: "gpt-5.4", To: "gpt-5.5"}}
	}
	return []OAuthModelMappingRule{}
}

func validateOAuthModelMappings(rules []OAuthModelMappingRule) error {
	bad := func(message string) error { return infraerrors.BadRequest("AUTO_CONFIG_INVALID", message) }
	if len(rules) > 100 {
		return bad("at most 100 model mapping rules allowed")
	}
	seen := make(map[string]bool, len(rules))
	for _, rule := range rules {
		from, to := strings.TrimSpace(rule.From), strings.TrimSpace(rule.To)
		if from == "" || to == "" || len(from) > 256 || len(to) > 256 || strings.IndexFunc(from+to, unicode.IsSpace) >= 0 || strings.IndexFunc(from+to, unicode.IsControl) >= 0 {
			return bad("model names must contain 1–256 bytes without whitespace or control characters")
		}
		if strings.Contains(strings.TrimSuffix(from, "*"), "*") || strings.Contains(to, "*") {
			return bad("only a trailing source wildcard is allowed; target must be a model name")
		}
		if seen[from] {
			return bad("model mapping sources must be unique")
		}
		seen[from] = true
	}
	return nil
}

// Called only from account creation. Templates replace identity passthroughs,
// while explicit custom targets take precedence. Clone both maps to keep the
// caller's credentials and other accounts untouched.
func applyOAuthModelMappings(input *CreateAccountInput, rules []OAuthModelMappingRule) bool {
	if len(rules) == 0 {
		return false
	}
	mapping := make(map[string]any)
	switch existing := input.Credentials["model_mapping"].(type) {
	case nil:
	case map[string]any:
		maps.Copy(mapping, existing)
	case map[string]string:
		for from, to := range existing {
			mapping[from] = to
		}
	default:
		// Do not silently replace a malformed, explicitly supplied mapping.
		return false
	}
	hadExplicitMapping := len(mapping) > 0
	applied := false
	for _, rule := range rules {
		from, to := strings.TrimSpace(rule.From), strings.TrimSpace(rule.To)
		if current, exists := mapping[from]; exists {
			target, ok := current.(string)
			if !ok || target != from || target == to {
				continue
			}
		}
		mapping[from] = to
		applied = true
	}
	if applied {
		input.Credentials = maps.Clone(input.Credentials)
		if input.Credentials == nil {
			input.Credentials = make(map[string]any)
		}
		input.Credentials["model_mapping"] = mapping
		// Adding a convenience alias to an unrestricted OAuth account must not
		// turn it into a one-model allowlist. Explicit import scopes stay intact.
		if input.Platform == PlatformOpenAI && input.Type == AccountTypeOAuth && !hadExplicitMapping {
			if _, explicitMode := input.Credentials[OpenAIModelMappingModeKey]; !explicitMode {
				input.Credentials[OpenAIModelMappingModeKey] = "aliases"
			}
		}
	}
	return applied
}
