# Vendored fonts (#107)

Metric-compatible open-source replacements for two Windows font families
that no packaged Alpine font provides. Installed into the Docker image at
`/usr/share/fonts/vendored/` and aliased via
`fontconfig-windows-aliases.conf` (Windows family name -> vendored face).

| File | Family | Replaces | License | Source |
|---|---|---|---|---|
| `Caladea-*.ttf` | Caladea | Cambria (metric-compatible) | SIL OFL 1.1 (`OFL.txt`) | https://github.com/googlefonts/caladea |
| `Selawik-*.ttf` | Selawik | Segoe UI (metric-compatible) | SIL OFL 1.1 (© Microsoft; reserved font name "Selawik" — see note) | https://github.com/microsoft/Selawik (release 1.01) |

License note: both faces are SIL OFL 1.1. Selawik carries a **reserved font
name** ("Selawik" is a Microsoft trademark) — we do not redistribute it under
that name as a standalone family; the container only ever requests "Segoe UI"
and fontconfig resolves it to the vendored file, matching how Linux distros
ship the face. `OFL.txt` is the license text for both.

The rest of the Windows coverage comes from Alpine packages (see Dockerfile):

- `font-croscore` — Arimo (Arial), Tinos (Times New Roman), Cousine
  (Courier New). The package ships no fontconfig conf of its own; the
  Arial->Arimo / Times New Roman->Tinos / Courier New->Cousine aliasing comes
  from the fontconfig package's `30-metric-aliases.conf`;
- `font-carlito` — Carlito (Calibri). Same: the Calibri->Carlito alias lives
  in fontconfig's `30-metric-aliases.conf`, not in the package;
- `font-urw-base35` — Nimbus Roman/Sans/Mono PS, P052 (Palatino), Z003
  (Gothic), C059 (Century) lookalikes. Font files only: its `69-urw-*.conf`
  alias files sit in `conf.avail` unenabled, and none of them alias Windows
  family names anyway — without `fontconfig-windows-aliases.conf`,
  Georgia/Verdana/Tahoma/Consolas/Impact all resolve to DejaVu (measured).

So the full alias story in the container is: fontconfig's own
`30-metric-aliases.conf` (the Arial/Times/Courier/Calibri/Cambria cluster) +
`fontconfig-windows-aliases.conf` (everything else, incl. the generic pins).
Nothing else aliases families.

Note on the generic-family pin: `fc-match sans-serif` -> Nimbus Sans and
`fc-match monospace` -> Nimbus Mono PS (the pins lose to fontconfig's
Helvetica->Arial / Nimbus Mono PS->Courier fallback chains under `fc-match`),
but that is not what a web page sees. Verified by measureText in headless
Chromium in the image: `sans-serif` resolves to Arimo widths, `serif` to
Tinos, and `monospace` to both Cousine and Nimbus Mono PS within 0.05px —
i.e. the conf comment "probe strings match Windows widths" holds for all
three generics in the surface that actually fingerprints. A stage-C mock of
generic resolution should still use the real per-family metrics, not assume
the pin target.

Deliberately NOT vendored:

- Real Microsoft fonts (mscorefonts) — license-restricted redistribution;
- Noto CJK (SimSun / Microsoft YaHei stand-ins) — tens of MB, not in the
  top-30 desktop probe list of #107.

Resync note: if a vendored TTF is ever re-downloaded, keep the exact
upstream file (metrics are the point) and bump the commit message with the
upstream tag/date.
