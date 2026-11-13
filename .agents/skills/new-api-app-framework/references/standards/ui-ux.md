# UI/UX Standard

Applies to: any screen built in this stack (Next.js App Router + Tailwind CSS v4
+ DaisyUI 5). This is the baseline for review; product-specific branding may
change tokens, but not the accessibility or state rules.

Official documentation:

- WCAG 2.2 quick reference: https://www.w3.org/WAI/WCAG22/quickref/
- WAI-ARIA Authoring Practices (patterns): https://www.w3.org/WAI/ARIA/apg/patterns/
- MDN `prefers-reduced-motion`: https://developer.mozilla.org/en-US/docs/Web/CSS/@media/prefers-reduced-motion
- web.dev Core Web Vitals (CLS): https://web.dev/articles/cls
- Material Design state layers: https://m3.material.io/foundations/interaction/states

Vendored background: `../vendor/ui-ux-pro-max/skills/ui-ux-pro-max/SKILL.md`,
`../vendor/ui-ux-pro-max/skills/design-system/references/`.

## 1. Design Tokens

One set of tokens, defined once, consumed by every screen. In this stack the
tokens are DaisyUI theme variables plus the Tailwind spacing/type scale.

| Group | Rule | Example |
|-------|------|---------|
| Spacing | 4 px base, multiplicative steps | `2, 3, 4, 6, 8, 12, 16` → `gap-2`, `p-6`, `py-16` |
| Section rhythm | Three tiers only | component `gap-3/4`, section `py-12/16`, page `py-20/24` |
| Type scale | Fixed ramp, no ad-hoc sizes | `text-xs/sm/base/lg/xl/2xl/3xl` |
| Radius | One selector radius, one box radius | `rounded-lg` for controls, `rounded-xl` for cards |
| Elevation | At most three shadow levels | `shadow-sm` cards, `shadow-md` popovers, `shadow-lg` modals |
| Z-index | Named scale, never arbitrary | `10` sticky, `40` overlay, `50` toast |
| Color | Semantic tokens only | `bg-base-100`, `text-base-content/70`, `bg-primary` |

Rules:

- Add a token instead of a one-off value. If a screen needs a new size, ask
  whether the ramp is wrong before writing `text-[13px]`.
- Do not mix two radius languages (pill buttons with sharp cards) unless the
  brand explicitly requires it.
- Use the semantic color tokens from `daisyui.md`; never raw hex.
- Keep one visual hierarchy: page title > section title > body > caption. Do not
  create a fourth emphasis level with bold uppercase small text.

## 2. Typography and Contrast

- Body text is 16 px minimum (`text-base`). 14 px is allowed only for secondary
  metadata, never for primary reading content.
- Line height 1.5 for body, 1.25 for headings. Long-form text stays within
  65–75 characters (`max-w-prose`).
- Headings: 600–700 weight; body: 400; labels: 500. Avoid faux-bold and
  letter-spacing on body text.
- Contrast: 4.5:1 for normal text, 3:1 for large text (≥24 px or ≥19 px bold),
  3:1 for icons, borders of controls, and focus indicators.
- Dark mode is designed, not inverted. Re-check contrast per theme; a color that
  passes on light often fails on dark.
- Use tabular numerals (`tabular-nums`) for prices, quotas, token counts, and
  timers so digits do not shift.
- Never truncate the only copy of important text. Wrap, or truncate with an
  ellipsis **and** a tooltip/title.

## 3. Focus and Keyboard

```tsx
<button className="btn btn-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary">
  Save
</button>
```

Rules:

- Every interactive element is reachable and operable by keyboard: links,
  buttons, inputs, custom dropdowns, tabs, modals.
- Focus is always visible. Do not use `outline-none` without a replacement ring
  of at least 2 px and 3:1 contrast.
- Tab order follows visual order. Do not use positive `tabIndex`.
- Provide a "Skip to content" link as the first focusable element.
- Modal/dialog: focus moves in on open, is trapped inside, `Escape` closes, and
  focus returns to the trigger on close.
- Clickable containers (`div onClick`) are forbidden; use `button` or `a`.
- Icon-only buttons need `aria-label`; toggle buttons need `aria-pressed`.
- Do not block browser shortcuts, zoom, or paste into inputs.

## 4. States: Loading, Empty, Error, Disabled

Every data surface needs four states, not just the happy path.

