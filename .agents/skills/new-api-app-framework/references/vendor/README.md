# Vendored Third-Party References

This directory holds verbatim copies of external agent skills used to write the
standards in `../standards/`. They are **reference material only**. Do not edit
files here; the normative rules for building an external web app against new-api
live in `../standards/`.

Everything is copied without modification except for the file filter described
below. Read the original license before redistributing any content outside this
repository.

## Inventory

| Directory | Source (absolute path) | Version | License file |
|-----------|------------------------|---------|--------------|
| `nextjs-react-typescript/` | `C:\Users\Administrator\.trae-cn\skills\nextjs-react-typescript` | unversioned (single `SKILL.md`) | none shipped by the source |
| `vercel-react-best-practices/` | `C:\Users\Administrator\.trae-cn\skills\vercel-react-best-practices` | `1.0.0` (per `SKILL.md` front matter, author `vercel`) | `vercel-react-best-practices/SKILL.md` declares `license: MIT`; no separate LICENSE file exists in the source |
| `ui-ux-pro-max/` | `C:\Users\Administrator\.claude\plugins\cache\ui-ux-pro-max-skill\ui-ux-pro-max\2.5.0` | `2.5.0` (per `skill.json`) | `ui-ux-pro-max/LICENSE` (MIT, author `NextLevelBuilder`); the `ui-styling` sub-skill also ships `ui-ux-pro-max/skills/ui-styling/LICENSE.txt` |

## What Was Copied

### `nextjs-react-typescript/`

The complete source directory: `SKILL.md` only.

### `vercel-react-best-practices/`

The complete source directory: `SKILL.md`, `README.md`, `AGENTS.md` (the
compiled 69-rule guide) and `rules/*.md` (one file per rule).

### `ui-ux-pro-max/`

- Root documents: `LICENSE`, `README.md`, `skill.json`, `CLAUDE.md`.
- `.claude/skills/**` mapped to `skills/**`, keeping the relative structure
  (for example `.claude/skills/design/SKILL.md` becomes
  `skills/design/SKILL.md`).
- Only text-like assets were copied: `.md`, `.csv`, `.json`, `.cjs`, `.js`,
  `.yaml`, `.yml`, `.svg`.
- `skills/ui-styling/LICENSE.txt` was kept because it is a license document for
  content that is included.

Copied sub-skills: `banner-design`, `brand`, `design`, `design-system`,
`slides`, `ui-styling`, `ui-ux-pro-max`.

## What Was Excluded

Excluded from `ui-ux-pro-max/` (and why):

- **Font binaries**: 54 `.ttf` files (~5.16 MB) under
  `skills/ui-styling/canvas-fonts/`, plus the associated `*-OFL.txt` font license
  texts. They are canvas rendering assets, not guidance, and would dominate the
  skill size.
- **Python tooling**: `skills/**/scripts/*.py` and `skills/**/data/_sync_all.py`.
  The search/generation scripts require a Python runtime and the full data set;
  the rules they surface are already summarized in `../standards/`.
- **Other non-rule assets**: `.coverage`, `requirements.txt`, `.txt` data files,
  and any binary or generated artifact.
- **Repository scaffolding outside `.claude/skills/`**: `cli/`, `src/`, `docs/`,
  `preview/`, `screenshots/`, `.github/`, `.claude-plugin/`, `.in_use/`,
  `.gitignore`, `.npmignore`. Not part of the agent-facing skill.
- **`node_modules/` and `.git/`** are never copied.

Current vendored size: roughly **0.79 MB / 163 files**. No font binaries are
present.

## How to Update

Re-run the copy from the same absolute source paths after the upstream skill
updates:

```powershell
$dest = "<repo>\.agents\skills\new-api-app-framework\references\vendor"

Copy-Item -Recurse -Force "C:\Users\Administrator\.trae-cn\skills\nextjs-react-typescript" "$dest\nextjs-react-typescript"
Copy-Item -Recurse -Force "C:\Users\Administrator\.trae-cn\skills\vercel-react-best-practices" "$dest\vercel-react-best-practices"

$src = "C:\Users\Administrator\.claude\plugins\cache\ui-ux-pro-max-skill\ui-ux-pro-max\2.5.0"
$d = Join-Path $dest "ui-ux-pro-max"
Copy-Item -Force (Join-Path $src "LICENSE"),(Join-Path $src "README.md"),(Join-Path $src "skill.json"),(Join-Path $src "CLAUDE.md") $d

$skillsRoot = Join-Path $src ".claude\skills"
$allowed = @('.md','.csv','.json','.cjs','.js','.yaml','.yml','.svg')
Get-ChildItem -Recurse -File $skillsRoot |
  Where-Object { $allowed -contains $_.Extension.ToLowerInvariant() } |
  ForEach-Object {
    $rel = $_.FullName.Substring($skillsRoot.Length + 1)
    $target = Join-Path (Join-Path $d "skills") $rel
    New-Item -ItemType Directory -Force (Split-Path $target -Parent) | Out-Null
    Copy-Item -Force $_.FullName $target
  }
```

After updating, re-check two things:

1. `references/vendor/` stays well under 10 MB and contains no `.ttf`, `.otf` or
   `.woff` files.
2. The `ui-ux-pro-max` version in `skill.json` is updated in the inventory table
   above, and the source path table matches the cache directory actually used.

Then re-read the relevant vendored files and update `../standards/` where the
upstream guidance changed. The vendored copies are not loaded at runtime by the
application under development; they only inform the standards.
