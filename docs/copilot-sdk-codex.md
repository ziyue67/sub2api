# Copilot SDK：账号在哪里配置，怎样接入 Codex

适用于 Sub2API **v2.8.9 的 Copilot SDK 账号模式**。本教程及 [配套脚本](../deploy/copilot-sdk/) 均在公开仓库，不需要访问运维私库。配套脚本在 v2.8.9 发布后补充到公开仓库；v2.8.9 安装包本身没有包含这些脚本，需按下面步骤另行获取。

## 先分清：谁要配置什么

**v2.8.9 后台没有“导入 GitHub 账号”或“GitHub OAuth 登录”按钮。** `Copilot SDK` 复选框只切换转发协议，不能完成 GitHub 授权。

- **站点管理员**：在服务器安装 sidecar（独立适配服务），运行设备登录脚本，用有 Copilot 权限的 GitHub 账号授权；随后在 Sub2API 后台添加 OpenAI API Key 账号，指向该服务。
- **普通使用者**：不需要 GitHub Token，不需要运行 sidecar。向管理员获取站点地址、Copilot 分组的 Sub2API API Key 和可用模型名，按本文最后的 Codex 配置连接即可。

```text
使用者的 Codex → 站点 Sub2API → 管理员部署的 sidecar → GitHub Copilot
```

## 三种凭据填哪里

| 凭据 | 如何取得 | 放在哪里 | 给普通使用者吗 |
| --- | --- | --- | --- |
| GitHub OAuth Token | 在浏览器完成设备码授权后，脚本自动取得 | 服务器 `/var/lib/sub2api-copilot/access-token` | 不给，也不填到后台 API Key 字段 |
| sidecar API Key | 登录脚本首次执行时生成 | 服务器 `sidecar-key` 文件；复制到后台上游账号的 API Key 字段 | 不给 |
| Sub2API API Key | 在站点的 API Key 页面创建，选择专用 Copilot 分组 | 使用者电脑的 `SUB2API_API_KEY` 环境变量 | 给对应使用者 |

另有 `signing-key`，用于工具续接签名，只保存在 sidecar 服务器。不要复制到任何客户端；更换它会使已有工具调用 ID 失效。

## 管理员：1. 安装 sidecar 并登录 GitHub

下面示例适用于 **Linux + systemd，Sub2API 二进制与 sidecar 在同一主机运行**。需要 git、Python 3.11+、venv/pip，以及访问 GitHub 和 Copilot 的网络。命令需要 sudo 权限；路径是示例，修改路径时应同步修改 unit 和环境文件。

Docker 用户先阅读后面的网络说明，不要直接把容器内的 `127.0.0.1` 当宿主机。

### 获取公开脚本和固定版本适配器

```bash
sudo git clone --branch main --single-branch \
  https://github.com/ziyue67/sub2api.git /opt/sub2api-public

sudo mkdir -p /opt/sub2api-copilot
sudo bash /opt/sub2api-public/deploy/copilot-sdk/prepare.sh \
  /opt/sub2api-copilot/adapter
```

如果 `/opt/sub2api-public` 已存在，不要重复 clone 或覆盖本地改动，应在干净 checkout 更新到包含本教程的 main 提交。准备脚本固定 ghcp_proxy 提交 `ad23ce2db3b5212c0355762d981c3877322fb160` 与 `github-copilot-sdk==1.0.14`，不会启动服务。

### 创建服务用户、授权 GitHub

```bash
id sub2api-copilot >/dev/null 2>&1 || \
  sudo useradd --system --home-dir /var/lib/sub2api-copilot \
    --shell /usr/sbin/nologin sub2api-copilot

sudo install -d -o sub2api-copilot -g sub2api-copilot -m 0700 \
  /var/lib/sub2api-copilot

sudo chown -R sub2api-copilot:sub2api-copilot /opt/sub2api-copilot/adapter

sudo -u sub2api-copilot /opt/sub2api-copilot/adapter/.venv/bin/python \
  /opt/sub2api-public/deploy/copilot-sdk/login.py \
  --state-dir /var/lib/sub2api-copilot
```

终端会显示 GitHub 设备登录网址和一次性设备码。在浏览器打开该网址，登录你拥有且有 Copilot 权限的 GitHub 账号，输入设备码并授权。看到 `Authorized. Credential stored privately` 后，登录完成。不要把设备码授权给不认识的请求者，也不需要把 GitHub 密码输入服务器终端。

脚本在上述状态目录保存 `access-token`、`sidecar-key` 和 `signing-key`。重跑不会覆盖已有两个 sidecar 密钥；重新登录会更新 GitHub Token。普通用于仓库操作的 PAT 不等于可用的 Copilot 登录凭据，不要用任意 GitHub Token 替代设备登录。

