// Adapted from MACOS-DO/sub4api at 254e7ec4932e65ab8830e7d10e66f184140041bd.
package service

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

// Only parse field boundaries. Environment text is XML-like, but fields such as
// subagents and network can contain unescaped text and are opaque to this rewrite.
var openAIEnvironmentFieldStart = regexp.MustCompile(`^<([A-Za-z_][A-Za-z0-9_.:-]*)(?:[ \t\r\n]+(?:[^<>"']|"[^"]*"|'[^']*')*)?[ \t\r\n]*/?>`)

// Closing tags may contain whitespace before >. Match only the field name;
// nested fields and unescaped characters in the body remain opaque.
func findOpenAIEnvironmentFieldEnd(text, name string) (start, end int) {
	prefix := "</" + name
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], prefix)
		if index < 0 {
			break
		}
		start = offset + index
		offset = start + len(prefix)
		rest := strings.TrimLeft(text[offset:], " \t\r\n")
		if strings.HasPrefix(rest, ">") {
			return start, len(text) - len(rest) + 1
		}
	}
	return -1, -1
}

func rewriteOpenAIRequestEnvironment(text, timezone string) (rewritten, previous, reason string) {
	trimmed := strings.TrimSpace(text)
	root := openAIEnvironmentFieldStart.FindStringSubmatchIndex(trimmed)
	if root == nil || trimmed[root[2]:root[3]] != "environment_context" {
		return text, "", "no_environment_context"
	}
	rootCloseStart, rootCloseEnd := findOpenAIEnvironmentFieldEnd(trimmed[root[1]:], "environment_context")
	if strings.HasSuffix(trimmed[:root[1]], "/>") || rootCloseEnd != len(trimmed)-root[1] {
		return text, "", "invalid_environment_context"
	}
	content := trimmed[root[1] : root[1]+rootCloseStart]
	valueStart, valueEnd := -1, -1
	for offset := 0; offset < len(content); {
		rest := strings.TrimLeftFunc(content[offset:], unicode.IsSpace)
		offset = len(content) - len(rest)
		if rest == "" {
			break
		}
		field := openAIEnvironmentFieldStart.FindStringSubmatchIndex(rest)
		if field == nil {
			return text, "", "invalid_environment_context"
		}
		name := rest[field[2]:field[3]]
		if name == "environment_context" {
			return text, "", "invalid_environment_context"
		}
		selfClosing := strings.HasSuffix(rest[:field[1]], "/>")
		if name == "timezone" && (valueStart >= 0 || selfClosing) {
			return text, "", "invalid_environment_context"
		}
		offset += field[1]
		if selfClosing {
			continue
		}
		end, closeEnd := findOpenAIEnvironmentFieldEnd(content[offset:], name)
		if end < 0 {
			return text, "", "invalid_environment_context"
		}
		if name == "timezone" {
			value := content[offset : offset+end]
			previous = strings.TrimSpace(value)
			if previous == "" || strings.Contains(value, "<") {
				return text, "", "invalid_environment_context"
			}
			valueStart = offset + len(value) - len(strings.TrimLeftFunc(value, unicode.IsSpace))
			valueEnd = offset + len(strings.TrimRightFunc(value, unicode.IsSpace))
		}
		offset += closeEnd
	}
	if valueStart < 0 {
		return text, "", "no_timezone"
	}
	if previous == timezone {
		return text, previous, "already_target"
	}
	// Patch only the timezone value, preserving all original tags and whitespace.
	base := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace)) + root[1]
	return text[:base+valueStart] + timezone + text[base+valueEnd:], previous, "replaced"
}

func normalizeOpenAIRequestLocale(ctx context.Context, account *Account, body []byte, transport string) []byte {
	if account == nil || !account.IsOpenAI() {
		return body
	}
	timezone := account.OpenAIRequestTimezone()
	before := make([]*string, 0)
	after := make([]*string, 0)
	matched := 0
	replaced := 0
	invalidEnvironment := false
	rewriteText := func(path string, text gjson.Result) {
		if text.Type != gjson.String {
			return
		}
		next, previous, reason := rewriteOpenAIRequestEnvironment(text.String(), timezone)
		if reason == "no_environment_context" {
			return
		}
		matched++
		var prior, current *string
		switch reason {
		case "invalid_environment_context":
			invalidEnvironment = true
		case "replaced", "already_target":
			loggedPrevious := previous
			if len(loggedPrevious) > 128 {
				loggedPrevious = loggedPrevious[:128]
			}
			prior, current = &loggedPrevious, &loggedPrevious
			if reason == "replaced" {
				if updated, err := sjson.SetBytes(body, path, next); err == nil {
					body = updated
					current = &timezone
					replaced++
				} else {
					invalidEnvironment = true
				}
			}
		}
		before = append(before, prior)
		after = append(after, current)
	}
	input := gjson.GetBytes(body, "input")
	if input.IsArray() {
		for inputIndex, item := range input.Array() {
			if item.Get("role").String() != "user" {
				continue
			}
			content := item.Get("content")
			path := "input." + strconv.Itoa(inputIndex) + ".content"
			if content.Type == gjson.String {
				rewriteText(path, content)
			} else if content.IsArray() {
				for contentIndex, part := range content.Array() {
					if part.Get("type").String() == "input_text" {
						rewriteText(path+"."+strconv.Itoa(contentIndex)+".text", part.Get("text"))
					}
				}
			}
		}
	}
	webSearchBefore := make([]*string, 0)
	webSearchAfter := make([]*string, 0)
	tools := gjson.GetBytes(body, "tools")
	if tools.IsArray() {
		for index, tool := range tools.Array() {
			kind := tool.Get("type").String()
			if kind != "web_search" && !strings.HasPrefix(kind, "web_search_") {
				continue
			}
			value := tool.Get("user_location.timezone")
			if value.Type == gjson.String {
				previous := value.String()
				loggedPrevious := previous
				if len(loggedPrevious) > 128 {
					loggedPrevious = loggedPrevious[:128]
				}
				current := loggedPrevious
				if previous != timezone {
					if updated, err := sjson.SetBytes(body, "tools."+strconv.Itoa(index)+".user_location.timezone", timezone); err == nil {
						body = updated
						current = timezone
						replaced++
					}
				}
				webSearchBefore = append(webSearchBefore, &loggedPrevious)
				webSearchAfter = append(webSearchAfter, &current)
			}
		}
	}
	reason := "no_environment_context"
	if replaced > 0 {
		reason = "replaced"
	} else if invalidEnvironment {
		reason = "invalid_environment_context"
	} else if matched > 0 {
		reason = "no_timezone"
		for _, value := range before {
			if value != nil {
				reason = "already_target"
				break
			}
		}
	}
	logger.FromContext(ctx).Debug("openai request timezone normalization",
		zap.Int64("account_id", account.ID), zap.String("transport", transport),
		zap.Bool("timezone_replaced", replaced > 0), zap.Any("timezone_before", before),
		zap.Any("timezone_after", after), zap.Any("web_search_timezone_before", webSearchBefore),
		zap.Any("web_search_timezone_after", webSearchAfter), zap.String("target_timezone", timezone),
		zap.Int("matched_count", matched), zap.Int("replaced_count", replaced), zap.String("reason", reason))
	return body
}
