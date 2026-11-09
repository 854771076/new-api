# Tasks

- [x] Task 1: 后端令牌创建返回创建结果
  - [x] SubTask 1.1: `controller/token.go` 的 `AddToken` 成功响应新增 `data`，包含 `id`、`name`、`key`、`expired_time`、`remain_quota`、`unlimited_quota`
  - [x] SubTask 1.2: 在 `controller/token_test.go` 增补断言：成功响应含 `data.id` 与完整 `key`；失败分支无 `data`
- [x] Task 2: CLI 令牌创建参数与默认值
  - [x] SubTask 2.1: `cmd/newapi-cli/internal/commands/resources.go` 的 `token create` 增加 `--unlimited-quota`（默认 true）、`--quota`（默认 500000）、`--expires-in`（默认空 = 永不过期，映射 `expired_time=-1`）
  - [x] SubTask 2.2: 按参数组装请求体：无限额度时 `unlimited_quota=true`；否则传 `remain_quota`；`--expires-in` 为空时 `expired_time=-1`，否则取当前时间加时长
- [x] Task 3: CLI `--pretty` 输出紧凑化
  - [x] SubTask 3.1: `internal/output/output.go` 单元格截断到 48 字符（越界以 `…` 结尾），嵌套对象/数组转紧凑 JSON 后同样截断，保持列对齐与分页信息
  - [x] SubTask 3.2: 新增 `internal/output/output_test.go`，覆盖长字符串、嵌套对象、分页信封与对象键值输出
- [x] Task 4: 验证
  - [x] SubTask 4.1: `go build ./...`、`go test ./controller/ -run TestAddToken`、`go test ./cmd/newapi-cli/...`、`gofmt -l`、`go vet ./cmd/newapi-cli/...`
  - [x] SubTask 4.2: 实机联调：`token create` 输出 `id` 与 `key`，新建令牌可实际调用；`--pretty` 表格可读

# Task Dependencies

- Task 2 依赖 Task 1（需要创建接口返回 `id`/`key`）
- Task 4 依赖 Task 1、Task 2、Task 3
- Task 3 与 Task 1、Task 2 相互独立，可并行
