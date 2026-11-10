# AI Outfit App - 实现计划

## Task 1: new-api `/oauth/userinfo` 扩展额度字段
- **Status**: `completed`
- **Priority**: high
- **Depends On**: None
- **Completion Evidence**:
  - `controller/oauth_userinfo.go`: 当 scope 含 `model.OAuthScopeAPIRead` ("api:read") 时，附加 `quota / used_quota / request_count / group`。
  - `model/oauth_scope.go`: OAuthScopeAPIRead 描述更新为 "Read AI models, usage data, and your quota/usage on your behalf"。
  - `go test ./controller/ -run TestOAuthUserInfo -v` → 4/4 PASS（WithApiReadScope / WithoutApiReadScope / NoToken / InvalidToken）。
  - `grep -rn 'encoding/json' controller/oauth_userinfo.go` → 无命中（使用 `common.DecodeJson` 或 gin.H）。
  - bootstrap.sh/.ps1 已自动把 scopes 设为 `openid profile email api:read api:write`。

## Task 2: new-api `POST /oauth/topup/checkout`
- **Status**: `completed`
- **Priority**: high
- **Depends On**: None
- **Completion Evidence**:
  - 新 `controller/oauth_topup.go`：OAuthTopupCheckout 要求 `model.OAuthScopeAPIWrite` ("api:write")；amount<=0 → 400；token 无效 → 401；scope 缺失 → 403；non-common.Message 错误映射为 502 BadGateway；**已修**移除暴露自增主键的 `topup_id` 字段。
  - `controller/topup_waffo_pancake.go`：抽出 `generateWaffoPancakeCheckout(ctx, user, amount) -> (*waffoPancakeCheckoutResult, error)` 纯函数；原 dashboard handler 委托调用，行为不变。
  - `router/oauth-provider-router.go` 注册 `POST /oauth/topup/checkout`。
  - 1 个 scope 校验 + 2 个 token 校验 + 2 个参数校验 + 1 个 JSON 校验 测试全部 PASS。
  - success 场景留空（真实 Waffo 需要支付配置；dashboard 端也类似）。

## Task 3: new-api `GET /oauth/logs`
- **Status**: `completed`
- **Priority**: high
- **Depends On**: None
- **Completion Evidence**:
  - 新 `controller/oauth_logs.go`：OAuthLogsList，scope 需 api:read；page 默认 1、max 10000；page_size 默认 20、max 100；**已修** page clamp 防 startIdx 溢出；**已修** 脱敏白名单移除 `channel_id / upstream_request_id / type / is_stream`，避免暴露 channel 自增主键与内部请求追踪。
  - `router/oauth-provider-router.go` 注册 `GET /oauth/logs`。
  - 3 个测试全部 PASS：InvalidToken / ScopeForbidden / WhitelistNoSensitive（断言 channel_id/upstream_request_id/type/is_stream/content/other/ip/token_id/user_id 全部不存在）。

## Task 4: AI Outfit 导航栏硬编码 + 新增三个 BFF Route Handler
- **Status**: `completed`
- **Priority**: high
- **Depends On**: Task 1, Task 2, Task 3
- **Completion Evidence**:
  - `src/app/layout.tsx`：两处 `newapi.0xfiang.com` 硬编码替换为 `${config.gatewayPublicUrl}/app.html#/topup|/log}`。
  - `grep -rn 'newapi.0xfiang.com' apps/ai-outfit/src` → 无命中。
  - 新 `src/lib/gateway.ts`：`fetchGatewayUserinfo / fetchGatewayTopup / fetchGatewayLogs`，接受可选 accessToken；统一 401 → refreshGatewayToken → 重试 1 次。
  - 三个 Route Handler：`api/gateway/userinfo/route.ts`、`api/gateway/topup/route.ts`（amount min/max 校验）、`api/gateway/logs/route.ts`。
  - 全部用 `createServerSupabase().auth.getUser()` 校验 session；service_role / PAT 不向客户端返回。

## Task 5: /profile 页面升级（额度卡片 + 消费日志 + 充值 Modal）
- **Status**: `completed`
- **Priority**: high
- **Depends On**: Task 1, Task 4
- **Completion Evidence**:
  - SSR Server Component + Client Component `ProfileClient.tsx`。
  - DaisyUI cards：账户信息、额度（quota/used_quota/remaining in ¥ 按 QUOTA_PER_UNIT=500_000 换算）、消费日志 table。
  - 充值按钮 → POST `/api/gateway/topup` → DaisyUI `<dialog>` Modal + iframe src=`checkoutUrl` → 降级 `window.open(checkoutUrl, '_blank', 'width=720,height=640')` + 备用新窗口链接。
  - 积分规则 collapse。

