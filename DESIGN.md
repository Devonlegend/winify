# Design

The visual system for the DevOps Control Center dashboard. It is a **dark
operator console** (Coolify-style): fixed left sidebar, dense but breathable
panels, hairline borders, one indigo accent, and semantic state colors. Tokens
live in `internal/static/web/css/app.css` under `:root`.

## Palette

| Token | Value | Use |
|---|---|---|
| `--bg` | `#0a0c11` | page ground |
| `--panel` | `#10131a` | cards, sidebar, form fieldsets |
| `--panel-2` | `#151925` | hover fills, secondary buttons |
| `--elev` | `#1a1f2b` | raised hover |
| `--border` | `#212734` | hairlines |
| `--border-strong` | `#2c3444` | control borders |
| `--text` | `#e7eaf0` | primary text |
| `--muted` | `#8a93a6` | secondary text |
| `--faint` | `#5f6878` | labels, placeholders |
| `--accent` | `#6366f1` | primary action, active nav, focus |
| `--accent-ink` | `#c7c9ff` | accent text on dark |
| `--ok` / `--warn` / `--err` / `--info` | `#34d399` / `#fbbf24` / `#f87171` / `#38bdf8` | status |

Each semantic color ships with a `*-soft` tint (e.g. `--ok-soft`) used as a
translucent background behind the same hue — status text is tinted from its own
hue, never neutral gray.

## Type

- UI: `system-ui, -apple-system, "Segoe UI", Roboto, ...`
- Data, IDs, logs, `.env`: a monospace stack (`--mono`)
- Base 15px / 1.55. `h1` 1.5rem, `h2` ~1.1rem, body weight 400, headings 600.
- Tracking stops at `-0.02em`; tabular numerals on stats.

## Space, radius, depth

- Radius: `--r-sm` 6px (controls), `--r` 10px (panels), `--r-lg` 14px (cards).
- Depth is declared once per element — either a 1px border or the single soft
  `--shadow`; never both a wide shadow and a border.
- More space above a heading than below it; tight groups, generous separation.

## Components

- **Sidebar** — brand, nav, a `Settings` group, and account/logout pinned to the
  bottom. The Projects section lists each project group with its resources.
- **Topbar** — sticky, translucent (`backdrop-filter`), mobile menu toggle, and
  the current section label.
- **Buttons** — `btn` (secondary), `btn-primary` (accent), `btn-ghost`,
  `btn-danger`, `btn-sm`. Legacy `button.secondary`, `.link` and form submit
  buttons are mapped onto the same system.
- **Forms** — `.admin-form` grid of labelled rows, `fieldset` groups, help
  placeholders. Focus is an accent border plus `--ring`.
- **Cards** — `.card` / `.metric-card`; resource cards are links with a status
  pill and a Deploy action.
- **Tables** — `.deploys`, faint uppercase headers, hairline rows, row hover.
- **Pills / badges** — `.status`, `.pill`, `.badge`, `.chip`, `.svc` for deploy
  status, target type, token scope, and service state.
- **Tabs** — in-page `.tabs` (Overview / Deployments / Environment / Settings).
- **Wizard** — `.wizard` with a step rail, `.choice` cards, a `.review` summary
  and a collapsed `.advanced` block. Degrades to a plain form without JS.
- **Deploy drawer** — fixed bottom console (`#deploy-drawer`) that streams the
  deploy log over SSE and shows live status.
- **Onboarding** — `.onboard` checklist shown on an empty dashboard.

## States

Every interactive element has hover, focus-visible (`--ring`), and disabled
states. Data surfaces have empty (`.placeholder`), loading (status `running` /
gauges at `--`), success, and error states; errors name the problem and appear
above the relevant log. The deploy drawer is `aria-live="polite"`.

## Motion

Minimal and purposeful: 0.12–0.2s ease on color/border/background, a 0.4s
transform on gauge fills (never width), and a 0.2s slide for the mobile sidebar.
One authored moment — the deploy drawer streaming in. All transitions are
disabled under `prefers-reduced-motion: reduce`.

## Responsive

At ≤900px the sidebar becomes an off-canvas panel (menu toggle) and the deploy
drawer spans the full width. Content is a single column with fluid form grids.

## Browser surfaces

Text selection, scrollbars, the caret, focus rings, and tabular numerals are all
themed from the palette rather than left at browser defaults.

## Accessibility

Keyboard-operable controls with visible focus, semantic landmarks (`nav`,
`main`), `aria-live` on the streaming log, and status conveyed by text as well
as color. Body and placeholder text target ≥4.5:1 on the dark ground.
