---
version: alpha
name: micro-git-github-style
description: A dense, quiet, developer-first interface modelled on GitHub (Primer). Content is code and history, so chrome recedes. Light and dark themes follow the system setting. One blue for links and focus, one green for the primary action, hairline borders instead of shadows, small radii, and a 14px base text size. Source of truth for the values is frontend/src/index.css; this file explains how to use them.

colors:
  light:
    background: "#ffffff"
    foreground: "#1f2328"
    muted: "#f6f8fa"
    muted-foreground: "#59636e"
    border: "#d1d9e0"
    link-primary: "#0969da"
    action: "#1f883d"
    destructive: "#d1242f"
    success: "#1a7f37"
    header: "#f6f8fa"
  dark:
    background: "#0d1117"
    foreground: "#e6edf3"
    muted: "#161b22"
    muted-foreground: "#9198a1"
    border: "#3d444d"
    link-primary: "#4493f8"
    action: "#238636"
    destructive: "#f85149"
    success: "#3fb950"
    header: "#010409"

typography:
  font-family: "-apple-system, BlinkMacSystemFont, 'Segoe UI', 'Noto Sans', Helvetica, Arial, sans-serif"
  mono: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
  page-title: { size: 20px, weight: 600 }
  section-title: { size: 24px, weight: 400, border-bottom: "1px solid border" }
  repo-name-in-list: { size: 18px, weight: 600, color: link-primary }
  body: { size: 14px, weight: 400 }
  caption: { size: 12px, weight: 400, color: muted-foreground }

rounded:
  control: 6px
  pill: 9999px

spacing:
  page-max-width: 1152px
  page-padding: 16px
  row-padding-y: 16px
  gap: 12px
---

# Design: GitHub style

## Principles
- **Dense and quiet.** 14px text, 32px-high controls, hairline `1px` borders. No drop shadows, no gradients, no large empty areas.
- **Two accent roles only.** Blue (`primary`) = links, current tab, focus ring. Green (`action`) = the single primary button on a screen (New repository, Add file, Register, Create). Red (`destructive`) = danger zone and errors.
- **Surfaces.** Page and cards use `background`; headers of boxes, the repo header band and the top bar use `muted` / `header`. Separation comes from borders, not elevation.
- **Theme.** Colors are CSS variables in `frontend/src/index.css` (`:root`, dark under `prefers-color-scheme` and `.dark`). Never put hex values in components; use `bg-background`, `text-muted-foreground`, `border-border`, `bg-action`, `text-primary`.

## Layout
- Top bar (`Nav`): 56px, logo + "micro git" on the left; avatar + username (links to the profile) and Log out on the right; Log in / Register when anonymous.
- Content width `max-w-6xl`, centred, 16px side padding.
- **Repository page:** header band with `owner / name` and a Public/Private pill, description, then tabs (Code, Commits, Compare, Settings for the owner) with a 2px blue underline on the current tab. Below: latest-commit bar, file table, README box, and a right column (About, Branch, Actions).
- **Repository list** (home, profile): "Find a repository…" input, then rows separated by a bottom border: blue 18px name, Public/Private pill, grey description, small "Created" line.
- **Profile** (`/:owner`): left column with the initial-letter avatar and name, right column with the Repositories list.
- **Settings:** left sidebar of section links (General, Commit history, Branches, Danger zone); right column of sections with a `24px` light heading and a bottom border. The Danger zone is a box with a red border; each row has a title, a grey explanation and an outline button on the right.

## Components
- **Button:** 32px high (`sm` 28px), radius 6px, weight 500. `default` = green `action` with white text. `outline` = `secondary` fill, `border` outline, `foreground` text. `destructive` for confirmed deletes. Destructive triggers in the Danger zone are outline buttons with red text.
- **Input / Textarea / Select:** 32px high, radius 6px, `border-input`, `background` fill, blue focus ring.
- **Badge (RepoBadge):** pill, 12px, `border` outline, `muted-foreground` text. "Public" or "Private".
- **Box (Section):** radius 6px, `border`, header strip in `muted` with a 14px semibold title.
- **Avatar:** circle, first letter of the username, `accent` fill. No uploaded pictures.
- **Dialogs:** use the shadcn AlertDialog/Dialog already in `components/ui`. Names the repo, confirms, shows errors inline.

## Do
- Keep one green button per view.
- Show visibility on every repository name (list, header).
- Keep file, commit and branch names in the mono font, `text-sm`.

## Don't
- No Apple-style pill buttons, photography tiles or large display headings.
- No new colors outside the tokens; no arbitrary pixel values in components (see `_docs/design-system.md`).
- No shadows to separate content; use borders.

## Notes
- Written by hand: GitHub is not in the getdesign.md catalog, so this file records the values already implemented in `index.css` (GitHub Primer light/dark) and the layout of the reference screenshots.
- The previous Apple `DESIGN.md` was replaced on 2026-10-18.
