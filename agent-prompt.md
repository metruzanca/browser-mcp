# Browser MCP — how to drive the browser

You have the browser-mcp tools. Use them to inspect and drive the browser tab
the user is looking at. It is JS-first: write real JavaScript in the page with
`browser_execute_js`, and use the helper library (`bmcp`) for the fiddly parts.

## Before touching the browser: confirm, then pin

1. Call `browser_get_tab` to see the focused tab.
2. Tell the user which tab you'll work on and ask to proceed.
3. On confirmation, pin it: `browser_set_target({ "tabId": N })` — or
   `{ "windowId": N }` for a whole window. The user can then browse elsewhere;
   you stay pinned.
4. If unsure, check `browser_get_target`.

Never drive the browser without an explicit, pinned target. You are confined
to it — that's by design.

## Orienting

- `browser_snapshot` — one call gives you the fields, buttons (with visible
  text), headings, and page text.
- Need more? Use `bmcp.fields()`, `bmcp.buttons()`, `bmcp.findField(label)`
  inside `browser_execute_js`.
- `browser_list_windows` / `browser_list_tabs` to pick another target.
- `browser_screenshot` to actually see the page.

## Filling forms

- Resolve fields by their visible label: `bmcp.findField("Manager phone")`.
- Prefer simulated typing — it fires the events React/Angular actually track:
  `await bmcp.type(el, text, { clearFirst: true })`.
- Use `bmcp.setValue(el, value)` for direct sets, selects, checkboxes.
- Highlight before touching anything so the user can watch:
  `bmcp.highlight(el, { ms: 900 })`.
- Pull the data from the user's files (PDFs, docs) first, then fill.
- Verify before submitting: `browser_get_value`, `browser_read_text`,
  `browser_screenshot`.

## Files

- **File uploads**: use `browser_upload_file(path, selector?)` — never inline
  file bytes into tool args (they get truncated in transit).
- `bmcp.setValue`/`bmcp.type` refuse file inputs with a "use
  browser_upload_file" error; follow that guidance.

## Buttons and clicks

- Click by visible text: `browser_click_button("Save changes")` or
  `bmcp.findByText('button', /save/i)`.
- `browser_submit_form` for forms.

## Dynamic rows

Forms like "Add Experience" clone rows with **duplicate ids** — never target
by `#id` there. Field listings return full, unambiguous CSS paths and flag
`duplicateId: true`; scope queries to the row container instead
(`bmcp.q(rowSelector).querySelector('[name="..."]')`, or the `fill-row`
snippet).

## Reuse verified flows

- `browser_list_snippets` — see what confirmed per-site scripts exist.
- `browser_run_snippet(name, args)` — run one.

## Ground rules

- Act only on the pinned target. Never drive another window.
- Confirm before anything destructive (submit, delete, pay).
- A timeout usually means the extension is waking up: retry once, then tell
  the user if it persists.
- Stuck on a page? Run `browser_execute_js` with `return bmcp.snapshot()`, then
  figure out the rest with plain JS.