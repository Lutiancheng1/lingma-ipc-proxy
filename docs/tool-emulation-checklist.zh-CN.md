# Tool Calling 实现清单

这份清单覆盖 OpenAI / Anthropic 标准工具调用的完整实现。

目标是把"纯聊天 API 支持 tools 调用"拆成可逐项完成、可逐项验证的实现面。

## 1. Prompt Contract

- 明确告诉模型当前有可用工具，不要声称“工具不可用”
- 列出全部工具：
  - 名称
  - 简短描述
  - 参数 schema 摘要
- 固定动作输出格式：
  - ` ```json action ... ``` `
- 明确多轮规则：
  - 独立动作可并行
  - 依赖动作要等 tool result
  - 无需工具时才输出普通文本
- 明确 `tool_choice` 约束：
  - `any`
  - 指定 tool
- 给至少一个合法 action block 示例
- 最好再给一个“tool result 回来后继续决策”的 few-shot

验收标准：

- 模型第一轮能稳定输出合法 action block
- 第二轮收到 tool result 后，不会轻易掉回普通解释文本

## 2. Request Normalization

- OpenAI:
  - 解析 `tools`
  - 解析 `tool_choice`
  - 解析 `assistant.tool_calls`
  - 解析 `tool`
- Anthropic:
  - 解析 `tools`
  - 解析 `tool_choice`
  - 解析 `tool_use`
  - 解析 `tool_result`
- 统一归一化成内部结构：
  - tools
  - choice
  - messages
  - history state
- 识别“当前轮没带 tools，但历史里已有 tool 调用”的场景

验收标准：

- 第二轮即使不重复传 `tools`，也能继续走 tool calling

## 3. Tool History Projection

- 把历史 assistant 工具调用重投影成 action text
- 不要把结构化历史原样丢给上游模型
- 保留：
  - tool name
  - arguments
  - call id
- 投影结果应和真实 action block 尽量一致

验收标准：

- 模型在多轮中能“看到”自己之前做过什么动作

## 4. Tool Result Continuation

- tool result 不要裸塞回去
- 包装成明确续写指令：
  - 当前哪个 call 的结果回来了
  - 基于结果继续下一步动作
- 对空结果、错误结果、部分结果做统一包装

验收标准：

- 模型收到 tool result 后能继续：
  - 再发起新工具调用
  - 或输出最终答案

## 5. Parser Contract

- 识别：
  - ` ```json action `
  - 普通 ` ```json `
- 容忍：
  - 智能引号
  - 尾逗号
  - 参数是字符串化 JSON
- 支持提取：
  - `tool`
  - `name`
  - `parameters`
  - `arguments`
  - `input`
- 能从正文里剥离 action block
- 支持多 block

验收标准：

- 同一回复里多个 action block 都能被解析
- 正文和动作块可以正确拆分

## 6. Retry Policy

- 触发条件：
  - 明确要求工具调用但没产出 action block
  - 命中 refusal 文本
  - `tool_choice=any`
  - `tool_choice=tool`
- retry 消息要更强约束：
  - 必须输出 action block
  - 不要解释
  - 必要时必须调用指定工具
- 控制 retry 次数
- 记录 retry 原因

验收标准：

- refusal 回复能被纠偏
- retry 不会无限循环

## 7. Refusal Detection

- 维护 refusal 关键词表：
  - `I don't have tools`
  - `tools are unavailable`
  - `没有可用的工具`
  - `无法调用工具`
- 识别“软拒答”：
  - 只解释、不行动
  - 强调环境限制
- 区分：
  - 真正不该调用工具
  - 本该调用工具却在推脱

验收标准：

- 常见“我没有工具”类回复能稳定触发 retry

## 8. Response Re-encoding

- OpenAI:
  - `message.tool_calls`
  - `finish_reason = tool_calls`
- Anthropic:
  - `content[].tool_use`
  - `stop_reason = tool_use`
- 无工具时回普通文本
- 文本和工具调用共存时保持协议兼容

验收标准：

- 下游客户端无需知道上游其实不支持 native tools

## 9. Streaming Strategy

- OpenAI stream:
  - 先发 role chunk
  - 再发 text delta
  - 再发 tool_calls delta
- Anthropic stream:
  - `message_start`
  - `content_block_start`
  - `content_block_delta`
  - `content_block_stop`
  - `message_delta`
  - `message_stop`
- 如果当前实现是“先完整拿结果再合成流”，文档里要明确说明

验收标准：

- 下游看到的流式协议字段合法

## 10. Multi-turn State Machine

- 状态至少区分：
  - 等待模型首次决策
  - 已发起工具调用
  - 等待 tool result
  - 收到 tool result，等待下一轮决策
  - 最终回答完成
- 状态切换依据应来自消息历史，而不是只看本轮字段
- 不要把“工具历史存在”误判成“必须再调工具”

验收标准：

- 一轮以上的 agent loop 稳定

## 11. Observability

- 打日志：
  - 是否进入 tool calling
  - 解析到几个 tool calls
  - 是否触发 retry
  - refusal 命中原因
- 最好记录：
  - prompt contract 是否注入
  - tool history 是否被识别

验收标准：

- 出问题时能判断是：
  - prompt 不够强
  - parser 失败
  - retry 没触发
  - 状态机断了

## 12. 测试矩阵

- OpenAI:
  - 单轮 tool call
  - 多轮 tool result 回灌
  - 第二轮不重复传 `tools`
  - 指定 tool
  - `tool_choice=any`
- Anthropic:
  - 单轮 tool_use
  - 多轮 tool_result 回灌
  - 第二轮不重复传 `tools`
  - 流式 tool_use
- 异常场景：
  - refusal
  - 无效 JSON
  - 多 action block
  - 普通文本结束

验收标准：

- 至少覆盖“第一轮调用工具”和“第二轮继续决策”两大关键场景

## 13. 下一步优先级

如果当前系统已经能跑，最值得优先继续做的是：

1. 多轮再次发起新工具调用的 few-shot
2. 基于历史状态的 retry 强化
3. 更细的 refusal 分类
4. parser 容错增强
5. 流式工具事件细化
