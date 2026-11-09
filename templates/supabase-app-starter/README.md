# Supabase App Starter（new-api 网关版）

> 一套**配置驱动**的业务脚手架。复制一份、填四个环境变量，就能拉起一款完整的小程序 / H5 App。
> 核心能力全部复用统一网关：OIDC 身份、AI relay + 计费、支付核心。
> 业务系统（Supabase）**不建独立账本**，所有额度/充值/订阅直接走网关。

```
┌──────────────────────────────────────────────────────┐
│                  业务 App（本脚手架）                  │
│  ┌─────────┐  ┌──────────────┐  ┌─────────────────┐ │
│  │ H5 / 小程序 │  │ Supabase Auth │  │ Supabase PostgREST│ │
│  │ React 模板  │  │ + Edge Funcs  │  │ + RLS + Storage  │ │
│  └─────┬─────┘  └──────┬───────┘  └────────┬────────┘ │
│        │ OAuth token   │ OIDC code         │ 业务数据   │
└────────┼───────────────┼───────────────────┼──────────┘
         │               │                   │
┌────────▼───────────────▼───────────────────▼──────────┐
│              new-api 统一网关（已部署）                 │
│  OIDC Provider  AI Relay（流式+计费）  支付核心（Stripe 等） │
└───────────────────────────────────────────────────────┘
```

## 1. 目录结构

```
supabase-app-starter/
├── package.json                # React 19 + Rsbuild + Tailwind 4
├── rsbuild.config.ts
├── tsconfig.json
├── index.html
├── .env.example                # ← 抄成 .env.local，填 4 个值
│
├── supabase/
│   ├── schema.sql              # 通用表（user_profiles / ai_generations）+ RLS
│   └── functions/
│       └── user-sync/          # Edge Function：首次登录时写 gateway_user_id
│
└── src/
    ├── main.tsx / App.tsx
    ├── lib/
    │   ├── config.ts           # 唯一配置入口
    │   ├── supabase.ts         # Supabase client 单例
    │   └── gateway.ts          # 网关 client（chat 流式 + 额度查询）
    └── pages/
        ├── Login.tsx           # 触发 OIDC 登录
        ├── AuthCallback.tsx    # 网关 → Supabase 回调，写 gateway_access_token
        ├── Profile.tsx         # 用户信息 + 网关额度
        ├── AIGenerate.tsx      # AI 生成（直连网关，流式）
        └── History.tsx         # 历史记录（Supabase PostgREST + RLS）
```

## 2. 4 步上线

### Step 1：网关侧配置 OAuthApp

在网关后台（或直接 API）创建一个新的 OAuthApp：

```bash
# 以管理员 token 调（或直接进网关后台 /api/oauth-apps）
curl -X POST $GATEWAY/api/oauth-apps \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-app",
    "redirect_uris": ["https://<project-ref>.supabase.co/auth/v1/callback"],
    "post_logout_redirect_uris": ["https://<project-ref>.supabase.co/auth/v1/callback"],
    "scopes": ["openid","profile","email","api:read","api:write"],
    "supabase_project_ref": "abcdefghij"
  }'
```

返回：**记下 `client_id`（形如 `nac_xxxxxxxxxxxxxxxx`）和 `client_secret`（只出现一次）**。

> 可选：给 App 配 `ai_model_whitelist`（细粒度 model_name，JSON 数组）——只有白名单内的模型会对这个 App 的 OAuth access_token 放行。默认不填 = 不限制。

### Step 2：Supabase Auth → Custom Provider

Supabase Dashboard → Authentication → Providers → 找 **OIDC** / **Custom** → 新增：

| 字段 | 值 |
|---|---|
| Provider URL / Well-known | `https://<gateway-host>/.well-known/openid-configuration` |
| Client ID | Step 1 拿到的 `client_id` |
| Client Secret | Step 1 拿到的 `client_secret` |
| Redirect URI | `https://<project-ref>.supabase.co/auth/v1/callback` |
| Scopes | `openid profile email api:read api:write` |

Signing Algorithm 保持 `RS256`（网关签发 id_token 用的就是 RS256）。

保存后，Supabase 就能作为客户端完整走通网关的授权码 + PKCE 流程。

### Step 3：Supabase Schema + Edge Function

