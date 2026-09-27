package service

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// stripOpenAICodexUnsupportedWebSearchFields removes provider-specific web
// search controls that the ChatGPT/Codex inference endpoint does not accept.
// BPS requests never reach this helper; only native OAuth/Codex forwarding does.
func stripOpenAICodexUnsupportedWebSearchFields(body []byte) ([]byte, bool, error) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body, false, nil
	}
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return body, false, nil
	}
	changed := false
	for index, tool := range tools.Array() {
		kind := strings.ToLower(strings.TrimSpace(tool.Get("type").String()))
		if !strings.HasPrefix(kind, "web_search") {
			continue
		}
		path := fmt.Sprintf("tools.%d.external_web_access", index)
		if !gjson.GetBytes(body, path).Exists() {
			continue
		}
		updated, err := sjson.DeleteBytes(body, path)
		if err != nil {
			return body, changed, err
		}
		body = updated
		changed = true
	}
	return body, changed, nil
}
