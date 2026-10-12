//go:build unit

package service

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 本文件锁死审计 N4：`data` 是极常见的通用键名，仅凭键名就把其中的 base64 当作
// 多模态图片负载（1600 token/块）会让"把文档 base64 后塞进普通 data 字段"的请求
// 被低估数个数量级。多模态的 data 必须由其**父键**上下文确认。

// TestJsonParentKeyBefore 验证外层键名回溯（N4 判定基础）。
func TestJsonParentKeyBefore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		pos  int // valueStart（base64 串的第一个字节）
		want string
	}{
		{
			name: "anthropic-source",
			body: `{"source":{"type":"base64","media_type":"image/png","data":"XXXX"`,
			pos:  len(`{"source":{"type":"base64","media_type":"image/png","data":"`),
			want: "source",
		},
		{
			name: "gemini-inline-data",
			body: `{"parts":[{"inline_data":{"mime_type":"image/png","data":"XXXX"`,
			pos:  len(`{"parts":[{"inline_data":{"mime_type":"image/png","data":"`),
			want: "inline_data",
		},
		{
			name: "gemini-inline-data-camel",
			body: `{"inlineData":{"mime_type":"image/png","data":"XXXX"`,
			pos:  len(`{"inlineData":{"mime_type":"image/png","data":"`),
			want: "inlineData",
		},
		{
			name: "openai-input-audio",
			body: `{"input_audio":{"data":"XXXX"`,
			pos:  len(`{"input_audio":{"data":"`),
			want: "input_audio",
		},
		{
			name: "generic-payload",
			body: `{"payload":{"data":"XXXX"`,
			pos:  len(`{"payload":{"data":"`),
			want: "payload",
		},
		{
			name: "top-level-data-has-no-parent",
			body: `{"a":"x","data":"XXXX"`,
			pos:  len(`{"a":"x","data":"`),
			want: "",
		},
		{
			name: "root-object-has-no-parent",
			body: `{"data":"XXXX"`,
			pos:  len(`{"data":"`),
			want: "",
		},
		{
			name: "array-element-has-no-parent",
			body: `[{"data":"XXXX"`,
			pos:  len(`[{"data":"`),
			want: "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, jsonParentKeyBefore([]byte(tc.body), tc.pos))
		})
	}
}

// TestMultimodalPayloadKeyIsBinary_DataRequiresParentContext 验证 `data` 键的判定：
// 已知的多模态父键 → 二进制；其它已知父键 → 不按二进制（回落稠密/文本口径）；
// 父键无法确证 → 保持既有行为（仍是二进制，避免把真实图片降级而误 403）。
func TestMultimodalPayloadKeyIsBinary_DataRequiresParentContext(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		pos  int
		want bool
	}{
		{
			name: "source-data-is-binary",
			body: `{"source":{"type":"base64","media_type":"image/png","data":"XXXX"`,
			pos:  len(`{"source":{"type":"base64","media_type":"image/png","data":"`),
			want: true,
		},
		{
			name: "inline-data-is-binary",
			body: `{"parts":[{"inline_data":{"mime_type":"image/png","data":"XXXX"`,
			pos:  len(`{"parts":[{"inline_data":{"mime_type":"image/png","data":"`),
			want: true,
		},
		{
			name: "generic-data-is-not-binary",
			body: `{"payload":{"data":"XXXX"`,
			pos:  len(`{"payload":{"data":"`),
			want: false,
		},
		{
			name: "unverifiable-parent-keeps-legacy",
			body: `{"data":"XXXX"`,
			pos:  len(`{"data":"`),
			want: true,
		},
		{
			name: "image-url-is-unaffected",
			body: `{"m":{"image_url":"XXXX"`,
			pos:  len(`{"m":{"image_url":"`),
			want: true,
		},
		{
			name: "text-key-is-not-multimodal",
			body: `{"m":{"text":"XXXX"`,
			pos:  len(`{"m":{"text":"`),
			want: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := []byte(tc.body)
			key := jsonKeyBefore(body, tc.pos)
			require.Equal(t, tc.want, multimodalPayloadKeyIsBinary(body, tc.pos, key))
		})
	}
}