```bash
# 1. 把 schema.sql 推到 Supabase（或直接在 SQL Editor 里执行）
supabase db push

# 2. 首次运行时把当前 App 的 client_id 写进 app_config（一次性）
#    （Schema 里 current_app_client_id() 从这张表取）
supabase sql "insert into public.app_config (client_id) values ('nac_xxxxxxxxxxxxxxxx')"

# 3. 部署 user-sync Edge Function
cd supabase/functions/user-sync
supabase functions deploy user-sync \
  --env-file <(echo GATEWAY_BASE_URL=https://gateway.example.com; echo GATEWAY_CLIENT_ID=nac_xxxxxxxxxxxxxxxx)

# 4. （可选）Supabase Auth → Hooks → Access Token Hook
#    把 user-sync 注册为 Access Token Hook，每次签发会话自动调用
#    不注册也能跑通——脚手架的 AuthCallback.tsx 里有兜底
```

### Step 4：前端模板填配置

```bash
cp .env.example .env.local
# 然后把 4 个值填上：
#   VITE_SUPABASE_URL     = https://<project-ref>.supabase.co
#   VITE_SUPABASE_ANON_KEY = supabase-anon-key
#   VITE_GATEWAY_BASE_URL  = https://gateway.example.com
#   VITE_GATEWAY_CLIENT_ID = nac_xxxxxxxxxxxxxxxx
```

然后改 `src/pages/Login.tsx` 里的 `OIDC_PROVIDER`（Supabase Custom Provider 的 slug，默认脚手架用 `newapi`）。

```bash
bun install         # 或 npm / pnpm
bun run dev         # http://localhost:5173
```

首次启动时脚手架会尝试把 `client_id` 写入 `app_config`（如果已存在则 upsert）。

---

## 3. 新增 App Checklist（无代码）

| # | 事项 | 在哪做 | 耗时 |
|---|---|---|---|
| 1 | 创建网关 OAuthApp，拿到 client_id + secret | 网关后台 | 5 min |
| 2 | Supabase Custom Provider 填 client_id/secret | Supabase Dashboard | 5 min |
| 3 | 把 `schema.sql` 推到新 Supabase 项目 | CLI / SQL Editor | 2 min |
| 4 | 写一条 `insert into app_config` 把 client_id 放进去 | SQL Editor | 1 min |
| 5 | 部署 user-sync Edge Function | CLI | 2 min |
| 6 | 填 `.env.local` 四个值 | 脚手架 repo | 1 min |
| 7 | `bun install && bun run dev`，验证登录 → AI → 历史 → Profile | 本地 | 5 min |
| 8 | （可选）App 级模型白名单 | 网关后台 OAuthApp | 1 min |

**总计约 25 分钟拉起一款全新 App，没有一行业务代码。**

## 4. 链路速查

```
① 用户在 App 点"登录"
   → Supabase /auth/v1/authorize?provider=newapi
   → 网关 /oauth/authorize（PKCE）
   → 用户在网关同意
   → 网关发 code → Supabase 换 id_token + access_token
   → Supabase 自己签会话 JWT（会话存 Supabase）
   → user-sync Edge Function 写 gateway_user_id + client_id 到 user_profiles
   → AuthCallback.tsx 把 gateway_access_token 写进 app_metadata

② App 直连网关 AI
   POST https://gateway/v1/chat/completions
   Authorization: Bearer <OAuth access_token>    ← 不是 Supabase JWT
   → middleware.OAuthTokenAuth 校验 + 注 user quota/group/client_id
   → controller.Relay: GenRelayInfo → 白名单检查（仅 OAuth token 入口）
   → relay 上游 provider → 网关按 user Quota 扣减（复用现有 billing）

③ App 业务数据（PostgREST）
   GET /ai_generations → Supabase 自动校验 auth.uid()，RLS 强制 client_id 匹配

④ 用户充值（沿用网关）
   App 跳转 网关 /app.html#/topup
   Stripe/Creem/Waffo/Epay → CompleteSubscriptionOrder → 用户 Quota 增加
   App 侧 Profile 页调 gateway /api/user/amount 轮询显示
```

## 5. 注意事项

- **App 级白名单默认不生效**：OAuthApp 不填 `ai_model_whitelist` 时，脚手架 App 能调该用户账户有权限的所有模型。要按 App 隔离，在网关后台给 OAuthApp 填 JSON 数组（每个元素是 model_name 精确值）。
- **网关直连要求业务域名在网关 CORS / 业务域名白名单里**；小程序也要在网关后台加业务域名。
- **Supabase schema.sql 里 RLS 用 `current_app_client_id()`** 从 `app_config` 取 client_id；多 App 共用同一个 Supabase 项目时改 `app_config.client_id` 即可切换隔离，不用改 SQL 策略。
- **脚手架代码里 `newapi`（OIDC provider slug）是默认值**，如果运营在 Supabase 后台给 Custom Provider 起了别的 slug，去 `src/pages/Login.tsx` 改一行常量。
