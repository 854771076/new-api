# AI Outfit App - 功能完善与体验优化 Spec

## Overview
- **Summary**: 补齐换装工作室、服装库、姿势库的搜索与多维度筛选；实现"我的人物"真实上传；扩展 new-api `/oauth/userinfo` 带额度字段；新增 new-api `/oauth/topup/checkout` 与 `/oauth/logs` OAuth-access-token 端点，让外部 app 可以站内弹出充值 Modal 并展示消费日志；美化个人中心与导航栏；同步完善 new-api-oauth-integration skill 的外部 app 模式文档。
- **Purpose**: 把当前只能"登录 + studio 生成"的半成品 app 升级为可用产品，并补齐 new-api 对外部 OAuth client 的可消费能力（之前 `/oauth/userinfo` 只有 `sub/username/name/email`，完全没额度；充值跳仪表板；日志无接口）。
- **Target Users**: 使用 AI Outfit 换装工作室的普通用户、维护该外部 app 的开发者。

## Goals
- Studio 页面：支持搜索和 category/system/category/angle 多维度筛选，列表超过 12 条，布局更美观。
- /wardrobe、/poses：顶部关键词搜索 + 现有 tab 保持 + 可能的 angle/tag 多维度筛选。
- /characters：替换占位的真实图片上传（Route Handler + Supabase Storage + upsert characters），StudioClient 内可内嵌上传模态框。
- /profile：显示 quota / used_quota / request_count / group（来自扩展后的 `/oauth/userinfo`）；消费日志表格分页；充值按钮站内弹 Modal 打开 checkout_url（或 `window.open` 新窗口），不再跳 newapi 仪表板。
- 导航栏头像下拉中的 `https://newapi.0xfiang.com/...` 硬编码改为 `config.gatewayPublicUrl`。
- new-api 后端对 OAuth access token 开放额度、充值、日志三个能力（scope 受控）。
- new-api-oauth-integration skill 补齐上述外部 app 能力和实现规范。

## Non-Goals
- 不实现完整支付回调在 Supabase 侧的对账（充值单仍由 new-api 管理）；app 只调用 checkout 端点、消费 checkout_url。
- 不实现订阅、套餐、优惠券、affiliate 的外部 app 展示——这些仍跳仪表板或延后。
- 不重写 admin 控制台（antd/refine）——仅保留现有。
- 不更换 Supabase 表结构或新增数据库迁移，除非 upload Route Handler 所需的 policy 变更（应在同一个 init_schema 内已覆盖）。
- 不修改 new-api 主仪表板前端。

## Background & Context
- new-api 现有 `/oauth/userinfo`（controller/oauth_userinfo.go）只返回 `sub/username/name/email`，不包含任何额度字段。
- new-api 有完整的 `/api/user/self`（含 `quota/used_quota/request_count/group`）、`/api/user/waffo-pancake/pay`（返回 `checkout_url`）、`/api/user/topup/options`（返回 `amount_options`）、`/api/log/`（消费日志），但这些端点需要 dashboard session 或 PAT——OAuth access token 不能调（middleware/access_token_routes.go 的 scope 表对 dashboard endpoint 是另一个 namespace）。
- AI Outfit app 的 characters 表已存在、RLS 已启用、`media-user` bucket 已设置；Studio page 只拉 12 条服装 + 12 条姿势 + 用户所有人物，硬编码 limit。
- /profile 当前完全依赖跳转 new-api 仪表板，因为没接口可拿额度和日志。
- 导航栏 `layout.tsx` 里下拉菜单硬编码 `https://newapi.0xfiang.com/topup|log`，应改成 `config.gatewayPublicUrl + '/app.html#/topup|log'`。

## Functional Requirements

### FR-1: new-api `/oauth/userinfo` 扩展额度字段
- 当 OAuth access token 的 scope 集合包含 `api:read` 时，`GET /oauth/userinfo` 除了现有 `sub/username/name/email` 外，再返回 `quota`、`used_quota`、`request_count`、`group`。字段名保持蛇形或 camel 与现有 userinfo 一致（用 snake，贴合其他 external）。
- 当 scope 不含 `api:read` 时，不返回上述四个字段，保持向后兼容。

