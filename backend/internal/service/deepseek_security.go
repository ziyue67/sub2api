package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/net/http/httpguts"
)

const deepSeekCredentialRedaction = "[redacted]"

func withDeepSeekRedirectsDisabled(ctx context.Context, account *Account) context.Context {
	if account == nil || !account.IsDeepSeekAPIKey() {
		return ctx
	}
	return WithHTTPUpstreamRedirectsDisabled(ctx)
}

// redactDeepSeekAPIKey removes the credential before upstream errors reach
// logs, ops events, failover errors, or downstream responses.
func redactDeepSeekAPIKey(account *Account, body []byte) []byte {
	if account == nil || !account.IsDeepSeekAPIKey() || len(body) == 0 {
		return body
	}
	apiKey := account.GetDeepSeekAPIKey()
	if apiKey == "" {
		return body
	}

	if json.Valid(bytes.TrimSpace(body)) {
		return redactDeepSeekJSONStrings(body, apiKey)
	}
	if redacted, ok := redactDeepSeekSSE(body, apiKey); ok {
		return redacted
	}

	// Invalid JSON and plain-text errors have no structure to preserve. Decode
	// any complete JSON string fragments first, then cover literal/canonical
	// escaped echoes in the remaining text.
	redacted := redactDeepSeekJSONStrings(body, apiKey)
	return redactDeepSeekPlainText(redacted, apiKey)
}

func redactDeepSeekAPIKeyString(account *Account, value string) string {
	return string(redactDeepSeekAPIKey(account, []byte(value)))
}

// sanitizeDeepSeekResponseHeadersInPlace makes every subsequent consumer of a
// DeepSeek response (downstream writers, ops events and failover errors) see
// only validated, credential-redacted header values.
func sanitizeDeepSeekResponseHeadersInPlace(account *Account, headers http.Header) {
	if account == nil || !account.IsDeepSeekAPIKey() || headers == nil {
		return
	}
	sanitized := make(http.Header, len(headers))
	apiKey := account.GetDeepSeekAPIKey()
	for name, values := range headers {
		if !httpguts.ValidHeaderFieldName(name) {
			continue
		}
		for _, value := range values {
			if !httpguts.ValidHeaderFieldValue(value) {
				continue
			}
			sanitized.Add(name, redactDeepSeekHeaderValue(value, apiKey))
		}
	}
	clear(headers)
	for name, values := range sanitized {
		headers[name] = values
	}
}

// redactDeepSeekHeaderValue also recognizes JSON-style escapes embedded in an
// otherwise plain header value (for example ds-\u0063anary). Header values are
// not necessarily complete JSON strings, so the body JSON redactor cannot be
// used for this case.
func redactDeepSeekHeaderValue(value, apiKey string) string {
	if value == "" || apiKey == "" {
		return value
	}
	value = strings.ReplaceAll(value, apiKey, deepSeekCredentialRedaction)
	keyRunes := []rune(apiKey)
	if len(keyRunes) == 0 {
		return value
	}

	var out strings.Builder
	last := 0
	for start := 0; start < len(value); {
		end, ok := deepSeekEscapedHeaderMatchEnd(value, start, keyRunes)
		if ok {
			out.WriteString(value[last:start])
			out.WriteString(deepSeekCredentialRedaction)
			last = end
			start = end
			continue
		}
		_, size := utf8.DecodeRuneInString(value[start:])
		if size <= 0 {
			size = 1
		}
		start += size
	}
	if last == 0 {
		return value
	}
	out.WriteString(value[last:])
	return out.String()
}

func deepSeekEscapedHeaderMatchEnd(value string, start int, key []rune) (int, bool) {
	position := start
	for _, want := range key {
		got, size, ok := decodeDeepSeekHeaderRune(value, position)
		if !ok || got != want {
			return 0, false
		}
		position += size
	}
	return position, true
}

func decodeDeepSeekHeaderRune(value string, start int) (rune, int, bool) {
	if start < 0 || start >= len(value) {
		return 0, 0, false
	}
	if value[start] != '\\' {
		r, size := utf8.DecodeRuneInString(value[start:])
		return r, size, r != utf8.RuneError || size > 1
	}
	if start+1 >= len(value) {
		return 0, 0, false
	}
	switch value[start+1] {
	case '"', '\\', '/':
		return rune(value[start+1]), 2, true
	case 'b':
		return '\b', 2, true
	case 'f':
		return '\f', 2, true
	case 'n':
		return '\n', 2, true
	case 'r':
		return '\r', 2, true
	case 't':
		return '\t', 2, true
	case 'u':
		first, ok := parseDeepSeekUnicodeEscape(value, start)
		if !ok {
			return 0, 0, false
		}
		if utf16.IsSurrogate(first) && start+12 <= len(value) && value[start+6:start+8] == `\u` {
			second, secondOK := parseDeepSeekUnicodeEscape(value, start+6)
			if secondOK {
				decoded := utf16.DecodeRune(first, second)
				if decoded != utf8.RuneError {
					return decoded, 12, true
				}
			}
		}
		return first, 6, !utf16.IsSurrogate(first)
	default:
		return 0, 0, false
	}
}