// TestEstimateRequestInputTokensUpperBound_DataKeyNeedsParentContext 是 N4 的端到端
// 回归：同一份 1.2MB 高熵 base64，放在普通 data 字段必须按稠密折算（≈1 token/字节），
// 放在已知多模态父键下才按固定 allowance（1 块）折算。
func TestEstimateRequestInputTokensUpperBound_DataKeyNeedsParentContext(t *testing.T) {
	t.Parallel()

	raw := make([]byte, 900000)
	for i := range raw {
		raw[i] = byte(i*31 + 7)
	}
	blob := base64.StdEncoding.EncodeToString(raw) // ≈1.2MB 高熵

	// 普通 data 字段：必须按稠密费率（≈1 token/字节），不得按 1600/块。
	generic := EstimateRequestInputTokensUpperBound([]byte(`{"payload":{"data":"` + blob + `"}}`))
	require.Greater(t, generic, 1_000_000,
		"普通 data 字段里的高熵 base64 必须按稠密折算（审计 N4）")

	// Anthropic source.data：仍是多模态块（1 块 × 1600）。
	anthropic := EstimateRequestInputTokensUpperBound([]byte(
		`{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + blob + `"}}]}`))
	require.Less(t, anthropic, 10_000, "Anthropic source.data 仍是多模态块")

	// Gemini inline_data.data：仍是多模态块。
	gemini := EstimateRequestInputTokensUpperBound([]byte(
		`{"parts":[{"inline_data":{"mime_type":"image/png","data":"` + blob + `"}}]}`))
	require.Less(t, gemini, 10_000, "Gemini inline_data.data 仍是多模态块")

	// 不受 N4 影响的键：image_url（Responses 字符串形态 / chat 对象形态）。
	responses := EstimateRequestInputTokensUpperBound([]byte(
		`{"input":[{"type":"input_image","image_url":"data:image/png;base64,` + blob + `"}]}`))
	require.Less(t, responses, 10_000, "Responses 字符串形态 image_url 仍是多模态块")

	chat := EstimateRequestInputTokensUpperBound([]byte(
		`{"messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + blob + `"}}]}]}`))
	require.Less(t, chat, 10_000, "chat 对象形态 image_url.url 仍是多模态块")

	// 回归保护：纯文本仍按 len/2 保守折算，不被误判为稠密。
	plain := EstimateRequestInputTokensUpperBound([]byte(
		`{"model":"gpt-4o","messages":[{"role":"user","content":"` + strings.Repeat("hello world ", 8000) + `"}]}`))
	require.Greater(t, plain, 0)
	require.Less(t, plain, 200_000, "普通英文文本不应被判为稠密")
}

func TestEstimateRequestInputTokensUpperBound_GenericURLAndFileDataStayDense(t *testing.T) {
	t.Parallel()

	raw := make([]byte, 900000)
	for i := range raw {
		raw[i] = byte(i*47 + 13)
	}
	blob := base64.StdEncoding.EncodeToString(raw)

	genericURL := EstimateRequestInputTokensUpperBound([]byte(`{"payload":{"url":"` + blob + `"}}`))
	genericFile := EstimateRequestInputTokensUpperBound([]byte(`{"payload":{"file_data":"` + blob + `"}}`))
	require.Greater(t, genericURL, 1_000_000, "通用 url 字段不能套用图片固定额度")
	require.Greater(t, genericFile, 1_000_000, "通用 file_data 字段不能套用图片固定额度")

	mediaURL := EstimateRequestInputTokensUpperBound([]byte(`{"image":{"url":"` + blob + `"}}`))
	mediaFile := EstimateRequestInputTokensUpperBound([]byte(`{"input_file":{"file_data":"` + blob + `"}}`))
	require.Less(t, mediaURL, 10_000, "媒体对象中的 url 仍应按多模态块估算")
	require.Less(t, mediaFile, 10_000, "媒体对象中的 file_data 仍应按多模态块估算")
}

// 本文件同时锁死审计 R1：`url` / `file_data` 与 `data` 一样是通用键名。
// PR#16 之前它们一律按 1 块固定额度折算（实测 ≈1627）；PR#16 改成"只有父键命中媒体
// 白名单或值前带 `;base64,` 标记才算多模态块，否则回落稠密/文本"。方向正确（堵住
// 自定义字段里的长文本被按图片低估），但**两个口径在 base64 串长约 1.6KB 处相交**：
// 以字节折算低于块额度的区间里，改动后反而比改动前更低（实测 500B 处 1627 → 695，
// −57%），也就是"堵住一个方向的低估、又打开另一个方向的低估"。
//
// 修复后的口径 = max(稠密, 1 块固定额度)：按字节折算更高就用稠密，否则用块额度，
// 因此对任一尺寸都不低于两个口径中的较大者。
func TestEstimateRequestInputTokensUpperBound_GenericMediaKeysNeverBelowBothFloors(t *testing.T) {
	t.Parallel()

	blobOf := func(n int) string {
		raw := make([]byte, n)
		for i := range raw {
			raw[i] = byte(i*47 + 13) // 伪随机高熵，确保被判为 base64 而不是低熵文本
		}
		return base64.StdEncoding.EncodeToString(raw)
	}

	// 覆盖交叉点两侧：500/512 落在 min 处，1400/2000 落在 max 处。
	for _, rawLen := range []int{500, 512, 700, 1000, 1400, 2000} {
		blob := blobOf(rawLen)
		floor := requestSpendImageTokenAllowance
		if len(blob) < floor {
			floor = len(blob) // max(稠密, 块额度) 的较小者：稠密费率是 1 token/字节
		}

		gotURL := EstimateRequestInputTokensUpperBound([]byte(`{"payload":{"url":"` + blob + `"}}`))
		require.GreaterOrEqual(t, gotURL, floor,
			"通用 url 键的估算不得低于 max(稠密, 块额度) 的较小者（rawLen=%d, blobLen=%d）", rawLen, len(blob))

		gotFile := EstimateRequestInputTokensUpperBound([]byte(`{"payload":{"file_data":"` + blob + `"}}`))
		require.GreaterOrEqual(t, gotFile, floor,
			"通用 file_data 键的估算不得低于 max(稠密, 块额度) 的较小者（rawLen=%d, blobLen=%d）", rawLen, len(blob))
	}

	// 交叉点以上必须回落稠密：串远超块额度时若仍按 1 块额度折算就是 R1 之前 PR#16 想堵的
	// 那个低估方向（同一份内容按字节折算可达 737 倍）。
	longBlob := blobOf(20000)
	require.Greater(t, len(longBlob), requestSpendImageTokenAllowance)
	require.Greater(t,
		EstimateRequestInputTokensUpperBound([]byte(`{"payload":{"url":"`+longBlob+`"}}`)),
		len(longBlob),
		"通用 url 键上的超长串必须按稠密口径（1 token/字节）折算")
	require.Greater(t,
		EstimateRequestInputTokensUpperBound([]byte(`{"payload":{"file_data":"`+longBlob+`"}}`)),
		len(longBlob),
		"通用 file_data 键上的超长串必须按稠密口径（1 token/字节）折算")

	// 确认是媒体负载的键不受 R1 修复影响：仍按 1 块固定额度。
	for _, body := range []string{
		`{"image":{"url":"` + longBlob + `"}}`,
		`{"input_image":{"url":"` + longBlob + `"}}`,
		`{"input_file":{"file_data":"` + longBlob + `"}}`,
		`{"payload":{"url":"data:image/png;base64,` + longBlob + `"}}`,
	} {
		require.Less(t,
			EstimateRequestInputTokensUpperBound([]byte(body)),
			requestSpendImageTokenAllowance*2,
			"确诊的媒体负载仍应按固定 allowance 折算，不能因 R1 修复被改成稠密")
	}
}
