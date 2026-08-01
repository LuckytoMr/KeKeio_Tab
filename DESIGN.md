# Design

## System

kekeio is a productivity tool UI. It should feel quiet and precise, with dense but readable controls, restrained motion, and strong contrast.

## Color Strategy

Use a light, airy base with neutral ink and a small set of functional accents. Avoid saturated single-hue themes. The default style uses:

- `--bg`: oklch(97% 0.01 230)
- `--surface`: oklch(100% 0 0)
- `--surface-soft`: oklch(95% 0.012 230)
- `--ink`: oklch(24% 0.015 250)
- `--muted`: oklch(45% 0.02 250)
- `--accent`: oklch(61% 0.16 245)
- `--accent-2`: oklch(66% 0.14 175)
- `--danger`: oklch(58% 0.19 25)

Always provide sRGB fallbacks before OKLCH declarations.

## Typography

Use system UI fonts for extension reliability:

```css
font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
```

Use compact headings inside tool surfaces. Do not use hero-scale type except the optional brand mark on the new tab.

## Layout

The first screen is the actual new tab:

- Centered brand/search/navigation stack.
- Left utility rail for power actions.
- Stable icon grid dimensions so labels and hover states do not shift layout.
- Settings opens as a full-height drawer/dialog, not a landing page.

## Components

- Icon buttons use lucide icons with `aria-label`.
- Repeated shortcut tiles are the only card-like items.
- Settings panels use full-width bands or unframed sections; avoid nested cards.
- Menus and dialogs use fixed positioning so they are not clipped by scroll containers.

## Motion

Use subtle transform/opacity transitions under 180ms. Respect `prefers-reduced-motion`. Wallpaper changes use crossfade only.
