# DaisyUI 5 + Tailwind CSS v4 Standard

Applies to: **DaisyUI 5.x with Tailwind CSS 4.x** (no `tailwind.config.js`),
Next.js 15+ App Router.

Official docs: [Install](https://daisyui.com/docs/install/) ·
[Themes](https://daisyui.com/docs/themes/) ·
[Colors](https://daisyui.com/docs/colors/) ·
[Components](https://daisyui.com/docs/card/) ·
[Tailwind v4 with Next.js](https://tailwindcss.com/docs/installation/framework-guides/nextjs)

Vendored background: `../vendor/ui-ux-pro-max/skills/design-system/references/`.

## 1. Installation

Tailwind v4 is configured in CSS, not in a JavaScript config file. Do not create
`tailwind.config.js` / `tailwind.config.ts` for a new app.

```bash
bun add -d tailwindcss @tailwindcss/postcss daisyui@latest
```

```js
// postcss.config.mjs
export default { plugins: { '@tailwindcss/postcss': {} } }
```

```css
/* app/globals.css */
@import "tailwindcss";
@plugin "daisyui";
```

- v4 has no `content` array; it scans the project. Add
  `@source "../packages/ui";` only for files outside the app root.
- Import the stylesheet once, in the root `layout.tsx`.
- Do not write `@tailwind base; @tailwind components; @tailwind utilities;` —
  that is v3 syntax and invalid in v4.

## 2. Themes

```css
@plugin "daisyui" {
  themes: light --default, dark --prefersdark;
}
```

- `--default` is the initial theme; `--prefersdark` follows the OS preference.
- `data-theme="<name>"` on `<html>` selects a theme at runtime. DaisyUI reads
  that attribute, not a `.dark` class.
- Built-ins include `light`, `dark`, `corporate`, `business`, `emerald`, `nord`,
  `dracula`, `night`, `coffee`, `winter`, `dim`, `cupcake`, `retro`.
- A brand theme is declared with `@plugin "daisyui/theme"`; never fork a
  component to re-brand it.

```css
@plugin "daisyui/theme" {
  name: "newapi";
  default: true;
  prefersdark: false;
  color-scheme: light;
  --color-base-100: oklch(100% 0 0);
  --color-base-200: oklch(97% 0 0);
  --color-base-300: oklch(93% 0 0);
  --color-base-content: oklch(21% 0.006 285);
  --color-primary: oklch(55% 0.2 260);
  --color-primary-content: oklch(98% 0.01 260);
  --radius-box: 0.75rem;
}
```

**Dark mode** must be server-readable to avoid a flash. Persist the choice in a
cookie and set `data-theme` on `<html>`:

```tsx
// app/layout.tsx
export default async function RootLayout({ children }: { children: React.ReactNode }) {
  const theme = (await cookies()).get('theme')?.value === 'dark' ? 'dark' : 'light'
  return (
    <html lang="en" data-theme={theme}>
      <body className="bg-base-100 text-base-content">{children}</body>
    </html>
  )
}
```

- A client toggle writes the cookie (`document.cookie = 'theme=dark; path=/; max-age=31536000; samesite=lax'`)
  and updates `document.documentElement.dataset.theme`.
- The `dark:` Tailwind variant does **not** follow `data-theme`. Use semantic
  tokens or a `data-theme` selector for theme-dependent styling.
- Never ship two component trees, one per theme.

## 3. Components Before Custom Markup

DaisyUI ships 60+ components. Reach for one before writing a new pattern:

| Need | Use | Typical modifiers |
|------|-----|-------------------|
| Action | `btn` | `btn-primary`, `btn-ghost`, `btn-sm`, `btn-square` |
| Surface | `card` + `card-body` | `card-border`, `card-side`, `card-actions` |
| Text input | `input` | `input-sm`, `input-error`, `input-ghost` |
| Select | `select` | `select-sm`, `select-error` |
| Form group | `fieldset` + `fieldset-legend` | `label`, `fieldset-label` |
| Message | `alert` | `alert-info`, `alert-success`, `alert-error`, `alert-soft` |
| Status chip | `badge` | `badge-primary`, `badge-soft`, `badge-outline` |
| Dialog | `dialog.modal` + `modal-box` | `modal-backdrop`, `modal-action` |
| Async indicator | `loading` | `loading-spinner`, `loading-sm` |
| Data grid | `table` | `table-zebra`, `table-pin-rows` |

```tsx
<div className="card bg-base-100 shadow-sm">
  <div className="card-body gap-4">
    <h2 className="card-title">Sign in</h2>
    <fieldset className="fieldset">
      <legend className="fieldset-legend">Email</legend>
      <input className="input w-full" type="email" name="email" autoComplete="email" required />
    </fieldset>
    <div role="alert" className="alert alert-error alert-soft">Invalid credentials.</div>
    <div className="card-actions justify-end">
      <button className="btn btn-primary" type="submit">
        <span className="loading loading-spinner loading-sm" aria-hidden="true" /> Continue
      </button>
    </div>
  </div>
</div>
```

- One primary action per screen; secondary actions use `btn-ghost` or
  `btn-outline`.
- Put the spinner inside the triggering button and disable it during submission.
- Use the `dialog` element for modals; it provides focus handling and Escape.
- DaisyUI 5 removed the `*-bordered` input modifiers; inputs are bordered by
  default. Do not copy DaisyUI 4 snippets with `input-bordered`.

## 4. Semantic Color Tokens

```
Backgrounds : bg-base-100  bg-base-200  bg-base-300
Text        : text-base-content  text-base-content/70  text-base-content/60
Primary     : bg-primary  text-primary  text-primary-content  bg-primary/10
Status      : info  success  warning  error   (each with -content and /10 tints)
Borders     : border-base-300  divide-base-300
Focus       : outline-primary  ring-primary
```

- Never hardcode `#hex`, `rgb()`, or a raw Tailwind palette (`bg-blue-600`,
  `text-gray-500`) for semantic meaning; those break theming and dark mode.
- Pair every background with its foreground token: `bg-primary` **and**
  `text-primary-content`.
- Use alpha tints (`bg-success/10`, `border-error/40`) for subtle surfaces.
- Muted body text is `text-base-content/70`, never a gray palette color.
- Status color never carries meaning alone; add an icon or label.

**Anti-pattern**: `<div className="bg-white text-gray-900 border-gray-200">`

**Correct**: `<div className="bg-base-100 text-base-content border border-base-300">`

## 5. Custom Style Boundaries

DaisyUI covers structure and color; Tailwind utilities cover layout; custom CSS
is the exception.

- Allowed: layout (`flex`, `grid`, `gap-4`, `max-w-5xl`), spacing, responsive
  breakpoints, and composition of DaisyUI classes.
- Allowed: a component-scoped class when DaisyUI has no equivalent, defined in
  `@layer components` with theme variables.
- Forbidden: globally overriding `.btn`, `.card`, `.input`; it breaks every
  screen and future updates.
- Forbidden: `!important` to win specificity; fix class order or scope instead.

```css
@layer components {
  .compare-header {
    position: sticky;
    top: 0;
    background-color: var(--color-base-100);
    border-bottom: 1px solid var(--color-base-300);
  }
}
```

## 6. Accessibility Hooks

- Keep the default `focus-visible` outline; if changed, use a ≥3:1 ring
  (`outline-2 outline-offset-2 outline-primary`).
- Disabled controls need `disabled` plus
  `disabled:opacity-50 disabled:cursor-not-allowed`.
- Icon-only buttons need `aria-label`; decorative icons `aria-hidden`.
- Announce async regions with `aria-busy` / `role="status"`.

## 7. Checklist

- [ ] `globals.css` starts with `@import "tailwindcss"; @plugin "daisyui";`.
- [ ] No `tailwind.config.js`, no v3 `@tailwind` directives.
- [ ] `postcss.config.mjs` uses `@tailwindcss/postcss`.
- [ ] Theme is set via `data-theme` on `<html>` from a server-readable cookie.
- [ ] Cards, buttons, inputs, alerts, badges, modals, and loading states use
      DaisyUI classes.
- [ ] No hardcoded hex/rgb or raw palette colors for semantic meaning.
- [ ] Backgrounds always pair with their `-content` foreground token.

## 8. 强制使用与验收

以下条款对消费端路由（`src/app/**`、`src/components/**`）强制生效；`src/app/admin/**` 是唯一例外，后台固定使用 Ant Design + Refine，不得改成 DaisyUI，也不得与消费端共用布局。验证基线：DaisyUI 5.7.50 + Tailwind CSS 4.3.3，`globals.css` 仅含 `@import "tailwindcss";` 与 `@plugin "daisyui";`，无 `tailwind.config.js/ts`。

### 8.1 DaisyUI 4 → 5 迁移对照

旧类名在 DaisyUI 5 中**不存在**，写了不会报错，但没有任何效果。

| DaisyUI 4 | DaisyUI 5 | 说明 |
|-----------|-----------|------|
| `input-bordered` | `input` | 5.x 的 `input` 自带边框，附加 `-bordered` 无效果 |
| `textarea-bordered` | `textarea` | 同上 |
| `select-bordered` | `select` | 同上 |
| `card-compact` | `card-sm` | 紧凑卡片改用尺寸修饰符 |
| `card-bordered` | `card-border` | 需显式描边时使用 |
| `join` / `join-item` | `flex gap-*` 或 `label.input` | **整个组件在 5.x 被移除**；输入框+图标用 `<label class="input"><svg/><input class="grow"/></label>` |
| `tabs-boxed` | `tabs-box` / `tabs-border` | `tabs-boxed` 已移除，`tabs-box` 为分段容器，`tabs-border` 为下划线 |
| `chip` / `chip-primary` / `chip-outline` | `btn btn-xs` / `badge` | 从未存在于 DaisyUI；筛选标签用 `btn btn-xs`（选中 `btn-primary`，未选 `btn-ghost bg-base-200`） |
| `alert-sm` / `alert-xs` | `alert` + `text-sm` | alert 没有尺寸修饰符，写了无效 |
| `form-control` / `label-text` | `fieldset` + `fieldset-legend` | 5.x 表单组结构已替换 |
| `btn-block` | `btn-block` | 仍然有效 |
| `table-zebra` | `table-zebra` | 仍然有效 |
| `badge-soft` | `badge-soft` | 仍然有效 |

### 8.2 手写 → DaisyUI 对照

| 手写实现 | 替换为 |
|----------|--------|
| `rounded-box` + `shadow` + `p-*` 面板 | `card` + `card-body` |
| 裸错误 `<div>`（无角色、无样式） | `alert alert-error alert-soft`（配 `role="alert"`） |
| 自定义 `fixed inset-0` 弹层 + 手写遮罩 | `<dialog class="modal">` + `modal-box` |
| 自定义 `@keyframes` 旋转动画 | `loading loading-spinner` |
| 自定义圆角小标签 `<span>` | `badge`（`badge-primary` / `badge-soft` / `badge-outline`） |
| `<ul>` + 手写链接列表作为导航 | `menu`（`menu-horizontal` 或 `menu-sm`） |

### 8.3 验收命令

新改动或修改的消费端页面必须通过，**无输出即通过**（命中即存在禁用类）。

```powershell
# Windows / PowerShell — 排除 admin 路由树
Get-ChildItem -Path src -Recurse -Include *.tsx |
  Where-Object { $_.FullName -notmatch '\\admin\\' } |
  Select-String -Pattern 'input-bordered|textarea-bordered|select-bordered|card-compact|card-bordered|alert-sm|\bjoin-item\b|tabs-boxed|chip-|form-control|label-text'
```

```bash
# macOS / Linux
grep -rn "input-bordered\|textarea-bordered\|select-bordered\|card-compact\|card-bordered\|alert-sm\|join-item\|tabs-boxed\|chip-\|form-control\|label-text" src --exclude-dir=admin
```

### 8.4 交付检查项

- [ ] 容器、输入、提示、弹窗、加载、标签均优先复用 DaisyUI 组件，未用工具类重造其视觉。
- [ ] 无 `input-bordered` / `textarea-bordered` / `select-bordered` / `card-compact` / `card-bordered` / `join-item` / `tabs-boxed` / `chip-*`。
- [ ] 无原始调色板（`bg-gray-*`、`text-blue-*` 等）、无 `#hex`、无 `rgb()`；背景与 `-content` 前景成对出现。
- [ ] 上述验收命令无输出，且结果已写入交付说明（改动的页面 + 复用的组件 + 命令实际输出）。
