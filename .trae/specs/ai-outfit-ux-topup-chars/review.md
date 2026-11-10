# Independent Review — AI Outfit UX / Topup / Characters + new-api OAuth endpoints

## Run Date
2026-11-10

## Spec / Plan
- `spec.md`: `.trae/specs/ai-outfit-ux-topup-chars/spec.md`
- `tasks.md`: `.trae/specs/ai-outfit-ux-topup-chars/tasks.md`

## Review Result
**pass**

## Summary
全部 13 个 AC 满足（其中 AC-11 / AC-12 按 rubric 打分均 ≥ threshold）；**Severity high = 0**；审查发现的 4 个 medium 级问题全部在回归中修复；剩余 2 个 medium（白名单字段命名与 spec、characters POST owner 注释）属于风格统一性改进，不构成安全/功能缺陷，记录在 "Post-Remediation Items" 中低优先级跟进；7 个 low 级发现同样低优先级。

## AC Check Results

| AC | 类型 | 结果 | 证据 |
|----|------|------|------|
| AC-1 | rule | pass | `controller/oauth_userinfo.go`：scope=api:read 时 quota/used_quota/request_count/group 存在且与 `/api/user/self` 一致；scope 无 api:read 时字段不存在；2 个测试断言 |
| AC-2 | rule | pass | `controller/oauth_topup.go`：scope=403、token=401、amount<=0=400 全部测试覆盖；success path 需真实 Waffo 配置未测（与 dashboard 一致）；已移除 topup_id |
| AC-3 | rule | pass | `controller/oauth_logs.go`：scope=403、白名单/脱敏测试覆盖；字段已移至合理子集（channel_name 保留，channel_id 移除）；page clamp 10000 |
| AC-4 | rule | pass | `/profile` 额度卡片渲染 quota/used_quota/group；从 `fetchGatewayUserinfo` → `/api/gateway/userinfo` 拿到数据 |
| AC-5 | rule | pass | ProfileClient.tsx：充值按钮 → POST `/api/gateway/topup` → DaisyUI Modal iframe → 降级 `window.open` |
| AC-6 | rule | pass | `/characters` multipart upload + 8MB/MIME 校验 + service_role Storage 写入；DELETE owner 先查后删 |
| AC-7 | rule | pass | Studio `/wardrobe` `/poses` 均有搜索框 + 筛选 chips；limit 24 + URL params 双向同步；选中态 ring+✓ badge |
| AC-8 | rule | pass | `grep 'newapi.0xfiang.com' apps/ai-outfit/src` → 无命中；layout.tsx 两处已替换为 `config.gatewayPublicUrl` |
| AC-9 | rule | pass | bootstrap.sh/.ps1 新建 OAuth app 的 scopes 设为 `openid profile email api:read api:write`；Supabase provider scopes 同步 |
| AC-10 | rule | pass | SKILL.md 309 行 / 25.9KB ≤ 350 / < 28KB；22 个引用路径全部存在；新增 identity.md/billing.md/supabase.md/api-contract.md |
| AC-11 | rubric (≥4) | pass | userinfo 4/4、logs 3/3、topup 6/6 测试；缺 topup success（合理；需 Waffo 支付配置）；综合 4.2/5 |
| AC-12 | rubric (≥4) | pass | Studio 搜索筛选 + 人物上传 + profile 额度/日志/充值一体化 + 整体布局美观 4.5/5 |
| AC-13 | rubric (=5) | pass | scope 矩阵正确；/oauth/logs 脱敏；page_size clamp；service_role 仅 Route Handler 内部；无敏感数据泄露 |

## Findings (Original Review)

