# Browser MCP

**Give your AI agent hands in the browser.**

browser-mcp is a local MCP server (Go) + a Chrome extension that lets coding
agents like **Claude Code** and **opencode** read, click, and type in the tab
you're looking at — the same way you would.

Fill out that maddening form on a creaky, old-school website, extract the text
from any page, or click through a multi-step flow. You keep the browser; the
agent does the busywork.

## Why it's different

Most browser automation fights the page. This one hands it to the agent:

- **JS-first by design.** The primary tool is `browser_execute_js` — your agent
  writes real JavaScript and runs it in the page, using `querySelector`,
  `querySelectorAll`, or whatever it needs. Nothing is out of reach.
- **Typing that frameworks believe.** Simulated keystrokes fire the events
  React, Angular, and friends actually track, so controlled inputs update like
  a real user typed into them.
- **Sees what you see.** By default it runs in the page's main JS context, so
  the agent can also reach framework internals and page globals — not just the
  DOM.
- **Local and private.** Everything runs on `127.0.0.1`. No cloud, no accounts,
  no tracking. The browser stays yours.

## What you can do with it

- **Slay any annoying form** — that legacy portal with a thousand tiny boxes, a
  quirky date picker, and red asterisks everywhere. The agent reads the form,
  matches fields by their visible labels, and types the answers.
- **Answers straight from your PDFs** — just like it reads files in any other
  context, the agent digs the details out of your documents to figure out what
  goes where.
- **Extract anything** — job descriptions, articles, table data.
- **Drive multi-step flows** — click through wizards, logins, and checkout
  funnels while confirming each step with a screenshot.
- **Reuse verified scripts** — proven flows are saved as snippets per site, so
  the agent can replay them next time.

## How it works

```
Claude Code / opencode              Chrome browser
      │  MCP over stdio                   │
      ▼                                   ▼
┌──────────────┐  agent WS   ┌────────────────────┐  extension WS  ┌─────────────┐
│ MCP instance │ ──────────► │  browser-mcp hub   │ ◄───────────── │ extension    │
│ (stdio)      │ ──────────► │  (127.0.0.1:18765) │                │  (Chrome SW) │
└──────────────┘  N agents   └────────────────────┘                └─────────────┘
```

A small Go MCP server speaks to your agent over stdio and connects to a local
**hub** that the Chrome extension talks to. One hub, many agents, no port
juggling — each opencode session is its own session and can be pinned to its
own tab or window, so two agents can work in two browser windows at once
without stepping on each other.

## Quick start

```sh
go build -o browser-mcp ./cmd/browser-mcp
```

1. Load the `extension/` folder in `chrome://extensions` (Developer mode →
   Load unpacked).
2. Click the extension icon and hit **Connect**.
3. Add the MCP to your project (below).

That's it. The extension reconnects on its own and keeps the connection alive.

## Install for a project (local-only)

Browser control is a per-project tool: installing it globally hands every
agent on your machine a browser it'll be all too eager to drive. Keep it
scoped to the projects that need it.

**opencode** — add a project-scoped config. Either an `opencode.json` in the
project root, or `.opencode/opencode.json` (this repo uses the latter; it's
git-ignored here so your machine-specific binary path never gets committed):

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

A ready-to-paste template lives in [`opencode.example.json`](opencode.example.json).

**Claude Code** — register it for the project:

```sh
claude mcp add --scope project browser-mcp -- /abs/path/to/browser-mcp
```

(or drop a `.mcp.json` in the project root — see Claude Code's docs).

If you're using the binary built in this repo, point the `command` at
`/abs/path/to/browser-mcp` where you built it — or install it somewhere on
`PATH` (e.g. `~/.local/bin/browser-mcp`) and reference that.

Full setup, the tool reference, and troubleshooting live in
[`docs/technical.md`](docs/technical.md).

## Example

Ask your agent: _"Fill out this form. The details are in those PDFs."_

The agent reads the PDFs, snapshots the form, matches each field by its visible
label, and types the answers in — highlighting each field as it goes, so you
can watch it work:

```js
const el = bmcp.findField("Manager phone number");
bmcp.highlight(el, { ms: 800 });
await bmcp.type(el, args.phone, { clearFirst: true });
```

## Project layout

```
cmd/browser-mcp/    the MCP server
extension/          the Chrome extension
internal/           bridge + tool implementations
snippets/           verified per-site page scripts
docs/technical.md   the technical reference
```

## License

MIT