### 安装配置并启动

```bash
sudo install -d -m 0755 /etc/sub2api
sudo install -m 0600 /opt/sub2api-public/deploy/copilot-sdk/sidecar.env.example \
  /etc/sub2api/copilot-sidecar.env
sudo install -m 0644 /opt/sub2api-public/deploy/copilot-sdk/copilot-sdk-sidecar.service \
  /etc/systemd/system/copilot-sdk-sidecar.service
sudo systemctl daemon-reload
sudo systemctl enable --now copilot-sdk-sidecar.service
sudo systemctl status --no-pager copilot-sdk-sidecar.service
curl --fail http://127.0.0.1:18765/healthz
```

`healthz` 返回 `{"status":"ok"}` 仅表示进程启动成功。模型列表检查才会调用 SDK；runtime 首次使用可能要下载文件。

用以下命令读取真实模型列表。它从文件读 sidecar Key，不把 Key 写入命令参数或打印出来。此处 client ID `1` 仅用于管理员本机诊断；正常转发时由 Sub2API 覆盖为实际客户端 Key ID。

```bash
sudo -u sub2api-copilot /opt/sub2api-copilot/adapter/.venv/bin/python - <<'PY'
from pathlib import Path
import httpx
key = Path('/var/lib/sub2api-copilot/sidecar-key').read_text().strip()
r = httpx.get('http://127.0.0.1:18765/v1/models', headers={
    'Authorization': 'Bearer ' + key,
    'X-Sub2API-Client-ID': '1',
}, timeout=120)
print('HTTP', r.status_code)
if r.is_success:
    print('\n'.join(item['id'] for item in r.json().get('data', [])))
else:
    print('模型查询失败，请检查服务日志和 GitHub 登录权限。')
PY
```

使用返回的模型名；只有列表实际包含 `auto` 时才填写 `auto`。不要根据其他账号、模型别名或 GitHub 网页展示猜测接口可用模型。

## 管理员：2. 在 Sub2API 后台添加账号

先确认**运行中的服务和前端已升级到 v2.8.9 或包含此功能的版本**。仅下载新包或 GitHub 发版不会自动升级正在运行的实例。

1. 打开管理后台 → **分组管理**（`/admin/groups`），新建独立 OpenAI 分组，例如 `Copilot`。首版该组只绑定一个 sidecar 账号，不配置其他账号或 fallback 分组。
2. 打开 **账号管理**（`/admin/accounts`）→ 添加账号，平台选 **OpenAI**，账号类型选 **API Key**。已有 OpenAI API Key 账号也可在编辑弹窗中配置。
3. 按下表填写并保存。`Copilot SDK` 复选框在账号弹窗下方、自动透传开关之前；选 OAuth 时不会出现。

| 后台字段 | 填写值 |
| --- | --- |
| 名称 | 自定，例如 `Copilot-个人账号` |
| 平台 / 类型 | `OpenAI` / `API Key` |
| Base URL | 同主机二进制部署填 `http://127.0.0.1:18765/v1`；不要留空，也不要填 GitHub 地址 |
| API Key | 服务器 `/var/lib/sub2api-copilot/sidecar-key` 的完整内容 |
| Copilot SDK | 勾选 |
| 分组 | 上一步新建的 `Copilot` 分组 |
| WebSocket Mode | 保持关闭；客户端到 sidecar 使用 HTTP/SSE |
| Responses 模式 | 若显示该选项，选择 Responses，避免通用 Chat Completions 回退 |
| 模型配置 | 使用前一步查询到的真实模型名，不依赖普通账号模型别名替换 |

管理员可通过自己的受限终端/文件读取方式查看 `sidecar-key` 并填入后台；不要将其贴到工单、截图或普通日志。当前表单可能仍显示“您的 OpenAI API Key”这一通用提示；本模式这里填 **sidecar-key**，不是 OpenAI Key、GitHub Token，也不是登录设备码。

开启 Copilot SDK 后，后台会自动按该模式转发；不必另外勾选“自动透传”。默认关闭此功能，不能只填 Key 而漏掉 Copilot SDK 开关。

上游 URL 安全策略必须允许这个受信任的本机 HTTP 地址。若提示禁止 HTTP/私有地址，应核实 `security.url_allowlist.allow_insecure_http`、`allow_private_hosts` 和 upstream host allowlist；不要为了接入而全局取消 URL 校验。

## 管理员 / 使用者：3. 创建分组 API Key

在站点的 **API Key** 页面（`/keys`）创建一个 Key，分组选 `Copilot`。如果使用者看不到该组，先由管理员检查分组可用性、用户权限/订阅和站点模式；不要让使用者拿 sidecar Key 代替。

