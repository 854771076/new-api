# Tasks

- [x] Task 1: 内置第三方参考资料（vendor）
  - [x] SubTask 1.1: 复制 `nextjs-react-typescript`（SKILL.md）到 `references/vendor/nextjs-react-typescript/`
  - [x] SubTask 1.2: 复制 `vercel-react-best-practices` 全文（`SKILL.md`、`rules/`、`README.md`、`AGENTS.md`）到 `references/vendor/vercel-react-best-practices/`
  - [x] SubTask 1.3: 复制 `ui-ux-pro-max` 2.5.0 的 skill 文本与脚本（`.claude/skills/**` 下 md/csv/json/cjs/yaml），排除字体等二进制，保留 LICENSE/README，到 `references/vendor/ui-ux-pro-max/`
  - [x] SubTask 1.4: 编写 `references/vendor/README.md`：来源路径、版本、许可证、排除项与更新方法

- [x] Task 2: 编写 `references/standards/` 规范文档
  - [x] SubTask 2.1: `nextjs.md`（App Router、Server/Client 边界、取数缓存、环境变量、loading/error、TS 严格）
  - [x] SubTask 2.2: `daisyui.md`（Tailwind v4 `@plugin`、主题与暗色、组件优先、自定义边界）
  - [x] SubTask 2.3: `vercel.md`（CLI 命令、预览/生产 env、部署后三处地址同步、Supabase 联调顺序）
  - [x] SubTask 2.4: `supabase.md`（CLI 工作流、custom OIDC provider、RLS 与 client_id 隔离、Edge Function 鉴权、@supabase/ssr）
  - [x] SubTask 2.5: `ui-ux.md`（token、排版、可访问性、loading/空/错误态、响应式、动效）

- [x] Task 3: 重新定位并重构 `SKILL.md`
  - [x] SubTask 3.1: 改写开头定位为「外部应用开发统一规范（Web）」，列出技术栈矩阵与从 0 到上线的步骤
  - [x] SubTask 3.2: 新增「参考资料索引」表格，链接 standards 与 vendor 文件
  - [x] SubTask 3.3: 将原 OAuth/relay/计费/Supabase 内容重组为「身份接入 / AI 调用 / 用户与计费 / 后端」章节
  - [x] SubTask 3.4: 示例代码改为 Next.js 版本（Server Route Handler + Client Component 两种形态）

- [x] Task 4: 迁移 `references/web/outfit-demo` 到 Next.js + DaisyUI
  - [x] SubTask 4.1: 新建 Next.js App Router 工程骨架（`app/`、`next.config.ts`、`tailwind` + DaisyUI、`tsconfig` 严格）
  - [x] SubTask 4.2: 迁移 `lib/`（config、supabase 单例、gatewayAuth）为 Next.js 兼容版本
  - [x] SubTask 4.3: 迁移页面为 Server/Client 组件组合（`/`、`/wardrobe`、`/history`、`/profile`、`/login`、`/auth/callback`）
  - [x] SubTask 4.4: 用 DaisyUI 组件类重写 UI（btn/card/modal/alert/input/theme）
  - [x] SubTask 4.5: 保留 `supabase/`（schema、functions/user-sync）并补 `next`/Vercel 所需配置与 `README.md`
  - [x] SubTask 4.6: 删除 Rsbuild 相关文件与依赖（`rsbuild.config.ts`、`index.html`、`react-router-dom`）

- [x] Task 5: 更新 CLI 自举脚本与 Step 0 文档
  - [x] SubTask 5.1: `check-env.ps1` 增加 `-Install`：winget/npm 安装 bun、supabase、vercel、cloudflared、newapi-cli
  - [x] SubTask 5.2: `check-env.sh` 增加 `--install`：brew/npm/curl 安装同上，含 Vercel
  - [x] SubTask 5.3: 校验项加入 `vercel --version`，安装后复验并汇总 PASS/FAIL
  - [x] SubTask 5.4: `SKILL.md` Step 0 更新为「一条命令自举 + 自检」，补充 Vercel CLI 与 Next.js 项目创建命令

- [x] Task 6: 验证与收尾
  - [x] SubTask 6.1: `bun install` + `bun run build` 通过，无 TypeScript 错误
  - [x] SubTask 6.2: 本地启动后走通 Supabase custom provider 登录 → 回调 → user-sync → 个人中心显示
  - [x] SubTask 6.3: 跑 `check-env.ps1`（不带参数与带 `-Install` 的干跑路径）确认行为正确
  - [x] SubTask 6.4: 更新 demo `README.md`（Next.js 命令、环境变量、Vercel 部署步骤）

# Task Dependencies

- Task 2 依赖 Task 1（vendor 资料作为提炼来源参考）
- Task 3 依赖 Task 2 与 Task 1（索引需指向实际文件）
- Task 4 依赖 Task 2（按 nextjs/daisyui/supabase 规范落地）
- Task 5 独立，可与 Task 4 并行
- Task 6 依赖 Task 3、4、5
