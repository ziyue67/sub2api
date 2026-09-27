package basispoints

import "fmt"

func (b *Bridge) validateHistoryContent(value any, inputIndex int, field string) error {
	content, _ := value.([]any)
	for index, rawPart := range content {
		part, _ := rawPart.(object)
		switch text(part["type"]) {
		case "input_text", "output_text", "text", "refusal":
		case "input_image":
			var err error
			if field == "output" && b.nativeToolImages[text(part["image_url"])] {
				// Only this request's fully validated tool screenshots may remain inline.
				if _, exists := part["file_id"]; exists {
					err = fmt.Errorf("basispoints input_image requires exactly one image reference")
				} else {
					err = validateImageDetail(part)
				}
			} else {
				err = validateImage(part)
			}
			if err != nil {
				return fmt.Errorf("%w (path=input[%d].%s[%d])", err, inputIndex, field, index)
			}
		case "encrypted_content":
			return fmt.Errorf("basispoints cannot forward encrypted_content message parts; refresh the model catalog and start a new conversation without a multi-agent v2 override, or resend the original plaintext (path=input[%d].%s[%d]; type=encrypted_content)", inputIndex, field, index)
		default:
			return fmt.Errorf("basispoints supports text and HTTPS input_image content only (path=input[%d].%s[%d]; type=%s)", inputIndex, field, index, contentTypeDiagnostic(part))
		}
	}
	return nil
}

// Report only fixed protocol labels. A caller-controlled type can itself contain
// private data or log injection, so unrecognized values are never echoed.
func contentTypeDiagnostic(part object) string {
	if part == nil {
		return "non_object"
	}
	value, present := part["type"]
	if !present {
		return "missing"
	}
	kind, ok := value.(string)
	if !ok {
		return "non_string"
	}
	switch kind {
	case "image", "image_url", "input_file", "file", "document",
		"input_audio", "output_audio", "audio", "reasoning_text", "summary_text",
		"tool_use", "tool_result", "thinking", "redacted_thinking":
		return kind
	default:
		return "unknown"
	}
}