### FR-2: new-api `POST /oauth/topup/checkout`
- 接受 `Authorization: Bearer <oauth access token>`，scope 需包含 `api:write`（充值属于写操作）。
- 请求体 `{ "amount": 10 }`（或支持 `channel` 选择 waffo_pancake/waffo/epay/stripe，当前版本先只支持 waffo_pancake，最小可用）。
- 从 access token 解析 user id，调用现有 topup_waffo_pancake.go 里同款逻辑创建 TopUp 记录并返回 `{ "checkout_url": "...", "order_id": "...", "expires_at": 1730000000 }`。
- 不复制粘贴，复用现有的 payment generator 函数（抽一个内部包 `internal/oauthpay` 或直接在新 controller 里调用 waffo 包；避免跨 controller 硬耦合）。
- 端点要做新的 router 注册，path 在 `/oauth` 组下。scope 规则需要在 middleware/access_token_routes.go 的 OAuth access token 路由表里加上。

### FR-3: new-api `GET /oauth/logs`
- 接受 `Authorization: Bearer <oauth access token>`，scope 需包含 `api:read`。
- 查询该 user 的日志（现有的 log service 里有分页能力），只返回脱敏字段：`id, created_at, request_id, prompt_tokens, completion_tokens, total_quota, status, model, latency_ms, channel_type`——**绝不包含 prompt 原文、response 原文、任何上游 API key**。
- 支持 query 参数 `page`、`page_size`、`order=desc|asc`，默认 `page=1, page_size=20, order=desc`。

### FR-4: 导航栏硬编码域名修复
- `apps/ai-outfit/src/app/layout.tsx` 下拉菜单 `充值额度 ↗` 和 `消费日志 ↗` 的 href 从硬编码改为 `config.gatewayPublicUrl + '/app.html#/topup'` 和 `config.gatewayPublicUrl + '/app.html#/log'`。

### FR-5: /profile 页面升级
- 从扩展后的 `/oauth/userinfo` 读取额度（用 gateway access token）；无 token 时降级显示"无法获取，请重新登录"。
- 显示卡片：当前额度、已用额度、剩余额度金额（按默认 `QuotaPerUnit=500_000` 换算）、请求次数、用户所在 group。
- 新增"消费日志"表格（从新的 `/api/logs` BFF Route Handler 读取，分页）。
- 充值按钮改为**站内 checkout Modal**：点击后调 BFF `/api/topup` 拿到 `checkout_url`，弹出 Modal（DaisyUI `modal` + `<iframe src={checkoutUrl}>`）。完成支付后用户可关闭 Modal，profile 自动刷新额度（前端 refetch 或用户刷新页面）。如遇 iframe 被 Waffo 策略阻止，降级到 `window.open(checkoutUrl, '_blank', 'width=720,height=640')`。
- 去掉"积分规则说明 alert"里的跳转文案，把规则折叠进一个 `collapse`。

### FR-6: 新增三个 BFF Route Handler
- `POST /api/topup`：接受 Supabase session + gateway access token，调 new-api `/oauth/topup/checkout`（OAuth access token），返回 new-api 响应。
- `GET /api/gateway/userinfo`：接受 Supabase session + gateway access token，调 new-api `/oauth/userinfo`（OAuth access token，scope 要含 `api:read`），返回 new-api 响应。供 `/profile` 和 header 额度条复用。
- `GET /api/gateway/logs`：接受 Supabase session + gateway access token + query（page/page_size），调 new-api `/oauth/logs`，返回 new-api 响应。
- 所有 BFF 都要校验 Supabase session 合法、gateway access token 可用（401 时触发 refreshGatewayToken 后重试一次）。

