# EzhikLB design system

The local design-system search tool could not run because Python is not
installed in the workspace. These tokens use the skill's general dashboard,
accessibility and motion defaults plus the visual language of `bio.html`.

## Character

Dark, technical and calm. Warm neutral highlights distinguish EzhikLB from
generic blue infrastructure dashboards. Information density is high, but the
layout keeps clear grouping and generous interactive targets.

## Tokens

- Background: `#090a0b`
- Raised surface: `#111315`
- Elevated surface: `#17191c`
- Primary text: `#f1eee9`
- Secondary text: `#a19d96`
- Muted text: `#716d67`
- Border: `rgba(255,255,255,.08)`
- Accent: `#e7e3dc`
- Healthy: `#65c795`
- Warning: `#d6ad62`
- Critical: `#df7373`

Inter is used for UI text and JetBrains Mono for addresses, ports, versions and
metrics. The application must remain usable with system fonts while web fonts
load or when external font loading is blocked.

## Interaction

- Minimum interactive target: 44 by 44 px.
- Visible keyboard focus on every control.
- Motion: 150-250 ms and disabled under `prefers-reduced-motion`.
- Never use color as the only status signal; pair it with text and an icon.
- Dialogs have a title, Escape handling and focus restoration.
- Forms use persistent labels and field-local errors.

