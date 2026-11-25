# Scriba — Design System

Scriba is a knowledge base written by AI agents and read by people: engineering journals, tickets, ADRs, runbooks and postmortems in Markdown. The agent is the scribe; the human is the reader and reviewer. The interface must feel like a quiet, well-set manuscript desk: calm, precise, typographically serious. Reading long text is the primary job; everything else (dashboard, tree, search) serves it.

## Principles
1. **Reading first.** Prose is set for long-form reading: serif body, ~68 characters per line, generous line height. Chrome recedes.
2. **Ink and margin.** Content lives on a pale "paper" surface; UI chrome is thin hairlines, not boxes and shadows.
3. **The scribe is visible.** Agent activity (live writing, unread changes, authorship) is always legible at a glance through one warm "scribe" color, distinct from the accent.
4. **Calm density.** Dashboards are dense but quiet: small numbers in tabular figures, no gradients, no glow, no decorative illustrations.

## Color tokens
Light theme (default):
- `paper` #F5F6F4 — page background, a cool off-white with a slight green-grey bias
- `surface` #FFFFFF — cards, inputs, editor
- `ink` #1C2227 — primary text
- `muted` #5B6670 — secondary text, metadata, labels
- `line` #E1E5E2 — hairline borders and dividers (1px)
- `accent` #1D6B74 — deep teal: links, wikilinks, selection, primary buttons, focus ring, heatmap scale
- `accent-soft` accent at 9% opacity — selected nav item, chips, search highlight
- `scribe` #C46A3A — warm copper, used ONLY for agent presence: live-writing caret, agent avatar, "editing now" badge
- `code-bg` #EEF1EF — inline code and code blocks
- Semantic: success #3A9A5B, warning #B7791F, critical #B4443C (status only, never decorative)

Dark theme:
- `paper` #111417, `surface` #171B1F, `ink` #E2E7E9, `muted` #95A0A7, `line` #283037
- `accent` #62B8C0, `accent-soft` accent at 12%, `scribe` #D9875A, `code-bg` #1D2329
Dark is a re-tuned palette, never an inversion.

## Typography
- **Reading / headings:** Literata (serif, optical sizes). Note titles 34px/1.2 weight 600, letter-spacing -0.01em. H2 22px/1.3 600. Prose body 18px/1.72 weight 400, max-width 68ch.
- **Interface:** IBM Plex Sans. UI body 14px/1.45; small labels 12px; section eyebrows 11px uppercase, letter-spacing .06em, weight 600, muted color.
- **Code, paths, agent names, file tree:** JetBrains Mono 12–14px.
- Numbers in dashboards and tables use tabular figures.
- Headings use balanced wrapping. Never use all-caps for anything but small section eyebrows.

## Shape, spacing, elevation
- Radius: 8px for cards, inputs, buttons; 6px for nav items; 999px only for chips and pills; 2px for heatmap cells.
- Spacing scale (px): 4, 6, 8, 12, 16, 24, 32, 48. Page gutters 24px desktop, 16px mobile.
- Elevation: flat. Borders (1px `line`) separate regions. A single soft shadow only for floating layers (search results, menus).

## Layout
- App shell: left sidebar 248px (brand, views, note types with counts, file tree in mono) · main column · top bar with search.
- Note reader: reading column (max 68ch) + right context rail 240px (outline "In this note", backlinks "Linked from", properties: date, author agent, words, reading time). Rail collapses below the article under 1000px.
- Dashboard: summary tiles row (Notes, Words, Indexed, Disk) → contribution heatmap (52 weeks × 7 days, 11px cells, 5 accent steps) → two columns: recent activity by agent, storage breakdown bar with legend.
- Editor: split view, Markdown source in mono on the left, live preview on the right.
- Connector consent: same single column as Connectors; one highlighted card with the client name, the return host, the agent name input, a primary "Aprobar" and a secondary "Rechazar".

## Components
- **Wikilink:** accent text with a 1px underline at 40% accent; missing target = muted text with dashed underline.
- **Unread dot:** 7px accent dot next to a file in the tree (VS Code style); pulsing scribe-colored dot while an agent is writing.
- **Change marker:** new blocks since last read get a 3px accent bar in the left margin and a 7% accent wash; banner above the note: "Changes since your last read · Mark as read".
- **Agent presence badge:** pill with a 22px round avatar in `scribe` color + "Claude Code is editing journal/2026-11-16.md"; live caret in `scribe` color at the end of the text being written.
- **Search (⌘K):** input with leading icon and kbd hint; floating results list with title, mono path, and snippet with highlighted match.
- **Chips:** note type in accent-soft; tags as outlined muted pills prefixed with #.
- **Buttons:** primary = solid accent with surface-colored text; secondary = surface with 1px line border, accent on hover.
- **Blockquote:** 3px accent left rule, italic, muted.
- **Tables:** sans 14px, hairline row dividers, no zebra stripes, no vertical borders.

## Voice
Spanish-first UI copy with technical terms in English. Short, active, specific labels ("Marcar como leído", "Enlazada desde", "En esta nota"). No emoji, no exclamation marks, no marketing tone.

## Avoid
Purple/blue gradients, glassmorphism, neon or acid accents, heavy drop shadows, card-in-card nesting, centered hero sections, stock illustrations, emoji as icons.
