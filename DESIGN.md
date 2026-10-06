# Design

The visual system for the DevOps Control Center dashboard. It is a **monochrome
operator console**: warm-neutral black surfaces, a fixed left sidebar, dense but
breathable panels, hairline borders, and one source of emphasis — light ink.
There is no brand hue. State is carried by value, weight, and one inverted chip,
not by color. Tokens live in `internal/static/web/css/app.css` under `:root`.

## Palette

Every surface is a shade of warm black.

| Token | Value | Use |
|---|---|---|
| `--bg` | `#0a0a09` | page ground |
| `--panel` | `#121210` | cards, sidebar, form fieldsets |
| `--panel-2` | `#191917` | hover fills, secondary buttons |
| `--elev` | `#211f1c` | raised hover |
| `--border` | `#262522` | structural hairlines (panels, cards, dividers) |
| `--border-strong` | `#37352f` | emphasis edges only (dashed empty states, modals) |
| `--line` | `rgba(236,233,228,0.08)` | embedded control edge (buttons, fields) |
| `--line-strong` | `rgba(236,233,228,0.15)` | embedded edge on hover, outlined badges |
| `--text` | `#ece9e4` | primary text (warm off-white, never pure white) |
| `--muted` | `#a8a49d` | secondary text |
| `--faint` | `#85817a` | labels, placeholders (dimmer than `--muted`, still ≥4.5:1 on `--bg`) |

**Accent = ink.** `--accent: #ece9e4` with `--accent-contrast: #0a0a09`. Primary
buttons are light ink with black text; active nav, focus rings and selected
choices reuse the same ink. `--accent-soft` is a faint translucent white fill.

**Status.** `.status-*` / `.pill-*` are grayscale. Success is a restrained
outline (`--ok-soft` on `--ok`), running is a mid-gray with a pulsing dot,
queued/waiting is a dim dashed outline. **Failure is the only inverted chip**
(`--accent` ground, `--accent-contrast` text) so the eye lands on it first
without a red hue. `--ok` / `--warn` / `--err` / `--info` are gray values; the
`*-soft` tints are translucent white.

## Type

- UI: `"IBM Plex Sans"` → system sans fallback.
- Display headings (`h1`, login title): `"Newsreader"` serif, weight 500, tight
  tracking (`-0.02em`) and `1.12` line-height. This is the editorial signature;
  `h2`/`h3` stay sans for dense data.
- Data, IDs, logs, `.env`: `"IBM Plex Mono"` (`--mono`).
- All three load from Google Fonts with a full local fallback stack, so an
  offline host still renders cleanly.
- Base 15px / 1.6. `h1` 2rem, `h2` 1.05rem, body weight 400, headings 600.
- Tabular numerals on stats.

## Space, radius, depth

- Radius: `--r-sm` 4px (controls, badges), `--r` 8px (panels), `--r-lg` 10px
  (cards). Crisp, never pill-shaped for containers or primary buttons.
- Depth is declared once per element — either a 1px hairline or the single soft
  `--shadow`; never both.
- More space above a heading than below it; tight groups, generous separation.

## Background depth

The body carries one fixed radar of warm light (`radial-gradient` at ~3.5%
opacity, top-right) so wide sections are not flat black. It is a single
background-image layer, never a scrolling element.

## Components

- **Sidebar** — brand mark (geometric stack glyph), nav, a `Settings` group, and
  account/logout pinned to the bottom. Active items use a `--panel-2` fill with
  a short centred ink rule at the leading edge — never a full-height bar, which
  reads as a bracket artifact inside the pill radius.
- **Topbar** — sticky, translucent (`backdrop-filter`), mobile menu toggle, and
  the current section label.
- **Buttons** — `btn` (secondary), `btn-primary` (ink), `btn-ghost`,
  `btn-danger`, `btn-sm`. Secondary and danger buttons are **embedded**: a
  `--panel-2` fill with a `--line` edge, not an outlined box; only the primary
  button is a solid ink block. Buttons depress with `scale(0.98)` on `:active`.
- **Forms** — `.admin-form` grid of labelled rows, `fieldset` groups, help
  placeholders. Fields use a `--bg` well and a `--line` edge so they read as
  recessed into the panel; focus is an ink border plus `--ring`.
- **Cards** — `.card` / `.metric-card`; resource cards are links with a status
  pill and a Deploy action. Hover shifts the hairline, not a shadow.
- **Tables** — `.deploys`, faint uppercase headers, hairline rows, row hover.
- **Pills / badges** — `.status`, `.pill`, `.badge`, `.chip`, `.svc` for deploy
  status, target type, token scope, and service state.
- **Tabs** — in-page `.tabs` (Overview / Deployments / Environment / Settings).
- **Wizard** — `.wizard` with a step rail (active step is an ink chip),
  `.choice` cards, a `.review` summary and a collapsed `.advanced` block.
  Degrades to a plain form without JS.
- **Deploy drawer** — fixed bottom console (`#deploy-drawer`) that streams the
  deploy log over SSE and shows live status.
- **Onboarding** — `.onboard` checklist shown on an empty dashboard.

## States

Every interactive element has hover, focus-visible (`--ring`), and disabled
states. Data surfaces have empty (`.placeholder`), loading (status `running` /
gauges at `--`), success, and error states; errors name the problem and appear
above the relevant log. Errors read distinctly from neutral notices through a
stronger ink border and heavier text — deliberately not a thick left tab. The
deploy drawer is `aria-live="polite"`.

## Motion

Minimal and purposeful: 0.12–0.2s ease on color/border/background, a 0.4s
transform on gauge fills (never width), a 1.6s opacity pulse on the running
status dot, and a 0.2s slide for the mobile sidebar. All transitions are
disabled under `prefers-reduced-motion: reduce`.

## Responsive

At ≤900px the sidebar becomes an off-canvas panel (menu toggle) and the deploy
drawer spans the full width. Content is a single column with fluid form grids.

## Browser surfaces

Text selection, scrollbars, the caret, focus rings, and tabular numerals are all
themed from the black scale rather than left at browser defaults.

## Accessibility

Keyboard-operable controls with visible focus, semantic landmarks (`nav`,
`main`), `aria-live` on the streaming log, and status conveyed by text and
border treatment as well as fill. Body and placeholder text target ≥4.5:1 on the
dark ground.
