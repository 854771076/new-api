# 多租户（App）体系 Spec

## Why

外部系统（SaaS、分销平台、企业内部系统）希望把 new-api 当作 AI 网关底座，自行管理其终端用户的计费、API key、支付与任务记录，而不需要让每个终端用户都注册并登录 new-api 控制台。当前系统只有"平台用户"一种身份，缺少租户级隔离与面向集成的服务端管理凭证，外部系统无法安全地以程序化方式创建/管理归属自己的用户。

## What Changes

* 新增 **App（应用/租户）** 实体：平台管理员（Admin/Root）在控制台创建 App，App 是用户隔离边界。

* 新增 **App Admin Key**（`nak_` 前缀，SHA-256 哈希存储，仅创建时返回明文一次）：外部系统的服务端凭证，携带 scope（`users:read/write`、`api_keys:read/write`、`quota:read/write`、`logs:read`、`tasks:read`）。

* 新增集成 API 路由组 **`/api/app/v1`**：App Admin Key 鉴权，可创建/管理 App 旗下用户、签发用户 API key（sk- token）、查询/授予/扣减用户额度、查询用量日志与任务记录。所有数据访问强制按 `app_id` 隔离。

* `users` 表新增 `app_id`（默认 0 = 平台原生用户）与 `external_id`（外部系统用户 ID，`(app_id, external_id)` 唯一索引，平台用户为 NULL 以兼容三库唯一索引语义）。

* App 旗下用户复用现有 relay 链路（其 sk- token 走原有 `TokenAuth`），不改动计费、quota 预扣/结算核心逻辑。

* 新增控制台 App 管理页面（Admin）：App CRUD、Admin Key 签发/吊销、App 用户数概览。

* 交付 `sdk/openapi.yaml`（`/api/app/v1` 的 OpenAPI 3.1 描述）、`sdk/typescript/`（零依赖 fetch 薄封装 SDK）与 `skills/new-api-app-integration/`（供编码 agent 使用的集成 skill）。

* 支付集成方式：外部系统自收单，通过 quota grant 接口为用户入账；new-api 侧生成 `LogTypeTopup` 记录并可查询。**不**在 v1 复制 stripe/creem/waffo 收银台。

## Impact

* Affected specs: 用户体系、API key 体系、额度/账单、日志、任务记录、控制台管理界面

* Affected code:

  * 新增：`model/app.go`、`model/app_admin_key.go`、`middleware/app_auth.go`、`controller/app.go`（控制台管理）、`controller/app_api.go`（集成 API）、`router/app-router.go`

  * 修改：`model/user.go`（加 `AppId`/`ExternalId` 字段与建用户支持）、`model/main.go`（AutoMigrate 注册）、`router/api-router.go` 或主路由挂载、`web/src`（新增 App 管理页与路由）

  * 新增交付物：`sdk/openapi.yaml`、`sdk/typescript/`、`skills/new-api-app-integration/SKILL.md`

## ADDED Requirements

### Requirement: App 实体与管理（控制台）

系统 SHALL 提供 App 实体的创建、查询、更新、停用/删除能力，仅平台 Admin/Root 可用。App 字段：`id`、`name`、`slug`（唯一，URL 友好标识）、`status`（enabled/disabled）、`owner_user_id`（创建者）、`remark`、`created_at`、`updated_at`。

#### Scenario: 管理员创建 App

* **WHEN** Admin/Root 调用 `POST /api/app/` 提交 `{name, slug, remark}`

* **THEN** 创建 App 并返回完整对象；`slug` 重复时返回 409 语义错误；非管理员请求被拒绝

#### Scenario: 停用 App

* **WHEN** App 被置为 disabled

* **THEN** 该 App 所有 Admin Key 立即鉴权失败（401），但其旗下用户的 sk- token 与 relay 不受影响（App 是管理面隔离，不中断在网用户流量）

### Requirement: App Admin Key

系统 SHALL 支持为每个 App 签发多个 Admin Key。明文仅在创建响应中返回一次；库中只存 SHA-256 哈希与 8 字符 hint。Key 携带 scope 集合与可选过期时间，可被吊销（删除）。生成使用 `crypto/rand` 32 字节，格式 `nak_<base62>`。创建/吊销/使用失败写入审计日志（不含明文密钥）。

#### Scenario: 签发 Key

* **WHEN** Admin 调用 `POST /api/app/:id/keys` 提交 `{name, scopes, expires_at?}`

* **THEN** 返回一次性明文 `nak_...`、`token_ref`（哈希）与 hint；后续 `GET` 列表只返回 hint/scope/时间，永不返回明文

#### Scenario: 超 scope 访问被拒

* **WHEN** 仅持 `logs:read` 的 Key 调用 `POST /api/app/v1/users`

* **THEN** 返回 403，并写审计事件；Key 过期/不存在/被吊销返回 401

#### Scenario: App 间隔离

* **WHEN** App A 的 Key 尝试访问属于 App B 的用户（通过 ID 猜测）或平台用户（`app_id=0`）

* **THEN** 一律返回 404（不泄露存在性），任何查询路径都无法跨 App 读取数据

### Requirement: 集成 API — App 用户管理

系统 SHALL 在 `/api/app/v1` 下提供用户管理：`POST /users`（按 `external_id` 创建，可指定初始 quota、group、username 后缀去重）、`GET /users`（分页）、`GET /users/:id`、`PATCH /users/:id`（status/group/remark）。App 用户是正常 `users` 行（`app_id>0`），默认无密码、不可登录控制台，仅通过 API key 使用 relay。

