# Tasks: AI Outfit — UI 美化 + 数据通路修复

## Task 0: 前置 — 解压 costume library + seed outfits
**Priority: high** — 无数据时 wardrobe 永远是空态

- [ ] 解压 costume-library.part1.zip + part2.zip 到 asset-packs/costume-library/
- [ ] 确认 pack.json、thumbs/、previews/ 目录完整
- [ ] `bun run scripts/seed-assets.ts --pack-root <path> --client-id nac_-9qFZvp3y2dsVZzQ3qOnkx4ybg4n5rSF`
- [ ] 验证 outfits 表 row count > 1000

**Blocked By**: 本地是否有 zip 文件？→ 让用户提供路径或确认已解压
**Unblock Condition**: 用户提供 zip 路径或确认已经解压好

---

## Task 1: 基础工具层 — signed URL helper + Lucide React 安装
**Priority: high**
**Depends On**: 无

- [ ] `bun add lucide-react`
- [ ] 创建 `src/lib/media-urls.ts`：`getSignedUrls(bucket, paths: string[])` → `Record<string, string>`；使用 server Supabase admin client，service_role，expiresIn=3600
- [ ] 创建 `src/components/MediaCard.tsx`：可复用卡片组件，props `{ title, signedUrl, aspectRatio, fallback }`，渲染 `<img loading="lazy">` + DaisyUI card 容器
- [ ] `bun run build` 通过

**TR**:
- (rule) `getSignedUrls` 接受多 path，返回 map，无 SSR client 泄漏
- (rule) MediaCard 渲染 `<img>` 带 loading="lazy" 和 aspect-ratio placeholder
- (rule) lucide-react import 成功且 bundle size 增加 ≤ 50KB gzip

---

## Task 2: Poses 页面修复 — signed URL + 缩略图渲染
**Priority: high**
**Depends On**: Task 1

- [ ] 重构 `src/app/poses/page.tsx`：SSR 阶段从 poses 表读 `is_active=true`，调用 `getSignedUrls('media-official', poses.map(p=>p.thumb_path))`，传给 MediaCard
- [ ] 加 empty state：0 条 DaisyUI `.alert alert-info` + "稍后再来看看"
- [ ] 加骨架：分页加载用 `.skeleton` 占位（如需要，否则 1439 条一次查完）
- [ ] DaisyUI `.tabs` 按 system/category/angle 筛选（或简化为 分类 chips）
- [ ] `bun run build` 通过

**TR**:
- (rule) `/poses` HTML 渲染包含 `<img src="...supabase.co...X-Supabase-Signature=...">`
- (rule) 图片 aspect-ratio 稳定（CLS 不显著）
- (rubric) Empty state 质量 0-2：2=文案+行动按钮；1=只有文案；0=裸 div

---

## Task 3: Wardrobe 页面修复 — signed URL + DaisyUI 组件化
**Priority: high**
**Depends On**: Task 1, Task 0 (outfits 有数据)

- [ ] 重构 `src/app/wardrobe/page.tsx`：SSR 从 outfits 表读，category 筛选，分页，signed URL
- [ ] 加 empty state + skeleton loading
- [ ] DaisyUI `.tabs` 按 category 切换（tops/bottoms/outerwear/shoes/accessories）
- [ ] 卡片 hover 态（DaisyUI card 自带）
- [ ] `bun run build` 通过

**TR**:
- (rule) `/wardrobe` 每张卡片有 `<img>`
- (rule) 0 条时显示 DaisyUI alert
- (rubric) 框架组件替换率：≥ 80% 元素来自 DaisyUI

---

## Task 4: Studio 三栏最小可用
**Priority: high**
**Depends On**: Task 1

- [ ] 创建 `src/app/studio/_components/StudioCharacterPicker.tsx`：读 characters 表 + signed URL
- [ ] 创建 `src/app/studio/_components/StudioOutfitPicker.tsx`：读 outfits 表 + category chips + signed URL
- [ ] 创建 `src/app/studio/_components/StudioPosePicker.tsx`：读 poses 表 + signed URL
- [ ] 三栏状态组件：selected state 用 DaisyUI `.btn-active` 或自定义 border 高亮
- [ ] "开始生成"按钮：选中 item 后 enabled，点击调 `/api/generate`，loading 状态，完成后 redirect `/history`
- [ ] 人物上传：创建 `POST /api/characters` route handler（service_role storage upload + insert）
- [ ] `bun run build` 通过