## Task 6: /characters 真实上传 + Studio 快捷入口 + Studio 页面升级
- **Status**: `completed`
- **Priority**: high
- **Depends On**: None
- **Completion Evidence**:
  - 新 `src/app/api/characters/route.ts`：GET（RLS client 查询）/POST（multipart 校验：size ≤ 8MB、MIME ∈ image/jpeg|png|webp；service_role 写 Storage + upsert）/PATCH + DELETE（owner 先查后校验再删 Storage 对象 + DB 行）。
  - 新 `src/components/CharacterUploader.tsx`：'use client'、drag-drop + click + XHR upload progress + 预览；上传后 `router.refresh()`。
  - 重写 `/characters/page.tsx`（SSR 预取 signed URL + grid）和 `/characters/_components/CharactersClient.tsx`（客户端交互）。
  - 重写 Studio page + StudioClient：limit 24 + URL params 搜索/筛选 + 选中态 ring+✓ badge + 各栏"去服装库 / 去姿势库"链接 + CharacterUploader 内嵌 Modal。

## Task 7: /wardrobe 与 /poses 搜索 + 多维度筛选
- **Status**: `completed`
- **Priority**: medium
- **Depends On**: None
- **Completion Evidence**:
  - wardrobe：Server Component + `searchParams.q` 输入框 + 现有 category tab + 空状态"没找到？试试清除筛选条件"。
  - poses：Server Component + `q=` 输入框 + system tab + angle chips（distinct 查询）+ 空状态。
  - URL search params 双向同步（Server Component 读取 + `<form>` 提交）。

## Task 8: new-api-oauth-integration skill 文档同步
- **Status**: `completed`
- **Priority**: medium
- **Depends On**: Task 1, Task 2, Task 3, Task 4, Task 5, Task 6
- **Completion Evidence**:
  - SKILL.md：Step 0.3 追加 scopes 自动同步说明；Step 4 重写为"不要跳 newapi 仪表板"；309 行 / 25.9KB（约束 ≤ 350 行 / < 28KB ✅）。
  - identity.md 追加 OAuth 端点全集（三个新端点 + userinfo 扩展）。
  - billing.md 追加站内 topup 推荐模式 + 消费日志推荐模式。
  - supabase.md 追加人物上传 Route Handler 标准实现（size/MIME/owner 校验 + service_role 仅 Route Handler 内用）。
  - 新建 `references/standards/api-contract.md`（从 SKILL.md 下沉的分页 + 响应码规范）。
  - 22 个引用路径全部存在。

## Task 9: 全量回归 + 独立审查
- **Status**: `completed`
- **Priority**: high
- **Depends On**: Task 1–Task 8
- **Completion Evidence**:
  - `GOWORK=off go build ./...` → 通过
  - `go test ./controller/ -run 'OAuth(UserInfo|Topup|Logs)' -count=1` → **13/13 PASS**
  - `bun run build` → 通过，17 条 route（gateway/userinfo、gateway/topup、gateway/logs、characters、profile、studio、wardrobe、poses 等）
  - `grep newapi.0xfiang.com apps/ai-outfit/src` → 无命中
  - **独立审查**（agent review）结果 overall=pass，high=0；修复了 4 个 medium 中的关键 2 个：
    1. oauth_logs page clamp 10000 防 startIdx 溢出
    2. oauth_topup 移除暴露自增主键的 topup_id 字段；把非业务错误映射为 502
    3. oauth_logs 白名单移除 channel_id/upstream_request_id/type/is_stream（与脱敏目的一致）
  - 修复后重新跑 gofmt + go build + 13 个测试全部 PASS
  - 剩余 2 个 medium 为风格统一性（characters POST owner 注释、白名单字段命名），已在 review.md 里记录为低优先级改进；7 个 low 同

# Dependency Graph
- Task 1/2/3（后端） — Task 4 → Task 5 并行 Task 6/7（前端）
- Task 8 依赖 Task 1–7
- Task 9（回归 + 审查 + medium 修复）最后
