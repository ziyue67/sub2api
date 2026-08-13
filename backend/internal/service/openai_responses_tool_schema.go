package service

import "sort"

const (
	// 工具定义在多轮历史里最多再嵌套一层 tools，留出余量后截断，避免畸形请求体
	// 造成无界递归。
	openAIResponsesToolSchemaMaxDepth = 4
	// JSON Schema 里 type 只能是字符串或字符串数组；显式 null 无论哪个方言都非法，
	// 补成 object 与 upstream 对该工具的实际期望一致。
	openAIResponsesToolSchemaFallbackType = `"object"`
	// 显式 null 在 JSON 里只有这一种字面量形态。
	openAIResponsesToolSchemaNullLiteral = "null"
	// 仅用于跳过不关心的 JSON 值的结构深度上限。超过上限按无变更处理，避免把
	// 恶意深嵌套 body 递归到栈溢出。
	openAIResponsesToolSchemaJSONMaxDepth = 128
)

// openAIResponsesToolSchemaNullType 记录一处待修正的 null，用原始 body 上的
// 绝对字节偏移表示，便于最后一次性拼接。
type openAIResponsesToolSchemaNullType struct {
	offset int
	length int
}

// sanitizeOpenAIResponsesToolParameterTypes 修正请求体中显式为 null 的
// tools[].parameters.type。
//
// Codex Desktop 内置的 automation_update 工具会带 parameters.type = null，
// OpenAI 直接回 400 invalid_function_parameters，而网关把该状态归一成可重试的
// 502 upstream_error；该工具定义又会沉进多轮历史，导致之后每一轮继续失败并在
// 账号池里反复重放同一份坏 Schema。
//
// 只修正显式 null：缺失 type 的 Schema 本身合法（等价于不约束），补写会收窄
// 客户端语义，因此保持原样。
//
// 这里不使用逐项 gjson/sjson 路径查询。对一个有数千个 tools 的 body，路径查询会
// 为每个 tool 创建临时 Result/路径对象，即使最终只拼接一次也会线性增加分配次数。
// 轻量扫描器只记录原始 body 中的 null 偏移，最后一次性拼出新 body。
func sanitizeOpenAIResponsesToolParameterTypes(body []byte) ([]byte, bool, error) {
	if len(body) == 0 {
		return body, false, nil
	}

	hits := make([]openAIResponsesToolSchemaNullType, 0, 2)
	root := skipOpenAIResponsesToolSchemaWhitespace(body, 0)
	if root < len(body) && body[root] == '{' {
		collectOpenAIResponsesRootToolSchemaNullTypes(body, root, &hits)
	}
	if len(hits) == 0 {
		return body, false, nil
	}

	// tools 与 input 在 body 里的先后顺序由客户端决定，收集顺序不保证单调。
	sort.Slice(hits, func(i, j int) bool { return hits[i].offset < hits[j].offset })

	sanitized := make([]byte, 0, len(body)+len(hits)*len(openAIResponsesToolSchemaFallbackType))
	cursor := 0
	for _, hit := range hits {
		// 收集阶段已逐个校验过区间，这里再挡一次重叠，保证拼接严格单调向前。
		if hit.offset < cursor {
			continue
		}
		sanitized = append(sanitized, body[cursor:hit.offset]...)
		sanitized = append(sanitized, openAIResponsesToolSchemaFallbackType...)
		cursor = hit.offset + hit.length
	}
	sanitized = append(sanitized, body[cursor:]...)
	return sanitized, true, nil
}

