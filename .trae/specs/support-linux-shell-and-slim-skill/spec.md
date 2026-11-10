# Linux Shell 支持与主 Skill 瘦身 Spec

## Why

1. `new-api-oauth-integration` 与 `newapi-cli` 两个 skill 的文档和脚本目前偏 Windows/PowerShell：一键 bootstrap 只有 `bootstrap.ps1`，暴露 tunnel 回调的示例是 PowerShell 片段，导致 macOS/Linux 使用者只能逐条手工改写命令。
2. 主 `SKILL.md` 已达 1409 行 / 约 68KB，超过文件读取工具 64KB 上限，agent 无法一次读入，且大量细节与 `references/standards/*.md` 重复，维护时容易两处失配。

## What Changes

- **只读核查 CLI 跨平台性**：`cmd/newapi-cli` 本体已跨平台（`os.UserHomeDir`、npm `platform.js` 映射 `linux`/`darwin`、无 PowerShell 依赖），本次不修改 CLI 代码，只在 skill 文档中如实说明 Linux/macOS 用法与 `.exe` 差异。
- 新增 `scripts/bootstrap.sh`：与 `bootstrap.ps1` 功能一一对应的 POSIX bash 实现，参数用 `--project-name` / `--client-id` / `--pack-root`。
- `SKILL.md` 的 Step 0 bootstrap 章节改为「Windows PowerShell / macOS-Linux bash」双入口，删除 PowerShell 专属标题。
- 主 `SKILL.md` 的 tunnel 回调追加示例补充 POSIX 版（`jq` 实现 GET→追加→PUT），保留 PowerShell 版。
- **主 SKILL.md 瘦身（按需引用）**：把实现级细节迁入 `references/standards/`，主文件只保留契约、决策点、必守规则与「何时阅读哪份资料」索引。
  - 新增 `references/standards/identity.md`（OAuth/OIDC 详细代码与 Supabase provider 配置细节）
  - 新增 `references/standards/relay.md`（BFF 转发实现、日志规则、直连模式）
  - 新增 `references/standards/billing.md`（userinfo 字段、quota 单位、PAT、支付通道、订阅、管理员接口）
  - 新增 `references/standards/security.md`（安全清单全量条目、令牌生命周期细节）
  - 新增 `references/standards/local-acceptance.md`（Cloudflare tunnel + Supabase 本地验收流程）
- 目标体积：`SKILL.md` ≤ 350 行且 < 28KB，单章节 ≤ 40 行；所有引用路径真实存在。
- `newapi-cli` skill 增加 Linux/macOS shell 说明：`~/.newapi-cli/config.json`、`go build -o newapi-cli`（Windows 为 `.exe`）、npm 全局安装在 Linux 下的 PATH 提示。

## Impact

- Affected specs：外部应用 Web 开发规范（`define-external-app-web-standard` 已交付内容的行为不变，仅重组文档与新增脚本）。
- Affected code：
  - `.agents/skills/new-api-oauth-integration/SKILL.md`
  - `.agents/skills/new-api-oauth-integration/references/standards/{identity,relay,billing,security,local-acceptance}.md`（新增）
  - `.agents/skills/new-api-oauth-integration/scripts/bootstrap.sh`（新增）
  - `.agents/skills/newapi-cli/SKILL.md`
- 不涉及 Go / 前端 / 数据库代码，无数据库兼容性与迁移影响。

## ADDED Requirements

### Requirement: POSIX 一键 bootstrap

系统 SHALL 在 `new-api-oauth-integration` skill 中提供 `scripts/bootstrap.sh`，使 macOS/Linux 用户无需手工翻译 PowerShell 命令即可完成同等初始化。

#### Scenario: Linux 上完整初始化

- **WHEN** 用户在已登录 `newapi-cli` 的 Linux/macOS 环境中于 Next.js 项目根执行 `./scripts/bootstrap.sh --project-name "My App"`
- **THEN** 脚本依次完成工具链检查、读取 self user 与 OAuth apps、复用带 `supabase_project_ref` 的 public client、`supabase link`、拉取 anon/service_role key、写 `.env.local`、`supabase db push`、幂等创建 `custom:newapi` provider、authorize 冒烟测试、`app_admins` 首管写入

