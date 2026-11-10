# Checklist

- [x] `cmd/newapi-cli` 跨平台核查结论已记录：配置路径、npm 平台映射、文件权限在 Linux/macOS 下均成立，无 PowerShell 依赖
- [x] `newapi-cli` skill 的 Install 段落同时说明 Windows `.exe` 与 macOS/Linux 构建/安装方式及 PATH 提示
- [x] `newapi-cli` skill 内示例命令均为 POSIX 可执行形式，无未标注平台的 PowerShell 专属命令
- [x] `scripts/bootstrap.sh` 存在，参数为 `--project-name` / `--client-id` / `--pack-root` / `-h|--help`
- [x] `bootstrap.sh` 与 `bootstrap.ps1` 步骤一一对应：工具链检查、auth status、self+apps、复用 public client、link、api-keys、`.env.local`、db push、provider 幂等、authorize 冒烟、首管 UUID、可选 seed
- [x] `bootstrap.sh` 在缺少登录或可用 OAuth app 时以非 0 退出并打印修复命令
- [x] `bootstrap.sh` 写入 `.env.local` 时限制文件权限，且不打印 service_role key 明文
- [x] `bash -n scripts/bootstrap.sh` 通过；无 Windows 专属路径或 `ConvertTo-Json` 等残留
- [x] 存在 `references/standards/identity.md`，覆盖 OAuth 代码流程、Supabase provider 配置与令牌刷新实现
- [x] 存在 `references/standards/relay.md`，覆盖 BFF 转发、日志/重试规则与直连模式
- [x] 存在 `references/standards/billing.md`，覆盖 userinfo 字段、quota 单位、PAT、支付通道、订阅与管理员接口
- [x] 存在 `references/standards/security.md`，包含完整安全清单与 Token Lifecycle
- [x] 存在 `references/standards/local-acceptance.md`，包含 tunnel 验收流程与两种 shell 的 redirect 追加写法
- [x] `SKILL.md` 行数 ≤ 350 且字节数 < 28KB（实测 292 行 / 24,231 字节）
- [x] `SKILL.md` 无单章节超过 40 行
- [x] `SKILL.md` 中所有 `references/**`、`scripts/**` 相对路径均存在，无死链
- [x] `SKILL.md` 参考索引表含「何时阅读」列，并列出新增的五份 reference
- [x] `SKILL.md` Step 0 以并列形式给出 Windows 与 macOS/Linux 的 check-env 与 bootstrap 命令，无 PowerShell 专属标题
- [x] `SKILL.md` 的 tunnel redirect 追加示例同时提供 POSIX 与 PowerShell 版本
- [x] 瘦身后 OAuth 流程约束、凭据面隔离、令牌生命周期、安全清单、计费口径、scope 表、模型白名单均仍可从主文件或其直接引用获取
- [x] 资产入库细节（bucket、对象键、表与冲突键、pack 目录、凭证）已补入 `references/standards/supabase.md`，主文件保留入口链接
- [x] 未新增 `docs/` 或 `plugins/` 下的文档，未改动受保护的项目标识
- [x] 本次改动仅涉及 skill 文档与脚本，已确认无 Go/前端/数据库行为变更

## 验证记录

- `bash -n scripts/bootstrap.sh`：通过（WSL bash，退出码 0）
- `shellcheck`：当前环境未安装，无法执行；已人工复核引号、`local`、`cd` 保护与 `read -r` 等 warning 级问题
- 文档巡检（排除 `references/vendor/`）：`ConvertFrom-Json|ConvertTo-Json|Invoke-RestMethod|Invoke-WebRequest|Set-Content|Get-Command|winget|$env:` 仅命中 `.ps1` 脚本本体与明确标注 Windows 的可选示例块，且均有 POSIX 对照
- 基线审计：`git merge-base origin/main HEAD` = `8faf1d45`；改动文件为两个 skill 的 Markdown 与 `scripts/bootstrap.{ps1,sh}`，无 Go/前端/数据库文件
