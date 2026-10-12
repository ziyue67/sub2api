# 从节点管理添加静态代理

管理员可将 IP 管理中的订阅节点、动态代理添加到“静态代理 / IP 代理池”，之后在账号编辑页的代理选项中单独选择。

## 页面操作

1. 在 IP 管理的“节点”或“动态代理”页找到节点。节点需要已启用，Mihomo 内核需要正在运行。
2. 单条使用该行的“添加为静态代理”；批量时先勾选节点，再点击“批量添加为静态代理”。批量操作只处理选中的节点，不会在未选择时添加全部筛选结果。
3. 确认后切回“静态代理 / IP 代理池”。新代理默认名为 `Mihomo · 节点名称`，账号编辑页也可直接选择。
4. 节点的“批量编辑”支持恢复、停用和添加为静态代理；恢复仍受地区规则限制。

已添加的节点显示标记。重复添加复用原代理，不重复创建记录。可在静态代理页修改名称、状态和有效期，节点出口地址由系统管理。代理已被账号使用时，沿用原有删除限制。

## 固定出口的含义

每个节点拥有持久化的本地 HTTP 代理端口，Mihomo 将该端口直接指向节点，而非负载均衡或轮换组。支持 Mihomo 已导入的订阅协议，不将 Shadowsocks、VMess 等节点的服务器地址误当成 HTTP/SOCKS5 代理。

本地监听仅绑定 `127.0.0.1`，用于本实例的账号请求；复制地址到其他机器不能使用。内核停止时出口不可用。节点停用、检测失败、用后移出、被地区规则排除或从订阅/动态来源移除时，端口转为拒绝流量，不会切换到其他节点或直连。恢复同一节点后可以继续使用原端口。

**动态代理固定的是供应商代理配置，不保证公网 IP 永久不变。**供应商仍可在每次连接时更换出口，添加确认窗口会提示这一点。

## 持久化与升级

- `274_mihomo_static_proxy_bindings.sql` 为代理表新增可空的 `mihomo_node_id`，并对未删除记录建立唯一索引；普通代理原有字段和行为兼容。
- Mihomo 的私有 `settings.json` 保存节点 ID 与本地端口绑定，权限保持 `0600`。重启后恢复绑定；需要同时备份数据库和该配置目录。
- 静态节点使用本地端口 `32000` 至 `41999`，首次添加会避开已占用端口。最多保留 4096 个绑定记录，单次 API 最多处理 100 个节点，页面自动分批。
- 删除静态代理会移除监听，但保留端口预留；该端口不会重新分配给不同节点，避免旧地址悄悄改变出口。同一节点重新添加时复用预留端口。
- 导出/导入普通代理地址不能代替迁移 Mihomo 节点绑定和配置。已有安装未添加节点时不会产生这些监听。

## 验证

前端组件测试覆盖单条和批量添加、动态 IP 提示、所选节点范围、失败保留、代理列表刷新，以及固定出口字段不可修改。后端测试覆盖固定监听配置、重复添加、状态/地区限制、数据库写入失败时撤销新监听、数据库唯一性与删除后重新添加。

官方 Mihomo 内核验证使用两个本机模拟代理，检查两个节点分别转发到各自出口、重启恢复、停用拒绝以及移除监听；不访问真实供应商或调用模型。

```bash
cd backend
go test -tags=unit ./internal/mihomo ./internal/service ./internal/handler/admin ./internal/repository -run 'Static|Mihomo|Proxy' -count=1
go test -tags=integration ./internal/repository -run '^TestProxyRepoSuite/TestMihomoNodeIdentity' -count=1
MIHOMO_INSTALL_SMOKE=1 go test ./internal/mihomo -run '^TestStaticNodeBindingsWithOfficialKernel$' -count=1
# 已有本地官方内核时，用 MIHOMO_KERNEL_BINARY=/path/to/mihomo 替代下载开关。

cd ../frontend
pnpm exec vitest run src/views/admin/settings/MihomoSettings.spec.ts src/views/admin/__tests__/ProxiesView.management.spec.ts src/views/admin/__tests__/ProxiesView.credentials.spec.ts
pnpm run build
```
