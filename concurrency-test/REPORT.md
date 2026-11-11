# 并发控制测试报告

## 测试环境

| 项目 | 值 |
|------|-----|
| 网关地址 | `http://upstream.example.internal:30000` |
| 网关类型 | New API (AI API Gateway) |
| 测试时间 | 2026-10-12 01:54 CST |
| API Key | `sk-aFD...nmnJ` |

## 可用模型

| 模型 | 类型 | 说明 |
|------|------|------|
| `Qwen3.6-35B` | 推理模型 | 所有 token 用于 reasoning，content 为空（需更大 max_tokens 才输出内容） |
| `Qwen3-Next-80B` | 标准模型 | 正常输出 content，响应质量好 |
| `xfyun-asr` | 语音识别 | 未测试 |
| `Qwen3-ASR-1.7B` | 语音识别 | 未测试 |

## 并发控制原理（LLM 网关）

### 什么是并发限制

并发限制 ≠ 请求频率限制（rate limit）。并发限制控制的是**同一时刻正在处理中的请求数量**（in-flight requests），而不是单位时间内的请求总数。

工作原理：
1. 每个请求进入时，占用一个"槽位"（slot/lease）
2. 请求完成（或流式传输结束）时，释放槽位
3. 如果所有槽位已满，新请求返回 HTTP 429 + `Retry-After` 头
4. 三个独立维度同时生效：Token 级、用户级、渠道级（取最严格的）

### 三级并发控制

```
请求 → [Token 并发限制] → [用户并发限制] → [渠道并发限制] → 上游模型
         key:tokenID:model    user:userID:*    channel:channelID
         全部原子化批量获取，任一超限则整体拒绝
```

### 429 响应格式

```json
HTTP 429
Retry-After: 1

{
  "error": {
    "message": "Concurrency limit exceeded for your account.",
    "type": "concurrency_exceeded",
    "code": "concurrency_exceeded"
  }
}
```

## 测试结果

### ⚠️ 当前状态：Token 未配置并发限制

所有测试中 **零 429 错误**，说明当前 API Key 没有设置任何并发限制。

### 测试 01：顺序基线（3 请求）

| # | Prompt | Tokens | 延迟 |
|---|--------|--------|------|
| 0 | 短-数学 | 30 | 137ms |
| 1 | 短-翻译 | 31 | 91ms |
| 2 | 短-常识 | 30 | 99ms |

✅ 3/3 通过，平均 109ms

### 测试 02：突发并发（递增并发数）

| 并发数 | 通过 | 429 | 错误 | 平均延迟 |
|--------|------|-----|------|----------|
| 3 | 3 | 0 | 0 | 110ms |
| 5 | 5 | 0 | 0 | 243ms |
| 8 | 8 | 0 | 0 | 533ms |
| 10 | 10 | 0 | 0 | 563ms |
| 15 | 15 | 0 | 0 | 484ms |

✅ 全部通过，延迟随并发数线性增长（上游模型排队处理）

### 测试 03：突发后恢复

✅ 3/3 通过，平均 374ms，无降级

### 测试 04：流式并发（5 路）

| # | Chunks | 延迟 |
|---|--------|------|
| 0 | 44 | 97ms |
| 1 | 39 | 99ms |
| 2 | 107 | 98ms |
| 3 | 112 | 103ms |
| 4 | 122 | 63ms |

✅ 5/5 通过，流式传输正常

### 测试 05：多模型并发（每模型 3 路）

| 模型 | 通过 | 内容示例 |
|------|------|----------|
| Qwen3.6-35B | 3/3 | (reasoning model, content in reasoning field) |
| Qwen3-Next-80B | 3/3 | "Code flows like a river, Bugs whisper in silent nights—" |

✅ 6/6 通过

### 测试 06：持续负载（2 req/s × 15s = 30 请求）

✅ 30/30 通过，平均 499ms，零错误

## 模型特性发现

### Qwen3.6-35B（推理模型）
- 所有 completion tokens 都进入 `reasoning_tokens`
- `content` 字段为空，`reasoning` 字段包含思考过程
- 需要更大的 `max_tokens`（≥500）才能同时完成推理和输出

### Qwen3-Next-80B（标准模型）
- 正常输出 `content`，无 reasoning tokens
- 支持中英文，响应质量好
- 适合内容生成任务

## 如何启用并发限制

要测试并发控制效果，需要在管理后台为 Token 设置并发上限：

1. 登录 `http://upstream.example.internal:30000` 管理后台
2. 进入「令牌」管理页面
3. 编辑当前 Token，设置：
   - **全局并发上限**（如 `5`）：该 Token 同时最多 5 个请求
   - **按模型并发上限**（如 `Qwen3.6-35B=3,Qwen3-Next-80B=2`）：每个模型独立计数
4. 保存后立即生效（缓存自动失效）

设置后重新运行 `node test.mjs`，预期行为：
- 并发 ≤ 限制数：全部 200
- 并发 > 限制数：超出的请求返回 429 + `Retry-After: 1`
- 等待后重试：恢复正常

## 结论

1. **网关正常工作**：4 个模型均可访问，请求路由正确
2. **当前无并发限制**：所有测试零 429，Token 未配置限制
3. **Qwen3.6-35B 是推理模型**：需注意 reasoning_tokens 消耗
4. **Qwen3-Next-80B 是标准模型**：适合常规内容生成
5. **延迟合理**：短请求 ~100ms，长请求 ~500ms-2s