func parseDeepSeekUnicodeEscape(value string, start int) (rune, bool) {
	if start < 0 || start+6 > len(value) || value[start:start+2] != `\u` {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value[start+2:start+6], 16, 16)
	return rune(parsed), err == nil
}

func redactDeepSeekJSONStrings(body []byte, apiKey string) []byte {
	if len(body) == 0 || apiKey == "" {
		return body
	}

	var out []byte
	last := 0
	for start := 0; start < len(body); {
		rel := bytes.IndexByte(body[start:], '"')
		if rel < 0 {
			break
		}
		quoteStart := start + rel
		quoteEnd := deepSeekJSONStringEnd(body, quoteStart)
		if quoteEnd < 0 {
			start = quoteStart + 1
			continue
		}

		var decoded string
		token := body[quoteStart : quoteEnd+1]
		matches := false
		if err := json.Unmarshal(token, &decoded); err == nil {
			matches = strings.Contains(decoded, apiKey)
		}
		if matches {
			replacement := strings.ReplaceAll(decoded, apiKey, deepSeekCredentialRedaction)
			encoded, err := json.Marshal(replacement)
			if err == nil {
				if out == nil {
					out = make([]byte, 0, len(body))
				}
				out = append(out, body[last:quoteStart]...)
				out = append(out, encoded...)
				last = quoteEnd + 1
			}
		}
		start = quoteEnd + 1
	}
	if out == nil {
		return body
	}
	return append(out, body[last:]...)
}

func deepSeekJSONStringEnd(body []byte, start int) int {
	if start < 0 || start >= len(body) || body[start] != '"' {
		return -1
	}
	for i := start + 1; i < len(body); i++ {
		switch body[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

func redactDeepSeekPlainText(body []byte, apiKey string) []byte {
	redacted := bytes.ReplaceAll(body, []byte(apiKey), []byte(deepSeekCredentialRedaction))
	if encoded, err := json.Marshal(apiKey); err == nil && len(encoded) >= 2 {
		escaped := encoded[1 : len(encoded)-1]
		if !bytes.Equal(escaped, []byte(apiKey)) {
			redacted = bytes.ReplaceAll(redacted, escaped, []byte(deepSeekCredentialRedaction))
		}
	}
	return redacted
}

func redactDeepSeekSSE(body []byte, apiKey string) ([]byte, bool) {
	first := bytes.TrimSpace(body)
	if !(bytes.HasPrefix(first, []byte("data:")) || bytes.HasPrefix(first, []byte("event:")) ||
		bytes.HasPrefix(first, []byte("id:")) || bytes.HasPrefix(first, []byte("retry:")) || bytes.HasPrefix(first, []byte(":"))) {
		return body, false
	}

	lines := bytes.SplitAfter(body, []byte{'\n'})
	out := make([]byte, 0, len(body))
	for _, wireLine := range lines {
		lineEnd := len(wireLine)
		if lineEnd > 0 && wireLine[lineEnd-1] == '\n' {
			lineEnd--
		}
		if lineEnd > 0 && wireLine[lineEnd-1] == '\r' {
			lineEnd--
		}
		line := wireLine[:lineEnd]
		newline := wireLine[lineEnd:]
		colon := bytes.IndexByte(line, ':')
		if colon < 0 {
			if strings.TrimSpace(string(line)) == apiKey {
				out = append(out, []byte(deepSeekCredentialRedaction)...)
				out = append(out, newline...)
			} else {
				out = append(out, wireLine...)
			}
			continue
		}
		field := line[:colon]
		if strings.TrimSpace(string(field)) == apiKey {
			out = append(out, []byte(deepSeekCredentialRedaction)...)
		} else {
			out = append(out, field...)
		}
		out = append(out, ':')
		valueStart := colon + 1
		if valueStart < len(line) && line[valueStart] == ' ' {
			valueStart++
			out = append(out, ' ')
		}
		value := line[valueStart:]
		if json.Valid(bytes.TrimSpace(value)) {
			out = append(out, redactDeepSeekJSONStrings(value, apiKey)...)
		} else {
			out = append(out, redactDeepSeekPlainText(redactDeepSeekJSONStrings(value, apiKey), apiKey)...)
		}
		out = append(out, newline...)
	}
	return out, true
}
