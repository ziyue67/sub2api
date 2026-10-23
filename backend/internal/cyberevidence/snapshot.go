// Package cyberevidence retains only the context visible in a confirmed upstream
// cyber_policy request. It never resolves referenced conversations or responses.
package cyberevidence

import (
	"encoding/json"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	MaxParseBytes    = 1024 * 1024
	MaxStoredBytes   = 256 * 1024
	RedactionVersion = "cyber-evidence-v1"
)

type Payload struct {
	Source             string `json:"source"`
	UpstreamHTTPStatus int    `json:"upstream_http_status,omitempty"`
	SessionID          string `json:"session_id,omitempty"`
	Input              string `json:"input"`
	Reason             string `json:"upstream_reason"`
	Complete           bool   `json:"complete"`
	Truncated          bool   `json:"truncated"`
	Omission           string `json:"omission,omitempty"`
	OriginalBytes      int    `json:"original_bytes"`
	RedactionVersion   string `json:"redaction_version"`
}

var (
	headerSecret     = regexp.MustCompile(`(?im)\b(?:authorization|proxy-authorization|cookie|set-cookie)\s*:\s*[^\r\n]*`)
	privateKey       = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
	secretAssignment = regexp.MustCompile(`(?i)((?:authorization|proxy.authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|secret|cookie|set.cookie|ticket|票据)\s*["']?\s*[:=]\s*["']?)(?:bearer\s+)?[^\s"',;}{]+`)
	bearerPattern    = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]+`)
	keyPattern       = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{8,}|AKIA[A-Z0-9]{16}|gh[pousr]_[A-Za-z0-9]{16,}|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)
	emailPattern     = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	urlPattern       = regexp.MustCompile(`(?i)(?:https?|wss?)://[^\s"'<>]+`)
	ipPortPattern    = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}:\d{1,5}\b`)
	bracketedIP      = regexp.MustCompile(`\[[0-9a-fA-F:.]+\](?::\d{1,5})?`)
	ipPattern        = regexp.MustCompile(`[0-9a-fA-F:.]{3,}`)
)

func secretField(key string) bool {
	key = strings.ToLower(key)
	key = strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(key)
	for _, part := range []string{"authorization", "apikey", "token", "password", "passwd", "secret", "cookie", "ticket", "credential", "票据"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return key == "email" || key == "ip" || key == "ipaddress" || key == "clientip"
}

// RedactText is best effort, not a guarantee that arbitrary business secrets can
// be recognized. URLs and media payloads are deliberately withheld in v1.
func RedactText(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = headerSecret.ReplaceAllString(s, "[REDACTED AUTH HEADER]")
	s = privateKey.ReplaceAllString(s, "[REDACTED PRIVATE KEY]")
	s = secretAssignment.ReplaceAllString(s, "${1}[REDACTED]")
	s = bearerPattern.ReplaceAllString(s, "Bearer [REDACTED]")
	s = keyPattern.ReplaceAllString(s, "[REDACTED KEY]")
	s = emailPattern.ReplaceAllString(s, "[REDACTED EMAIL]")
	s = urlPattern.ReplaceAllString(s, "[URL omitted]")
	s = ipPortPattern.ReplaceAllStringFunc(s, func(v string) string {
		host := v[:strings.LastIndexByte(v, ':')]
		if _, err := netip.ParseAddr(host); err == nil {
			return "[REDACTED IP]"
		}
		return v
	})
	s = bracketedIP.ReplaceAllStringFunc(s, func(v string) string {
		end := strings.IndexByte(v, ']')
		if end > 1 {
			if _, err := netip.ParseAddr(v[1:end]); err == nil {
				return "[REDACTED IP]"
			}
		}
		return v
	})
	return ipPattern.ReplaceAllStringFunc(s, func(v string) string {
		if _, err := netip.ParseAddr(v); err == nil {
			return "[REDACTED IP]"
		}
		return v
	})
}

type limiter struct {
	remaining int
	truncated bool
}

func (l *limiter) walk(value any, depth int) any {
	if depth > 16 || l.remaining <= 0 {
		l.truncated = true
		return "[omitted: limit]"
	}
	switch v := value.(type) {
	case map[string]any:
		// JSON keys are sorted so output and truncation are deterministic.
		keys := sortedKeys(v)
		out := map[string]any{}
		for i, k := range keys {
			if i >= 128 || l.remaining <= 0 {
				l.truncated = true
				break
			}
			if secretField(k) {
				out[boundedKey(k)] = "[REDACTED]"
				continue
			}
			if k == "image_url" || k == "audio" || k == "data" || k == "file_data" || k == "encrypted_content" {
				out[boundedKey(k)] = "[media/opaque data omitted]"
				l.truncated = true
				continue
			}
			out[boundedKey(k)] = l.walk(v[k], depth+1)
		}
		return out
	case []any:
		out := make([]any, 0)
		for i, item := range v {
			if i >= 256 || l.remaining <= 0 {
				l.truncated = true
				break
			}
			out = append(out, l.walk(item, depth+1))
		}
		return out
	case string:
		// Tool arguments/outputs often carry JSON inside a string.
		var nested any
		if depth < 16 && (strings.HasPrefix(strings.TrimSpace(v), "{") || strings.HasPrefix(strings.TrimSpace(v), "[")) && json.Unmarshal([]byte(v), &nested) == nil {
			sanitized, _ := json.Marshal(l.walk(nested, depth+1))
			return string(sanitized)
		}
		s := RedactText(v)
		limit := min(l.remaining, 16384)
		if len(s) > limit {
			s = validPrefix(s, limit)
			l.truncated = true
		}
		l.remaining -= len(s) + 16
		return s
	default:
		l.remaining -= 16
		return v
	}
}

func boundedKey(k string) string {
	if len(k) > 128 {
		return "[oversized field name omitted]"
	}
	return RedactText(k)
}
func validPrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func Snapshot(body []byte, reason string) Payload {
	return SnapshotWithSession(body, reason, "")
}

// SnapshotWithSession additionally keeps the sanitized explicit client session
// reference already extracted by the gateway. It is an identifier, never a
// header dump, and is still passed through redaction.
func SnapshotWithSession(body []byte, reason, sessionID string) Payload {
	p := Payload{
		Source:           "upstream_error_code:cyber_policy",
		Reason:           validPrefix(RedactText(reason), 4096),
		SessionID:        validPrefix(RedactText(strings.TrimSpace(sessionID)), 256),
		OriginalBytes:    len(body),
		RedactionVersion: RedactionVersion,
	}
	if len(body) > MaxParseBytes {
		p.Truncated = true
		p.Omission = "request_exceeds_parse_limit"
		return p
	}
	var root map[string]any
	if len(body) == 0 || json.Unmarshal(body, &root) != nil {
		p.Omission = "input_unavailable_or_invalid"
		return p
	}
	selected := map[string]any{}
	for _, key := range []string{"messages", "input", "instructions", "system", "tools", "tool_choice", "previous_response_id", "conversation", "thread_id", "session_id"} {
		if v, ok := root[key]; ok {
			selected[key] = v
		}
	}
	if _, messages := root["messages"]; !messages {
		if _, input := root["input"]; !input {
			p.Omission = "no_visible_input"
			return p
		}
	}
	l := limiter{remaining: 96 * 1024}
	raw, err := json.Marshal(l.walk(selected, 0))
	if err != nil || len(raw) > MaxStoredBytes {
		p.Truncated = true
		p.Omission = "structured_input_exceeds_limit"
		return p
	}
	p.Input = string(raw)
	p.Truncated = l.truncated
	p.Complete = !p.Truncated
	return p
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
