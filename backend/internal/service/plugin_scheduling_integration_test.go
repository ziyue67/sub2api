package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/stretchr/testify/require"
)

// 进程级端到端验收：把打包产出的 .s2plugin 真正拉起来，验证两条能力都能工作。
//
// 这是唯一能证明"插件契约实现正确"的测试 —— 单元测试只能证明逻辑正确，
// 无法证明 go-plugin 握手、gRPC 帧序、配置协商这些跨进程契约真的对得上。
//
// 设置 SUB2API_TEST_PLUGIN_PACKAGE 指向包路径即可运行。
func TestCPAAdvancedCoreProcessIntegration(t *testing.T) {
	packagePath := os.Getenv("SUB2API_TEST_PLUGIN_PACKAGE")
	if packagePath == "" {
		t.Skip("未设置 SUB2API_TEST_PLUGIN_PACKAGE，跳过插件进程集成测试")
	}
	raw, err := os.ReadFile(packagePath)
	require.NoError(t, err)

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	keyID := "process-integration-publisher"

	root := t.TempDir()
	cfg := testPluginConfig(root, false)
	cfg.Plugins.TrustedPublishers = map[string]string{
		keyID: base64.StdEncoding.EncodeToString(publicKey),
	}
	cfg.Plugins.StartTimeoutSeconds = 15
	installer := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.2.8", BuildType: "release"})
	installation, err := installer.Install(t.Context(), bytes.NewReader(resignForTest(t, raw, privateKey, keyID)), nil)
	require.NoError(t, err, "安装必须通过")

	// 拉起真实插件子进程 —— go-plugin 握手、gRPC 注册都在这里发生。
	runtime, err := startPluginRuntime(
		t.Context(), installation, 15*time.Second,
		filepath.Join(root, "runtime"), nil,
	)
	require.NoError(t, err, "插件进程必须能启动并通过身份校验")
	defer runtime.kill()

	// 1) 身份与契约版本必须与清单一致（startPluginRuntime 已内部校验，
	//    这里再读一次确认 capabilities 声明完整）。
	info, err := runtime.api.GetInfo(t.Context(), &pluginv1.GetInfoRequest{})
	require.NoError(t, err)
	require.Equal(t, "ziyue67.cpa-advanced-core", info.GetPluginId())
	require.Contains(t, info.GetCapabilities(), PluginCapabilityOpenAIOAuthOutbound)
	require.Contains(t, info.GetCapabilities(), PluginCapabilityOpenAIAccountScheduling)
	require.EqualValues(t, pluginv1.SchedulingAPIVersion, info.GetSchedulingApiVersion())

	// 2) 配置协商：空对象必须被规范化成一份完整配置。
	normalized, err := runtime.validateAndApplyNormalizedConfig(t.Context(), []byte(`{}`))
	require.NoError(t, err, "空配置必须被接受并规范化")
	var applied map[string]any
	require.NoError(t, json.Unmarshal(normalized, &applied))
	require.Contains(t, applied, "affinity_enabled")
	require.Contains(t, applied, "max_concurrency_per_auth")

	// 非法配置必须被拒绝（未知字段），且不能影响已生效的配置。
	_, err = runtime.validateAndApplyNormalizedConfig(t.Context(), []byte(`{"unknown_field":1}`))
	require.Error(t, err, "含未知字段的配置必须被拒绝")

	// 3) 调度能力：提名最低负载账号。
	nomination, err := runtime.api.NominateAccount(t.Context(), &pluginv1.NominateAccountRequest{
		RequestId:   "itest-1",
		Platform:    "openai",
		SessionHash: "integration-session",
		Candidates: []*pluginv1.ScheduleCandidate{
			{AccountId: 101, LoadRate: 0.9, InFlight: 9, MaxConcurrency: 10},
			{AccountId: 202, LoadRate: 0.1, InFlight: 1, MaxConcurrency: 10},
		},
	})
	require.NoError(t, err)
	require.True(t, nomination.GetHandled(), "应给出提名")
	require.Equal(t, int64(202), nomination.GetNominatedAccountId(), "应提名最低负载账号")

	// 同一会话再问一次：必须命中亲和，即使负载已经反转。
	nomination2, err := runtime.api.NominateAccount(t.Context(), &pluginv1.NominateAccountRequest{
		RequestId:   "itest-2",
		Platform:    "openai",
		SessionHash: "integration-session",
		Candidates: []*pluginv1.ScheduleCandidate{
			{AccountId: 101, LoadRate: 0.1, InFlight: 1, MaxConcurrency: 10},
			{AccountId: 202, LoadRate: 0.9, InFlight: 9, MaxConcurrency: 10},
		},
	})
	require.NoError(t, err)
	require.Equal(t, int64(202), nomination2.GetNominatedAccountId(), "同一会话应粘在亲和账号上")
	require.True(t, nomination2.GetAffinityHit())

	// 4) 出站传输能力：真实 HTTP 往返。
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(request.Body)
		require.NoError(t, readErr)
		require.Equal(t, "payload-body", string(body))
		// Host 头必须被还原（上游常按它做路由）。
		require.Equal(t, "upstream.test", request.Host)
		writer.Header().Set("X-Upstream-Marker", "ok")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("upstream-response-body"))
	}))
	defer upstream.Close()

	statusCode, responseBody, responseHeader, err := forwardViaPlugin(t, runtime, upstream.URL, "payload-body")
	require.NoError(t, err, "转发必须成功")
	require.Equal(t, http.StatusOK, statusCode)
	require.Equal(t, "upstream-response-body", responseBody)
	require.Equal(t, "ok", responseHeader.Get("X-Upstream-Marker"))

	// 5) 上游返回 429 后，插件必须把该账号列入避让并解除其亲和。
	flaky := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":"rate limit exceeded"}`))
	}))
	defer flaky.Close()

	// 先让账号 202 建立亲和。
	_, _, _, err = forwardViaPluginForAccount(t, runtime, flaky.URL, "x", 202)
	require.NoError(t, err)

	// 再让账号 202 吃到 429。
	statusCode, _, _, err = forwardViaPluginForAccount(t, runtime, flaky.URL, "x", 202)
	require.NoError(t, err, "429 是合法 HTTP 响应，不是传输错误")
	require.Equal(t, http.StatusTooManyRequests, statusCode)

	// 此时同一会话不应再被引向 202：插件已把它标记为避让中。
	nomination3, err := runtime.api.NominateAccount(t.Context(), &pluginv1.NominateAccountRequest{
		RequestId:   "itest-3",
		Platform:    "openai",
		SessionHash: "integration-session",
		Candidates: []*pluginv1.ScheduleCandidate{
			{AccountId: 202, LoadRate: 0.1, InFlight: 1, MaxConcurrency: 10},
			{AccountId: 101, LoadRate: 0.5, InFlight: 5, MaxConcurrency: 10},
		},
	})
	require.NoError(t, err)
	require.Equal(t, int64(101), nomination3.GetNominatedAccountId(),
		"账号吃到 429 后必须改提名健康账号，并解除其亲和")

	// 6) 健康检查必须快速返回且带状态快照。
	health, err := runtime.api.Health(t.Context(), &pluginv1.HealthRequest{})
	require.NoError(t, err)
	require.True(t, health.GetHealthy())
	require.NotEmpty(t, health.GetStatusJson(), "应返回状态快照供配置 UI 展示")
}

