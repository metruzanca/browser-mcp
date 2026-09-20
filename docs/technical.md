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

## Architecture: hub + sessions

One long-lived **hub** owns the single WebSocket to the Chrome extension.
Every opencode session runs a `browser-mcp` agent that connects to the hub as
a session; multiple agents coexist on the same fixed port — no per-session
port juggling.

```
Claude Code / opencode              Chrome browser
      │  MCP over stdio                   │
      ▼                                   ▼
┌──────────────┐  agent WS   ┌────────────────────┐  extension WS  ┌─────────────┐
│ MCP instance │ ──────────► │  browser-mcp hub   │ ◄───────────── │ extension    │
│ (stdio)      │ ──────────► │  (127.0.0.1:18765) │                │  (Chrome SW) │
└──────────────┘  N agents   └────────────────────┘                └─────────────┘
```

- The hub is a detached daemon (`browser-mcp daemon`). The first MCP instance
  to start spawns it automatically; it survives opencode sessions. Logs live in
  `~/.local/state/browser-mcp/daemon-<port>.log`.
- The hub routes each agent's requests to the extension and back, tagged with
  the agent's session id, so two opencode sessions never cross wires.
- If the extension is busy or Chrome restarts, the hub keeps agent connections
  alive and the extension reconnects on its own.

### Session targets (pinning)

Each agent session can be **pinned** to a tab or a window. Until pinned, it
acts on the active tab of the focused window (the classic single-session
behavior). The recommended workflow:

1. The agent calls `browser_get_tab` to see what's focused.
2. The agent **confirms with the user** which tab it will work on.
3. The agent pins it with `browser_set_target({tabId})` (or `{windowId}`).
4. The user can browse elsewhere; the agent stays on the pinned tab.

Pinning is enforced by the hub: a session pinned to tab 10 cannot act on tab
999 (`browser_set_target`/`browser_get_target` manage the pin). This is what
makes multiple windows with different tabs tractable — one agent per window.

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

### 3. Add the MCP to a project (local-only)

Keep it scoped to projects that need it — a global install gives every agent a
browser to drive.

**opencode** — add a project-scoped config. Either an `opencode.json` in the
project root, or `.opencode/opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "browser": {
      "command": ["/abs/path/to/browser-mcp"],
      "type": "local",
      "enabled": true
    }
  }
}
```

**Claude Code** — register for the project:

```sh
claude mcp add --scope project browser-mcp -- /abs/path/to/browser-mcp
```

The extension connects on demand and reconnects automatically. If you close
Chrome or reload the extension, just open the popup and click Connect again.

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
| `browser_upload_file` | Upload a local file by path to an `<input type="file">` (DataTransfer + change event). |
| `browser_run_snippet` | Run a reusable confirmed page script by name (see below). |
| `browser_list_snippets` | List available snippets with descriptions. |
| `browser_list_windows` | List Chrome windows (id, focused, active tab). |
| `browser_list_tabs` | List tabs (all or one window) with ids/urls/titles. |
| `browser_set_target` | Pin this session to a tab or window (`{tabId}`, `{windowId}`, or `{"clear":true}`). |
| `browser_get_target` | Show this session's current pin. |

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

Snippets must `return` a value; the returned JSON is surfaced to the agent
(exactly like `browser_execute_js`).

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
- **Timeouts fail fast, reads retry once**: the extension replies with an error
  after ~20s (per-call `timeoutMs` overrides) instead of hanging, and the Go
  bridge resets the connection when a request times out so the next call
  starts fresh. Read-only actions (`browser_get_tab`, `browser_list_fields`,
  `browser_read_text`, `browser_get_value`, `browser_snapshot`,
  `browser_list_windows`, `browser_list_tabs`, `browser_wait`,
  `browser_highlight`, `browser_find`, and `browser_execute_js` with
  `readonly: true`) retry once with a short backoff on transient timeouts.
  Writes (`browser_type_text`, `browser_set_value`, `browser_click`,
  `browser_submit_form`, `browser_upload_file`, plain `browser_execute_js`)
  fail fast so nothing double-fires.
- **Service worker**: the extension keeps the MV3 service worker alive with a
  10s keepalive. If Chrome force-kills it anyway, the extension reconnects and
  tools recover on the next call.
- **File inputs**: `bmcp.setValue`/`bmcp.type` refuse file inputs with a clear
  "use browser_upload_file" error. Uploads read the file in the daemon and ship
  the bytes over the extension channel, so large blobs never travel through
  MCP tool arguments (default limit 20MB, `BROWSER_MCP_MAX_UPLOAD`).
- **Payload size**: `browser_execute_js` / `browser_run_snippet` reject
  `code`+`args` over 128KB (`BROWSER_MCP_MAX_ARGS`) with a clear error. Big
  data should go through `browser_upload_file` or a file path, never inline
  tool args — large args can be silently truncated by the agent transport.
- **Duplicate ids (dynamic rows)**: selectors in field listings are full,
  unambiguous CSS paths — the `#id` shortcut is only used when the id is
  unique on the page. Fields whose id is duplicated carry `duplicateId: true`.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| Tools say "No browser extension is connected" | Open the extension popup and click Connect; check the port matches `-port`. |
| Connect keeps failing | The MCP server must be running first (`./browser-mcp`). It prints `extension bridge listening on ws://127.0.0.1:PORT/ws`. |
| Script errors mention CSP / unsafe-eval | Rely on the auto-fallback to isolated world, or pass `world: "isolated"`. |
| The popup is stale after Chrome sleeps | Chrome may kill the service worker on idle; the keepalive reconnects on next activity. Reload the extension if needed. |

## Project layout

```
cmd/browser-mcp/main.go      agent (stdio) + daemon (hub) entry points
internal/hub/                hub: extension + multi-agent WS, session pinning, routing
internal/bridge/             agent client (dial hub, request/control, daemon spawn)
internal/daemon/             detached hub-daemon lifecycle (pid file, spawn)
internal/mcp/                tool definitions, handlers, canned snippets, snippet store
extension/                   MV3 extension (background.js, popup, icons)
snippets/                    reusable confirmed page scripts (site-scoped)
opencode.example.json        ready-to-paste agent config
```

## Roadmap (not yet)

- Multi-tab workflow helpers beyond per-call `tabId`
- Native messaging as an alternative transport
- A per-run token if it ever leaves loopback

## License

MIT