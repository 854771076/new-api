# Tasks

- [x] Task 1: 核查 CLI 与文档的 Linux shell 支持并补齐 `newapi-cli` skill
  - [x] SubTask 1.1: 复核 `cmd/newapi-cli` 无 PowerShell/Windows-only 依赖（`os.UserHomeDir`、npm `platform.js`、config 权限 0600/0700），记录结论
  - [x] SubTask 1.2: 在 `.agents/skills/newapi-cli/SKILL.md` 的 Install 段落补充 macOS/Linux 说明：`go build -o newapi-cli ./cmd/newapi-cli`（Windows 为 `.exe`）、npm 全局安装后的 PATH 位置、配置文件路径在三个平台均可用
  - [x] SubTask 1.3: 检查该 skill 内所有示例命令均为 POSIX 可执行形式，若存在 PowerShell 专属写法则补充 bash 版

- [x] Task 2: 新增 `scripts/bootstrap.sh`（POSIX bash，与 `bootstrap.ps1` 对齐）
  - [x] SubTask 2.1: 解析参数 `--project-name` / `--client-id` / `--pack-root` / `-h|--help`，非法参数退出码 2
  - [x] SubTask 2.2: 实现步骤 1–6：工具链检查、`newapi-cli auth status`、读取 self user 与 OAuth apps、复用 `public_client && supabase_project_ref`、`supabase link`、`supabase projects api-keys` 解析、写 `.env.local`（`umask 077`）
  - [x] SubTask 2.3: 实现步骤 7–10：`supabase db push --remote --yes`、`custom:newapi` provider 幂等检查/重建（curl + 不依赖 jq 的解析或显式声明 jq 依赖）、authorize 302 冒烟、`auth.users` UUID 映射并 upsert `app_admins`
  - [x] SubTask 2.4: 实现步骤 11：`--pack-root` 存在时执行 `bun run scripts/seed-assets.ts`；结束打印后续手工步骤
  - [x] SubTask 2.5: `chmod +x` 可执行位（以 `bash scripts/bootstrap.sh` 调用为主，避免依赖 exec bit），`bash -n` 语法校验通过

- [x] Task 3: 主 `SKILL.md` 按需引用重构
  - [x] SubTask 3.1: 新建 `references/standards/identity.md`，迁入 Step 2 的 BFF Route Handler 代码、Supabase custom provider 配置细节、PKCE refresh 实现与 gatewayAuth 行为表
  - [x] SubTask 3.2: 新建 `references/standards/relay.md`，迁入 Step 3 的 BFF 代理代码、日志与重试规则表、直连模式
  - [x] SubTask 3.3: 新建 `references/standards/billing.md`，迁入 Step 4 的 userinfo 字段、quota 单位、PAT、会话、支付通道/订阅/管理员接口表格与 Step 4.7 全部规则
  - [x] SubTask 3.4: 新建 `references/standards/security.md`，迁入安全清单全量条目与 Token Lifecycle 细节
  - [x] SubTask 3.5: 新建 `references/standards/local-acceptance.md`，迁入 tunnel + Supabase 本地验收流程（含 PowerShell 与 POSIX 两种 redirect 追加写法）
  - [x] SubTask 3.6: 重写 `SKILL.md` 主体：保留定位、技术栈、凭据面矩阵、模式选择、Step 0–8 的契约级要点（每步 5–15 行）、scope 表、模型白名单，其余指向 references
  - [x] SubTask 3.7: 重建「参考资料索引」表，增加「何时阅读」列，覆盖 standards 与 scripts

- [x] Task 4: Step 0 与 tunnel 章节双平台化
  - [x] SubTask 4.1: 0.1/0.2/0.3/0.4 改为 Windows 与 macOS/Linux 并列命令，删除「（PowerShell）」标题
  - [x] SubTask 4.2: 将 tunnel redirect 追加示例拆成 POSIX（`jq`）与 PowerShell 两个代码块
  - [x] SubTask 4.3: 保持 `check-env.ps1` / `check-env.sh` 说明与实际能力一致（含 `--install` 的平台差异与失败退出码）

- [x] Task 5: 验证与收尾
  - [x] SubTask 5.1: `bash -n scripts/bootstrap.sh`、`shellcheck -S warning`（可用时）通过；无 Windows 路径/内置命令残留
  - [x] SubTask 5.2: 统计 `SKILL.md` 行数与字节数，确认 ≤ 350 行且 < 28KB
  - [x] SubTask 5.3: 脚本校验 `SKILL.md` 中所有 `references/**` 与 `scripts/**` 相对路径存在
  - [x] SubTask 5.4: 巡检 `.agents/skills`（排除 vendor）不再出现未标注平台的 PowerShell 专属命令
  - [x] SubTask 5.5: 对照基线审计本次改动（文档与脚本，无 Go/前端/数据库变更），记录结论

- [x] Task 6: 修复审计发现的文档细节丢失
  - [x] SubTask 6.1: 在 `references/standards/supabase.md` 补回资产入库章节（bucket、对象键、表与冲突键、pack 目录结构、凭证要求、跨平台说明）
  - [x] SubTask 6.2: `SKILL.md` Step 5 增加指向该章节的链接
  - [x] SubTask 6.3: 修复 `bootstrap.ps1` 在 `-ClientIdOverride` 下未解析 `supabase_project_ref` 的问题，与 `bootstrap.sh` 行为一致

# Task Dependencies

- Task 2 与 Task 3 相互独立，可并行
- Task 4 依赖 Task 3（重构后的章节位置）
- Task 5、Task 6 依赖 Task 1、Task 2、Task 3、Task 4
