# 内置 ModelTrace 指纹

来源：[xqy2006/ModelTrace](https://github.com/xqy2006/ModelTrace/tree/6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2)，固定提交 `6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2`，MIT 许可证见本目录 `LICENSE`。

- `data/unified_bank.json` 为该提交原文件，SHA256：`f267d0513aa06981d024ec5394746fceddf44e4e8a75fa6bf8304255f4b32d6d`。
- 题目生成移植自 `fingerprint.py:generate_challenges`；数字解析、Hellinger 特征、环境方向投影、有序块特征、逐回答融合与按样本数校准的 softmax 移植自 `fingerprint.py`。保留 355 维数值分布、0.5 平滑及库中的权重、中心与校准参数。
- `testdata/astra-reference.json` 仅摘取同提交 `data/gpt_reference.jsonl` 中 `gpt-6-astra` 的前三条 `clean` 公开参考回答和原始请求数量。这些是训练数据，用来检查移植回归，不是独立准确率证明。
- 服务启动后只读取嵌入的文件，无在线下载、远程评分、Python 或 NumPy 运行时。更新库需要审查新数据及算法兼容性，更新固定 SHA、校验值与回归数据后重新构建，不自动跟随上游移动分支。

概率是当前候选集合、均匀先验下的相对概率，不能用作开放集合身份认证、降智判定或自动封禁依据。不要把库内交叉验证准确率显示为实际账号测试准确率。
