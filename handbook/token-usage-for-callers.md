# 调用方如何获取与解析单次请求的 Token 用量

本文面向通过 new-api 调用模型的业务方（SDK / 业务系统），说明响应中 token 用量的字段位置与解析方式。

- 适用范围：单次请求的输入 / 输出 token 用量。
- 不适用范围：账户累计消耗查询（那属于额度接口，单位是额度/金额，不是 token，见文末说明）。

---

## 0. 快速结论

| 接口形态 | 输入 token | 输出 token | 流式取用位置 |
| --- | --- | --- | --- |
| OpenAI Chat Completions | `usage.prompt_tokens` | `usage.completion_tokens` | 末尾 `choices` 为空的 chunk |
| Anthropic Messages | `usage.input_tokens`（不含缓存） | `usage.output_tokens` | `message_start` / `message_delta` 事件 |
| OpenAI Responses | `usage.input_tokens` | `usage.output_tokens` | `response.completed` 事件 |
| Gemini 原生 | `usageMetadata.promptTokenCount` | `usageMetadata.candidatesTokenCount + thoughtsTokenCount` | 末尾含 `usageMetadata` 的块 |

---

## 1. OpenAI Chat Completions（`/v1/chat/completions`）

### 1.1 非流式

响应体顶层带 `usage`：

```json
{
  "id": "chatcmpl-xxx",
  "choices": [ { "index": 0, "message": { "role": "assistant", "content": "..." } } ],
  "usage": {
    "prompt_tokens": 128,
    "completion_tokens": 64,
    "total_tokens": 192
  }
}
```

- 输入 = `usage.prompt_tokens`
- 输出 = `usage.completion_tokens`

### 1.2 流式

new-api 默认会在流末尾补发一个仅含用量的 chunk；为保证拿到的是**上游真实用量**而非本地估算，建议请求时显式开启：

```json
{
  "model": "gpt-4o",
  "stream": true,
  "stream_options": { "include_usage": true }
}
```

该用量 chunk 的特征是 `choices` 为空数组，格式如下（SSE）：

```
data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":128,"completion_tokens":64,"total_tokens":192}}

data: [DONE]
```

解析要点：

1. 按行读取 SSE，累积 `data:` 后的内容。
2. 只在 `choices` 为空数组的那个 chunk 读取 `usage`。
3. 遇到 `data: [DONE]` 结束，取已捕获的 `usage` 值即可，**不要逐块累加**。
4. 注意：若调用方自己传了 `stream_options.include_usage = false`，则不会返回用量。

---

## 2. Anthropic Messages（`/v1/messages`）

字段名与 OpenAI 不同，使用 `input_tokens` / `output_tokens`。

### 2.1 非流式

响应体顶层带 `usage`：

```json
{
  "id": "msg_xxx",
  "role": "assistant",
  "content": [ { "type": "text", "text": "..." } ],
  "usage": {
    "input_tokens": 100,
    "cache_creation_input_tokens": 20,
    "cache_read_input_tokens": 30,
    "output_tokens": 64
  }
}
```

- 输出 = `usage.output_tokens`
- 输入（**口径注意**）：`input_tokens` **不包含缓存部分**。
  - 若只统计"这次新增的输入"，用 `input_tokens`；
  - 若统计"总计费输入"，需自行相加：`input_tokens + cache_read_input_tokens + cache_creation_input_tokens`。

### 2.2 流式

用量分散在两个事件里，需要分别捕获：

```
event: message_start
data: {"type":"message_start","message":{...,"usage":{"input_tokens":100,"cache_read_input_tokens":30,"cache_creation_input_tokens":20,"output_tokens":0}}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":64}}
```

- 输入：从 `message_start` 事件取 `message.usage.input_tokens`（缓存值同样在 `message.usage` 下）。
- 输出：从 `message_delta` 事件取 `usage.output_tokens`。该值是**最终累计值**，取最后一个 `message_delta` 的值即可，不要累加。

---

## 3. OpenAI Responses API（`/v1/responses`）

### 3.1 非流式

顶层 `usage`：

```json
{
  "id": "resp_xxx",
  "status": "completed",
  "usage": { "input_tokens": 80, "output_tokens": 40, "total_tokens": 120 }
}
```

- 输入 = `usage.input_tokens`
- 输出 = `usage.output_tokens`

### 3.2 流式

在终态事件 `response.completed` 中读取：

```
event: response.completed
data: {"type":"response.completed","response":{...,"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}
```

- 取 `response.usage.input_tokens` / `response.usage.output_tokens`。
- 中途的 `response.in_progress` 等事件不携带最终用量，忽略即可。

---

