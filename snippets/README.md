# Snippets

Reusable, site-scoped page scripts. Each file is plain JavaScript run through
the same runner as `browser_execute_js` — `args` and `bmcp` are in scope,
`await` works, and the first line should be a `// description` comment (shown
by `browser_list_snippets`).

Run one with `browser_run_snippet(name, args)`. Lookup order:

1. `$BROWSER_MCP_SNIPPETS`
2. `~/.config/browser-mcp/snippets/`
3. `<binary-dir>/snippets/`
4. `./snippets/`

Name snippets after the site they target, e.g. `atproto-qr.app/fill-url.js`.
A snippet can also be generic (no site prefix) if it applies anywhere.

A snippet must `return` a value — that value is surfaced to the agent exactly
like `browser_execute_js`. Snippets whose body is `(async () => { ... })()`
return `null`; wrap in `return` instead.

Only add a snippet after the flow has been verified end to end — that is the
whole point: confirmed recipes, not experiments.