# 后台管理增强与真实换装链路 Spec

## Why

- 管理后台目前只有 5 个纯 CRUD 列表（服装 / 姿势 / 人物 / 生成记录 / 套餐），缺少**模型设置**与**提示词配置**，运营无法切换生成模型、调整默认尺寸、维护提示词模板，也无法看到整体运行情况。
- 「真实换装」并未完成：`/api/generate` 只把服装名、姿势名拼成纯文本 prompt，人物图、服装图、姿势图**从未作为参考图传给模型**，等于凭空文生图，换装结果与所选素材无关。

## What Changes

- 新增 `ai_models` 表：模型 ID、展示名、供应商、用途、启用/默认、默认尺寸与质量、排序。
- 新增 `prompt_templates` 表：按用途分类的提示词模板，支持 `{{变量}}` 占位符、负向提示词、启用/默认。
- 后台新增「概览」页：生成总数、成功率、今日生成、在售素材数、最近失败记录。
- 后台新增「模型设置」页与「提示词模板」页（沿用现有 Ant Design + Refine 表格模式）。
- 后台生成记录支持查看详情（prompt、参考图、结果图、错误信息）。
- 后台素材列表增加「上架 / 下架」快捷操作。
- `/api/admin/[table]` 白名单扩展到新表，并移除 `any`（当前 lint error）。
- `/api/generate` 改为读取人物图 + 服装图 + 姿势图的签名 URL，通过 `reference_images` / `character_image` / `outfit_image` / `pose_image` 传给网关；prompt 由 `prompt_templates` 渲染生成。
- Studio 页暴露「模型选择」「补充提示词」「失败原因」，提交时一并传给 `/api/generate`。

## Impact

- 受影响能力：管理后台、换装生成、Studio 交互。
- 受影响代码：
  - 新增迁移 `apps/ai-outfit/supabase/migrations/2026..._add_models_and_prompt_templates.sql`
  - `apps/ai-outfit/src/app/admin/**`（nav、新增 page）
  - `apps/ai-outfit/src/app/api/admin/[table]/route.ts`
  - `apps/ai-outfit/src/app/api/generate/route.ts`
  - `apps/ai-outfit/src/lib/gateway.ts`（签名 URL 复用）、`src/lib/prompt.ts`（新增模板渲染）
  - `apps/ai-outfit/src/app/studio/_components/StudioClient.tsx`

## ADDED Requirements

### Requirement: 模型设置

后台 SHALL 提供模型管理，运营可增删改查可选模型，并指定恰好一个默认模型。

#### Scenario: 切换默认模型

- **WHEN** 运营在「模型设置」把某个模型设为默认并保存
- **THEN** 该表其余模型的 `is_default` 变为 `false`，Studio 的模型下拉默认选中新模型

#### Scenario: 仅展示启用模型

- **WHEN** Studio 加载模型下拉
- **THEN** 只返回 `is_active = true` 的模型，按 `sort_order` 升序

### Requirement: 提示词模板

后台 SHALL 提供按用途分类的提示词模板管理，模板正文支持 `{{character}}`、`{{outfits}}`、`{{pose}}`、`{{extra}}` 占位符与可选负向提示词。

#### Scenario: 模板渲染

- **WHEN** 生成请求命中某用途的启用模板
- **THEN** 服务端用实际素材信息替换全部占位符，未命中的占位符替换为空字符串后再发给网关

#### Scenario: 无模板时降级

- **WHEN** 该用途没有任何启用模板
- **THEN** 使用代码内置的默认模板继续生成，不报错

### Requirement: 参考图换装

`/api/generate` SHALL 把人物原图、所选服装图、姿势图作为参考图传给网关，而不是只发送文字描述。

#### Scenario: 参考图上传成功

- **WHEN** 用户选择了人物、1 套以上服装和姿势并点击生成
- **THEN** 请求体包含 `reference_images`（人物图 + 服装图 + 姿势图的签名 URL），网关返回结果后落库为 `succeeded`

#### Scenario: 素材缺图

- **WHEN** 人物或姿势记录缺少可用的 `image_path` / `thumb_path`
- **THEN** 接口返回 `MATERIAL_IMAGE_MISSING`，不发起网关调用，也不扣费

### Requirement: 生成记录详情

后台生成记录 SHALL 可查看单条详情，包含渲染后的 prompt、参考图、结果图与失败原因。

#### Scenario: 查看失败记录

- **WHEN** 运营点击一条 `status = failed` 的记录
- **THEN** 抽屉展示错误原文与当时使用的模型、prompt

## MODIFIED Requirements

### Requirement: 管理后台导航

原 5 项菜单扩展为：概览、服装管理、姿势管理、人物管理、生成记录、模型设置、提示词模板、套餐管理。

### Requirement: `/api/generate`

在原有「校验 → 读素材 → 落 pending → 调网关 → 下载转存 → 回写状态」流程中，新增：按 `ai_models` 校验 model 合法性、按 `prompt_templates` 渲染 prompt、组装参考图。

## REMOVED Requirements

无。