## 4. Gemini 原生格式

用量在 `usageMetadata` 中：

```json
{
  "candidates": [ ... ],
  "usageMetadata": {
    "promptTokenCount": 100,
    "toolUsePromptTokenCount": 0,
    "candidatesTokenCount": 64,
    "thoughtsTokenCount": 32,
    "totalTokenCount": 196
  }
}
```

- 输入 = `promptTokenCount`（若 `toolUsePromptTokenCount > 0`，需一并计入输入）。
- 输出 = `candidatesTokenCount + thoughtsTokenCount`（思维链 token 计入输出）。
- 流式：取流中最后一个带完整 `usageMetadata` 的块。

---

## 5. 通用注意事项

1. **流式必须关注用量返回开关**
   - OpenAI 格式：new-api 默认会返回用量 chunk；显式传 `stream_options.include_usage = true` 更稳妥，可确保取到上游真实用量。
   - Claude / Responses 格式：用量本就随终态事件返回，无需额外开关。

2. **取值用"末次覆盖"，不要逐块累加**
   流式返回的用量是本次请求的最终值，累加会得到错误结果。

3. **缓存 token 口径不同**
   Claude 的 `input_tokens` 不含缓存；OpenAI 的 `prompt_tokens` 通常已包含缓存，另可用 `prompt_tokens_details.cached_tokens` 查看命中缓存的部分。跨厂商对比时先统一口径。

4. **字段缺失的两种情况**
   - 上游未返回用量时，new-api 会用本地估算填充，数值与官方口径可能存在偏差；此类记录的 `usage_source` / 日志会标注为本地估算。
   - OpenAI 流式若关闭了 `include_usage`，流中不会出现用量字段。

5. **没有"按请求查询 token"的开放接口**
   单次请求的用量只能从上述响应中获取；如需按请求回溯历史明细，请走控制台日志（需用户登录态，非 API Key）。

---

## 6. 额度类接口（与 token 用量的区别）

new-api 提供 OpenAI 兼容的额度查询接口，用于查询**账户/令牌累计消耗额度**，单位是额度（默认折算为金额），**不是 token 数**：

- `GET /v1/dashboard/billing/subscription`：额度上限、有效期等。
- `GET /v1/dashboard/billing/usage`：累计已用额度。
- `GET /api/usage/token`：当前 API Key 的总额度 / 已用 / 剩余。

仅当站点把"额度展示类型"配置为 TOKENS 时，上述数值才等价于 token 数量。这些接口返回的是聚合值，无法定位到单次请求。

---

## 7. 最小解析示例（伪代码）

```text
# OpenAI 非流式
input_tokens  = resp.usage.prompt_tokens
output_tokens = resp.usage.completion_tokens

# OpenAI 流式
for chunk in sse_lines:
    if chunk.choices is empty and chunk.usage exists:
        input_tokens  = chunk.usage.prompt_tokens
        output_tokens = chunk.usage.completion_tokens

# Anthropic 非流式
input_tokens  = resp.usage.input_tokens + resp.usage.cache_read_input_tokens + resp.usage.cache_creation_input_tokens
output_tokens = resp.usage.output_tokens

# Anthropic 流式
on event "message_start": input_tokens = event.message.usage.input_tokens (+ cache_*)
on event "message_delta": output_tokens = event.usage.output_tokens   # 末次覆盖

# Responses 流式
on event "response.completed":
    input_tokens  = event.response.usage.input_tokens
    output_tokens = event.response.usage.output_tokens
```

---

## 8. 并发受限 429 的 message 形态（错误识别）

请求因并发上限被网关拒绝时返回 HTTP 429，响应头带 `Retry-After: 1`，错误体的 `error.type` 与 `error.code` 恒为 `concurrency_exceeded`。**来源仅通过 `error.message` 文本区分**（短语识别，调用方请勿依赖整句逐字匹配，只匹配短语）：

| message 含有的短语 | 含义 |
| --- | --- |
| `Concurrency limit exceeded for your account` | 你的 Token / 用户并发已达上限（即时拒绝；若站点关闭了排队等待，这也是排队路径的表现） |
| `Timed out waiting` | 触发并发上限后进入排队等待，等待超时仍未拿到槽位 |
| `wait queue is full` | 触发并发上限后尝试排队，但等待队列已满，未能入队 |
| `Concurrency limit exceeded for the selected channel` | 所有候选渠道的并发槽位占满（relay 路径） |

上表四种形态（中间件三态 + relay 渠道饱和）的状态码、`error.type`、`error.code` 与 `Retry-After` 完全一致，仅 message 不同；收到 429 后稍后重试即可（槽位随在途请求结束立即释放）。
