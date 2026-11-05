package claude

import "strings"

// Claude Opus 4.7 introduced a new tokenizer; the same text costs noticeably
// more input tokens on it than on earlier models. These families predate it.
var (
	// legacyTokenizerFamilies match the bare ID or any "-suffixed" variant.
	legacyTokenizerFamilies = []string{
		"claude-opus-4-6",
		"claude-opus-4-5",
		"claude-opus-4-1",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5",
		"claude-haiku-4-5",
	}
	// legacyTokenizerExactIDs would otherwise prefix-match newer minors
	// (claude-opus-4 vs claude-opus-4-7); dated IDs normalize to these.
	legacyTokenizerExactIDs = []string{
		"claude-opus-4",
		"claude-sonnet-4",
	}
)

// UsesLegacyTokenizer reports whether a Claude model predates the tokenizer
// introduced with Claude Opus 4.7. Unknown and newer models report false.
func UsesLegacyTokenizer(model string) bool {
	id := normalizeEffortModelID(model)
	// Relay model IDs may use dots for the minor version (for example 4.5).
	id = strings.ReplaceAll(id, ".", "-")
	if strings.HasPrefix(id, "claude-3-") || strings.HasPrefix(id, "claude-2") {
		return true
	}
	for _, exact := range legacyTokenizerExactIDs {
		if id == exact {
			return true
		}
	}
	for _, family := range legacyTokenizerFamilies {
		if id == family || strings.HasPrefix(id, family+"-") {
			return true
		}
	}
	return false
}
