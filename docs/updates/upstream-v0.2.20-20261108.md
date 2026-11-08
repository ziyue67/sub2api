# 原版 Sub2API 0.2.20 同步记录

## 基线

- 魔改基线：ziyue67/sub2api main，b7e282215da0d571f1780df7d1754788b36cda65。
- 原版目标：Wei-Shaw/sub2api main，6b4b37db43443b5e88e424fe3c69602cc3810dfb。
- 后端 VERSION 从 0.2.19 同步为 0.2.20。
- 采用真实 merge 保留两侧历史。原版相对基线新增52个提交，含合并提交；不是52个独立功能。

## 合并内容

接入原版 Responses 完成消息字段、缓冲 reasoning、Gemini 工具媒体、请求体读取分类与诊断、Codex WS桥接指纹、GPT Image 2.6原生参数及路由、Antigravity用量与模型级惩罚、API Key错误分类、Terra元数据/effort/cache-key/订阅计费和Codex配置导出修复。

原版0.2.20部分资源保护、管理MCP功能此前已在魔改基线，不能将整个发行说明都视为本PR新增。本PR没有新增或改写数据库迁移文件。

## 冲突处理与保留行为

- Sol订阅保留魔改版500,000输入token（含缓存）边界：不超过门槛豁免，超过沿用既有阶梯；API Key不豁免。Terra新增遵循原版订阅豁免，API Key继续API价卡。保留所有原有Sol边界测试，并纳入Terra及超过500k测试。
- Responses消息采用原版完整序列化；保留魔改reasoning summary空数组、tool_search client execution/对象arguments和custom tool namespace。
- Chat原生reasoning与flat alias的有效值解析保持，接入Terra effort校验。
- 保留区域认证、请求计时/真实输出时间、图片记录、退款、安全修复、管理员Key来源IP策略与缓存计费定制。
- Codex导出保留Fast/模型发现/用户自定义provider名称/UTF-8编码，并接入live search和provider scoped图片认证。原版默认模型更新为GPT-6.1 Sol，默认API Key认证；legacy方式仍可选。
- 保留魔改模型白名单精简与图片模型发布日期；避免合并重复的switch分支及价卡JSON键。

## 边界

- 本PR不宣称修复amsg_ encrypted content hydration failed；原版仍有相关未关闭报告。
- 不修改生产数据、白名单开关或管理Key，不部署生产服务器。
- 验证与最终CI证据以PR说明为准；源码合并和CI不等同于生产业务验收。
