# 管理员生图留档

在现有 Sub2API 的 Images 转发链路中采集图片，管理员从「生图记录」查看。复用现有服务器和 PostgreSQL，不需要新增生图中转服务或更换上游地址。

## 使用

1. 部署包含此功能的版本。启动时执行新增迁移 `300_image_request_archive.sql`，创建独立留档表。
2. 管理员打开「生图记录 → 留档设置」。默认关闭，选择要采集的分组或上游账号后开启。
3. 账号和分组采用“或”关系，任一命中即采集；范围为空时不采集。其他服务进程最多 15 秒同步配置。
4. 默认保存 30 天、文件容量上限 20 GiB。修改保留天数只影响新记录。过期数据启动时及每小时清理。
5. 在图库中按客户 ID、分组、账号 ID、请求模型或时间筛选，打开记录查看原图、参考图、mask、提示词和参数。

只保存启用后的新请求。此前仅有 usage_logs 的请求无法从计费记录恢复图片。

## 保存内容与兼容范围

- OpenAI/API Key 和 OAuth 的 `/v1/images/generations`、`/v1/images/edits`；Grok 独立 Images 入口。
- 非流式 `data[].url` / `data[].b64_json`，以及 Images SSE 最终结果；不存流式局部预览。
- multipart 图片和 mask、JSON 编辑参考图、Seedream `reference_images`。
- 原始结果文件及 SHA256、图片尺寸、缩略图、提示词、上游修订提示词、常用生成参数。
- 从认证上下文关联客户、API Key、分组和实际选中的上游账号，记录请求模型、上游模型与上游请求 ID。
- 图库和图片文件均位于管理员认证边界内。浏览器通过带认证的 API 获取 Blob，不开放原图静态 URL。

第一版不采集聊天中的图片工具、Gemini 原生生图或视频，不提供客户生图页面。

## 保存与失败处理

每个实际转发尝试拥有独立的采集对象。未出图的失败尝试释放资源；已有最终图片的部分成功请求仍可留档。失败切换不会把前一个账号的图片关联到后一个账号。

请求线程只做有界的数据复制和入队，后台下载、生成缩略图并写入磁盘与数据库。采集或保存失败不会改写客户响应、重新请求上游或触发额外计费。已有的上游排空流程在客户断开后继续采集最终图。

留档为尽力保存：队列满、内存不足、异常重启或下载链接失效时，部分图片可能没有留档。页面展示最近的留档异常，以及当前进程的处理中、异常和丢弃计数；这些计数会在进程重启后归零。已写入的记录及状态持久化在数据库中。

## 存储边界

- 文件目录：相对于服务工作目录的 `data/image-archive`；生产部署必须持久化此目录。
- 容量统计包含原图、参考图、mask 和缩略图。使用 PostgreSQL advisory lock 串行化磁盘写入和清理，避免多个进程同时突破上限。
- 单图最多 32 MiB，单次采集最多 64 MiB，所有在途和排队采集总预算 128 MiB，最多 8 个在途/排队任务。
- 处理时优先保存结果图。每次写入重新检查容量和磁盘余量，至少预留 2 GiB；达到上限时跳过保存，客户转发继续。
- 支持 PNG/JPEG/WebP/GIF；原图保持原始字节。超过 2000 万像素时保留原图并跳过缩略图，降低解码内存。
- URL 下载仅接受无用户名密码、443 端口的公网 HTTPS；每次连接和跳转校验目标地址，拒绝私网、元数据地址和 DNS 重绑定，不携带客户或上游 API 密钥。失败展示为留档异常。
- 正常过期清理删除文件后再删除记录。崩溃留下的无记录目录，满 24 小时后由清理任务回收。
- 第一版适合单个网关实例，或所有相关实例共享同一个持久化目录的部署。独立磁盘的多机部署需要先接对象存储。

## 管理 API

全部接口复用现有 `/api/v1/admin` 认证、审计及权限检查。

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/image-archive` | 分页查询，支持 `user_id/group_id/account_id/model/from/to` |
| GET | `/image-archive/stats` | 配置、容量和当前进程状态 |
| PUT | `/image-archive/config` | 保存开关、范围、保留天数和容量 |
| GET | `/image-archive/:id` | 记录详情 |
| GET | `/image-archive/:id/files/:file` | 鉴权读取原图；`thumbnail=true` 读取缩略图 |

时间筛选使用 RFC3339。模型筛选采用请求模型精确匹配。API 返回文件 ID，不暴露磁盘路径。

## 验证

专项测试覆盖按范围采集、失败尝试隔离、真实身份关联、multipart 参考图/mask、Seedream 参考图、SSE 最终图及断开连接、下载失败、容量及磁盘不足、数据库写入失败、关闭采集、过期/残留清理、路径限制、下载地址限制、SQL 参数化和分页，以及管理员文件/元数据权限。

前端测试覆盖鉴权 Blob 加载、切换图片时取消旧请求、Object URL 释放和图片错误状态。Playwright 使用本地样例 API 验证图库、详情、设置保存及 390px 布局；截图不含真实客户数据。

```powershell
# 本机全局 GOOS 为 linux；执行 Windows 测试时显式覆盖。
$env:GOOS='windows'
$env:GOTOOLCHAIN='auto'
go test ./internal/service ./internal/repository ./internal/server/routes -run 'Test(ImageArchive|OpenAIImages|CodexDirectImages|ForwardGrokMedia)' -count=1

# frontend 目录
pnpm exec vitest run src/components/admin/__tests__/ImageArchiveAsset.spec.ts
pnpm run build
```

SQL repository 测试使用 sqlmock；本次开发没有在生产数据库执行迁移，也没有用真实客户流量验收。