### FR-7: /characters 真实上传
- 新增 Route Handler `POST /api/characters`：接受 `multipart/form-data`（`file`, `name`, `description` 可选），校验 size ≤ 8MB、类型 image/jpeg|png|webp，生成 `characters/{uuid}.{ext}` 路径，写 `media-user` bucket（service_role client），upsert characters 表。
- 新增 `DELETE /api/characters?id=`：删除 Storage 对象 + 数据库行（RLS 只允许 owner）。
- 页面 `/characters` 升级：显示 signed URL、重命名、删除、上传按钮、封面图；StudioClient 的左栏空状态仍可跳 `/characters`。
- 为 `/characters` 页面与 Studio 新增上传模态框（或组件）：drag-and-drop + click-to-upload + 进度 + 成功提示。

### FR-8: Studio 搜索与筛选 + 布局
- `page.tsx` 拉取全部或分页拉取服装/姿势，支持 `?outfit_q=&outfit_cat=&pose_q=&pose_sys=&pose_cat=&pose_angle=`。
- 新增 `GET /api/gateway/outfits?page=&page_size=&q=&category=` 和 `GET /api/gateway/poses?...` 可选（如果 studio page 还能用 Server Component 直接查 Supabase，就不需要 BFF；因为 outfits/poses 是公开表、RLS 允许 authenticated 查询）。让 server component 直接查，避免多一层 BFF。
- StudioClient 顶部加"搜索框 + 服装 category chip + 姿势 system chip + 姿势 angle chip"。
- 三栏里分别增加"去服装库 / 去姿势库"快捷按钮。
- 显示数量从 12 提到 24；支持分页或"显示更多"。
- 选中态更显眼：选中的卡片有明显的 border + ring + 勾选角标。

### FR-9: /wardrobe 与 /poses 搜索与多维度筛选
- 顶部加关键词搜索（`name_zh`, `name_en`, `keywords`, `description` 模糊，Supabase `ilike`）。
- 现有 tab 保留；服装新增按 `tags`/`is_active` 多筛选（如果 schema 已有 tags 列）；姿势新增按 `angle` 筛选。
- 搜索 + 筛选 + 分页可叠加（URL search params）。
- 空状态提示更友好："没找到？试试清除筛选条件"。

### FR-10: skill 文档同步升级
- 在 `new-api-oauth-integration/SKILL.md` 或 references 下补充：
  - OAuth access token 可调用的新端点：`/oauth/userinfo`（扩展）、`/oauth/topup/checkout`、`/oauth/logs`，以及各自 scope（`api:read` / `api:write`）和返回字段。
  - 外部 app 站内 topup 推荐模式：BFF Route Handler（access token 转发）+ Modal iframe / 窗口，不要调 `/api/user/self`（dashboard 专用）。
  - 人物上传 Route Handler 的标准实现（service_role → Storage → upsert characters；size/type 校验；RLS owner 限制）。
  - StudioClient / wardrobe / poses 推荐的搜索 + 筛选 + 分页布局（Server Component 直查 Supabase，避免多一层 BFF）。
- 注意 SKILL.md 体积约束（≤ 350 行，< 28KB），实现细节迁到 `references/standards/identity.md` 或 `billing.md` 或新建一个文档。
- 不新增 `docs/` 目录文档。

## Non-Functional Requirements
- **NFR-1**: new-api 后端改动必须保持零数据库迁移（全部走现有 User 字段和 payment 逻辑）。
- **NFR-2**: `/oauth/userinfo` 扩展后仍必须通过现有 `oauth_userinfo_test.go` 全部测试并追加新的 scope 字段测试。
- **NFR-3**: `/oauth/topup/checkout` 与 `/oauth/logs` 必须有单元测试（scope 缺失返回 403；access token 无效返回 401；正常返回字段结构正确）。
- **NFR-4**: 所有外部 app BFF Route Handler 必须**不向前端透传 service_role key** 或 new-api 原始 PAT。
- **NFR-5**: 上传 Route Handler 必须对文件大小、MIME、owner RLS 做服务器端校验（前端校验是 UX，Server 必须再次校验）。
- **NFR-6**: app 不得有未标注平台的 PowerShell 命令。
- **NFR-7**: 代码保持项目既有规范（Go 端用 `common.Marshal/Unmarshal`，不直接用 `encoding/json`；前端 Next.js App Router；Supabase Server Component 优先）。

