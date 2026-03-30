# 纯聊天 API 模拟 Tools 调用的方法论

这份文档总结的是一种通用做法：

- 上游模型只有普通聊天接口
- 不原生支持 `tools` / `tool_calls` / `tool_use`
- 但下游调用方希望继续走 OpenAI 或 Anthropic 风格的工具调用协议

核心思路不是“骗上游说自己支持 tools”，而是：

1. 在代理层把工具定义改写成一套稳定的提示词契约
2. 让模型用约定的结构化文本输出动作
3. 再由代理把结构化文本还原成标准协议里的 `tool_calls` 或 `tool_use`

## 核心原则

### 1. 不依赖上游原生能力

如果上游不支持原生工具调用，最稳的路线不是继续透传 `tools` 字段，而是把工具定义下沉成提示词层协议。

换句话说：

- 对模型来说，它看到的是“你有这些动作，可以按某种格式发起调用”
- 对客户端来说，它看到的仍然是标准 OpenAI / Anthropic 工具协议

代理层负责做两次映射。

### 2. 工具调用必须降维成可解析文本

一个可落地的格式必须满足：

- 模型容易学会
- 人容易读
- 代理容易解析
- 多轮场景里不容易歧义

本项目采用的是 fenced block：

```text
```json action
{"tool":"NAME","parameters":{"key":"value"}}
```
```

这个格式比“自然语言里自己说我要调用某个工具”稳定很多。

### 3. 代理是状态机，不只是转发器

一旦进入 emulation 模式，代理就不能再只是简单透传。

它至少要承担这些职责：

- 注入工具说明
- 把历史工具调用改写回上下文
- 把工具结果回灌成下一轮提示
- 识别拒答和跑偏
- 必要时做 retry
- 把文本动作重新编码成标准工具协议

## 一条完整链路

### 输入侧

客户端发来：

- OpenAI `tools` / `tool_choice`
- 或 Anthropic `tools` / `tool_choice`

代理做三件事：

1. 抽取工具名称、描述、参数 schema
2. 归一化 tool choice
3. 判断是否进入 emulation 模式

进入 emulation 后，不再把原始 `tools` 直接交给上游，而是改写系统提示词。

### 提示词侧

提示词里至少要包含：

- 你有工具可用，不要声称“工具不可用”
- 工具列表
- 固定动作格式
- 多轮规则
- `tool_choice` 约束
- 一个有效示例

建议的约束重点：

- 需要工具时必须输出 `json action`
- 独立动作可以一次输出多个 block
- 依赖动作必须等工具结果回来再继续
- 不需要工具时才允许输出普通文本
- 不要解释“为什么不能调用工具”

### 输出侧

模型回复后，代理扫描 `json action` block：

- 解析出 `tool`
- 解析出 `parameters`
- 从正文里剥离 action block

然后映射回：

- OpenAI `message.tool_calls`
- Anthropic `content[].tool_use`

如果没有解析到动作，就把剩余文本当普通 assistant 回复。

## 多轮工具调用

这是最容易做坏的部分。

### 单轮模拟并不够

只做第一轮 `tool_calls` 很容易，但这还不是真正的 agent loop。

真正有用的是：

1. 第一轮模型发起工具调用
2. 外部执行工具
3. 把工具结果回灌
4. 模型继续决策
5. 可能再次发起工具调用
6. 或输出最终回答

### 回灌工具结果时，不要只塞原始结果

稳定做法是把工具结果包装成明确的续写指令，而不是只把结果裸塞回去。

例如：

```text
Tool result for call_1:
pong

Based on the tool result above, continue with the next appropriate action using the structured format.
```

这样模型更清楚当前处于“继续 agent loop”的阶段，而不是另起一轮普通问答。

### 第二轮不应强依赖重复传 tools

复杂客户端并不一定会在每一轮都重复把 `tools` 发回来。

因此代理应把这些历史也视作“仍处于 emulation 会话中”的信号：

- OpenAI:
  - assistant 消息里已有 `tool_calls`
  - 后续有 `tool` 角色消息
- Anthropic:
  - 历史里已有 `tool_use`
  - 后续有 `tool_result`

只要这些历史存在，即使当前轮未重新传 `tools`，代理也应继续以 emulation 方式处理。

### 历史里的工具调用要重新投影成动作文本

模型并不理解 OpenAI / Anthropic 的结构化历史字段。

因此代理要把历史里的：

- `assistant.tool_calls`
- `assistant tool_use`

重新投影成：

