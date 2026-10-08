package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// normalizeAnthropicToolImages is an opt-in compatibility adapter, not a billing
// correction. Only structured images in user tool results are lifted. Raw JSON
// preserves unknown fields and integer precision; untouched messages are copied
// verbatim and the caller's input is never mutated (including on retries).
func normalizeAnthropicToolImages(account *Account, body []byte) ([]byte, error) {
	if account == nil || account.Platform != PlatformAnthropic || account.Type != AccountTypeAPIKey || account.Extra["anthropic_tool_result_images"] != true {
		return body, nil
	}
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("normalize tool images: invalid JSON")
	}
	type patch struct {
		start, end int
		value      string
	}
	patches := []patch{}
	root := string(body)
	for _, message := range gjson.Get(root, "messages").Array() {
		content := message.Get("content")
		if message.Get("role").String() != "user" || !content.IsArray() {
			continue
		}
		blocks := content.Array()
		lastTool := -1
		for i, b := range blocks {
			if b.Get("type").String() == "tool_result" {
				lastTool = i
			}
		}
		rewritten := make([]string, 0, len(blocks))
		attachments := []string{}
		for _, block := range blocks {
			toolID := block.Get("tool_use_id")
			id := toolID.String()
			nested := block.Get("content")
			if block.Get("type").String() != "tool_result" || toolID.Type != gjson.String || strings.TrimSpace(id) == "" || !nested.IsArray() {
				rewritten = append(rewritten, block.Raw)
				continue
			}
			replacement := []string{}
			images := []string{}
			count := 0
			for _, item := range nested.Array() {
				source := item.Get("source")
				valid := (source.Get("type").String() == "base64" && source.Get("data").Type == gjson.String && strings.TrimSpace(source.Get("data").String()) != "") || (source.Get("type").String() == "url" && source.Get("url").Type == gjson.String && strings.TrimSpace(source.Get("url").String()) != "")
				if item.Get("type").String() != "image" || !valid {
					replacement = append(replacement, item.Raw)
					continue
				}
				count++
				label := fmt.Sprintf("[Image %d from tool result %s]", count, id)
				replacement = append(replacement, toolImageText(label+" (attached below)"))
				images = append(images, toolImageText(label), item.Raw)
			}
			if count == 0 {
				rewritten = append(rewritten, block.Raw)
				continue
			}
			updated, err := sjson.SetRaw(block.Raw, "content", "["+strings.Join(replacement, ",")+"]")
			if err != nil {
				return nil, err
			}
			if cache := block.Get("cache_control"); cache.Exists() {
				updated, err = sjson.Delete(updated, "cache_control")
				if err != nil {
					return nil, err
				}
				tail := len(images) - 1
				if gjson.Get(images[tail], "cache_control").Exists() {
					images = append(images, toolImageText(fmt.Sprintf("[End of images from tool result %s]", id)))
					tail++
				}
				images[tail], err = sjson.SetRaw(images[tail], "cache_control", cache.Raw)
				if err != nil {
					return nil, err
				}
			}
			rewritten = append(rewritten, updated)
			attachments = append(attachments, images...)
		}
		if len(attachments) == 0 {
			continue
		}
		all := append([]string{}, rewritten[:lastTool+1]...)
		all = append(all, attachments...)
		all = append(all, rewritten[lastTool+1:]...)
		patches = append(patches, patch{content.Index, content.Index + len(content.Raw), "[" + strings.Join(all, ",") + "]"})
	}
	if len(patches) == 0 {
		return body, nil
	}
	size := len(body)
	for _, p := range patches {
		size += len(p.value) - (p.end - p.start)
	}
	out := make([]byte, 0, size)
	offset := 0
	for _, p := range patches {
		out = append(out, body[offset:p.start]...)
		out = append(out, p.value...)
		offset = p.end
	}
	out = append(out, body[offset:]...)
	return out, nil
}

func toolImageText(text string) string {
	encoded, _ := json.Marshal(text)
	return `{"type":"text","text":` + string(encoded) + `}`
}
