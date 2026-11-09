# Checklist

- [x] `POST /api/token/` 成功响应的 `data` 含新令牌 `id` 与完整 `key`，`success` / `message` 结构未变
- [x] 创建失败分支（名称非法、额度越界、超出上限）仍返回原错误，且不含 `data`
- [x] Web 前端令牌创建流程未受影响（新增 `data` 为附加字段）
- [x] `token create --name x` 默认创建不限额度、`expired_time=-1` 的令牌
- [x] `token create` 支持 `--unlimited-quota`、`--quota`、`--expires-in` 且取值正确映射到请求体
- [x] CLI 输出包含新建令牌的 `id` 与 `key`
- [x] `--pretty` 表格单元格截断至 48 字符并保留列对齐与分页信息
- [x] 新增/更新的单元测试通过
- [x] `go build ./...`、`gofmt -l`、`go vet ./cmd/newapi-cli/...` 通过