```text
```json action
{
  "tool": "ping",
  "parameters": {
    "value": "123"
  }
}
```
```

这样模型才能在多轮里看到自己“之前做过什么动作”。

## Few-shot 怎么设计

### 最小 few-shot

至少给一个合法动作示例：

```text
```json action
{
  "tool": "read_file",
  "parameters": {
    "path": "README.md"
  }
}
```
```

它的作用不是示范业务逻辑，而是强制模型学会“输出形状”。

### 更稳的 few-shot

如果目标是复杂 agent loop，推荐再补一个“工具结果回来后再次决策”的 few-shot。

例如三段式：

1. 用户请求
2. assistant 发起工具调用
3. user 提供 tool result
4. assistant 再次发起新工具调用或结束

这个 few-shot 能显著减少模型在第二轮以后掉回普通文本解释。

### few-shot 要突出状态转换

最重要的不是工具本身，而是让模型明确以下三种状态：

- 该调用工具
- 该等待工具结果
- 该输出最终回答

复杂 loop 不稳，通常就是状态转换没教明白。

## Retry 怎么设计

### Retry 的触发条件

比较实用的触发条件：

- 本轮本应调用工具，但没有解析出 action block
- 模型回复了“没有工具”“工具不可用”“我无法调用”
- `tool_choice=any`
- `tool_choice=tool`

### Retry 的方式

不要只重发原请求。应显式补一条纠偏消息，例如：

```text
Your last response did not include any ```json action``` block.
You must respond with at least one valid action block now.
Do not explain. Output the action block directly.
```

如果是强制指定某个工具，再额外加：

```text
You must call "ping".
```

### Retry 不要无限循环

建议设置：

- 小次数重试
- 每次 retry 都更强约束
- 只在明确需要工具调用时触发

否则很容易把普通自然回复误判成失败。

## 协议映射建议

### OpenAI

输入：

- `tools`
- `tool_choice`
- `assistant.tool_calls`
- `tool`

输出：

- `finish_reason = "tool_calls"`
- `message.tool_calls`

### Anthropic

输入：

- `tools`
- `tool_choice`
- `content[].tool_use`
- `content[].tool_result`

输出：

- `stop_reason = "tool_use"`
- `content[].tool_use`

流式时，再映射成对应的 SSE 事件。

## 常见坑

### 1. 只做第一轮

这会让你看起来“支持 tools”，但一进入 agent loop 就断掉。

### 2. 历史工具调用没有重投影

模型看不到自己的历史动作，多轮就不稳。

### 3. 工具结果回灌过于裸

只把 `pong` 塞回去，模型不一定知道自己该继续决策。

### 4. 没有 refusal 检测

很多模型会下意识说：

- 我没有工具
- 当前环境无法调用
- 我只能提供建议

不识别这类模式，就不会进入纠偏 retry。

### 5. 文本解析规则太脆弱

解析器至少要容忍：

- ` ```json action ` 或普通 ` ```json `
- 智能引号
- 末尾逗号
- 参数对象有时是字符串化 JSON

## 推荐的最小实现

如果要做一个最小可用版，建议先只做：

1. 工具定义注入
2. `json action` 解析
3. refusal 检测
4. 一次 retry
5. OpenAI 非流式返回

然后再逐步补：

1. Anthropic 非流式
2. OpenAI 流式
3. Anthropic 流式
4. 多轮 tool history 投影
5. 更强 few-shot

## 适用边界

这套方法适合：

- 上游不支持原生 tools
- 你又必须对外兼容标准工具协议
- 目标任务以工程类、文件类、检索类工具为主

它不适合：

- 对工具调用正确率极高要求的强生产场景
- 上游已经支持原生 tools，但你还硬要绕一层文本模拟

如果上游能原生支持工具调用，优先使用原生协议。

## 本项目里的落地经验

在 `lingma-ipc-proxy` 里，这套方法最终证明了两点：

1. 只靠透传 `tools` 给 Lingma 不够，模型会继续说“没有可用工具”
2. 代理层做 emulation 后，可以稳定还原出：
   - OpenAI `tool_calls`
   - Anthropic `tool_use`
   - 多轮 tool result 回灌后的继续决策

进一步要增强稳定性，最值得继续打磨的是：

- 多轮再次发起新工具调用的 few-shot
- 基于历史状态的更细 retry 策略
- 不同工具类别的专用示例

配套实现清单：

- [tool-emulation-checklist.zh-CN.md](./tool-emulation-checklist.zh-CN.md)

