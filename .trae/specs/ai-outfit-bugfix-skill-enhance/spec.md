# Spec: Bug 修复 + Skill 增强（分页规范 + 响应码规范）

## Problem

### Bug: 姿势库 + 服装库页面无数据
用户反馈"姿势库也没有加载出来"、"服装 zip 解压了但 wardrobe 没数据"。

**已验证的事实**：
- Supabase Storage 里 poses 文件确实存在（1439 个 thumbs + 1439 个 previews）
- SQL 里 poses 表有 1439 条，thumb_path 是 `nac_-.../poses-thumbs/xxx.webp`，跟 Storage 匹配
- `createSignedUrls` 确实能生成带完整 token 的 signed URL
- 直接 curl signed URL → HTTP 200，图片能下载
- **推测的根因**：dev server 没重启 / 用户未登录被 redirect / 浏览器缓存了旧 page.tsx 的编译产物
- costume-library 已解压（`C:\Users\Administrator\xwechat_files\...\asset-packs\costume-library\img\`，有 1000+ webp），但 pack.json 的 `media.tiers[0]` 声明 thumb 目录是 `thumbs`（实际不存在），只有 preview 目录 `img` 存在。seed-assets.ts 目前假设 thumbs 和 previews 目录都存在

### Skill 缺：统一 API 分页规范 + 响应码规范
`.agents/skills/new-api-oauth-integration/SKILL.md` 目前：
- 有 "Step 6: Frontend Standards" 但没涉及 **API 分页规范**（p/page_size offset、cursor、分页元数据字段）
- 没写 **接口响应码规范**（success payload shape `{ code, message, data }` 还是 `{ success, error, data }`、HTTP 状态码映射）
- new-api 后端自己有 `common.ApiErrorT` / `common.NewMessage` 格式（`{"success":bool, "message":"..."}` + HTTP status），但前端自定义的 Next.js Route Handler（如 `/api/admin/[table]`、`/api/generate`、`/api/characters`）目前风格不一——需要统一并写入 skill

## Goals

- **端到端验证姿势库加载**：dev server 重启 + 登录 + 浏览器页面看到姿势缩略图网格
- **costume library seed 适配**：seed-assets.ts 自动检测 media.tiers 里哪个目录实际存在，优先 preview 目录当 thumb_path（只有一个目录时双用）
- **wardrobe 有数据**：seed 后 outfits 表 row count > 1000
- **Skill 新增两章**：API 分页规范 + 接口响应码规范，引用 new-api 后端已确立的 `ApiError` / `ApiSuccess` 格式

## Non-Goals

- 不改现有 page.tsx 的 signed URL 逻辑（已验证是对的）
- 不改 pose-library seed（已完成，不重跑）
- 不重构 `media-urls.ts` 或 `MediaCard.tsx`
- 不改 skill 里其他章节（如 bootstrap、OAuth provider、UI/UX）

## Functional Requirements

### AC-1: 姿势库页面真能看到图片 (rule)
浏览器登录 → `/poses` → 至少前 6 张卡片渲染 `<img>`，且图片内容是对应姿势缩略图。

**Evidence**: MCP Chrome DevTools 截图 `/poses` 页面 DOM 包含 `<img src="https://jknkenfhhwpxwwgqmcxv.supabase.co/storage/v1/object/sign/media-official/...token=...">`

### AC-2: costume library seed 适配单目录情况 (rule)
seed-assets.ts 读取 pack.json 的 `media.tiers`，自动检测每个 tier 的 `dir` 是否存在。当 thumbs 和 previews 其中一个缺失时：
- 缺失的那个用另一个存在的目录顶上（thumb_path = preview_path）
- outfits 表 `thumb_path` 和 `preview_path` 字段都有值且指向 Storage 里真实存在的对象

**Evidence**: seed 日志输出 "thumb dir missing, using preview dir as fallback" + Supabase Storage list 返回 1000+ 对象在 costumes 前缀下

### AC-3: 服装库页面真能看到图片 (rule)
seed 完成后 → `/wardrobe` → 至少前 6 张卡片渲染 `<img>`。

**Evidence**: 浏览器 DOM + outfits 表 row count > 1000

### AC-4: Skill 新增 API 分页规范章节 (rule)
`SKILL.md` 里新增 "API 分页规范" 章节，包含：
- 分页参数：`?page=1&page_size=20`（offset-based，GORM `count` + `range`）
- 分页响应结构：`{ items: T[], total: number, page: number, page_size: number, total_pages: number }`
- 排序参数：`?sort=created_at&order=desc|asc`
- 筛选参数：`?category=tops&is_active=true`
- 前端分页控件：DaisyUI `.join` + `.btn` 页码，total_pages > 7 时中间省略

**Evidence**: Grep `## API 分页规范` in SKILL.md → 命中且章节内容包含上述要点

### AC-5: Skill 新增接口响应码规范章节 (rule)
`SKILL.md` 里新增 "接口响应码规范" 章节，包含：
- **成功**：`{"success": true, "message": "OK", "data": <T>}`（HTTP 200）
- **创建**：`{"success": true, "message": "Created", "data": <T>}`（HTTP 201）
- **业务错误**：`{"success": false, "message": "<i18n key>", "error_code": "<code>"}`（HTTP 400/403/404/409）
- **认证错误**：`{"success": false, "message": "Unauthorized"}`（HTTP 401）
- **服务端错误**：`{"success": false, "message": "Internal error"}`（HTTP 500）
- **列表响应**：同分页规范的 `{ items, total, ... }` 包在 data 字段里
- 引用 new-api 后端已有的 `common.ApiErrorT` / `common.NewMessage` 实现
- Next.js Route Handler 模板：`NextResponse.json({ success: true, data }, 200)`

**Evidence**: Grep `## 接口响应码规范` in SKILL.md → 命中且章节内容包含上述要点

### AC-6: Build + TS 通过 (rule)
`bun run build` 成功，`tsc --noEmit` 无错误

## Constraints & Assumptions

- **pose 数据通路已验证正确**：signed URL 生成 + Storage 下载都没问题，问题在 dev server 缓存 / 用户端
- **seed-assets.ts 只改 outfits 路径逻辑**，pose 路径不动（pose 已 seed 完且正确）
- **pack.json media.tiers 是数组**，每个 tier 有 `{ id, dir, ext }`，用 `id` 识别 thumb/preview 比硬编码更稳
- **skill 在同一 repo 里**（`.agents/skills/new-api-oauth-integration/SKILL.md`），直接改文件
- **AC-4/AC-5 是 skill 增强**，不是 apps/ai-outfit 代码；不影响 build

## Open Questions

- seed costume library 后 thumb_path 用 preview 图顶替 → wardrobe 卡片显示大尺寸图（640×640 webp），可能比较慢。可以接受（后续如有需要在 storage 里额外上传缩略图）
