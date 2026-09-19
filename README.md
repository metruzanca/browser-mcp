# browser-mcp

A local MCP server (Go) + Chrome extension that lets agents like **Claude Code**
or **opencode** inspect and drive the browser tab you're looking at — filling
forms, clicking buttons, reading text — using arbitrary JavaScript.

The design is **JS-first**: `browser_execute_js` is the primary tool. The agent
uses `querySelector`/`querySelectorAll` and its own DOM logic, with a small
injected helper library (`bmcp`) for the fiddly parts (simulated typing that
React/Angular actually notice, label-based field lookup, shadow-DOM traversal).
The other tools are thin wrappers over the same runtime.

Typical use: auto-filling job applications, background-check forms (dates, job
titles, manager name/phone, addresses), extracting page text — anything where
the agent brings the data and the browser has the form.

```
Claude Code / opencode              Chrome browser
      │  MCP over stdio                    │
      ▼                                    ▼
┌─────────────────────┐  WS 127.0.0.1  ┌──────────────────────────────┐
│ Go MCP server        │ ◄──────────── │ extension (service worker)    │
│  mcp-go + gorilla/ws │  req/resp     │  - owns WebSocket connection  │
│  stdio transport     │               │  - chrome.scripting injection │
│  bridge (ids+timeout)│               │  - popup (connect/status)     │
└─────────────────────┘               └──────────────────────────────┘
```

The MCP server speaks stdio to the agent and opens a loopback WebSocket
(`ws://127.0.0.1:18765/ws`, configurable with `-port`) for the extension.
One extension connection is served at a time; a new one replaces the old.

## Quick start

### 1. Build the MCP server

```sh
go build -o browser-mcp ./cmd/browser-mcp
```

### 2. Load the extension

1. Open `chrome://extensions`
2. Enable **Developer mode**
3. **Load unpacked** → select the `extension/` directory
4. Click the extension icon → the popup opens → click **Connect**
   (it defaults to `127.0.0.1:18765`; change the port if you ran the server
   with `-port`)

The MCP server process must be running when you click Connect.

### 3. Point your agent at the server

**opencode** — add to `~/.config/opencode/opencode.json` (or see
`opencode.example.json`):

```json
"mcp": {
  "browser": {
    "command": ["/abs/path/to/browser-mcp"],
    "type": "local",
    "enabled": true
  }
}
```

**Claude Code**:

```sh
claude mcp add --scope user browser-mcp -- /abs/path/to/browser-mcp
```

The extension connects on demand and reconnects automatically (every 20s
keepalive keeps the service worker alive). If you close Chrome or reload the
extension, just open the popup and click Connect again.

## Tools

| Tool | Description |
| --- | --- |
| `browser_execute_js` | **Primary.** Run arbitrary JS in the page. `args` and `bmcp` are in scope, `await` works, DOM nodes are auto-serialized. |
| `browser_get_tab` | Current tab URL/title/tabId + extension connection status. |
| `browser_snapshot` | One-call snapshot: fields, buttons (with visible text + selectors), headings, truncated text. |
| `browser_list_fields` | All form fields with stable CSS selectors, labels, types, required flags, options. |
| `browser_find` | Resolve an element by CSS, label text, or visible button/link text → selector + metadata. |
| `browser_click_button` | Click a button/link by its visible text (regex allowed). |
| `browser_type_text` | Simulated typing (execCommand `insertText` by default) so React/Angular state updates. |
| `browser_set_value` | Direct value set via native setter + input/change. |
| `browser_get_value` | Read a field's current value. |
| `browser_read_text` | innerText of the page or a container (job description extraction). |
| `browser_click` | Click an element by selector, optionally highlighting it first. |
| `browser_submit_form` | Find + submit a form. |
| `browser_wait` | Sleep, or poll until a selector appears. |
| `browser_highlight` | Flash an outline around an element (trust UX). |
| `browser_screenshot` | Capture the visible tab as a PNG image. |
| `browser_run_snippet` | Run a reusable confirmed page script by name (see below). |
| `browser_list_snippets` | List available snippets with descriptions. |

`browser_type_text` / `browser_set_value` accept `returnSnapshot: true` to also
get the whole form back in one call.

Every tool call targets the **active tab of the focused window**. Pass a
`tabId` to target a specific tab.

## The `bmcp` runtime

When your code runs in the page, two bindings are in scope:

- **`args`** — the JSON object you passed via the `args` parameter (prefer this
  over string-interpolating values into `code`).
- **`bmcp`** (also `window.__bmcp`) — helper library:

