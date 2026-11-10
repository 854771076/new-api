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
