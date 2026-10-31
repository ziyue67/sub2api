export default {
  "started": "请求开始（本地时间）",
  "finished": "请求完成（本地时间）",
  "total": "总耗时",
  "first_content": "首内容 token（可见文本）",
  "connection": "获取连接",
  "response_headers": "等待响应头",
  "stream_output": "流输出",
  "local_processing": "本地处理（事件编码）",
  "details": "耗时详情",
  "overlap": "所有偏移相对测试开始。总耗时包含各阶段；首内容包含连接与响应头等待，流输出与本地处理有重叠。各项不可相加，也不能据此推导模型计算时间。",
  "range": "观测偏移：{start} → {end}",
  "segments": "累计 {count} 个编码片段",
  "missingFinal": "— 未取得服务端最终指标，不推算完成时间或耗时。",
  "delivery": {
    "idle": "尚未开始",
    "running": "等待服务端最终指标",
    "finished": "已取得服务端计时",
    "legacy": "此服务端未提供计时数据",
    "incomplete": "事件流在完成前结束",
    "canceled": "已在此浏览器取消，尚未确认服务端完成",
    "error": "连接中断，最终指标可能不可用"
  },
  "outcome": {
    "success": "成功",
    "error": "错误",
    "timeout": "超时",
    "canceled": "取消"
  },
  "boundaries": {
    "connection": "HTTP GetConn → GotConn，包含连接池、DNS、TCP、TLS 等待；连接复用单独标注。",
    "response_headers": "请求完整写出 → 完整响应头可用，包含上游等待，并非纯网络传输时间。",
    "first_content": "单次上游调用开始 → 首次解析出非空白可见文本。注释、心跳、元数据、首字节均不计；这是内容观测，不是 tokenizer 内部计时。",
    "stream_output": "首个可见文本 → 解析器因完成、错误或取消返回，包含读取、解析、调度和本地写出，并非客户端收到时间。",
    "local_processing": "仅记录各测试 SSE 事件写出前的 JSON 编码。时长累加不连续片段，偏移为片段外包区间；排除上游等待、写出、最终计时事件和其他未采集本地工作。"
  },
  "reasons": {
    "not_observed": "— 未观测到边界或尚未到达该阶段。",
    "no_content": "— 未观测到非空白可见文本。",
    "not_isolated": "— 未单独测量本地处理。",
    "invalid_observation": "— 观测值超出本次测试区间。",
    "connection_reused": "— 复用已有连接，不声明新建连接耗时。",
    "multiple_requests": "— 包含多次上游请求或重试，无法表示为单一阶段。",
    "non_stream": "— 非流式响应不适用。",
    "unsupported_path": "— 此路径未暴露实时内容解析边界。",
    "collector_limit": "— 已达到采集上限，省略不完整指标。"
  }
}
