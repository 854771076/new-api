# Outfit Demo — 换装应用演示

基于 new-api OAuth + Supabase Auth custom provider 的参考应用，预填 Supabase
项目配置，可直接运行验证完整链路：OIDC 登录 → RLS 数据查询 → AI relay。

技术栈：Next.js App Router + TypeScript（strict）+ React 19 + Tailwind CSS v4 +
DaisyUI 5 + `@supabase/ssr`，包管理器为 bun。

## 快速启动

```bash
# 从仓库根目录进入参考应用
cd .agents/skills/new-api-oauth-integration/references/web/outfit-demo

# 1. 安装依赖
bun install

# 2. 配置环境变量
cp .env.example .env.local
# 编辑 .env.local：设置 NEXT_PUBLIC_GATEWAY_BASE_URL 和 NEXT_PUBLIC_GATEWAY_CLIENT_ID
# NEXT_PUBLIC_SUPABASE_URL / NEXT_PUBLIC_SUPABASE_ANON_KEY 已预填演示项目

# 3. 启动开发服务器（默认 http://localhost:3000）
bun run dev

# 生产构建 / 启动
bun run build
bun run start

# 只做类型检查
bun run typecheck
```

## 环境变量

| 变量 | 用途 |
|------|------|
| `NEXT_PUBLIC_SUPABASE_URL` | Supabase 项目地址，已预填 |
| `NEXT_PUBLIC_SUPABASE_ANON_KEY` | Supabase anon/publishable key，已预填，可进前端 |
| `NEXT_PUBLIC_GATEWAY_BASE_URL` | new-api 网关地址，例如 `http://localhost:3001` 或公网 HTTPS 地址（Next.js dev 默认占用 3000） |
| `NEXT_PUBLIC_GATEWAY_CLIENT_ID` | 网关「OAuth 应用」列表里的 `nac_...`，每个应用单独生成 |

`NEXT_PUBLIC_*` 会在构建时内联进浏览器包：修改后需要重启 dev server / 重新构建；
在 Vercel 上则需要在部署前设置好，否则构建产物里会是空值。

参考应用注册为**公共客户端**（`public_client`）：它没有 `client_secret`，依靠
PKCE 完成授权码交换。因此 `.env.local` 里没有、也不需要网关的客户端密钥；
Supabase provider 配置同样不应保存 secret。

Edge Function 的环境变量单独设置，不会打包进前端：

```bash
supabase secrets set \
  GATEWAY_BASE_URL=https://<公网网关地址> \
  GATEWAY_CLIENT_ID=<nac_...> \
  GATEWAY_OIDC_PROVIDER=custom:newapi \
  APP_ORIGIN=http://localhost:3000
```

## Supabase CLI 步骤

```bash
# 1. 登录并关联项目（project-ref 见 Supabase 控制台 Project Settings）
supabase login
supabase link --project-ref <project-ref>

# 2. 建表 + RLS：把 supabase/schema.sql 粘到 SQL Editor 执行；
#    或者放进 supabase/migrations/ 后执行
supabase db push

# 3. 部署首次登录同步的 Edge Function
supabase functions deploy user-sync

# 4. 设置函数 secrets（值见下方「环境变量」）
supabase secrets set GATEWAY_BASE_URL=... GATEWAY_CLIENT_ID=... \
  GATEWAY_OIDC_PROVIDER=custom:newapi APP_ORIGIN=http://localhost:3000

# 5. 本地栈 / 本地跑 Edge Function（可选）
supabase start
supabase functions serve user-sync --env-file supabase/.env.local
```

`supabase db reset` 只作用于本地栈，不要对已关联的远程项目执行。

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
（ServerAddress）完全一致。详见 `../../../SKILL.md` 的 Step 2.4（Public
client with PKCE / Supabase direct mode）。

## 部署到 Vercel

```bash
# 关联 Vercel 项目（首次会生成 .vercel/）
vercel link

# 从 Vercel 拉取环境变量（覆盖本地 .env.local）
vercel env pull .env.local

# 预览部署；正式发布用 vercel deploy --prod
vercel deploy
```

四个 `NEXT_PUBLIC_*` 变量都要在 Vercel 项目的 Environment Variables 里配置
（Production / Preview 都要），它们会在构建时内联。部署完成后，把新域名加入
网关 OAuth App 的额外允许来源（`APP_ORIGIN`）与 Supabase 的重定向白名单。

## 认证与会话

- 浏览器端用 `@supabase/ssr` 的 `createBrowserClient`，会话存放在 Cookie 中，
  这样 Server Component 也能读到同一个登录态。
- `proxy.ts`（Next.js 16 中是 `middleware.ts` 的新名字）在每次请求前调用
  `getUser()` 刷新会话 Cookie，Server Component 负责渲染前读取会话并做路由保护。
- 换装页、服装库、历史记录、个人中心的数据都在服务端按 RLS 读取；只有需要
  交互（选择服装、流式生成、退出登录）的部分是 Client Component。

## 令牌刷新

网关 access token 有效期 1 小时，且 Supabase 不会刷新 provider token。
`lib/gatewayAuth.ts` 会在调用 `/v1/*` 前用 `provider_refresh_token` 调网关
`/oauth/token` 换取新的 access token，并保存轮换后的 refresh token；
`invalid_grant` 表示刷新链已失效，需重新登录。
详见 SKILL.md 的「Token Lifecycle」与「Long-Running Work and Supabase Limits」。

