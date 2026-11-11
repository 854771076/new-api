# 并发控制测试报告 — 本地实例

## 测试环境

| 项目 | 值 |
|------|-----|
| 本地网关 | `http://localhost:3001`（new-api-monster 最新代码） |
| 上游 provider | `http://upstream.example.internal:30000`（用户内网网关） |
| 数据库 | SQLite（本地） |
| 并发控制后端 | Redis（`redis-master-190` 容器，`localhost:6379`） |
| Token 并发限制 | **5** |
| 测试模型 | `Qwen3.6-35B` |
| 测试时间 | 2026-10-12 02:33 CST |

## 并发控制原理

LLM 网关的并发限制 ≠ 频率限制。**并发限制控制的是同一时刻正在处理中的请求数量**（in-flight requests），而不是单位时间内的请求总数。

工作原理：
1. 每个请求进入时，从 Redis ZSET 中获取一个"槽位"（lease）
2. 请求完成（或流式传输结束）时，释放槽位
3. 如果所有槽位已满，新请求返回 HTTP 429 + `Retry-After: 1`
4. 三个独立维度同时生效（原子化批量获取，任一超限整体拒绝）：
   - **Token 级**：`key:tokenID:model` / `key:tokenID:*`
   - **用户级**：`user:userID:*`
   - **渠道级**：`channel:channelID`

## 测试结果

### ✅ 并发控制精确生效

| 测试 | 并发数 | 通过 | 429 | 结果 |
|------|--------|------|-----|------|
| 顺序基线 | 1×3 | 3 | 0 | ✅ |
| 突发 3（低于限制） | 3 | 3 | 0 | ✅ |
| 突发 5（等于限制） | 5 | 5 | 0 | ✅ |
| **突发 8（超出限制）** | 8 | **5** | **3** | ✅ 精确拒绝 |
| **突发 10（超出限制）** | 10 | **5** | **5** | ✅ 精确拒绝 |
| **突发 15（超出限制）** | 15 | **5** | **10** | ✅ 精确拒绝 |
| 恢复检查 | 3 | 3 | 0 | ✅ |
| **流式 8 并发** | 8 | **5** | **3** | ✅ 流式也受限 |
| 持续 3req/s×10s | 30 | 30 | 0 | ✅ 低速率不触发 |

### 关键验证点

1. **精确限流**：并发限制=5 时，8/10/15 并发恰好只放行 5 个，其余全部 429 ✅
2. **429 响应格式正确**：
   ```json
   HTTP 429
   Retry-After: 1
   {
     "error": {
       "code": "concurrency_exceeded",
       "message": "Concurrency limit exceeded for your account...",
       "type": "concurrency_exceeded"
     }
   }
   ```
3. **流式请求同样受限**：流式 8 并发时恰好放行 5 个 ✅
4. **突发后恢复**：429 的请求等待后重试成功 ✅
5. **低速率不触发**：3 req/s 持续 10 秒全部通过（因为请求在 1 秒内完成，同时在途不超过 5）✅

### 429 响应详情

所有 429 响应一致：
- `Retry-After: 1`（固定 1 秒建议）
- `type: "concurrency_exceeded"`
- `code: "concurrency_exceeded"`
- 不泄漏 scope/渠道 ID 等内部信息

## 测试数据明细

### Test 02: Burst 10 concurrent (limit=5)
```
✅ [ 0] 短-数学       40tok 145ms
✅ [ 1] 短-翻译       41tok 105ms
✅ [ 2] 短-常识       40tok 168ms
✅ [ 3] 中-解释       178tok 559ms
✅ [ 4] 中-代码       236tok 664ms
🚫 [ 5] 中-故事       429 Retry-After=1s
🚫 [ 6] 长-分析       429 Retry-After=1s
🚫 [ 7] 长-创意       429 Retry-After=1s
🚫 [ 8] 长-对比       429 Retry-After=1s
🚫 [ 9] 短-数学       429 Retry-After=1s
Summary: 5 ok, 5 429, 0 errors
```

### Test 04: Streaming 8 concurrent (limit=5)
```
✅ [ 0] 中-代码       60chunks
✅ [ 1] 中-故事       52chunks
✅ [ 2] 长-分析       146chunks
✅ [ 3] 长-创意       155chunks
🚫 [ 4] 长-对比       429
🚫 [ 5] 短-数学       429
✅ [ 6] 短-翻译       8chunks
🚫 [ 7] 短-常识       429
Summary: 5 ok, 3 429
```

## 结论

**并发控制功能完全正常**：

1. ✅ Token 级并发限制精确执行（limit=5，超出全部拒绝）
2. ✅ 429 响应格式符合规范（Retry-After + type/code）
3. ✅ 流式和非流式请求都受并发限制
4. ✅ 请求完成后槽位正确释放（恢复测试通过）
5. ✅ Redis ZSET-lease 计数机制工作正常
6. ✅ 低速率持续请求不误触发并发限制

## 测试脚本

- `concurrency-test/test-local.mjs` — 完整测试脚本（7 个测试场景）
- `concurrency-test/test.mjs` — 远程网关测试脚本（无并发限制场景）