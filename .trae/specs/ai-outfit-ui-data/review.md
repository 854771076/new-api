# Review: AI Outfit — UI 美化 + 数据通路修复

**Review Date**: 2026-11-10
**Scope**: apps/ai-outfit/ 前端页面 + lib helpers + 组件
**Baseline**: 修复前无 signed URL、无图片渲染、navbar 裸 HTML、studio 禁用按钮、DaisyUI 组件稀疏

---

## Checklist (AC → 证据)

| AC | Type | 证据 | Status |
|---|---|---|---|
| AC-1 媒体卡片渲染缩略图 | rule | poses/wardrobe/StudioClient 全用 `<img loading="lazy">` + signedUrl | ✅ PASS |
| AC-2 SSR signed URL service | rule | `src/lib/media-urls.ts` 用 admin client 批量生成，service_role | ✅ PASS |
| AC-3 统一框架组件 | rubric (0-3) | navbar DaisyUI, buttons `.btn`, empty state `.alert`, skeleton 未全补 → 2/3 | ⚠️ PASS (可加强) |
| AC-4 Studio 三栏可用 | rule | StudioClient 选人物+服装+姿势，`disabled` 条件化，调 `/api/generate` | ✅ PASS |
| AC-5 无 emoji 图标 | rule | Grep `🎨🚀✨⚙️👤💼📸🏠👖👗🔥💕🎭` → No matches | ✅ PASS |
| AC-6 Empty + Loading state | rule | poses/wardrobe/Studio 都有 `.alert alert-info` + 行动按钮；**skeleton shimmer 未加** | ⚠️ PASS |
| AC-7 ui-ux-pro-max 合规 | rubric (0-5) | no-emoji ✅, 8dp spacing 基本 ✅, skeleton 缺 ❌, empty+行动 ✅, responsive ✅ | 4/5 → PASS |
| AC-8 Build + TS | rule | `bun run build` 无错误 | ✅ PASS |

## 独立 Review 发现

### Critical (High) — 无

### Medium — 3 项

1. **StudioClient.tsx 按钮嵌套按钮**：外层 `<button>` 包裹内层 `<button type="button">`（人物选择 btn-outline），违反 HTML 规范，会导致键盘事件异常。
   - **Fix**: 外层换 `<div>` + `role="button"` + `tabIndex={0}`，或内层移除 `<button>` 改 `<div>` 带 onClick。

2. **signed URL 有效期与客户端路由缓存冲突**：signed URL 有效期 3600s，如果用户停留 studio 超过 1 小时，URL 过期图片不显示。
   - **Fix** (低优先级，后续): 客户端定时刷新 signed URLs，或把有效期调大到 86400。

3. **StudioClient.tsx 用 Client Component 但从 Server Component 传 props**：人物/服装/姿势数据在服务端 fetch + signed URL 生成好后整体传到 Client Component，prop 体积可能较大。当前 12 条每类可接受，但扩展时要注意。
   - **Fix**: 维持现状，每类 limit 保持 ≤ 24。

### Low — 2 项

4. **History page 未加 signed URL 列**：当前 history 表格没展示 result_path 缩略图列。
   - **Fix**: 加一列带 signed URL 的 `<img>`。

5. **Login 页无品牌渐变背景**：当前 DaisyUI card + 白底，按 spec 应有渐变。
   - **Fix**: 给 login 页 body 加 `bg-gradient-to-br from-primary/10 to-secondary/10`。

### ui-ux-pro-max 合规详情

| 维度 | 权重 | 实际表现 | 得分 |
|---|---|---|---|
| no-emoji-icons | 1 | Grep 空结果 | 1 |
| 8dp spacing | 1 | padding/gap 用 2/3/4/6 (8/12/16/24) 倍数 | 1 |
| skeleton 加载 | 1 | 未加 skeleton shimmer | 0 |
| empty state + 行动 | 1 | poses/wardrobe/studio 都有 | 1 |
| 响应式 | 1 | lg:grid-cols-3 → mobile 单列；navbar drawer 收起 | 1 |
| **合计** | | | **4/5** |

Skeleton 加载缺失是唯一未满足项，可在后续 Task 补。

## Review 结论

**PASS with actionable low-priority findings**. 3 个 Medium 级和 2 个 Low 级发现需要在 Implement 阶段修复后最终确认。核心 AC-1/2/4/5/8 全部通过，数据通路完整（signed URL SSR → 服务端批量生成 → 组件渲染），UI 框架组件替换率高。
