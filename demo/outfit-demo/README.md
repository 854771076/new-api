# Outfit Demo — 换装应用演示

基于 `supabase-app-starter` 模板，预填 Supabase 项目配置，可直接运行验证。

## 快速启动

```bash
cd demo/outfit-demo

# 1. 安装依赖
bun install

# 2. 配置环境变量（复制并修改）
cp .env.example .env.local
# 编辑 .env.local，把 VITE_GATEWAY_BASE_URL 改成你的 new-api 网关地址

# 3. 启动开发服务器
bun run dev
```

## 验证清单

- [ ] 登录页点击"使用 new-api 登录"跳转网关授权
- [ ] 授权后回调到 `/auth/callback` 并进入首页
- [ ] 个人中心显示用户信息和网关额度
- [ ] AI 生成页可以发送 prompt 并收到流式回复
- [ ] 历史记录页显示之前的生成记录

## 预置数据

Supabase 项目 `outfit-demo` 已包含：
- 8 件官方服装（上衣、下装、鞋子、配件）
- 完整的 RLS 策略（用户 + 应用双隔离）
- `user-sync` Edge Function（首次登录同步用户信息）

## 网关侧准备

在 new-api 管理后台创建 OAuth App：

| 字段 | 值 |
|------|-----|
| Name | outfit-demo |
| Client ID | `nac_demo_outfit_001` |
| Redirect URIs | `https://jknkenfhhwpxwwgqmcxv.supabase.co/auth/v1/callback` |
| Scopes | `openid profile email api:read api:write` |

## 目录结构

```
demo/outfit-demo/
├── src/
│   ├── lib/
│   │   ├── config.ts      # 环境变量配置
│   │   ├── supabase.ts    # Supabase 客户端
│   │   └── gateway.ts     # new-api 网关调用（AI + 额度）
│   ├── pages/
│   │   ├── Login.tsx      # 登录页（OIDC 跳转）
│   │   ├── AuthCallback.tsx  # 授权回调
│   │   ├── AIGenerate.tsx # AI 流式生成
│   │   ├── History.tsx    # 历史记录（Supabase RLS）
│   │   └── Profile.tsx    # 个人中心（额度 + 登出）
│   └── App.tsx            # 路由 + 导航
├── supabase/
│   ├── schema.sql         # 数据库 schema（含 RLS + seed）
│   └── functions/
│       └── user-sync/     # Edge Function：首次登录同步
└── .env.example           # 环境变量模板
```
