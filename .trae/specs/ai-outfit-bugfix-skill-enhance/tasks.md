# Tasks: Bug 修复 + Skill 增强

## Task 1: seed-assets.ts 适配单目录 costume library
**Priority: high** — wardrobe 无数据的根因

- [ ] 读 apps/ai-outfit/scripts/seed-assets.ts 当前逻辑
- [ ] 改 pack.json 解析：`media.tiers` 数组里每个 tier 的 `dir` 检测存在性
- [ ] 当 thumb tier 目录缺失时，用 preview 目录顶上（thumb_path = preview_path = `img/xxx.webp`）
- [ ] outfits 表的 `category` 从文件名前缀提取（AUT→春季, LNG→内衣, PTY→派对, SPR→春季, SUM→夏季, SWM→泳装）
- [ ] 重新 seed costumes（先 delete from public.outfits 再 upsert）
- [ ] 验证 outfits row count > 1000，且随机几条的 thumb_path 指向真实 Storage 对象

**TR**:
- (rule) seed 日志输出 tier 检测结果（"thumb dir thumbs missing, fallback to img"）
- (rule) outfits 表 row count > 1000
- (rule) 随机选 3 条 outfits，用 createSignedUrls + curl 验证图片 HTTP 200

---

## Task 2: 端到端验证姿势库 + 服装库真能加载
**Priority: high**

- [ ] 停旧 dev server，`bun run dev` 重新启动（清除编译缓存）
- [ ] 登录流程走完（OAuth → /auth/callback → session）
- [ ] `/poses` 页面截图 + DOM grep `<img src="...sign/...token=...">`
- [ ] `/wardrobe` 页面截图 + DOM grep `<img src="...sign/...token=...">`
- [ ] 确认前 6 张卡片都有图片渲染（不是 ImageOff fallback）

**TR**:
- (rule) `/poses` DOM 至少 6 个带 signedUrl 的 `<img>`
- (rule) `/wardrobe` DOM 至少 6 个带 signedUrl 的 `<img>`
- (rubric) 图片视觉质量 0-2：2=清晰加载；1=部分加载；0=全部 fallback

---

## Task 3: Skill 新增 API 分页规范章节
**Priority: medium**

- [ ] 在 `SKILL.md` 合适位置（Step 6 或新 Step）插入 `## API 分页规范` 章节
- [ ] 内容：offset-based 分页参数约定、响应 JSON 结构、排序参数、筛选参数、前端分页控件规范
- [ ] 给出 Next.js Route Handler 分页代码模板（`page.ts` + `total_pages`）
- [ ] 给 DaisyUI 分页组件模板（`.join` + `.btn btn-sm`）

**TR**:
- (rule) `grep "## API 分页规范" SKILL.md` 命中
- (rubric) 内容完整度 0-2：2=参数+结构+模板都有；1=缺一项；0=空

---

## Task 4: Skill 新增接口响应码规范章节
**Priority: medium**

- [ ] 在 `SKILL.md` 紧接 Task 3 分页规范之后插入 `## 接口响应码规范` 章节
- [ ] 内容：success/create/business-error/auth-error/server-error 的 JSON shape + HTTP status code 映射表
- [ ] 引用 new-api 后端 `common/common.go` 的 `NewMessage` / `ApiError` 实现，给出 `go i18n` key 示例
- [ ] Next.js Route Handler 响应模板（`NextResponse.json({ success: true, data }, 200)` 等 5 个模板）
- [ ] 列表响应 = 分页规范的 shape 包在 `data` 字段里

**TR**:
- (rule) `grep "## 接口响应码规范" SKILL.md` 命中
- (rubric) 内容完整度 0-2：2=5 种响应+HTTP 码+前后端模板都有；1=缺一项；0=空

---

## Task 5: 构建 + Review
**Priority: high**

- [ ] Task 1+2 完成后 `bun run build` 通过
- [ ] Task 3+4 完成后快速浏览 SKILL.md，确认两个章节内容准确可用
- [ ] 独立 Review：
  - seed outfits 数量 > 1000 ✓
  - 两个页面图片真能显示 ✓
  - skill 两个章节内容可直接当模板用
