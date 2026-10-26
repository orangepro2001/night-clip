# UI refresh: compact professional (2026-10-27)

Direction chosen by the user: **紧凑专业工具风**. This is a visual/interaction pass only; core APIs,
sync, pairing, vault format and background clipboard behavior are untouched.

## Goals

- One identity across Android and Windows: calm slate surfaces, deep-blue primary, teal for positive
  state, small radii and quiet 1 px borders.
- Higher information density without feeling cramped: 12–14 sp body text, 8–12 dp spacing,
  34–40 dp action rows, cards instead of floating white boxes.
- Keep every existing flow reachable; no feature removal and no data migration.

## Android (Compose)

- `ui/Theme.kt`: fixed light/dark schemes instead of wallpaper-derived dynamic color; compact
  typography scale; 12 dp default card radius.
- `ui/Components.kt`: `PageHeader`, `PanelCard`, `StatusPill`, `EmptyState`, denser `SectionTitle`.
- `AppRoot`: 66 dp navigation bar with tonal selected indicator, primary-colored selected icon/label.
- Devices: header + Tailscale status, grouped quick-send panel, compact peer cards with state pill
  and text actions, empty state for no peers.
- Inbox: header with count/clear, outlined content cards, metadata row, compact action buttons.
- Settings: grouped panels, compact switch/choice rows, header.
- Vault list: 12 dp cards, reduced row padding; tap-to-copy and separate view/edit buttons retained.

## Windows (WPF)

- `Themes/ClipBridgeTheme.xaml`: centralized colors, font, buttons, text boxes/password boxes,
  list items, cards, nav list and progress bars.
- `MainWindow`: 212 px sidebar with brand block, glyph+label navigation and selection accent;
  compact status bar; 1040×720 default, 880×540 minimum.
- Devices/Inbox/Transfers: card style, page titles/subtitles, semantic colors, compact spacing.
- Settings: grouped cards for sync, files, firewall, update and about.
- Diagnostics and vault panels inherit the same tokens; remaining hardcoded grays replaced with
  theme brushes.

## Deliberately not done

- No dark-mode switch on Windows (system theme integration would be a separate project).
- No new navigation destinations, no feature changes, no density setting.
- No custom window chrome/title bar; the standard Windows frame stays for accessibility.