## Constraints
- **Technical**: new-api 的 OAuth access token 与 dashboard PAT 是两种鉴权体系——access token 走 `OAuthAccessTokenMiddleware`，仅放行 `/oauth/*` 路径和白名单（middleware 有 scope 表）。新增的 `/oauth/topup/checkout` 与 `/oauth/logs` 必须注册到该中间件。
- **Technical**: `/api/user/waffo-pancake/pay` 是 dashboard route（auth middleware 不同），OAuth 端点不能直接调用 controller 函数，但可以在内部复用 payment generator 的逻辑（抽函数或内部包）。
- **Business**: 充值回调（支付成功后更新 TopUp 状态 + 给用户加 quota）仍由 new-api 的 webhook 处理器负责；外部 app 不参与结算。
- **Dependencies**: 外部 app 必须在 OAuth app 的 scope 列表里声明 `api:read` 才能拿到额度和日志；声明 `api:write` 才能发起充值。bootstrap 脚本需把这两个 scope 自动加进新 app 的 scopes 里。

## Assumptions
- new-api 当前已启用 waffo_pancake 支付（或 app 会在 demo 环境下自行启用）。
- AI Outfit OAuth app 的 `scopes` 字段可在 bootstrap.sh/.ps1 里加入 `api:read` 和 `api:write`。
- Supabase 的 `characters`、`outfits`、`poses` 表结构与 RLS policy 已满足 owner/authenticated 查询，无需再迁移。
- Waffo checkout_url 支持 iframe（如不支持，降级 window.open 方案仍可用）。

## Acceptance Criteria

### AC-1: /oauth/userinfo 返回额度字段（scope api:read）
- **Type**: `rule`
- **Pass Condition**: 当 token 带 `api:read` scope，`GET /oauth/userinfo` 返回 JSON 含 `quota`、`used_quota`、`request_count`、`group` 且值与 `GET /api/user/self` 对应字段一致（同一用户）；无 `api:read` scope 时四个字段不存在。
- **Evidence**: curl 请求 + 单元测试。

### AC-2: /oauth/topup/checkout 发起充值
- **Type**: `rule`
- **Pass Condition**: 带 `api:write` scope 的 token，POST `{ "amount": 10 }` 返回 200 + `{ checkout_url, order_id, expires_at }`；不带 `api:write` 返回 403；token 无效返回 401；amount<=0 返回 400。
- **Evidence**: curl + 单元测试；TopUp 记录表中有对应记录。

### AC-3: /oauth/logs 分页消费日志（脱敏）
- **Type**: `rule`
- **Pass Condition**: 带 `api:read` scope 的 token，GET `/oauth/logs?page=1&page_size=20` 返回 200 + 脱敏字段；不包含 prompt/response 原文。
- **Evidence**: curl + 单元测试；字段 grep 确认无敏感词。

### AC-4: AI Outfit profile 展示实时额度
- **Type**: `rule`
- **Pass Condition**: `/profile` 页面渲染后卡片显示 quota/used_quota/group；刷新后数据实时更新；无 gateway access token 时友好降级。
- **Evidence**: 浏览器页面截图 + 网络面板观察 `/api/gateway/userinfo` 请求。

### AC-5: 充值站内 Modal
- **Type**: `rule`
- **Pass Condition**: 点击 profile 的"充值"按钮，弹出 DaisyUI Modal，内部 iframe 或 window.open 打开 checkout_url；支付完成（或 iframe 被拦截时）有降级路径；不跳转到 newapi 仪表板。
- **Evidence**: 浏览器录制 / 手动点击演示。