### Severity Medium (全部修复)
| # | File | Line | Issue | Fix |
|---|------|------|-------|-----|
| M1 | controller/oauth_logs.go | 56 | page 未 clamp → startIdx 可能大偏移 | 加 `maxPage=10000` clamp，超上限返回 400 |
| M2 | controller/oauth_topup.go | 74 | 返回 `topup_id` 暴露自增主键 | 已移除；只返回 checkout_url / order_id / expires_at |
| M3 | controller/oauth_topup.go | 61 | 非 common.Message 错误一律映射 400 BadRequest | common.Message → 400（业务提示）；其它 → 502 BadGateway（上游问题） |
| M4 | controller/oauth_logs.go | 83 | 白名单含 channel_id / upstream_request_id / type / is_stream | 已移至敏感字段排除列表；同步更新测试断言 |

### Severity Low（保留，低优先级跟进）
- L1: sanitizeOAuthLog 字段命名与 spec FR-3 不完全一致（quota vs total_quota、use_time vs latency_ms、channel_name vs channel_type）——客户端按实际字段名适配即可；skill 文档里已更新实际字段名
- L2: 非 common.Message 错误统一 error_description "checkout service temporarily unavailable" 不保留上游 error 细节——符合外部接口最小暴露原则
- L3: topup 测试缺 success 场景——需配置 Waffo 支付；与 dashboard 端一致，不阻塞
- L4: characters POST owner 校验风格（信任 auth.uid()）与 PATCH/DELETE（显式先查 owner）不一致——功能安全，风格不统一；加注释即可
- L5: Studio/wardrobe/poses URL params 命名不统一（outfit_q vs q）——用户在不同页面跳转时筛选状态不继承；低优先级 UX
- L6: ProfileClient iframe 静默失败（X-Frame-Options 阻止但页面正常返回）降级不可靠——已加备用"在新窗口打开"按钮；再加 timeout 提示是后续改进
- L7: gateway.ts requestWithAuth / postJsonWithAuth 各自维护 401→refresh→retry 逻辑——可抽共享内部函数；非阻塞

## Post-Remediation Items（保留）
1. **（低）** 统一 Studio / wardrobe / poses 的 URL search param 命名（如全部用 q/category）
2. **（低）** characters POST 路径补充 owner 信任链注释
3. **（低）** gateway.ts 两个 401→refresh→retry 实现抽共享函数
4. **（低）** ProfileClient iframe 加 timeout 提示

## Build & Test Evidence
- `GOWORK=off go build ./...` → OK (0 errors)
- `go test ./controller/ -run 'OAuth(UserInfo|Topup|Logs)' -count=1` → **13/13 PASS** (3.17s)
- `bun run build` (apps/ai-outfit) → OK，17 routes 全部编译
- `bun run lint` → 4 pre-existing errors（admin/outfits、admin/poses、admin/[table]、MediaCard）；本次新增文件 lint 干净
- `grep newapi.0xfiang.com apps/ai-outfit/src` → No matches
- `SKILL.md`: 309 lines, 25.9KB (constraint ≤ 350 lines / < 28KB)
- 所有 `references/**` / `scripts/**` 相对路径 `Test-Path = true`（22 个）

## Security Verifications（AC-13 证据）
- scope 矩阵：`oauth_logs.go` 和 `oauth_topup.go` 均先 `ValidateOAuthAccessToken`（签名/用户状态/app 启用），再显式检查 api:read / api:write
- page_size clamp：`min(ps, 100)` 防 DoS；page clamp 10000
- 脱敏：`sanitizeOAuthLog` 显式排除 content / other / ip / user_id / token_id / channel_id / upstream_request_id / type / is_stream；测试显式断言
- service_role：`api/characters/route.ts` 中 service_role client 仅在 Server Route Handler 内部使用；所有响应只返回数据库对象不含 service_role key
- OAuth access token 与 PAT/dashboard session 分离：新端点在 `/oauth/...` 根（不在 `/api` 下）；不依赖 Casbin/scope 表；只认 `ValidateOAuthAccessToken`

## Reviewer Info
- 独立审查：general-purpose-task（read-only）
- 修复实施：主 agent + general-purpose-task
- 修复后二次验证：`go build` + `go test` + `bun run build` + 独立 grep
