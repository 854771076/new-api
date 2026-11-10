# Tasks

- [x] Task 1: 数据库迁移：新增 `ai_models` 与 `prompt_templates`
  - [x] SubTask 1.1: 编写 `supabase/migrations/2026..._add_models_and_prompt_templates.sql`，含字段、索引、`updated_at` 触发器、RLS（仅 `is_app_admin()` 可写，启用项可被已登录用户读取）
  - [x] SubTask 1.2: 插入默认数据：至少 1 个默认模型（沿用当前 `gpt-image-1`）与 1 个 `outfit` 用途模板
  - [x] SubTask 1.3: 本地执行 `supabase db push` 并在 `db_schema` 说明中确认可重复执行不报错

- [x] Task 2: 管理后台 API 扩展与类型清理
  - [x] SubTask 2.1: `ALLOWED_TABLES` 增加 `ai_models`、`prompt_templates`
  - [x] SubTask 2.2: 去掉 `[table]/route.ts` 里的 `any`，用 Supabase 查询类型替代（消除既有 lint error）
  - [x] SubTask 2.3: 为 `ai_models` 增加「设为默认」的原子更新（同表其他行 `is_default = false`）

- [x] Task 3: 后台「模型设置」页
  - [x] SubTask 3.1: 新增 `/admin/models`，复用现有 Refine/Ant Design 表格模式
  - [x] SubTask 3.2: 支持新增/编辑/删除、启用停用、设为默认、排序
  - [x] SubTask 3.3: 导航菜单加入「模型设置」

- [x] Task 4: 后台「提示词模板」页
  - [x] SubTask 4.1: 新增 `/admin/prompts`，字段：用途、名称、模板正文、负向提示词、启用、默认
  - [x] SubTask 4.2: 提供占位符说明与插入按钮（`{{character}}`、`{{outfits}}`、`{{pose}}`、`{{extra}}`）
  - [x] SubTask 4.3: 导航菜单加入「提示词模板」

- [x] Task 5: 后台概览与记录详情
  - [x] SubTask 5.1: 新增 `/admin` 概览页：生成总数、成功率、今日生成、素材数、最近 5 条失败记录
  - [x] SubTask 5.2: 生成记录页增加详情抽屉（prompt、参考图、结果图、错误）
  - [x] SubTask 5.3: 服装/姿势/人物列表增加「上架 / 下架」快捷操作

- [x] Task 6: 真实换装链路（参考图 + 模板渲染）
  - [x] SubTask 6.1: 新增 `src/lib/prompt.ts`，实现 `{{var}}` 渲染与内置默认模板降级
  - [x] SubTask 6.2: `/api/generate` 从 `characters.image_path`、选中 `outfits`、`poses.thumb_path` 生成签名 URL，组装 `reference_images`
  - [x] SubTask 6.3: 校验 `model` 必须存在于启用的 `ai_models`；缺失素材图返回 `MATERIAL_IMAGE_MISSING` 且不调网关
  - [x] SubTask 6.4: 新增 `GET /api/models` 返回启用模型供 Studio 使用

- [x] Task 7: Studio 交互补齐
  - [x] SubTask 7.1: 加入模型下拉（默认选中 `is_default`）
  - [x] SubTask 7.2: 加入「补充提示词」输入框，透传 `prompt_extra`
  - [x] SubTask 7.3: 生成失败时展示接口返回的原因与重试入口

- [x] Task 8: 验证
  - [x] SubTask 8.1: `bun run build` 与改动文件 `eslint` 通过，`/admin` 既有 lint error 清零
  - [x] SubTask 8.2: 浏览器实测：后台 3 个新页面可增删改查；Studio 生成一次成功，记录详情能看到参考图与 prompt
  - [x] SubTask 8.3: 回归 DaisyUI 禁用类扫描（消费端排除 admin）

- [x] Task 9: 实测暴露缺陷修复
  - [x] SubTask 9.1: `/api/generate` 服务端取 Supabase session 的 `provider_token` 透传网关（原先走浏览器 `gatewayAuth`，服务端恒为 401）
  - [x] SubTask 9.2: 后台新建默认模型/模板时先清除同作用域旧默认，避免唯一索引 500
  - [x] SubTask 9.3: 后台新建服装自动补 `client_id`，并补齐 `provider_id`/`bucket`/图片路径表单字段
  - [x] SubTask 9.4: 生成记录详情新增 `GET /api/admin/generation-references` 与参考图展示

# Task Dependencies

- Task 2 依赖 Task 1
- Task 3、Task 4、Task 5 依赖 Task 2（可并行）
- Task 6 依赖 Task 1
- Task 7 依赖 Task 6
- Task 8 依赖 Task 3–7 全部完成
- Task 9 依赖 Task 8 实测结果