#### Scenario: 创建 App 用户（幂等）

* **WHEN** 以已存在的 `external_id` 再次 `POST /users`

* **THEN** 返回 200 与既有用户（幂等返回），不重复建行；新 `external_id` 则创建并返回 201

#### Scenario: App 用户不可登录控制台

* **WHEN** App 用户尝试用户名密码登录

* **THEN** 失败（无密码凭据）；其仅能通过 App 签发的 sk- token 调用 `/v1` relay

### Requirement: 集成 API — 用户 API key（token）管理

系统 SHALL 提供 `POST /users/:id/tokens`（返回 sk- 明文一次）、`GET /users/:id/tokens`、`DELETE /tokens/:token_id`。创建的 token 归属目标 App 用户，字段与控制台创建一致（`remain_quota`、`expired_time`、`model_limits`、`group`），走既有 token 校验链路。

#### Scenario: 为 App 用户签发 sk- token

* **WHEN** `POST /api/app/v1/users/:id/tokens` 提交 `{name, remain_quota, expired_time, group?}`

* **THEN** 返回可用 sk- key，该 key 可直接调用 `/v1/chat/completions`；`:id` 不属于本 App 时返回 404

### Requirement: 集成 API — 额度（计费）管理

系统 SHALL 提供 `GET /users/:id/quota`（quota、used\_quota、request\_count）与 `POST /users/:id/quota/adjust`（`{delta, remark}`，正负 delta 表示授予/扣减，扣减不允许余额变负）。每次调整写 `LogTypeTopup`（授予）/对应管理日志，并带 `app_id` 与操作来源（admin key ref），可在既有日志体系检索。**BREAKING（无）**：不改动现有 quota 预扣/结算/退款逻辑。

#### Scenario: 外部支付完成后入账

* **WHEN** 外部系统收款后调用 `POST /users/:id/quota/adjust` `{delta: 100000, remark: "order#123"}`

* **THEN** 用户额度原子增加，产生一条含 remark 的 topup 日志，审计日志记录操作 key 指纹

#### Scenario: 扣减超限被拒

* **WHEN** `delta` 为负且绝对值大于用户当前余额

* **THEN** 返回 409 语义错误，余额不变，不产生日志

### Requirement: 集成 API — 用量日志与任务记录查询

系统 SHALL 提供 `GET /logs`（按 user\_id/类型/时间范围过滤，仅限本 App 用户）与 `GET /tasks`（本 App 用户的异步任务记录，含状态/计费摘要），分页参数与既有控制台接口一致。

#### Scenario: 查询本 App 用量

* **WHEN** `GET /api/app/v1/logs?user_id=..&type=consume&start_timestamp=..`

* **THEN** 仅返回该 App 旗下用户的日志；传入其他 App/平台用户 ID 时返回空集（或 404）

### Requirement: 控制台 App 管理页

系统 SHALL 在 Web 控制台为 Admin 提供 App 管理界面：App 列表/新建/编辑/停用，Admin Key 列表/签发（明文仅弹窗展示一次并支持复制）/吊销，以及每 App 用户数。UI 复用现有共享组件（Table、Dialog、CopyButton 等），文案走 i18n 七语言。

#### Scenario: 管理界面签发 Key

* **WHEN** Admin 在 App 详情页点击"签发密钥"并确认 scope

* **THEN** 弹窗展示一次性明文与复制按钮，关闭后不可再查看

### Requirement: SDK 与集成 Skill

系统 SHALL 交付 `sdk/openapi.yaml`（覆盖 `/api/app/v1` 全部端点）、`sdk/typescript/`（零依赖、基于 fetch 的类型化客户端，导出 `createAppClient({baseUrl, adminKey})`，覆盖用户/token/额度/日志/任务全部方法）与 `skills/new-api-app-integration/SKILL.md`（指导编码 agent 完成"创建 App→签发 key→创建用户→发 token→入账→查账"闭环）。

#### Scenario: 使用 SDK 完成集成闭环

* **WHEN** 外部系统按 skill 指引用 SDK 调用

* **THEN** 5 个调用内完成：建用户→发 sk- token→授予额度→查询用量日志

## MODIFIED Requirements

### Requirement: 用户模型

`users` 表新增 `app_id INT NOT NULL DEFAULT 0`（0=平台用户，索引）与 `external_id VARCHAR(128) NULL`；新增唯一索引 `(app_id, external_id)`（三库均允许同列多 NULL，平台用户不受影响）。注册、登录、既有管理接口行为不变；App 用户在用户列表对 Admin 可见并标注来源 App。

### Requirement: 认证与鉴权

新增 `middleware.AppKeyAuth()`：解析 `Authorization: Bearer nak_...`，SHA-256 查库，校验状态/过期/scope，注入 `app_id`、key id、scopes 到 gin context；失败统一 401（不区分 key 不存在/过期/停用 App）。不改动现有 session/PAT/sk- token 链路。

## REMOVED Requirements

无。

## 非目标（v1 明确不做）

* App 级余额池/分账（App 向平台预购额度再分配）——v1 由管理员直接管理平台额度，App 只管理其用户额度

* App 用户登录控制台 / App 自有 OAuth

* 出站 Webhook（用量事件推送）

* Go/Python SDK（先交付 TS SDK + OpenAPI，其他语言可由 OpenAPI 生成）

* Relay 链路按 App 维度限流/定价（v1 复用 user/group 体系）

