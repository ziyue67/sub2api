# 入口连接元数据采集

本目录是可选运维接入，代码提交不会自动修改 Caddy/Nginx、启用服务或重启生产。先上线带迁移 294 和管理员导入接口的应用，再按实际代理版本、现有 listener 和目录权限准备候选配置，验证配置后在已获授权的部署中 reload。不要直接覆盖现有站点。

## Nginx

将 `nginx-http.conf` 合入 `http {}`，将 `nginx-server.conf` 合入每个 Sub2API HTTP/HTTPS server。检查 server 和 location 层现存 `access_log off`，它们会覆盖新配置。日志使用 `$http_host`、`$server_protocol`、`$ssl_protocol`、状态、字节和耗时，不记录原始 `$request`、`$request_uri`、参数或 headers。仅白名单公开路径保存原值，其他路径记 `/[redacted]`。

`X-Edge-Request-ID` 是入口 ID，应用成功响应的 `X-Request-ID` 单独保留以便关联；没有到应用的连接不假造应用 ID。`nginx-logrotate.conf` 是轮转示例，使用 rename + reopen、delaycompress，不能改为 copytruncate。

配置候选由部署环境的 `nginx -t` 检查后再 reload。日志支持所有经过配置 listener 的 HTTP 请求，不依赖业务鉴权或用量。Nginx access log 无法还原尚未进入 HTTP 层的 TLS/TCP 失败，模板没有声称覆盖这部分。

## Caddy

将 `caddy-site.caddy` 的内容合入各 Sub2API site；站点若已有日志配置，应合并而非重复声明。使用实际版本的 `caddy adapt --validate` 检查候选配置后再 reload。

日志编码时删除 request/response headers 和原始 URI，仅对已知公开路由添加 `public_path`，其他路径由采集器记为脱敏路径。滚动文件控制大小和数量，入口没有关闭凭据脱敏。与 Nginx 相同，入口导入接口自身不记录，避免循环采集。

如已有 JSON 格式的 Caddy runtime 日志文件，可用第二个采集器实例读取其中 `http.stdlib` 的 `TLS handshake error from ...` 消息。只提取固定错误类型、事件时间和 peer IP，原消息不发送入库；host/URL 未采集会留空。不要为了采集 TLS 打开包含正文、headers 的 debug 日志。当前示例只配置 access log，没有自动配置 runtime 文件或启用 TLS 采集实例。

## 采集服务

安装目录示例 `/opt/sub2api-connection-diagnostics/collect.py`；为 `/var/lib/sub2api-connection-collector` 创建 root 可写目录。复制 `collector.service` 和 `collector.timer`，凭据参考 `collector.env.example` 外部注入，实际 env 必须 root:root / 0600，不放 Git。每个日志源用不同 node 和 state；运行多个来源时复制为独立的 service/timer/env，而不是共用游标。

默认每 5 秒做一次有界读取，单次最多 4 MiB，单行最多 64 KiB，每批最多 50 条，通过 HTTPS 或本机回环 POST 到 `/api/v1/admin/usage/connections/edge`。固定 node + 原行哈希产生稳定 UUID，重试不重复计数。导入确认必须包含实际接受条数。拒绝携带用户身份的导入设计，只有管理员身份可调用，使用既有管理员 API Key；不在终端参数或普通日志打印凭据。HTTP 重定向被拒绝，避免转发管理员凭据。

游标有进程锁，原子落盘；请求失败保留原游标供重试。rename 轮转时先读旧 inode 再切新文件。采集器落后到旧文件已压缩/丢失或日志被截断时应告警并人工确认恢复游标，不能静默跳过。拒绝/无法解析的行只输出计数，不输出原始内容；超长行停止，不自动跳过。首次上线会从当前文件开头导入，因此先规划所需历史窗口，别在启用后才删除游标。

可选 service 限制 96 MiB、10% CPU、40 秒单次任务。源文件权限、轮转与采集状态也需验收。只安装新版应用不会凭空产生入口日志，启用前的数据缺口不能回补。

## 离线验收（默认交给 CI）

`python3 -m unittest discover -s deploy/connection-diagnostics -p 'test_collect.py'`

测试使用 mock 导入确认和临时文件，不连接生产、不发模型请求。代理配置候选的实际验证、真实 UI 截图、TLS listener 及真实导入联通验收尚待部署阶段完成。

配置字段来源：[Nginx 日志模块](https://nginx.org/en/docs/http/ngx_http_log_module.html)、[Caddy log](https://caddyserver.com/docs/caddyfile/directives/log)、[Caddy log_append](https://caddyserver.com/docs/caddyfile/directives/log_append)。