| State | Requirement |
|-------|-------------|
| Loading | Skeleton matching the final layout for >300 ms; inline spinner inside the triggering button |
| Empty | Explain what would appear, why it is empty, and one primary action |
| Error | Cause + recovery action (retry, edit, contact); never only "Something went wrong" |
| Disabled | Reduced emphasis, `disabled` attribute, `cursor-not-allowed`, reason available |

Rules:

- Show the spinner in the control that caused the request, and disable that
  control until it settles.
- Use `aria-busy="true"` on the region being updated; announce async completion
  with `role="status"`.
- Reserve space for async content (fixed height or `aspect-ratio`) so CLS stays
  under 0.1.
- Empty states must not look like errors. Use neutral styling and an action.
- Error messages must be specific: what failed, what to do next. Keep raw stack
  traces and provider payloads out of the UI.
- Destructive actions need an explicit confirmation dialog and a danger style.

## 5. Responsive, Mobile-First

- Start at 375 px, then 768 px, 1024 px, 1440 px. Write the base style for
  mobile and add `sm:` / `md:` / `lg:` upwards.
- Never allow horizontal scrolling of the page body. Wide tables get their own
  `overflow-x-auto` container.
- Touch targets are at least 44×44 px with 8 px between adjacent targets.
- Body text stays ≥16 px on mobile to prevent iOS zoom on input focus.
- Use `min-h-dvh` instead of `100vh`; fixed headers/footers reserve padding so
  content is not hidden.
- Navigation adapts by width: bottom or top bar on small screens, sidebar at
  `lg`. Keep one navigation pattern per hierarchy level.
- Test one small phone width and one landscape orientation before delivery.

## 6. Motion

- Micro-interactions: 150–300 ms. Larger transitions: up to 400 ms. Nothing
  above 500 ms for UI feedback.
- Enter `ease-out`, exit `ease-in`; exits are ~60–70 % of the enter duration.
- Animate `transform` and `opacity` only. Animating `width`, `height`, `top`, or
  `left` causes layout thrash.
- Motion must express cause and effect (panel opening from its trigger, item
  added to a list). Decorative motion is not allowed.
- One or two animated elements per view. Stagger list items by 30–50 ms at most.
- Always honor reduced motion:

```css
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
    scroll-behavior: auto !important;
  }
}
```

- Never block input during an animation; interactions must remain interruptible.

## 7. Forms

- Every field has a visible `<label>` bound with `htmlFor`. Placeholder text is
  never the label.
- Mark required fields explicitly; do not rely on the asterisk alone — add
  "required" to the accessible name.
- Validate on blur, not on every keystroke. Re-validate on submit.
- Put the error below its field, wire `aria-invalid` and `aria-describedby`, and
  use `role="alert"` so it is announced.
- On submit failure, focus the first invalid field.
- Provide persistent helper text for rules (password length, file type), not only
  a placeholder.
- Use semantic input types (`email`, `tel`, `url`, `number`) and `autoComplete`
  so mobile keyboards and password managers work.
- Password fields get a show/hide toggle.
- Submit buttons show a loading state and are disabled while in flight; on
  success, confirm with inline feedback or a toast.
- Multi-step forms show progress and allow going back; long forms preserve drafts.
- Never pre-fill or echo secrets; never log form payloads containing credentials.

## 8. Content and Visual Quality

- Use one icon family with consistent stroke width; never emoji as structural
  icons.
- Images declare `width`/`height` (or `aspect-ratio`); meaningful images have
  `alt`, decorative ones `alt=""`.
- One primary call to action per screen; secondary actions are visually
  subordinate.
- Prefer native controls (button, select, dialog) over custom-built widgets.
- Keep state distinguishable without color: pair status colors with an icon or
  label.
- Numbers, dates, and currency use locale-aware formatting, not string
  concatenation.

## 9. Review Checklist

- [ ] Only tokens are used for spacing, type, radius, elevation, and color.
- [ ] Body text ≥16 px, line height ≥1.5, measure ≤75 characters.
- [ ] Text and UI contrast meet 4.5:1 / 3:1 in **both** themes.
- [ ] Keyboard reaches every control; focus ring is visible; modal traps and
      restores focus.
- [ ] Loading, empty, error, and disabled states exist for every data surface.
- [ ] No page-level horizontal scroll at 375 px; touch targets ≥44 px.
- [ ] Animations are 150–300 ms, transform/opacity only, and respect reduced
      motion.
- [ ] Every form field has a label, blur validation, and an accessible error.
- [ ] Destructive actions are confirmed and visually separated from primary ones.
