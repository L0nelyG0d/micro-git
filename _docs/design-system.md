# Design system

> **Superseded for visual values.** The source of truth for colors, radius, density and surfaces is `DESIGN.md` plus `frontend/src/index.css` (GitHub Primer palette: blue `#0969da` links/focus, green `#1f883d` action, hairline borders, no surface shadows, `--radius` 0.375rem, 14px base text). Token values and density rules below are historical (indigo `#4F46E5`, `p-3` cards, `shadow-xs`) and kept only for the non-visual rules (components, accessibility, tone, oids and diffs). Where they disagree, `DESIGN.md` and `index.css` win.

Read this before touching any UI. Use the tokens and components below; never hardcode colors, spacing, or font sizes in components.

## Approach
- shadcn/ui on Tailwind CSS (v4) with Radix primitives, in the React + TypeScript app under `frontend/`
- Add components with `npx shadcn@latest add <name>`. They are copied into `frontend/src/components/ui/` and are our code: edit them there, don't wrap them in extra layers
- Reuse a shadcn component before writing a custom one
- Style: clean and minimal. The code and history are the content, so keep chrome quiet. One accent color, rounded soft cards, dense and compact layout, small muted labels, and no large empty areas

## Approved packages
Approved once; the agent may install these without asking:
- `tailwindcss`, `@tailwindcss/vite`, `tw-animate-css`
- `class-variance-authority`, `clsx`, `tailwind-merge`
- `lucide-react` (icons)
- the `@radix-ui/*` packages that shadcn components require
- `sonner` (toasts)
- `react-router-dom` (routing, task 13)

Any other dependency still needs the user's approval first.

## Themes and tokens
- Light and dark, following the system setting (`prefers-color-scheme`). Every color is a CSS variable, so components never contain theme-specific code
- Tokens live in `frontend/src/index.css`: light under `:root`, dark under `.dark` (applied from the system setting)
- Use Tailwind classes that read the tokens (`bg-background`, `text-muted-foreground`, `border-border`, `bg-primary`). Never write raw hex values in components

| Token | Light | Dark | Use |
|---|---|---|---|
| `--background` | `#F8FAFC` | `#0F172A` | page background |
| `--card` | `#FFFFFF` | `#1E293B` | cards, tables, forms |
| `--border` | `#E2E8F0` | `#334155` | dividers, input borders |
| `--foreground` | `#0F172A` | `#F1F5F9` | main text |
| `--muted-foreground` | `#64748B` | `#94A3B8` | labels, notes |
| `--primary` | `#0969da` | `#4493f8` | links, focus (blue); see `--action` `#1f883d` / `#238636` for the primary button |
| `--primary-foreground` | `#FFFFFF` | `#0F172A` | text on primary |
| `--ring` | `#0969da` | `#1f6feb` | focus ring |
| `--destructive` | `#B91C1C` | `#F87171` | errors, deleted lines, delete |
| `--success` | `#15803D` | `#4ADE80` | saved, added lines (custom token) |

Text must meet WCAG AA contrast (4.5:1) in both themes. Check any new color pair before using it.

## Typography, spacing, shape
- System font stack, no web fonts. Body text 16px; labels and table text 14px; helper text 12px
- Weights: 400 body, 600 headings and totals
- Use Tailwind's default spacing scale only. No arbitrary values like `p-[13px]`
- Radius from `--radius`: `0.5rem` for inputs and buttons, `0.75rem` (`rounded-xl`) for cards
- Density: card and form padding is 12px (`p-3`), gaps between blocks 12px (`gap-3`), table cells `px-2 py-1.5` with small muted `text-xs` headers, page headings `text-xl`. Surfaces use `border border-border`; no shadow utilities on surfaces. The nav is a compact sticky top bar; there is no bottom tab bar
- Code, file contents and oids use the system monospace stack (`font-mono`), `text-sm`
- Page content: `max-w-4xl`, centered, 16px side padding on mobile
- Mobile-first. Tables scroll horizontally inside their own container; the nav stays on one row


## Components
Use these shadcn components for these jobs:

| Need | Component |
|---|---|
| Buttons | `Button` (one primary per screen; `outline` for secondary; `destructive` for delete) |
| Text fields | `Input` with `Label` always visible above it (no placeholder-only labels) |
| Forms and validation | `Form` (react-hook-form) with the error text below the field |
| Branch switcher | `Select` |
| Repo list, file tree, commit list | `Table` |
| Grouping | `Card` |
| Delete confirmation | `AlertDialog`, saying what will happen (e.g. "The repository and all its commits will be deleted") |
| Save feedback | `Sonner` toast, short success or error. No `alert()` |
| Editor | `Textarea` |

Every list and page has three states: loading (`Skeleton`), empty (a hint about what to do next), and error (with a retry button). Disable the submit button while a request is in flight.



## Accessibility
- Every input has a label; every icon-only button has an `aria-label`
- Keep the visible focus ring on all interactive elements. Never remove outlines
- Everything works with the keyboard. Dialogs trap focus and close with Escape (Radix does this; don't break it)
- Respect `prefers-reduced-motion`

## Tone
- Sentence case for buttons, headings, and labels ("New repository", not "New Repository")
- Error messages say what happened and how to fix it ("Email is already registered. Try logging in instead.")
- No exclamation marks, no jargon

## Code, oids and diffs
- Show oids as 7 characters in `font-mono`, with the full oid in a `title` and copyable
- Dates: `DD.MM.YYYY HH:mm`
- Diff lines: added lines use `--success`, removed lines `--destructive`, each with a `+` / `-` marker in the gutter. Never color alone
- Code and diff blocks scroll horizontally inside their own container and keep line numbers visible
