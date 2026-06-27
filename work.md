# UI Redesign Handoff — eac-xld-logchecker-web

## Context
Go web app (`html/template`, server-rendered on POST). Results render on the same
page after upload. Redesigned the single 960px stacked-column layout into a
responsive two-column dashboard. **No Go files touched** — only
`templates/index.html`, `styles/main.css`, `scripts/main.js`.

## Constraints honored
- Go template logic untouched (`{{if}}`, `{{range}}`, `{{.Field}}` bindings same).
  Only wrapper divs/classes restructured.
- All JS-referenced IDs preserved: `theme-toggle`, `upload-form`,
  `file-drop-zone`, `logfile`, `file-selected-name`, `btn-analyze`,
  `output-container`, `btn-expand`, `log-modal`, `log-modal-backdrop`,
  `log-modal-body`, `btn-modal-close`.
- Section labels kept as **"Summary"** and **"Log Output"** (NOT renamed to
  METADATA/ORIGINAL REPORT, per user).

## Layout structure
```
.page-wrapper (max-width 1320px)
  header.app-header        — flex bar: .app-brand (logo+title+subtitle) | .app-actions (theme/GitHub/Help icon-btns)
  {{if .Error}} .alert-error {{end}}
  .content-grid {{if .Summary}}with-sidebar{{end}}    — grid: minmax(0,1fr) 360px
    .main-col
      form#upload-form
        .card.upload-card → .upload-card-body[ .file-drop-zone | .supported-rippers ] + #file-selected-name
        button#btn-analyze (full-width, below card)
      {{if .Summary}}  .card "Summary" → .summary-grid (auto-fit sub-cards) {{end}}
      {{if .Details}}  .card "Details" {{end}}
      {{if .ResultID}} .card with .log-toolbar (search + #btn-expand + #wrap-toggle) + #output-container {{end}}
    {{if .Summary}} aside.sidebar (sticky)
        .card.score-card (ring)
        {{if .DiscIDs}} .card "Disc IDs" {{end}}
    {{end}}
  {{if .ResultID}} log modal (markup/IDs unchanged) {{end}}
```
Sidebar only renders when results exist → GET upload page stays full-width.

## Key implementation details

**Score ring** (`.score-card` in sidebar): Score is still emitted server-side as
a normal summary item but **hidden via CSS**
(`.summary-item[data-key="score"]{display:none}`). JS reads
`#summary-grid [data-key="score"] .summary-value`, then:
- Sets `#score-number` + ring fill via CSS var `--score-pct` (SVG
  `stroke-dashoffset`, circumference ≈326.7 for r=52).
- **Negative score → red empty ring** (`.neg` class on ring + number).
- **"Perfect Rip!" label + description only when score === 100**; otherwise the
  number shows alone (label/desc emptied).

**Summary sub-cards**: per-field accent icons via
`-webkit-mask-image`/`mask-image` inline-SVG data URIs keyed by `data-key`
(hexagon=ripper, code-brackets=ripper_version, globe=language,
shield-check=checksum_state, document=combined_log/rdbarr_rip). Grid
`repeat(auto-fit, minmax(150px,1fr))`.

**Disc IDs rows**: `grid-template-columns: auto 1fr auto` = abbreviation square +
main + actions. Squares get 2-letter labels + gradient colors via `data-key` CSS
(`MB`/`CT`/`DB`/`AR`, gnudb=music-note mask). Actions = copy button
(`.discid-copy` with `data-copy="{{.Value}}"`) + status pill
(`.badge-ok`/`.badge-miss`/`.badge-unknown`). External-link arrow
(`.discid-link`) only when `.URL` present (AccurateRip has no URL by design;
FreeDB URL was removed earlier per user).

**Log viewer** (JS-enhanced in the existing `fetch().then`):
- `buildLogLines()` parses the fetched HTML's `<pre>`, splits `innerHTML` on
  `\n`, rebuilds as `.log-line` rows = `.ln` (gutter number) + `.lc` (content).
  Each `.lc` stores original HTML in `_orig`. Assumes logchecker color spans
  never straddle newlines (true for EAC/XLD).
- **Word-wrap** (`#wrap-toggle`, default checked): toggles `.nowrap` on container
  + modal body (`white-space: pre` vs `pre-wrap`).
- **Search** (`#log-search`): regex-escaped, case-insensitive; wraps matches in
  `<mark>` within text nodes only (preserves coloring spans), dims non-matching
  rows (`.log-line.dim`); clears on empty.
- **Copy** (`.discid-copy`): `navigator.clipboard.writeText` with `execCommand`
  fallback; brief `.copied` green state.

**Shared `.icon-btn`** class now used by theme/GitHub/Help/expand/copy/modal-close.
Removed old `.theme-toggle` (was `position:fixed`), `.btn-expand`, `.upload-area`
rules.

## Responsive (CSS, multiple blocks)
- **≤900px**: sidebar drops below main (static, non-sticky); `.upload-card-body`
  stacks; `.log-toolbar` wraps (search full-width row 1, tools row 2).
- **≤480px**: shrink header/logo/title, smaller `.app-actions` buttons; summary
  2-col; disc-ID actions move to own row; ring `clamp()`-sized; narrower gutter.
- **(hover:none) and (pointer:coarse)**: 44×44px min on all interactive elements.
- **≤600px**: original modal `96vw/90vh` rule kept intact.

## Verification status
- `go build` clean; server renders full dashboard on POST; GET has no sidebar.
  Confirmed wiring: copy `data-copy` values, badges (Found/Matched), score=100
  readable by ring, CSS braces balanced (218/218), no stale rules.
- **NOT yet visually verified** — no browser/node in this env. Pending manual
  checks at 375/414/768px: ring fill + negative/red case (needs an rdbarr or
  low-score log), summary reflow, word-wrap toggle, search highlight/dim, copy
  flash, modal+Esc, theme toggle.
- To run locally: `go run .` or `go build -o /tmp/lcweb . && PORT=5151 /tmp/lcweb`,
  then upload a sample (e.g. `../logchecker-go/tests/logs/xld/utf8/macroman.log`).

## Earlier related work (same session, already done)
Backend already integrates logchecker-go v1.14.9 disc IDs: `collectDiscIDs()` in
`analyze.go` runs AccurateRip + gnudb lookups concurrently (8s shared timeout,
`discLookupTimeout` in `config.go`), populates `analysisResult.DiscIDs` (also in
JSON `/api`). MusicBrainz/CTDB have link URLs; AccurateRip is badge-only; gnudb
has ID+URL+match badge+title; FreeDB is ID-only.

## Status
Changes are **not committed** yet.