// collectOpenAIResponsesRootToolSchemaNullTypes 只检查根级 tools 和 input 数组中
// 每个历史条目的 tools，与 Responses 请求可出现的工具定义位置保持一致。
func collectOpenAIResponsesRootToolSchemaNullTypes(
	body []byte, objectStart int, hits *[]openAIResponsesToolSchemaNullType,
) {
	for i := objectStart + 1; ; {
		i = skipOpenAIResponsesToolSchemaWhitespace(body, i)
		if i >= len(body) || body[i] == '}' {
			return
		}
		keyStart := i
		keyEnd := scanOpenAIResponsesToolSchemaString(body, i)
		if keyEnd < 0 {
			return
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, keyEnd)
		if i >= len(body) || body[i] != ':' {
			return
		}
		valueStart := skipOpenAIResponsesToolSchemaWhitespace(body, i+1)
		valueEnd := scanOpenAIResponsesToolSchemaValue(body, valueStart, 0)
		if valueEnd < 0 {
			return
		}
		if openAIResponsesToolSchemaKeyEquals(body, keyStart, keyEnd, "tools") && valueStart < len(body) && body[valueStart] == '[' {
			collectOpenAIResponsesToolsArrayNullTypes(body, valueStart, 0, hits)
		} else if openAIResponsesToolSchemaKeyEquals(body, keyStart, keyEnd, "input") && valueStart < len(body) && body[valueStart] == '[' {
			collectOpenAIResponsesInputArrayToolSchemaNullTypes(body, valueStart, hits)
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, valueEnd)
		if i >= len(body) || body[i] == '}' {
			return
		}
		if body[i] != ',' {
			return
		}
		i++
	}
}

func collectOpenAIResponsesInputArrayToolSchemaNullTypes(
	body []byte, arrayStart int, hits *[]openAIResponsesToolSchemaNullType,
) {
	for i := arrayStart + 1; ; {
		i = skipOpenAIResponsesToolSchemaWhitespace(body, i)
		if i >= len(body) || body[i] == ']' {
			return
		}
		valueStart := i
		valueEnd := scanOpenAIResponsesToolSchemaValue(body, valueStart, 0)
		if valueEnd < 0 {
			return
		}
		if body[valueStart] == '{' {
			collectOpenAIResponsesInputItemToolSchemaNullTypes(body, valueStart, hits)
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, valueEnd)
		if i >= len(body) || body[i] == ']' {
			return
		}
		if body[i] != ',' {
			return
		}
		i++
	}
}

func collectOpenAIResponsesInputItemToolSchemaNullTypes(
	body []byte, objectStart int, hits *[]openAIResponsesToolSchemaNullType,
) {
	for i := objectStart + 1; ; {
		i = skipOpenAIResponsesToolSchemaWhitespace(body, i)
		if i >= len(body) || body[i] == '}' {
			return
		}
		keyStart := i
		keyEnd := scanOpenAIResponsesToolSchemaString(body, i)
		if keyEnd < 0 {
			return
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, keyEnd)
		if i >= len(body) || body[i] != ':' {
			return
		}
		valueStart := skipOpenAIResponsesToolSchemaWhitespace(body, i+1)
		valueEnd := scanOpenAIResponsesToolSchemaValue(body, valueStart, 0)
		if valueEnd < 0 {
			return
		}
		if openAIResponsesToolSchemaKeyEquals(body, keyStart, keyEnd, "tools") && valueStart < len(body) && body[valueStart] == '[' {
			collectOpenAIResponsesToolsArrayNullTypes(body, valueStart, 0, hits)
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, valueEnd)
		if i >= len(body) || body[i] == '}' {
			return
		}
		if body[i] != ',' {
			return
		}
		i++
	}
}

// collectOpenAIResponsesToolsArrayNullTypes 收集一个 tools 数组里所有需要修正的
// parameters.type 位置。不按 tool type 过滤：null 的 schema type 在 function、
// custom 以及任何 hosted 工具上都同样非法。
func collectOpenAIResponsesToolsArrayNullTypes(
	body []byte, arrayStart, depth int, hits *[]openAIResponsesToolSchemaNullType,
) {
	if depth > openAIResponsesToolSchemaMaxDepth {
		return
	}
	for i := arrayStart + 1; ; {
		i = skipOpenAIResponsesToolSchemaWhitespace(body, i)
		if i >= len(body) || body[i] == ']' {
			return
		}
		valueStart := i
		valueEnd := scanOpenAIResponsesToolSchemaValue(body, valueStart, 0)
		if valueEnd < 0 {
			return
		}
		if body[valueStart] == '{' {
			collectOpenAIResponsesToolObjectSchemaNullTypes(body, valueStart, depth, hits)
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, valueEnd)
		if i >= len(body) || body[i] == ']' {
			return
		}
		if body[i] != ',' {
			return
		}
		i++
	}
}

