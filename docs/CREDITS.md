# Credits

## hoaxisr/awg-manager — Tokyo Night theme + IBM Plex fonts

The Tokyo Night visual design (color palette, design tokens, layout conventions)
and the self-hosted IBM Plex Sans / IBM Plex Mono woff2 font files in
`web/static/fonts/` are ported from **[hoaxisr/awg-manager](https://github.com/hoaxisr/awg-manager)**
(MIT license, Copyright (c) 2026 hoaxisr).

### What was ported

- **Design tokens** — the Tokyo Night palette (accent `#7aa2f7`, bg
  `#1a1b26`/`#16161e`/`#24283b`, text `#c0caf5`/`#a9b1d6`/`#737aa2`, border
  `#3b4261`, success/error/warning/info/broken) + the light-theme overrides +
  the semantic-tint/border conventions (18%/40% via `color-mix`), radii
  (12px/6px/999px), shadow, z-index scale, settings-gap. Mapped onto DaisyUI v4
  OKLCH CSS variables in `web/static/css/tokyo-night.css`.
- **IBM Plex fonts** — the 14 woff2 files in `web/static/fonts/` (Sans
  400/500/600/700 + Mono 400/500/600, Latin + Cyrillic subsets) + the
  `@font-face` declarations (unicode-range splits, font-display: swap) in
  `web/static/css/fonts.css`.
- **Component conventions** — the card/table/badge/input/icon-button/scrollbar/
  settings-inset visual patterns in `web/static/css/app.css` (the `.tn-*`
  classes), adapted from their `frontend/src/app.css` + `tunnel-layout.css` +
  `serverCardShared.css`.

### What was NOT ported (different stack)

hoaxisr/awg-manager is a Svelte + Skeleton Labs + Tailwind v4 app; angry-box is
HTMX + Go Templ + DaisyUI v4. No Svelte components, no Skeleton primitives, no
Tailwind v4 `@theme` block were copied — only the **visual language** (CSS
tokens, fonts, conventions) was re-implemented in our stack. The AWG CPS
live-capture LOGIC (`internal/chain/awgcapture.go`) was separately ported in an
earlier cycle (also MIT, same source) — see `docs/PROGRESS.md` §0.7.

### License

The full MIT license text is preserved at `docs/LICENSES/hoaxisr-awg-manager-MIT.txt`.

```
MIT License

Copyright (c) 2026 hoaxisr

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```