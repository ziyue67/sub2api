# 独立 BPS 模型发现

BPS 账号的实时模型发现使用 `GET /basispoints/api/responses/access?include_models=true`，按 Excel 授权读取 allowed、restricted_models、模型可用性/策略、label 与 efforts。新的 GPT 模型无需更新静态名单。账号测试选择器、实时模型列表、固定账号/上游 Codex manifest 和管理端模型同步复用此来源。

只为启用 BPS 的模型路由使用 BPS 目录；部分模型仍走原生时保留其独立原生目录。全模型 BPS 不需要原生目录成功。混合账号的原生发现失败时只公布已取得的 BPS 结果；BPS access 失败则返回发现错误，不能拿原生目录冒充 BPS 权限。HTTP 错误不会改变业务账号状态。

权限结果在内存中按账号、bearer、代理配置隔离，沿用有界模型缓存，60 秒内复用；到期同步刷新，不返回陈旧权限。刷新有 5 秒上限、1 MiB 响应上限和 singleflight；受 API key 准入限制的调用自行持有并等待刷新。凭据只用于出站请求和缓存键摘要，不进入目录或错误内容。管理界面发现失败不再对 BPS 账号静默补默认模型。

账号映射与分组限制继续在目录投影层生效；分组显式本地配置目录仍由管理员配置生成，不以自动发现覆盖它。需要实时上游权限的客户端目录可使用固定账号/上游发现入口。没有写入模型映射、修改分组或自动开启新模型。

上游 efforts 在实时 Codex manifest 和管理模型列表中保留；BPS 仍不声明加密多代理 v2。已配置但无 BPS 权限的模型不会被实时目录补回。allowed 只代表访问目录声明，不保证某次生成的实际模型。

无数据库迁移。额外开销是按需只读 GET，缓存共享；没有生成请求。验证为 mock 权限过滤、未知模型、授权轮换、缓存失效/403、别名和混合路由、现有模型发现回归。真实账号与客户端验收尚未执行。

行为参考：[Nonary/ghcp_proxy 8beac45](https://github.com/Nonary/ghcp_proxy/commit/8beac45)、[f89ccfc 的目录实现](https://github.com/Nonary/ghcp_proxy/blob/f89ccfcea4497c4e60be891119a389a3cb68b050/excel_model_catalog.py)。独立 Go 实现。关联 #357 第 4 项。