### AC-6: /characters 真实上传 + 列表
- **Type**: `rule`
- **Pass Condition**: `/characters` 可上传 ≤8MB JPG/PNG/WEBP 图片，写入 media-user bucket，upsert characters；Studio 页面能选出上传人物。
- **Evidence**: 浏览器上传演示；Supabase Storage 控制台看到文件；数据库行存在。

### AC-7: Studio / wardrobe / poses 搜索 + 筛选 + 数量扩大
- **Type**: `rule`
- **Pass Condition**: 三个页面均有可见的顶部搜索框 + tab 筛选；URL search params 与筛选状态双向同步；列表 size 扩大到 ≥24 且显示"更多"或分页。
- **Evidence**: 浏览器页面 + URL bar 检查。

### AC-8: 导航栏硬编码修复
- **Type**: `rule`
- **Pass Condition**: `layout.tsx` 里不再出现 `newapi.0xfiang.com` 硬编码字符串；两处 href 均由 `config.gatewayPublicUrl` 拼接。
- **Evidence**: grep + `layout.tsx` 代码阅读。

### AC-9: 登录时拿到 `api:read` / `api:write` scope
- **Type**: `rule`
- **Pass Condition**: bootstrap 脚本把新建 OAuth app 的 scopes 设为 `openid profile email api:read api:write api:w`（按 skill 定义列表）；supabase provider scopes 同步更新。
- **Evidence**: bootstrap 脚本 diff；手动创建 app 后 `newapi-cli api GET /api/oauth-apps/<id>` 看到 scopes 包含 api:read/api:write。

### AC-10: Skill 文档同步升级
- **Type**: `rule`
- **Pass Condition**: `new-api-oauth-integration` skill 明确列出 `/oauth/userinfo` 扩展、`/oauth/topup/checkout`、`/oauth/logs` 的端点与 scope；SKILL.md 体积保持 ≤350 行、< 28KB；相对路径可达。
- **Evidence**: 行数、字节、Test-Path 校验。

### AC-11: 单元测试覆盖
- **Type**: `rubric`
- **Dimension**: Go 后端新/改端点的单元测试完备度
- **Scale**: 1-5
- **Anchors**: 1 = 没写测试；3 = 至少 happy path 一条；5 = 覆盖 scope 缺失、token 无效、参数校验、success；对扩展 userinfo 补 scope=有/无 两条。
- **Pass Threshold**: >= 4
- **Evidence**: `go test ./...` 输出 + 测试文件 diff。

### AC-12: AI Outfit 体验整体提升
- **Type**: `rubric`
- **Dimension**: UI/UX 体验（Studio 搜索筛选、人物上传、profile 美观度）
- **Scale**: 1-5
- **Anchors**: 1 = 占位 + 跳转；3 = 基本功能可走通但界面粗糙；5 = Studio 可搜索筛选、人物可上传管理、profile 额度/日志/充值一体化、整体布局美观、导航一致。
- **Pass Threshold**: >= 4
- **Evidence**: 浏览器截图 + 手动走查。

### AC-13: 安全不变量保持
- **Type**: `rubric`
- **Dimension**: 不泄露敏感数据（service_role、PAT、prompt 原文、上游 API key）
- **Scale**: 1-5
- **Anchors**: 1 = 明显泄露；3 = 大体安全但有隐患；5 = 所有 BFF 均不向客户端透传 service_role；/oauth/logs 脱敏；scope 矩阵正确；service_role 仅在 Server Component/BFF 内部。
- **Pass Threshold**: >= 5（安全项无宽松）
- **Evidence**: 代码 grep + review。

## Open Questions
- [ ] Waffo checkout_url 是否支持 iframe？实现时先写 `<iframe>`，若测试失败立即加降级 `window.open`。
- [ ] 当前 demo 环境的 waffo_pancake 是否已启用？如果没有，充值流程在 demo 中无法走通——但这不阻塞代码实现（功能按开关生效）。
- [ ] `api:write` 的含义和现有 scope 定义里 `api:w` 是否冲突？检查 `model/oauth_scope.go` 并统一（如旧 scope 有缩写问题就加新 scope 条目）。