func collectOpenAIResponsesToolObjectSchemaNullTypes(
	body []byte, objectStart, depth int, hits *[]openAIResponsesToolSchemaNullType,
) {
	for i := objectStart + 1; ; {
		i = skipOpenAIResponsesToolSchemaWhitespace(body, i)
		if i >= len(body) || body[i] == '}' {
			return
		}
		keyStart := i
		keyEnd := scanOpenAIResponsesToolSchemaString(body, i)
		if keyEnd < 0 {
			return
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, keyEnd)
		if i >= len(body) || body[i] != ':' {
			return
		}
		valueStart := skipOpenAIResponsesToolSchemaWhitespace(body, i+1)
		valueEnd := scanOpenAIResponsesToolSchemaValue(body, valueStart, 0)
		if valueEnd < 0 {
			return
		}
		if valueStart < len(body) && body[valueStart] == '{' {
			switch {
			case openAIResponsesToolSchemaKeyEquals(body, keyStart, keyEnd, "parameters"):
				collectOpenAIResponsesParameterTypeNull(body, valueStart, hits)
			case openAIResponsesToolSchemaKeyEquals(body, keyStart, keyEnd, "function"):
				collectOpenAIResponsesFunctionParameterTypeNull(body, valueStart, hits)
			}
		} else if openAIResponsesToolSchemaKeyEquals(body, keyStart, keyEnd, "tools") && valueStart < len(body) && body[valueStart] == '[' {
			collectOpenAIResponsesToolsArrayNullTypes(body, valueStart, depth+1, hits)
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, valueEnd)
		if i >= len(body) || body[i] == '}' {
			return
		}
		if body[i] != ',' {
			return
		}
		i++
	}
}

func collectOpenAIResponsesFunctionParameterTypeNull(
	body []byte, objectStart int, hits *[]openAIResponsesToolSchemaNullType,
) {
	for i := objectStart + 1; ; {
		i = skipOpenAIResponsesToolSchemaWhitespace(body, i)
		if i >= len(body) || body[i] == '}' {
			return
		}
		keyStart := i
		keyEnd := scanOpenAIResponsesToolSchemaString(body, i)
		if keyEnd < 0 {
			return
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, keyEnd)
		if i >= len(body) || body[i] != ':' {
			return
		}
		valueStart := skipOpenAIResponsesToolSchemaWhitespace(body, i+1)
		valueEnd := scanOpenAIResponsesToolSchemaValue(body, valueStart, 0)
		if valueEnd < 0 {
			return
		}
		if openAIResponsesToolSchemaKeyEquals(body, keyStart, keyEnd, "parameters") && valueStart < len(body) && body[valueStart] == '{' {
			collectOpenAIResponsesParameterTypeNull(body, valueStart, hits)
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, valueEnd)
		if i >= len(body) || body[i] == '}' {
			return
		}
		if body[i] != ',' {
			return
		}
		i++
	}
}

func collectOpenAIResponsesParameterTypeNull(
	body []byte, objectStart int, hits *[]openAIResponsesToolSchemaNullType,
) {
	for i := objectStart + 1; ; {
		i = skipOpenAIResponsesToolSchemaWhitespace(body, i)
		if i >= len(body) || body[i] == '}' {
			return
		}
		keyStart := i
		keyEnd := scanOpenAIResponsesToolSchemaString(body, i)
		if keyEnd < 0 {
			return
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, keyEnd)
		if i >= len(body) || body[i] != ':' {
			return
		}
		valueStart := skipOpenAIResponsesToolSchemaWhitespace(body, i+1)
		valueEnd := scanOpenAIResponsesToolSchemaValue(body, valueStart, 0)
		if valueEnd < 0 {
			return
		}
		if openAIResponsesToolSchemaKeyEquals(body, keyStart, keyEnd, "type") && valueEnd-valueStart == len(openAIResponsesToolSchemaNullLiteral) && string(body[valueStart:valueEnd]) == openAIResponsesToolSchemaNullLiteral {
			*hits = append(*hits, openAIResponsesToolSchemaNullType{offset: valueStart, length: len(openAIResponsesToolSchemaNullLiteral)})
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, valueEnd)
		if i >= len(body) || body[i] == '}' {
			return
		}
		if body[i] != ',' {
			return
		}
		i++
	}
}

func skipOpenAIResponsesToolSchemaWhitespace(body []byte, i int) int {
	for i < len(body) {
		switch body[i] {
		case ' ', '\n', '\r', '\t':
			i++
		default:
			return i
		}
	}
	return i
}