#### Scenario: 缺少前置条件

- **WHEN** `newapi-cli` 未登录或不存在带 `supabase_project_ref` 的 public client
- **THEN** 脚本以非 0 退出码结束，并打印可直接执行的修复命令

#### Scenario: 可选素材入库

- **WHEN** 传入 `--pack-root <dir>`
- **THEN** 脚本在其余步骤完成后执行 `bun run scripts/seed-assets.ts --pack-root <dir> --client-id <id>`；未传入时跳过

### Requirement: 两个平台的脚本参数与行为对齐

`bootstrap.sh` 与 `bootstrap.ps1` SHALL 暴露等价能力：项目名、手动指定 OAuth app id、可选素材根目录；任一参数缺失时的默认行为一致。

#### Scenario: 参数等价性

- **WHEN** 同一项目分别用 `-ProjectName` / `-ClientIdOverride` / `-PackRoot` 与 `--project-name` / `--client-id` / `--pack-root` 执行
- **THEN** 生成的 `.env.local` 变量集合与 `app_admins` 写入逻辑一致

### Requirement: Shell 无关的文档命令

两个 skill 的可执行命令 SHALL 为 shell 无关形式，或同时给出 POSIX 与 PowerShell 两种写法；不得只给 PowerShell。

#### Scenario: 文档巡检

- **WHEN** 在 `.agents/skills` 下（排除 `references/vendor/`）检索 `ConvertFrom-Json` / `$env:` / `Invoke-RestMethod` / `Set-Content`
- **THEN** 命中仅出现在明确标注为 Windows/PowerShell 的可选示例块中，且同一操作都有 POSIX 等价写法或说明

### Requirement: 主 Skill 按需引用

主 `SKILL.md` SHALL 只保留定位、技术栈、凭据矩阵、模式选择、安全不变量、scope 与模型白名单等契约级内容，实现细节 SHALL 由 `references/standards/` 承载，并在参考索引中标注阅读时机。

#### Scenario: 体积与结构约束

- **WHEN** 检查 `SKILL.md`
- **THEN** 行数 ≤ 350、字节数 < 28KB，且不包含超过 40 行的单章节

#### Scenario: 引用可达

- **WHEN** 校验 `SKILL.md` 中出现的每个 `references/...` 与 `scripts/...` 相对路径
- **THEN** 文件均存在，无死链

#### Scenario: 契约不丢失

- **WHEN** 对比瘦身前后的规范内容
- **THEN** OAuth/OIDC 流程约束、凭据面隔离、令牌生命周期、安全清单、计费口径、scope 表与模型白名单全部仍可在主文件或其直接引用中查到

## MODIFIED Requirements

### Requirement: Step 0 环境自举

自举章节 SHALL 以同等权重给出两条路径：

- Windows：`./scripts/check-env.ps1 -Install`、`./scripts/bootstrap.ps1 -ProjectName "..."`、`$env:NAME = '...'`
- macOS/Linux：`./scripts/check-env.sh --install`、`./scripts/bootstrap.sh --project-name "..."`、`NAME=... ./scripts/check-env.sh`

`newapi-cli` 仍以验证为主、安装为辅（npm 全局安装或源码构建），不强制由脚本安装。

#### Scenario: 自举入口可发现

- **WHEN** 使用者在 Step 0 阅读命令
- **THEN** 无需跨平台翻译即可找到与自身 shell 匹配的检查与 bootstrap 命令

### Requirement: 参考资料索引

索引表 SHALL 为每份 reference 增加「何时阅读」列，并新增 identity / relay / billing / security / local-acceptance 五个条目。

#### Scenario: 按需加载

- **WHEN** agent 只关心计费接入
- **THEN** 索引可直接指向 `references/standards/billing.md`，无需通读主文件全部细节

## REMOVED Requirements

### Requirement: PowerShell 专属 bootstrap 章节

**Reason**：单一 PowerShell 入口对 macOS/Linux 不可用，且标题暗示平台限制。

**Migration**：改为双平台小节，PowerShell 内容保留，新增 bash 入口；已有 `bootstrap.ps1` 使用者无行为变化。
