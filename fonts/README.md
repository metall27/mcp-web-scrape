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
  (Courier New), plus shipped fontconfig aliases;
- `font-carlito` — Carlito (Calibri), ships its own Calibri alias;
- `font-urw-base35` — Nimbus lookalikes with aliases for Georgia,
  Verdana, Tahoma, Consolas, Impact, ...

Deliberately NOT vendored:

- Real Microsoft fonts (mscorefonts) — license-restricted redistribution;
- Noto CJK (SimSun / Microsoft YaHei stand-ins) — tens of MB, not in the
  top-30 desktop probe list of #107.

Resync note: if a vendored TTF is ever re-downloaded, keep the exact
upstream file (metrics are the point) and bump the commit message with the
upstream tag/date.