**TR**:
- (rule) Studio 页面不包含任何 `disabled` 按钮在合法状态下
- (rule) 三栏选择器点击后有视觉反馈（border/background 高亮）
- (rubric) 交互反馈 0-2：2=150ms 过渡 + 选中态 + hover 态；1=选中态；0=无反馈

---

## Task 5: Navbar + Profile + Icon System
**Priority: medium**
**Depends On**: Task 1

- [ ] 全局 layout navbar 改为 DaisyUI `.navbar` + `.menu menu-horizontal`
- [ ] Profile 区域改为 DaisyUI `.dropdown`（菜单：Profile / Top-up / Logout）
- [ ] 所有导航 + 按钮图标用 lucide-react，移除 emoji
- [ ] Mobile: menu collapse 成 `.dropdown` 或 `.drawer`（≥ 640px breakpoint 断点）
- [ ] `bun run build` 通过

**TR**:
- (rule) `grep -rE '[🎨🚀✨⚙️👤💼📸🏠👖👗]' apps/ai-outfit/src/app` 结果为空
- (rule) Navbar 在 375px 视口下不溢出（dropdown/drawer 收起）
- (rubric) 响应式质量 0-2：2=三栏→单栏；1=部分响应；0=仅桌面

---

## Task 6: History + Login + Profile 页增强
**Priority: medium**
**Depends On**: Task 1

- [ ] history 表格加 signed URL 缩略图列，empty state，骨架
- [ ] login 页：DaisyUI `.card` + `.btn btn-primary` + lucide 图标 + 品牌色背景/渐变
- [ ] profile 页：Ant Design Descriptions 或 DaisyUI `.stats` 组件
- [ ] 所有页统一空状态 + 骨架 + 错误态
- [ ] `bun run build` 通过

**TR**:
- (rule) 三个页面都有空状态和加载骨架
- (rubric) 视觉一致性 0-2：2=一致 card/padding/typography；1=基本一致；0=混乱

---

## Task 7: Global CSS + ui-ux-pro-max 合规检查
**Priority: low**
**Depends On**: Task 2, 3, 4, 5, 6

- [ ] globals.css 定义 CSS 变量（spacing scale 0/4/8/12/16/24/32/48, radius scale）
- [ ] Tailwind config 确认 DaisyUI theme token 正确
- [ ] 跑 `ui-ux-pro-max` 约束 checklist：
  - [ ] no-emoji-icons → AC-5
  - [ ] 8dp spacing → 检查 gap/padding 值
  - [ ] skeleton 加载 → AC-6
  - [ ] empty state 有行动 → AC-6
  - [ ] hover/pressed feedback → 逐页点检查
  - [ ] 4.5:1 对比度 → Chrome devtools color contrast
  - [ ] touch target ≥ 44px → Studio 按钮最小高度
- [ ] `bun run build` 通过

**TR**:
- (rubric) ui-ux-pro-max 合规分 0-5 → ≥ 3 通过（AC-7）

---

## Task 8: Build + 端到端冒烟 + 修复
**Priority: high**
**Depends On**: Task 0-7

- [ ] `bun run build` 成功
- [ ] 启动 dev server，完整端到端：登录 → wardrobe 看图 → poses 看图 → studio 选择 → 生成 → history
- [ ] 修复发现的 bug（图片路径错、filter 不工作、状态没更新等）
- [ ] 375px 视口截图（mobile-first）
- [ ] 1024px 视口截图（desktop）

**TR**:
- (rule) 端到端冒烟通过所有 6 步
- (rule) 375px + 1024px 视口无水平滚动条
- (rubric) 整体视觉质量 0-3：3=专业级；2=良好；1=可用但粗糙；0=不可用

---

## Review 准备

实现阶段完成后，执行独立 Review：
1. 逐 AC/TR 验证证据
2. 跑 `ocr review` 或手动 diff 审计
3. 浏览器截图 + 交互录屏（关键路径）
