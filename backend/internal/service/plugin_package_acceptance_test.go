package service

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"sort"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 用宿主**真实**的安装校验路径验收打包器产出的 .s2plugin。
//
// 这一步不可省：宿主对包结构极其严格（未声明文件、缺失文件、哈希不符、
// 签名不受信任、清单字段多余都会直接拒绝安装）。打包器自己实现的"自校验"
// 只是同源复读，只有跑宿主代码才算真实验收。
//
// 设置 SUB2API_TEST_PLUGIN_PACKAGE 指向包路径即可运行；未设置时跳过，
// 保证常规 go test 不受影响。
func TestRealPackageInstallsThroughHostValidator(t *testing.T) {
	packagePath := os.Getenv("SUB2API_TEST_PLUGIN_PACKAGE")
	if packagePath == "" {
		t.Skip("未设置 SUB2API_TEST_PLUGIN_PACKAGE，跳过真实包验收")
	}
	raw, err := os.ReadFile(packagePath)
	require.NoError(t, err, "读取插件包")

	// 生成一对临时发布者密钥，把公钥写进受信任列表 —— 与部署者实际
	// 配置 trusted_publishers 的流程一致。
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	keyID := "acceptance-test-publisher"

	cfg := &config.Config{}
	cfg.Plugins.AllowUnsigned = false
	cfg.Plugins.DataDir = t.TempDir()
	cfg.Plugins.MaxUploadBytes = 256 << 20
	cfg.Plugins.MaxUncompressedBytes = 256 << 20
	cfg.Plugins.TrustedPublishers = map[string]string{
		keyID: base64.StdEncoding.EncodeToString(publicKey),
	}

	hostInfo := PluginHostInfo{Version: "0.2.8", BuildType: "test"}
	installer := NewPluginPackageInstaller(cfg, hostInfo)

	// 先把包内签名替换成用我们临时密钥重签的版本，否则签名不受信任。
	resigned := resignPackage(t, raw, privateKey, keyID)
	installation, err := installer.Install(t.Context(), bytes.NewReader(resigned), nil)
	require.NoError(t, err, "宿主安装校验必须通过")

	// 校验宿主实际读出来的关键字段与包内清单一致。
	//
	// 这里刻意断言 installation.Version == installation.Manifest.Version
	// （而不是某个写死的版本号）：测试要锁的是"宿主读出的版本与清单声明一致"
	// 这个不变量，不是某个具体版本。写死版本号会让每次发版都要改测试，
	// 而且失败信息会误导成"安装坏了"。
	declared := readManifestFieldForTest(t, raw, "version")
	require.NotEmpty(t, declared, "包内清单必须声明 version")
	require.Equal(t, declared, installation.Version, "宿主读出的版本必须与清单一致")

	declaredID := readManifestFieldForTest(t, raw, "id")
	require.NotEmpty(t, declaredID, "包内清单必须声明 id")
	require.Equal(t, declaredID, installation.PluginKey, "宿主读出的插件 ID 必须与清单一致")

	require.Equal(t, PluginSignatureTrusted, installation.SignatureStatus)
	require.True(t, installation.Compatibility.Compatible, "版本范围应兼容宿主")

	// 两个能力都必须被宿主接受（安装期白名单校验）。
	require.Len(t, installation.Manifest.Capabilities, 2)
	capabilityIDs := map[string]bool{}
	for _, capability := range installation.Manifest.Capabilities {
		capabilityIDs[capability.ID] = true
	}
	require.True(t, capabilityIDs[PluginCapabilityOpenAIOAuthOutbound], "应声明出站传输能力")
	require.True(t, capabilityIDs[PluginCapabilityOpenAIAccountScheduling], "应声明调度能力")

	// 账号可见范围必须由两个能力共同授予（调度能力也需要账号目录）。
	scope := pluginAccountScopeFromManifest(installation.Manifest)
	require.False(t, scope.Empty(), "调度能力应授予账号目录访问范围")

	// 当前平台的运行时二进制必须存在且可执行。
	require.FileExists(t, installation.BinaryPath)
	info, err := os.Stat(installation.BinaryPath)
	require.NoError(t, err)
	require.NotZero(t, info.Size(), "运行时二进制不能为空")
}

// resignPackage 用给定私钥重新签名包内的 manifest.json。
//
// 打包器产出的包是用开发机私钥签的，验收环境没有对应公钥。这里解包、
// 重签、再打包，从而在"包结构完全不变"的前提下换成可信签名 —— 既验证了
// 真实校验路径，又不需要把私钥带进测试环境。
func resignPackage(t *testing.T, archive []byte, privateKey ed25519.PrivateKey, keyID string) []byte {
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
			// 丢弃旧签名，稍后替换。
			continue
		case "manifest.json":
			manifestRaw = content
		}
		entries[file.Name] = content
	}
	require.NotEmpty(t, manifestRaw, "包内必须含 manifest.json")

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

// readManifestFieldForTest 从 .s2plugin 包内读取清单顶层字符串字段。
//
// 测试用它做"宿主读出的值"与"清单声明的值"的一致性断言，避免把具体
// 版本号/插件 ID 写死在测试里 —— 那样每次发版都要改测试，且失败信息
// 会误导成"安装坏了"。
func readManifestFieldForTest(t *testing.T, archive []byte, field string) string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	require.NoError(t, err)
	for _, file := range reader.File {
		if file.Name != "manifest.json" {
			continue
		}
		handle, err := file.Open()
		require.NoError(t, err)
		content, err := io.ReadAll(handle)
		require.NoError(t, handle.Close())
		require.NoError(t, err)
		var probe map[string]any
		require.NoError(t, json.Unmarshal(content, &probe), "包内清单必须是合法 JSON")
		value, ok := probe[field].(string)
		require.True(t, ok, "包内清单字段 %s 必须是字符串", field)
		return value
	}
	t.Fatalf("包内缺少 manifest.json")
	return ""
}
