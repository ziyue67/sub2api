# 凭证运营：前端启用凭据加密

管理员进入“凭证运营”，或“账号管理 → 添加账户 → 2FA 登录导入”，在“凭据加密”卡片点击“一键启用”。服务器生成并保存 AES-256-GCM 专用密钥，成功后立即允许保存或导入，不需要手工填写环境变量或重启服务。页面打开只检查状态，不会自动创建密钥。

账号表单中的“账号 2FA（TOTP）密钥”是账号登录所需的密钥：未开启账号 2FA 时可留空，已开启时应填写。它与服务器保存凭据用的加密密钥不同。自动巡检和自动重登仍按各账号开关执行；首次 2FA 登录仍使用原有登录服务，后续重登仍需要本地 Worker。本功能不安装或配置 Worker。

## 存储与已有部署

- 已有固定 `TOTP_ENCRYPTION_KEY` 或 `totp.encryption_key` 的部署继续沿用原加密器，不生成、替换或公开该密钥。
- 未配置固定密钥时，只为 OpenAI 重登密码、账号 TOTP 和 OTP URL 启用专用文件密钥；不修改全局用户 TOTP、支付或其他功能的加密设置。
- 文件位置依次使用 `DATA_DIR/secrets/credential-operations.key`、存在的 `/app/data/secrets/credential-operations.key`，否则使用进程工作目录下的 `data/secrets/credential-operations.key`。目录需允许服务用户写入，容器需将数据目录挂到持久卷。
- 密钥文件权限为 `0600`；先写完整临时文件并同步，再原子发布，重复或并发初始化不会覆盖已有文件。API 只返回是否就绪和来源，不返回密钥。
- 密钥损坏、权限过宽或已有加密凭据而密钥缺失时拒绝生成替代密钥，需恢复原文件或原服务器固定密钥。
- 文件与数据库应同时备份、恢复。多实例必须共享持久密钥文件，不能各自生成不同密钥。不能只备份数据库后丢弃密钥。

新密文使用 `credential-v1:` 前缀，旧的服务器密钥密文保持原格式；恢复固定服务器密钥后仍可解密两种来源的记录。旧版本不识别新前缀，启用文件密钥并保存凭据后，二进制降级不能替代凭据兼容性处理，不得删除或重新生成密钥来尝试恢复。

## 接口与导入行为

仅管理员认证路由开放：

- `GET /api/v1/admin/account-ops/token-guard-v2/encryption`：检查状态。
- `POST /api/v1/admin/account-ops/token-guard-v2/encryption/initialize`：首次启用，幂等。

两个接口均返回 `Cache-Control: no-store`。前端在状态未知或未启用时禁用保存、2FA 导入；后端在面向凭证运营的 2FA 登录任务开始前再次校验，避免缺失加密配置时先登录、建号，最后登记失败。既有旧守护客户端保持原约定。

## 界面验证

以下截图来自本地浏览器与模拟 API，账号数据为虚构值，不代表生产部署状态。验证了未启用时禁止添加、一键启用后可继续添加，以及账号 2FA 输入说明。

- [未启用凭据加密](screenshots/credential-encryption/unconfigured.png)
- [启用后允许添加账号](screenshots/credential-encryption/enabled.png)
- [账号 2FA 与自动巡检表单](screenshots/credential-encryption/account-editor.png)