// forwardViaPlugin 通过插件 Forward 流发出一次请求（账号 1）。
func forwardViaPlugin(t *testing.T, runtime *pluginRuntime, targetURL, body string) (int, string, http.Header, error) {
	t.Helper()
	return forwardViaPluginForAccount(t, runtime, targetURL, body, 1)
}

// forwardViaPluginForAccount 按指定账号发出一次插件转发。
//
// 完整走一遍宿主→插件→上游→插件的帧序：start → body_chunk → body_end，
// 响应侧 start → body_chunk* → end。
func forwardViaPluginForAccount(t *testing.T, runtime *pluginRuntime, targetURL, body string, accountID int64) (int, string, http.Header, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	stream, err := runtime.api.Forward(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_Start{
		Start: &pluginv1.ForwardRequestStart{
			RequestId:     "itest-forward",
			Method:        http.MethodPost,
			Url:           targetURL,
			Host:          "upstream.test",
			Headers:       map[string]*pluginv1.HeaderValues{"Content-Type": {Values: []string{"text/plain"}}},
			AccountId:     accountID,
			Platform:      "openai",
			AccountType:   "oauth",
			ContentLength: int64(len(body)),
			HasBody:       true,
		},
	}}))
	require.NoError(t, stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyChunk{BodyChunk: []byte(body)}}))
	require.NoError(t, stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyEnd{BodyEnd: true}}))
	require.NoError(t, stream.CloseSend())

	first, err := stream.Recv()
	if err != nil {
		return 0, "", nil, err
	}
	if frameError := first.GetError(); frameError != nil {
		return 0, "", nil, &PluginTransportError{
			Code:        frameError.GetCode(),
			Message:     frameError.GetMessage(),
			RequestSent: frameError.GetRequestSent(),
		}
	}
	start := first.GetStart()
	require.NotNil(t, start, "首个响应帧必须是 start")

	var received bytes.Buffer
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, "", nil, err
		}
		if chunk := frame.GetBodyChunk(); len(chunk) > 0 {
			received.Write(chunk)
			continue
		}
		if frame.GetEnd() != nil {
			break
		}
		if frameError := frame.GetError(); frameError != nil {
			return 0, "", nil, &PluginTransportError{
				Code:    frameError.GetCode(),
				Message: frameError.GetMessage(),
			}
		}
	}
	return int(start.GetStatusCode()), received.String(), headersFromProtoForTest(start.GetHeaders()), nil
}

func headersFromProtoForTest(headers map[string]*pluginv1.HeaderValues) http.Header {
	out := make(http.Header, len(headers))
	for key, values := range headers {
		if values != nil {
			out[key] = append([]string(nil), values.GetValues()...)
		}
	}
	return out
}

// testPluginConfig 由 plugin_package_test.go 提供（同包复用），此处不再重复声明。

// resignForTest 用测试私钥重签包内清单，使验收环境无需携带开发机私钥。
func resignForTest(t *testing.T, archive []byte, privateKey ed25519.PrivateKey, keyID string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	require.NoError(t, err)

	var manifestRaw []byte
	entries := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		handle, err := file.Open()
		require.NoError(t, err)
		content, err := io.ReadAll(handle)
		require.NoError(t, handle.Close())
		require.NoError(t, err)
		switch file.Name {
		case "signature.json":
			continue
		case "manifest.json":
			manifestRaw = content
		}
		entries[file.Name] = content
	}
	require.NotEmpty(t, manifestRaw)

	signed := ed25519.Sign(privateKey, manifestRaw)
	entries["signature.json"] = []byte(`{"algorithm":"ed25519","key_id":"` + keyID +
		`","signature":"` + base64.StdEncoding.EncodeToString(signed) + `"}`)

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write(entries[name])
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}
