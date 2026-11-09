# Outfit Demo — 换装应用演示

基于 new-api OAuth + Supabase Auth custom provider 的参考应用，预填 Supabase
项目配置，可直接运行验证完整链路：OIDC 登录 → RLS 数据查询 → AI relay。

## 快速启动

```bash
# 从仓库根目录进入参考应用
cd skills/new-api-oauth-integration/references/web/outfit-demo

# 1. 安装依赖
bun install

# 2. 配置环境变量
cp .env.example .env.local
# 编辑 .env.local：设置 VITE_GATEWAY_BASE_URL 和 VITE_GATEWAY_CLIENT_ID
# VITE_SUPABASE_URL / VITE_SUPABASE_ANON_KEY 已预填演示项目

# 3. 启动开发服务器
bun run dev
```

## 环境变量

| 变量 | 用途 |
|------|------|
| `VITE_SUPABASE_URL` | Supabase 项目地址，已预填 |
| `VITE_SUPABASE_ANON_KEY` | Supabase anon/publishable key，已预填，可进前端 |
| `VITE_GATEWAY_BASE_URL` | new-api 网关地址，例如 `http://localhost:3001` 或公网 HTTPS 地址 |
| `VITE_GATEWAY_CLIENT_ID` | 网关「OAuth 应用」列表里的 `nac_...`，每个应用单独生成 |

参考应用注册为**公共客户端**（`public_client`）：它没有 `client_secret`，依靠
PKCE 完成授权码交换。因此 `.env.local` 里没有、也不需要网关的客户端密钥；
Supabase provider 配置同样不应保存 secret。

Edge Function 的环境变量单独设置，不会打包进前端：

```bash
supabase secrets set \
  GATEWAY_BASE_URL=https://<公网网关地址> \
  GATEWAY_CLIENT_ID=<nac_...> \
  GATEWAY_OIDC_PROVIDER=custom:newapi \
  APP_ORIGIN=http://localhost:5173
```

## 网关侧准备

在 new-api 管理后台创建 OAuth App（也可用配套的 `newapi-cli` skill）：

| 字段 | 值 |
|------|-----|
| Name | outfit-demo |
| Client ID | 创建后自动生成（形如 `nac_xxxxxxxx`），复制到 `.env.local` |
| 客户端类型 | 公共客户端（`public_client`，PKCE，无 `client_secret`） |
| Redirect URIs | `<SUPABASE_URL>/auth/v1/callback` |
| Scopes | `openid profile email api:read api:write` |

Supabase 侧的 custom provider 必须通过 Auth admin API 创建，标识必须为
`custom:newapi`（含 `custom:` 前缀），且 `issuer` 与网关后台「服务器地址」
（ServerAddress）完全一致。详见 `../../../SKILL.md` 的 Step 0 与
「Supabase Custom Provider Setup」。

## 令牌刷新

网关 access token 有效期 1 小时，且 Supabase 不会刷新 provider token。
`src/lib/gatewayAuth.ts` 会在调用 `/v1/*` 前用 `provider_refresh_token` 调网关
`/oauth/token` 换取新的 access token，并保存轮换后的 refresh token；
`invalid_grant` 表示刷新链已失效，需重新登录。
详见 SKILL.md 的「Token Lifecycle and Long-Running Tasks」。

## 验证清单

- [ ] `supabase functions deploy user-sync` 已执行
- [ ] 登录页点击「使用网关账号登录」跳转网关授权
- [ ] 授权后回调到 `/auth/callback` 并进入首页
- [ ] 个人中心显示用户信息和网关入口
- [ ] AI 生成页可以发送 prompt 并收到流式回复
- [ ] 历史记录页显示之前的生成记录
- [ ] 登录超过 1 小时后仍能调用 AI（access token 自动刷新）

## 预置数据

演示 Supabase 项目已包含：

- 8 件官方服装（上衣、下装、鞋子、配件）
- 完整的 RLS 策略（用户 + 应用双隔离）；`user_profiles` 对客户端只读，
  `gateway_user_id` 仅由 `user-sync`（service_role）写入
- 单行 `app_config`（`id = 1`，主键 + CHECK 约束）
- `seed_outfit_demo()` 仅 service_role 可执行
- `user-sync` Edge Function：`AuthCallback.tsx` 用当前 Supabase access_token 调用，
  函数内部 `getUser(jwt)` 校验身份，缺失/无效 token 返回 401，且完全忽略请求 body
  中的用户身份字段

`.env.local` 已被 `.gitignore` 忽略，不要提交。

## 目录结构

```
skills/new-api-oauth-integration/references/web/outfit-demo/
├── src/
│   ├── lib/
│   │   ├── config.ts         # 环境变量集中校验
│   │   ├── supabase.ts       # Supabase 客户端（会话持久化 + 自动刷新）
│   │   ├── gateway.ts        # new-api 网关调用（AI 流式）
│   │   └── gatewayAuth.ts    # 令牌自动刷新（provider_refresh_token → /oauth/token）
│   ├── pages/
│   │   ├── Login.tsx         # 登录页（provider: 'custom:newapi'）
│   │   ├── AuthCallback.tsx  # 授权回调（exchangeCodeForSession + user-sync）
│   │   ├── OutfitGenerate.tsx # AI 换装（流式生成 + 写回）
│   │   ├── Wardrobe.tsx      # 服装库（PostgREST + RLS）
│   │   ├── History.tsx       # 历史记录
│   │   └── Profile.tsx       # 个人中心（网关入口 + 登出）
│   ├── App.tsx               # 路由 + 导航
│   └── main.tsx              # 入口
├── supabase/
│   ├── schema.sql            # 数据库 schema（含 RLS + seed）
│   ├── schema.generic.sql    # 通用底座（无换装业务表）
│   ├── config.toml           # 本地 Supabase 配置
│   └── functions/
│       └── user-sync/        # Edge Function：首次登录同步
├── .env.example              # 环境变量模板
└── README.md
```