func scanOpenAIResponsesToolSchemaString(body []byte, start int) int {
	if start >= len(body) || body[start] != '"' {
		return -1
	}
	for i := start + 1; i < len(body); i++ {
		switch body[i] {
		case '\\':
			i++
			if i >= len(body) {
				return -1
			}
		case '"':
			return i + 1
		}
	}
	return -1
}

func scanOpenAIResponsesToolSchemaValue(body []byte, start, depth int) int {
	if start >= len(body) || depth > openAIResponsesToolSchemaJSONMaxDepth {
		return -1
	}
	switch body[start] {
	case '"':
		return scanOpenAIResponsesToolSchemaString(body, start)
	case '{':
		return scanOpenAIResponsesToolSchemaObject(body, start, depth+1)
	case '[':
		return scanOpenAIResponsesToolSchemaArray(body, start, depth+1)
	default:
		i := start
		for i < len(body) {
			switch body[i] {
			case ' ', '\n', '\r', '\t', ',', ']', '}':
				return i
			default:
				i++
			}
		}
		return i
	}
}

func scanOpenAIResponsesToolSchemaObject(body []byte, start, depth int) int {
	for i := start + 1; ; {
		i = skipOpenAIResponsesToolSchemaWhitespace(body, i)
		if i >= len(body) {
			return -1
		}
		if body[i] == '}' {
			return i + 1
		}
		keyEnd := scanOpenAIResponsesToolSchemaString(body, i)
		if keyEnd < 0 {
			return -1
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, keyEnd)
		if i >= len(body) || body[i] != ':' {
			return -1
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, i+1)
		i = scanOpenAIResponsesToolSchemaValue(body, i, depth)
		if i < 0 {
			return -1
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, i)
		if i >= len(body) {
			return -1
		}
		if body[i] == '}' {
			return i + 1
		}
		if body[i] != ',' {
			return -1
		}
		i++
	}
}

func scanOpenAIResponsesToolSchemaArray(body []byte, start, depth int) int {
	for i := start + 1; ; {
		i = skipOpenAIResponsesToolSchemaWhitespace(body, i)
		if i >= len(body) {
			return -1
		}
		if body[i] == ']' {
			return i + 1
		}
		i = scanOpenAIResponsesToolSchemaValue(body, i, depth)
		if i < 0 {
			return -1
		}
		i = skipOpenAIResponsesToolSchemaWhitespace(body, i)
		if i >= len(body) {
			return -1
		}
		if body[i] == ']' {
			return i + 1
		}
		if body[i] != ',' {
			return -1
		}
		i++
	}
}

// openAIResponsesToolSchemaKeyEquals 在不分配字符串的前提下比较 JSON 对象键。
// 工具协议键均为 ASCII；同时兼容这些 ASCII 字符的 \u00XX 写法。
func openAIResponsesToolSchemaKeyEquals(body []byte, start, end int, want string) bool {
	if start < 0 || end <= start+1 || end > len(body) || body[start] != '"' || body[end-1] != '"' {
		return false
	}
	i := start + 1
	for j := 0; j < len(want); j++ {
		if i >= end-1 {
			return false
		}
		if body[i] != '\\' {
			if body[i] != want[j] {
				return false
			}
			i++
			continue
		}
		i++
		if i >= end-1 || body[i] != 'u' || i+4 >= end-1 {
			return false
		}
		value, ok := decodeOpenAIResponsesToolSchemaHex4(body[i+1 : i+5])
		if !ok || value != want[j] {
			return false
		}
		i += 5
	}
	return i == end-1
}

func decodeOpenAIResponsesToolSchemaHex4(raw []byte) (byte, bool) {
	if len(raw) != 4 || raw[0] != '0' || raw[1] != '0' {
		return 0, false
	}
	decode := func(ch byte) (byte, bool) {
		switch {
		case ch >= '0' && ch <= '9':
			return ch - '0', true
		case ch >= 'a' && ch <= 'f':
			return ch - 'a' + 10, true
		case ch >= 'A' && ch <= 'F':
			return ch - 'A' + 10, true
		default:
			return 0, false
		}
	}
	hi, ok := decode(raw[2])
	if !ok {
		return 0, false
	}
	lo, ok := decode(raw[3])
	if !ok {
		return 0, false
	}
	return hi<<4 | lo, true
}