## 验证清单

- [ ] `supabase functions deploy user-sync` 已执行
- [ ] 登录页点击「使用网关账号登录」跳转网关授权
- [ ] 授权后回调到 `/auth/callback` 并进入首页
- [ ] 个人中心显示用户信息和网关入口
- [ ] AI 换装页可以发送 prompt 并收到流式回复
- [ ] 历史记录页显示之前的生成记录
- [ ] 登录超过 1 小时后仍能调用 AI（access token 自动刷新）

## 预置数据

演示 Supabase 项目已包含：

- 8 件官方服装（上衣、下装、鞋子、配件）
- 完整的 RLS 策略（用户 + 应用双隔离）；`user_profiles` 对客户端只读，
  `gateway_user_id` 仅由 `user-sync`（service_role）写入
- 单行 `app_config`（`id = 1`，主键 + CHECK 约束）
- `seed_outfit_demo()` 仅 service_role 可执行
- `user-sync` Edge Function：`app/auth/callback/page.tsx` →
  `components/auth-callback.tsx` 用当前 Supabase access_token 调用，
  函数内部 `getUser(jwt)` 校验身份，缺失/无效 token 返回 401，且完全忽略请求 body
  中的用户身份字段

`.env.local` 已被 `.gitignore` 忽略，不要提交。

## 媒体资源

本 demo 里的 `image_url` 只是占位值，不代表存储方案。按统一规范，生产应用的所有
媒体资源（头像、用户上传图、生成的图片/视频、音频、PDF、附件、导出文件）必须存放在
**Supabase Storage** 的私有 bucket：数据库只保存 `bucket` + `path`
（`{client_id}/{user_id}/{uuid}.{ext}`），读取时由服务端生成短期 signed URL，
`storage.objects` 的 RLS 按 `client_id` 与 owner 限制访问，上传前后都要在服务端校验
MIME 与大小。禁止把 base64/二进制写进 Postgres、写到 Vercel/Next.js 本地磁盘，
或使用第三方图床/CDN 存用户内容。详见 `references/standards/supabase.md` 第 5 节。

## 积分、充值与订单

当前 demo **尚未打通** new-api 的钱包与订单数据：

- 没有读取 `quota` / `used_quota`，没有展示充值订单，也没有接订阅；
- `price_credits` 只是 UI 展示值，不是真实余额，也没有扣减逻辑；
- 「充值」按钮只是跳转网关 dashboard。

原因是 demo 走 Supabase direct mode，只有 OAuth access token，而 `/api/user/*`
不接受 OAuth token。要真正打通，三选一：

1. 用户在新-api dashboard 创建 PAT（`wallet:read`、`billing:read`、
   `usage:read`），由应用服务端保存并代理查询；
2. 使用与网关同站的 dashboard 会话，或 BFF 登录后转发 cookie；
3. 保持 OAuth-only：充值/订单/订阅一律跳转网关 dashboard，应用不本地记账。

生产应用必须按 `SKILL.md` 的 Step 4.7 执行：不建第二套余额或订单表，本地订单只做
`trade_no` 缓存，并以 `/api/user/topup/self` 对账。

## 目录结构

```
.agents/skills/new-api-oauth-integration/references/web/outfit-demo/
├── app/
│   ├── layout.tsx              # 导航 + data-theme + 服务端读取会话
│   ├── globals.css             # Tailwind v4 + DaisyUI
│   ├── page.tsx                # AI 换装（服务端取服装，客户端交互）
│   ├── wardrobe/page.tsx       # 服装库（分类筛选走 searchParams）
│   ├── history/page.tsx        # 历史记录
│   ├── profile/page.tsx        # 个人中心（网关入口 + 登出）
│   ├── login/page.tsx          # 登录（已登录直接回首页）
│   └── auth/callback/page.tsx  # 授权回调
├── components/
│   ├── outfit-generator.tsx    # 服装多选 + 流式生成 + 写回历史
│   ├── login-card.tsx          # provider: 'custom:newapi'
│   ├── auth-callback.tsx       # exchangeCodeForSession + user-sync
│   ├── logout-button.tsx       # 先 revoke 网关 refresh token
│   └── local-time.tsx          # 浏览器本地时区时间
├── lib/
│   ├── config.ts               # NEXT_PUBLIC_* 集中校验
│   ├── types.ts                # 业务行类型
│   ├── supabase/
│   │   ├── client.ts           # createBrowserClient（Cookie 会话）
│   │   └── server.ts           # createServerClient（Server Component）
│   ├── gateway.ts              # new-api 网关调用（AI 流式）
│   └── gatewayAuth.ts          # 令牌自动刷新（provider_refresh_token → /oauth/token）
├── proxy.ts                    # 会话 Cookie 刷新
├── supabase/
│   ├── schema.sql              # 数据库 schema（含 RLS + seed）
│   ├── schema.generic.sql      # 通用底座（无换装业务表）
│   ├── config.toml             # 本地 Supabase 配置
│   └── functions/
│       └── user-sync/          # Edge Function：首次登录同步
├── .env.example                # 环境变量模板
└── README.md
```
