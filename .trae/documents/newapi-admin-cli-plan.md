# newapi-cli 管理专用 CLI 实现计划

## Context

用户希望有一个 newapi 管理专用 CLI，方便自己和 AI agent 维护管理 newapi 实例（用户、渠道、令牌、日志、设置等），不再需要打开 web 控制台。CLI 登录沿用 newapi 自身账号体系：浏览器登录后确认授权，颁发凭证给 CLI 使用。

已确认决策：
1. **授权方式**：浏览器登录 → 确认授权 → 页面显示一次性授权码 → 粘贴到 CLI → CLI 调交换端点换取 Personal Access Token (PAT)。管理 API 零鉴权改动，复用现有 PAT scope 体系。
2. **落点**：仓库内 `cmd/newapi-cli`，根 Go module 下独立 main package，纯 HTTP 客户端，不 import 后端业务包。
3. **首版范围**：`auth login/logout/status` + user/channel/token/log/option 结构化子命令 + 通用 `api` 兜底命令。
4. **交互**：打印 URL + 粘贴授权码（适配 agent/SSH 无浏览器场景）。

## 现状关键事实（已验证）

- 管理 API 认证只认两类凭证：浏览器 session 和 PAT（[middleware/auth.go](file:///c:/Users/Administrator/Desktop/projects/new-api/middleware/auth.go) `authHelper`）。
- PAT 体系完整：scope 目录 [service/access_token_scope.go](file:///c:/Users/Administrator/Desktop/projects/new-api/service/access_token_scope.go)（`accessTokenStaticResources` + Casbin `authz.Catalog()`，personal/admin/system 三组），路由级权限表 [middleware/access_token_routes.go](file:///c:/Users/Administrator/Desktop/projects/new-api/middleware/access_token_routes.go)，上限每用户 20 个（`AccessTokenMaxPerUser`）。
- PAT 管理端点已限定浏览器 session：`requireBrowserSession(c)`（[controller/access_token.go](file:///c:/Users/Administrator/Desktop/projects/new-api/controller/access_token.go) L60/L118），挂 selfRoute（`UserAuth` + `CriticalRateLimit` + `UserCriticalRateLimit("access-token")`，[router/api-router.go](file:///c:/Users/Administrator/Desktop/projects/new-api/router/api-router.go) L104-L114）。
- scope catalog 已有现成端点 `GET /api/user/self/access_tokens/catalog`（`controller.GetAccessTokenCatalog`），前端授权页直接渲染。
- 一次性码存储模板：[model/oauth_authorization_code.go](file:///c:/Users/Administrator/Desktop/projects/new-api/model/oauth_authorization_code.go)（SHA-256 hash 入库、原子消费、TTL、统一错误）。
- OAuth 签发的 token 仅用于 relay 侧 AI 接口，不能调管理 API——本方案不动它。
- 根 go.mod 无 spf13/cobra，需新增依赖。

## 授权链路设计

```
newapi-cli auth login --server https://host
  → CLI 打印 https://host/cli/authorize
用户浏览器打开（未登录先登录，回跳）
  → 授权页拉 catalog 渲染 scope 勾选（默认勾选 CLI 推荐集）
  → 确认 → POST /api/user/self/cli/auth_codes（session 限定）
  → 页面显示一次性 code（base64url 43 字符，256bit）
用户粘贴 code 到 CLI
  → CLI POST /api/cli/exchange {code}（无认证端点）
  → 服务端原子消费 code（存在/未过期/未使用）→ 按 code 内绑定的 name+scopes 创建 PAT
  → 返回 {token, expires_at}（审计只记指纹，不记明文）
  → CLI 写 ~/.newapi-cli/config.json（0600）
之后所有 CLI 请求带 Authorization: Bearer nap_***
```

**安全要点**：scope 由用户在浏览器端确认并绑定进 code，exchange 端点**不接受** scope/name 参数，CLI 无法自选提权；code 高熵（32 字节 base64url）、TTL 10 分钟、原子单次消费；颁发/交换双限流；所有失败统一 `invalid_grant` 防枚举；审计日志只记 token 指纹前 8 位。遵循 OWASP ASVS V3.5（token 单次使用/短过期）与 Authentication Cheat Sheet。

## 改动清单

### 后端（根 module）

1. **新文件 `model/cli_auth_code.go`**（仿 oauth_authorization_code.go）
   - 表 `cli_auth_codes`：`code_hash` varchar(64) 主键、`user_id` int not null index、`name` varchar(64)、`scopes` varchar(2048)（JSON 数组字符串）、`expires_at` bigint index、`consumed_at` bigint default 0、`created_at` bigint
   - `GenerateCLIAuthCode()` / `CLIAuthCodeHash()` / `CreateCLIAuthCode()` / `ConsumeCLIAuthCode()`（事务内 `UPDATE ... WHERE code_hash=? AND consumed_at=0` 原子消费，过期/重放/不存在分别报错）/ `DeleteExpiredCLIAuthCodes(before)`
   - TTL 常量 `CLIAuthCodeTTL = 10 * time.Minute`
   - [model/main.go](file:///c:/Users/Administrator/Desktop/projects/new-api/model/main.go) AutoMigrate 列表追加 `&CLIAuthCode{}`

2. **新文件 `controller/cli_auth.go`**
   - `CreateCLIAuthCode`（颁发）：`requireBrowserSession` → 解析 `{name, scopes}` → `service.NormalizeAccessTokenScopes(identity.UserID, role, scopes)` 防越权 → 生成 code 存 hash → 返回明文一次
   - `ExchangeCLIAuthCode`（交换）：限流 → `model.ConsumeCLIAuthCode` → 复用 PAT 创建路径（同 `CreateAccessToken` 内部：`common.GenerateRandomCharsKey` + 指纹入库 + 20 个上限校验）→ 审计 → 返回 `{token, expires_at}`；失败统一 `invalid_grant`
   - 复用现有 `writeAccessTokenError` / 审计写法，不新增平行实现

3. **`router/api-router.go` 挂载**
   - 颁发：`selfRoute.POST("/self/cli/auth_codes", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("cli-auth"), controller.CreateCLIAuthCode)`（挂在 accessTokenRoute 附近）
   - 交换：apiRouter 公开段 `POST /api/cli/exchange`，仅 `CriticalRateLimit()` + 匿名 body 限制（参照 userRoute 公开段写法 L79-L90）

4. **测试 `controller/cli_auth_test.go`**（唯一新测试文件，testify require/assert，表驱动）
   - 颁发成功/越权 scope 拒绝/非 session（PAT）调用拒绝
   - 交换成功、错误 code 统一 invalid_grant、过期、重放（第二次失败）
   - 所得 PAT 调受护路由：scope 内 200、scope 外 403

### 前端（web/）

5. **新页面 `web/src/pages/CLIAuthorize/index.tsx`** + 路由注册 `/cli/authorize`（登录守卫，未登录跳登录后回跳，参照现有页面守卫写法）
   - 结构：说明文案 + scope 勾选（拉 `/api/user/self/access_tokens/catalog`，默认勾选 CLI 推荐集：`usage:read`、`user:read/write`、`log:read`、`model:read/write`、`deployment:read/write`、root 用户加 `option:read/write`）+ 确认 → POST 颁发 → 显示 code
   - 复用 [copy-button.tsx](file:///c:/Users/Administrator/Desktop/projects/new-api/web/src/components/copy-button.tsx)、[confirm-dialog.tsx](file:///c:/Users/Administrator/Desktop/projects/new-api/web/src/components/confirm-dialog.tsx) 等现有组件，先搜索确认再组合，不新造通用组件
   - i18n：`t('English key')`，7 个 locale 文件（en/zh/zh-TW/fr/ru/ja/vi）全部补 key

### CLI（cmd/newapi-cli，纯 HTTP 客户端）

6. **目录结构**
   ```
   cmd/newapi-cli/main.go                 # cobra 入口
   cmd/newapi-cli/internal/config/        # ~/.newapi-cli/config.json，0600，多 profile（--profile / NEWAPI_CLI_PROFILE）
   cmd/newapi-cli/internal/client/        # http.Client 封装：Bearer 注入、{success,message,data} 解包、错误映射
   cmd/newapi-cli/internal/commands/      # auth/user/channel/token/log/option/api
   cmd/newapi-cli/internal/output/        # 默认 JSON 输出（agent 友好）；--pretty 表格
   ```
   - 引入 `spf13/cobra`（8+ 子命令带 flag，标准库 flag 两级分发成本高；只影响根 module，独立构建 `go build ./cmd/newapi-cli`）
   - `auth login --server <url> [--name xxx]`：打印授权 URL → 读粘贴 code → exchange → 写配置；`auth status` 显示当前 profile/server/user；`auth logout` 删本地凭证（可选同时调 PAT 删除端点吊销）
   - 资源子命令示例：`newapi-cli user list --p 1 --page-size 20`、`channel get 42`、`token create --name x`、`log list --type consume`、`option get ServerAddress` / `option update key=value`
   - 兜底：`newapi-cli api GET /api/user/?p=1`、`api POST /api/channel/ --data @channel.json`
   - 退出码：0 成功 / 1 通用错误 / 2 认证错误 / 3 用法错误；错误 JSON 输出到 stderr
   - CLI 单测：`internal/config`、`internal/client`（httptest 假服务端）

## 分阶段任务

1. `model/cli_auth_code.go` + AutoMigrate 注册 + model 单测
2. `controller/cli_auth.go` 颁发/交换 + 路由挂载 + `cli_auth_test.go`
3. 前端授权页 + 路由 + 7 locale
4. CLI 骨架：cobra 接线 + config/client/output
5. auth login/logout/status
6. user/channel/token/log/option 子命令 + `api` 兜底
7. 三库迁移验证 + 全链路手工验证

## 验证

- `go build ./...`、`go test ./controller/ ./model/`、`gofmt` 检查
- SQLite 启动本地实例，手工跑全链路：`auth login` → 浏览器授权页确认 → 粘贴 code → exchange 成功 → `api GET /api/user/self` 返回 200 → 重放同一 code 失败
- 新表三库验证：docker 起 MySQL ≥5.7.8 与 PostgreSQL ≥9.6 各跑两遍启动，确认 AutoMigrate 幂等、索引正确；记录版本与命令
- 前端 `bun run build` 通过，授权页手工确认 scope 勾选与 code 展示
- CLI 交叉编译冒烟：`GOOS=linux go build ./cmd/newapi-cli`

## 安全合规记录（OWASP）

- 参考：OWASP Authentication Cheat Sheet、Session Management Cheat Sheet、ASVS 4.x V3.5（一次性/短过期 token）
- 控制：code 256bit 熵、TTL 10min、原子单次消费、双端限流、统一 invalid_grant 防枚举、scope 服务端 normalize 防越权、PAT 指纹入库、审计脱敏、颁发端点 session 限定（PAT 不可自颁新码防权限放大）