| Helper | Purpose |
| --- | --- |
| `bmcp.q(sel)` / `bmcp.qAll(sel)` | CSS query with helpful errors (throws on no match). |
| `bmcp.deepQuery(sel)` | Query walking open shadow roots + same-origin iframes (Workday-style widgets). |
| `bmcp.findField(label)` | Resolve a field by its visible label text. |
| `bmcp.info(el)` | Structured field metadata (selector, label, type, required, options…). |
| `bmcp.fields(scope, opts)` | All form fields on the page or inside `scope`. |
| `bmcp.setValue(el, value)` | Native setter + input/change (bypasses React value-tracking). |
| `bmcp.type(el, text, opts)` | Simulated typing: `mode: 'insertText'` (default, execCommand), `'keyboard'` (per-char key events), or `'native'`. Plus `clearFirst`, `pressEnter`, `delay`. |
| `bmcp.click(el)` | `el.click()` after scrolling into view. |
| `bmcp.get(el)` | Current value (input/select/checkbox/contenteditable). |
| `bmcp.text(scope, maxChars)` | innerText. |
| `bmcp.wait(ms)` / `bmcp.waitFor(sel, {timeout})` | Sleep / poll. |
| `bmcp.highlight(el, {ms, color})` | Flash an outline. |
| `bmcp.state` | Persistent object across calls (store grabbed elements/flags between `execute_js` calls). Note: state is per-world — a main-world run can't see state set from an isolated-world run and vice versa. |
| `bmcp.findByText(sel, text)` | Find an element (e.g. a button) by its visible text; regex allowed. |
| `bmcp.buttons(scope)` | All buttons/links/submit inputs with visible text + selector. |
| `bmcp.snapshot(opts)` | The page snapshot used by `browser_snapshot`. |
| `bmcp.pressKey(el, key)` | Dispatch a keydown/keyup (Enter, Tab, Escape…). |

### Example

```js
// Gather every field the agent can fill.
return bmcp.fields().map(f => ({ label: f.label, selector: f.selector, required: f.required }));
```

```js
// Simulated typing into a field resolved by its label — framework-safe.
const el = bmcp.findField("Manager phone number");
bmcp.highlight(el, { ms: 800 });
await bmcp.type(el, args.phone, { clearFirst: true });
return bmcp.info(el);
```

## World selection & CSP

By default code runs in the page's **main world**, so it can also read page JS
globals (`__NEXT_DATA__`, framework internals). Some sites set a strict CSP that
blocks dynamic code (`unsafe-eval`) in the main world; in that case execution
**automatically falls back to the isolated world** and the result reports
`worldUsed`. You can force a world with the `world` parameter
(`"main"` or `"isolated"`).

## Snippets

Reusable, site-scoped page scripts live in `snippets/` (also
`~/.config/browser-mcp/snippets/` and `$BROWSER_MCP_SNIPPETS`). Each file is
plain JS using `bmcp`/`args` (same runner as `browser_execute_js`), named after
the site it targets (e.g. `snippets/atproto-qr.app/create-dynamic.js`), with a
`// description` as the first line.

- `browser_list_snippets` — enumerate available snippets.
- `browser_run_snippet(name, args)` — run one (lookup order: env, user config,
  binary dir, `./snippets`).

Only add a snippet once a flow is verified end to end — confirmed recipes, not
experiments. The shipped `atproto-qr.app/*` snippets were verified live.

## Reliability notes

- **Simulated typing**: `execCommand('insertText')` fires proper `beforeinput`/
  `input` events so React/Angular controlled inputs update — more reliable than
  setting `.value` directly. For sites that ignore it, use
  `bmcp.type(el, text, { mode: 'keyboard' })`.
- **Promises**: returned promises are awaited; set `timeoutMs` (default 60s).
  Chrome force-kills long-running service-worker work near 5 minutes — that's a
  hard ceiling.
- **Result size**: serialized results are capped (~200KB) and truncated with a
  `__truncated` marker rather than blowing up your context.
- **Unsupported pages**: `chrome://`, the Chrome Web Store, and PDF viewers
  reject injection — the error tells you to navigate to a normal page.
- **Timeouts fail fast**: the extension replies with an error after ~12s instead
  of hanging, and the Go bridge resets the connection when a request times out
  so the next call starts fresh. A timed-out read is safe to retry; a timed-out
  write may have landed, so the agent decides rather than auto-retrying.
- **Service worker**: the extension keeps the MV3 service worker alive with a
  10s keepalive. If Chrome force-kills it anyway, the extension reconnects and
  tools recover on the next call.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| Tools say "No browser extension is connected" | Open the extension popup and click Connect; check the port matches `-port`. |
| Connect keeps failing | The MCP server must be running first (`./browser-mcp`). It prints `extension bridge listening on ws://127.0.0.1:PORT/ws`. |
| Script errors mention CSP / unsafe-eval | Rely on the auto-fallback to isolated world, or pass `world: "isolated"`. |
| The popup is stale after Chrome sleeps | Chrome may kill the service worker on idle; the keepalive reconnects on next activity. Reload the extension if needed. |

## Project layout

```
cmd/browser-mcp/main.go      flags, wires bridge + MCP server (stdio)
internal/bridge/             loopback WS server, correlated req/resp, keepalive
internal/mcp/                tool definitions, handlers, canned snippets, snippet store
extension/                   MV3 extension (background.js, popup, icons)
snippets/                    reusable confirmed page scripts (site-scoped)
opencode.example.json        ready-to-paste agent config
```

## Roadmap (not yet)

- Screenshots (`chrome.tabs.captureVisibleTab` → image content)
- A `tabId` selector surfaced per call / multi-tab workflow helpers
- Native messaging as an alternative transport
- A per-run token if it ever leaves loopback

## License

MIT