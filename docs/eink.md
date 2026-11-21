# E-ink redraw policy

The panel is the constraint, not the terminal. A full refresh stays visible for a fraction of a second; partial updates can leave ghosted text.

> Use deliberate full-frame redraws only: no spinner, cursor blink, animation, or colour-only meaning.

## Rebuild each frame

**Rule:** Rebuild the full frame from the model for every state change.

**Why:** Cell-by-cell patches can leave stale marks on an e-ink panel.

**Guard:** `TestFrameCount` and `TestFrameAudit`.

## Keep activity static

**Rule:** Show a static `…` while search runs or a book description loads. Do not animate or add a spinner.

**Why:** Extra frames without state changes create visible flashes.

**Guard:** `TestInFlightStatus`, `TestRunScreenDetailLoadingAndError`, and `TestFrameCount`.

## Hide the terminal cursor

**Rule:** Hide the cursor for the session. Mark query focus with a reversed cell.

**Why:** A blinking cursor repeatedly refreshes one panel region.

**Guard:** `TestCursorHidden` and `TestFrameAudit`.

## Show one frame per state change

**Rule:** Route every production `Show()` through `paint`; do not repaint for a key that changes nothing.

**Why:** Each full refresh is visible. Extra frames add flashes without useful state.

**Guard:** `TestFrameCount`. Review check: `grep -n '\.Show()' internal/tui/tui.go` should show only `paint`.

## Show the page as text and repaint it twice

**Rule:** The result page marker is text in the footer, as in `3 results · page 2`; never a spinner, bar, countdown, or animation, and never on the search row. A page request repaints once when it starts, with the static `…`, and once when its reply applies. A key that cannot change the page, such as `p` on page 1, does not repaint. An empty or failed page changes only the footer; the previous rows stay.

**Why:** A moving marker or an extra frame is a visible refresh on every page change. The search row keeps its caret in place across sources and pages.

**Guard:** `TestFrameCount` (the `n` and `p` cases), `TestMonoFrameParity`, and `TestPageMarkerAndHelp`.

## Make colour additive

**Rule:** `--mono` and non-empty `NO_COLOR` remove colour. Keep runes and attributes unchanged.

**Why:** Meaning must remain clear on monochrome panels and terminals.

**Guard:** `TestThemeParity` and `TestMonoFrameParity`.

## Keep the panel layout unboxed

**Rule:** Use one full-screen panel with left-aligned rows, no partial-width boxes, borders, or centered blocks. Fill the selected row across the full width. Below 80x24, show only the minimum-size message.

**Why:** Box edges and partial-width bands can force scrolling and leave trails.

**Guard:** `TestFrameAudit`. The results view is a single full-width column. Each record has a title line and a metadata line for author, year, format, and size; both lines of the selected record reverse across the full width.

## Keep the detail pane fixed

**Rule:** The detail view keeps ten field rows on rows 0–9 (the last is `download:`), a blank row 10, the dim `description:` label on row 11, the wrapped description on the ten rows from row 12 at 80x24, and the status or footer on the last row. `j`/`k` and `Down`/`Up` scroll the description one line; the ten field rows never move. An empty description shows `-` on row 12. A description that fits does not scroll.

**Why:** Scrolling the whole panel would hide the metadata and add redraw noise; a fixed body keeps the frame stable on e-ink.

**Guard:** `TestDetailLayout`, `TestDetailScrollClamps`, and `TestWrap`.

## Check on the device

**Rule:** Verify the redraw behavior on a Boox through Termux at 80x24.

**Why:** A desktop terminal cannot show the panel artifacts these rules prevent.

**Guard:** Manual device check; simulation tests cannot verify ghosting.

## Verify

Run `go test ./internal/tui -v`. On the Boox, run `booxd tui`, search for at least 22 results, move through the last result and one step past the end, open detail and help, then open the keyboard to resize. Check for no trail, flash, or panel scrolling.

## Review for policy regressions

Watch for a new production `Show()` call, a `time` import, `Style.Blink` or `Underline`, `Foreground` outside `newTheme`, box-drawing characters, borders, or a goroutine in the event loop.
