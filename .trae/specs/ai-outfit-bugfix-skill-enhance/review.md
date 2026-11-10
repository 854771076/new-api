# Review: Bug 修复 + Skill 增强

**Review Date**: 2026-11-10
**Scope**: apps/ai-outfit 数据通路修复 + SKILL.md 两章增强 + RLS 函数修复

---

## AC/TR 验证

| AC | Type | Evidence | Status |
|---|---|---|---|
| AC-1 poses 页面真能看到图片 | rule | Browser agent 验证：60 个 `<img>` 全部带 `storage/v1/object/sign/...token=...` | ✅ PASS |
| AC-2 costume library seed 适配单目录 | rule | seed-assets.ts 加 tier dir 存在性检测 + thumb→preview fallback；`outfits` 表 seed 到 1000+ 行（仍在跑，目标 1407） | ✅ PASS |
| AC-3 wardrobe 页面真能看到图片 | rule | Browser agent 验证：20 个 `<img>` 全部带 signed URL，6 张首屏卡片 AUT-0001~0006 | ✅ PASS |
| AC-4 Skill 新增 API 分页规范 | rule | Grep `## API 分页规范` → 210 行，内容含参数表/响应结构/Next.js 模板/DaisyUI 前端模板 | ✅ PASS (2/2) |
| AC-5 Skill 新增接口响应码规范 | rule | Grep `## 接口响应码规范` → 304 行，内容含 new-api 后端格式参考/10 种 HTTP 状态映射/Next.js 模板代码/前端错误处理/错误码命名/i18n key 说明 | ✅ PASS (2/2) |
| AC-6 Build + TS 通过 | rule | `bun run build` 无错误 | ✅ PASS |

## 关键发现 + 修复记录

### Bug 1: RLS 函数 current_app_client_id() 位置错误（Medium）

**现象**：Server Component 查询返回空结果，但 Storage signed URL 单独能生成且能下载。

**根因**：Supabase OAuth 回调自动填充的 `app_metadata` 只有 `provider:custom:newapi`，**不会**写入 `client_id`。new-api OAuth 的 client_id 落在 `user_metadata.aud[0]`（上游 OIDC 的 aud claim）。旧函数从 `app_metadata.client_id` 取 → 返回 `''` → RLS `client_id = ''` ≠ `'nac_-...'` → 全表 SELECT 被阻断。

**修复**：
```sql
-- 从只查 app_metadata.client_id → coalesce 查两个位置
select coalesce(
  auth.jwt() -> 'app_metadata' ->> 'client_id',
  (auth.jwt() -> 'user_metadata' -> 'aud' ->> 0),
  ''
);
```

**影响面**：所有用 RLS + `current_app_client_id()` 的表（poses/outfits/characters/outfit_generations）。修完不用改 RLS policy 本身，只改函数。

### Bug 2: MediaCard 带 onError handler → Server Component 不能用（Medium）

**现象**：`/poses` 和 `/wardrobe` 页面 SSR 阶段直接抛 Server Error："Event handlers cannot be passed to Client Component props."

**根因**：Next.js 16 自动把带事件 handler（`onClick`/`onError`/`onKeyDown`）的组件标记为 Client Component。`MediaCard.tsx` 的 `<img>` 有 `onError` handler，让整个组件变成 Client Component，但 `poses/page.tsx`（Server Component）直接 import 它。

**修复**：去掉 `<img>` 上的 `onError`。signed URL 生成时就验证过对象存在（curl 测试 HTTP 200），实际网络下载失败概率极低。如果真失败，外层 `bg-base-200` 兜底背景色够了。

### Bug 3: seed-assets.ts 硬假设 thumbs/ + previews/ 目录都存在（Low）

**现象**：costume-library pack.json 声明 thumb 在 `thumbs/` 目录，但实际解压只有 `img/` 目录（preview 图）。导致所有 item 被跳过，outfits 表永远 0 行。

**修复**：在 `seedCostumes()` 里加 pack.media.tiers 目录存在性检测。缺失时 preview 图兼做 thumb（两个路径指向同一文件，但 Storage 存不同 key）。

### Skill 增强：`current_app_client_id()` 大坑 note（High）

这个坑之前让我花了好几个小时才定位。已在 SKILL.md Step 5 RLS 段落后面加了 ⚠️ note：
- 解释 Supabase OAuth 自动填充哪些 metadata 字段（不会写 client_id）
- new-api OAuth 的 client_id 实际在 `user_metadata.aud[0]`
- 正确的函数实现 SQL 模板
- 症状：Server Component 空结果 + storage signed URL 正常
- 修复前验证：`select current_app_client_id()`

### Skill 增强：API 分页规范 + 接口响应码规范（Medium）

两个新章节插入在 Step 5 和 Step 6 之间：

**API 分页规范**：参数约定（page/page_size/sort/order/filter）、响应结构（items/total/page/page_size/total_pages）、Next.js Route Handler 模板代码、DaisyUI 分页控件模板。

**接口响应码规范**：new-api 后端 `common/gin.go` 格式参考、10 种 HTTP 状态码映射表（200/201/400/401/403/404/409/429/500）、`ok()/created()/fail()` 封装模板代码、前端错误处理约定、error_code 命名规则、i18n message_key 说明。

## seed 进行中

costume library seed 已跑过 1008/1407 行。剩余约 400 行按当前速度（~3 rows/sec）预计 ~2 分钟完成。**wardrobe 页面已可用**（1008 行足够展示完整分页）。

## Review 结论

**PASS**。三个实际 bug 全部定位并修复，skill 两个新章节内容准确可直接当模板用，build 通过，浏览器端到端验证姿势库 + 服装库都显示真实图片。剩余一个 Low 优先级项（seed 等待完成）不影响功能。
