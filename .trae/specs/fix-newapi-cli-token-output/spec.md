# 修复 newapi-cli 令牌创建与输出可读性 Spec

## Why

newapi-cli 联调验收发现两个可用性缺陷：

1. `newapi-cli token create --name x` 返回 `null`，调用方（尤其是 AI agent）拿不到新建令牌的 `id` 与 `key`；且请求体只带 `name`，后端 `expired_time` 落库为 `0`（立即过期）、`remain_quota` 为 `0`，创建出的令牌不可用。
2. `--pretty` 表格平铺对象全部字段（含嵌套 JSON、空列），超长值不截断，输出过宽、难以阅读。

## What Changes

- 后端 `AddToken` 在成功响应中新增 `data` 字段，返回新建令牌的 `id`、`name`、`key`、`expired_time`、`remain_quota`、`unlimited_quota`；原有 `success` / `message` 保持不变（向后兼容，**非 BREAKING**）。
- CLI `token create` 新增 `--unlimited-quota`（默认 `true`）、`--quota`（默认 `500000`，仅非无限额度时生效）、`--expires-in`（默认 `0` 表示永不过期，映射 `expired_time = -1`），与 Web 控制台令牌创建默认值一致。
- CLI `--pretty` 表格渲染：单元格值超过 48 个字符时截断并以 `…` 结尾；嵌套对象/数组先序列化为紧凑 JSON 再截断；保持列对齐与 `total` / `page` / `page_size` 分页信息。
- 测试：`controller/token_test.go` 增补 `AddToken` 返回 `data` 的断言；新增 `cmd/newapi-cli/internal/output/output_test.go` 覆盖表格截断与分页输出。

## Impact

- Affected specs: newapi-cli（令牌管理、输出格式）
- Affected code:
  - `controller/token.go`（`AddToken` 响应）
  - `controller/token_test.go`（增补用例）
  - `cmd/newapi-cli/internal/commands/resources.go`（`token create` 参数）
  - `cmd/newapi-cli/internal/output/output.go` + `output_test.go`
- 不影响：Web 前端（新增 `data` 为附加字段）、relay 链路、令牌鉴权与计费逻辑、数据库结构。

## ADDED Requirements

### Requirement: 令牌创建返回创建结果

系统 SHALL 在 `POST /api/token/` 成功时返回新建令牌的可识别信息。

#### Scenario: 创建成功
- **WHEN** 已登录用户提交合法令牌创建请求
- **THEN** 响应除 `success` 与 `message` 外还包含 `data`，其中含新建令牌的 `id`、`name`、完整 `key`、`expired_time`、`remain_quota`、`unlimited_quota`

#### Scenario: 创建失败
- **WHEN** 请求非法（名称为空、额度越界、超出令牌上限）
- **THEN** 返回既有错误结构与文案，不包含 `data`

### Requirement: CLI 创建可直接使用的令牌

CLI SHALL 提供与 Web 控制台一致的令牌创建默认值，并允许覆盖。

#### Scenario: 默认创建
- **WHEN** 执行 `newapi-cli token create --name my-key`
- **THEN** 创建不限额度、永不过期（`expired_time = -1`）的令牌，并输出 `data` 中的 `id` 与 `key`

#### Scenario: 限定额度与有效期
- **WHEN** 执行 `token create --name k --unlimited-quota=false --quota 100000 --expires-in 24h`
- **THEN** 请求体传递 `unlimited_quota=false`、`remain_quota=100000`、`expired_time = 当前时间 + 24h`

### Requirement: CLI 可读输出保持紧凑

`--pretty` SHALL 在表格中对超长与嵌套值做截断，避免列宽失控。

#### Scenario: 长字段与嵌套字段
- **WHEN** 列表项包含长字符串或嵌套对象/数组
- **THEN** 单元格内容不超过 48 个字符（超出部分以 `…` 结尾），列仍按最大宽度对齐

#### Scenario: 分页信封
- **WHEN** 响应为 `{items, total, page, page_size}` 结构
- **THEN** 先输出表格，再输出三行分页信息

## MODIFIED Requirements

### Requirement: 令牌列表行为
列表接口继续对 `key` 做掩码展示，创建接口仅在创建成功响应中返回这一次完整 `key`；后续查询仍为掩码。

## REMOVED Requirements

无