给使用者这三项即可：

- 站点的 API Base URL，例如 `https://api.example.com/v1`；
- 绑定 Copilot 分组的 **Sub2API API Key**；
- 该账号实际可用的模型名。

配置分组计价后再开放付费调用。`auto` 不保证固定到某个模型；不要承诺特定上下文长度、模型能力或按未验证的模型价格收费。

## 使用者：4. 配置 Codex

编辑用户级 `~/.codex/config.toml`；Windows 对应 `%USERPROFILE%\.codex\config.toml`。保留原文件其他设置，将下面的 provider 与模型配置合并进去，避免同名键重复。

```toml
model_provider = "sub2api_copilot"
model = "auto" # 换成管理员给的真实模型名

[model_providers.sub2api_copilot]
name = "Sub2API Copilot"
base_url = "https://api.example.com/v1" # 换成你的站点地址
wire_api = "responses"
env_key = "SUB2API_API_KEY"
requires_openai_auth = false
supports_websockets = false
```

不要把 `base_url` 填成自己电脑的 `127.0.0.1:18765`，除非整套服务确实部署在该电脑上。provider 配置应放用户级文件，参考 [Codex 官方配置说明](https://developers.openai.com/codex/config-reference/)。

Linux/macOS Bash 中可临时输入 Key（不回显、不把真实值写进 shell 历史）：

```bash
read -rsp 'Sub2API API Key: ' SUB2API_API_KEY
printf '\n'
export SUB2API_API_KEY
codex
```

Windows PowerShell 中可临时输入 Key：

```powershell
$copilotKey = Read-Host 'Sub2API API Key' -AsSecureString
$env:SUB2API_API_KEY = [System.Net.NetworkCredential]::new('', $copilotKey).Password
Remove-Variable copilotKey
codex
```

环境变量只影响当前终端及其启动的进程。使用 IDE 扩展时也要确保该 IDE 进程可读取变量，再重新打开扩展/会话。更换 provider、分组 Key 或 sidecar 后新开 Codex 会话，不要续用另一条上游的工具历史。

## 5. 首次验收与常见问题

先在临时测试目录让 Codex 执行“读取文件 → apply_patch 修改 → 运行测试 → 继续上一轮任务”。检查 Sub2API 请求日志中使用的是预期分组/账号。普通文本回复成功不能证明工具回填和会话续接均正常。

| 现象 | 检查位置 |
| --- | --- |
| 后台找不到 GitHub 登录/导入按钮 | v2.8.9 没有该按钮，使用服务器 `login.py` 完成授权 |
| 看不到 Copilot SDK 复选框 | 检查运行版本、前端缓存；平台选 OpenAI、类型选 API Key |
| 连接拒绝 / 502 | sidecar 是否启动、Base URL 是否可达、Docker 的网络命名空间是否正确；查看 `journalctl -u copilot-sdk-sidecar.service` |
| 401 | 先判断来自 Sub2API 还是 sidecar；客户端填 Sub2API Key，上游账号填 sidecar Key；检查 GitHub 登录是否有效 |
| `A trusted Sub2API client identity is required` | 检查运行的 Sub2API 是否包含此功能、账号是否勾选 Copilot SDK；不要给普通用户开放 sidecar 端口 |
| 409 / 工具结果无法续接 | 会话属于别的实例、Key 或线程，或 pending 回合已丢失；检查固定路由，新开 Codex 会话；不要反复切账号重试 |
| 模型不存在 | 用 sidecar `/v1/models` 的结果核实；不要把模型改名当作能力转换 |
| 看图失败 | 当前固定适配器把图片转成占位符，本路径尚不支持完整视觉任务 |

### Docker 网络说明

本教程的 systemd sidecar 只监听宿主机 loopback。Sub2API 在默认 Docker bridge 中时，`127.0.0.1` 指向 Sub2API 容器，不能访问宿主机 loopback；仅改成 `host.docker.internal` 也无法访问只绑定宿主机 loopback 的服务。

首版最直接的拓扑是 Sub2API 二进制与 sidecar 同主机，或由管理员明确配置共享网络命名空间。采用 Linux host networking 前，应先评估现有数据库/Redis 地址、端口冲突和暴露范围；不要为了本功能直接改动现有 Compose。需要跨主机/容器网络时，由管理员提供受限、鉴权的内部入口并验证网络连通性，不能把本机示例当作容器通用安装命令。

## 当前支持边界

首版为**单分组、单 sidecar 账号、单进程**；未实现后台一键 GitHub 登录、多账号导入池、跨实例迁移或 pending 工具回合的重启恢复。sidecar 的脚本和运行环境是独立组件，不由 Sub2API 的自动更新管理。只使用自己有权使用的 GitHub 账号。
