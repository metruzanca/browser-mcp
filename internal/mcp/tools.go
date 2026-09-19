package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/metruzanca/browser-mcp/internal/bridge"
)

const (
	toolExecuteJS    = "browser_execute_js"
	toolGetTab       = "browser_get_tab"
	toolListFields   = "browser_list_fields"
	toolTypeText     = "browser_type_text"
	toolSetValue     = "browser_set_value"
	toolGetValue     = "browser_get_value"
	toolReadText     = "browser_read_text"
	toolClick        = "browser_click"
	toolSubmit       = "browser_submit_form"
	toolWait         = "browser_wait"
	toolHighlight    = "browser_highlight"
	toolSnapshot     = "browser_snapshot"
	toolFind         = "browser_find"
	toolClickButton  = "browser_click_button"
	toolScreenshot   = "browser_screenshot"
	toolRunSnippet   = "browser_run_snippet"
	toolListSnippets = "browser_list_snippets"
	toolSetTarget    = "browser_set_target"
	toolGetTarget    = "browser_get_target"
	toolListWindows  = "browser_list_windows"
	toolListTabs     = "browser_list_tabs"
)

const defaultTimeout = 20 * time.Second

// Server wires MCP tools to the agent client that talks to the hub.
type Server struct {
	br *bridge.Client
}

func New(br *bridge.Client) *Server { return &Server{br: br} }

func (s *Server) Register(mcpServer *server.MCPServer) {
	// browser_execute_js — the primary tool.
	mcpServer.AddTool(
		mcp.NewTool(toolExecuteJS,
			mcp.WithDescription(
				"Run arbitrary JavaScript in the page the user is currently viewing (default world: \"main\", so you can also read page JS globals like __NEXT_DATA__ or framework internals). "+
					"Your code runs inside an async function, so `await` works and a returned promise is awaited. "+
					"Two bindings are in scope: `args` (the JSON `args` object you pass) and `bmcp`, a helper library installed on the page: "+
					"bmcp.q(sel)/bmcp.qAll(sel) (CSS query with helpful errors), bmcp.deepQuery(sel) (walks open shadow roots + same-origin iframes, for Workday-style widgets), "+
					"bmcp.findField(label) (resolve a field by its visible label text), bmcp.info(el) (structured field metadata), "+
					"bmcp.fields(scope) (all form fields), bmcp.setValue(el, value) (native value setter + input/change, works with React), "+
					"bmcp.type(el, text, opts) (simulated typing via execCommand insertText — makes the site think a user typed; most reliable for React/Angular), "+
					"bmcp.click(el), bmcp.get(el), bmcp.text(scope, maxChars), bmcp.wait(ms), bmcp.waitFor(sel, {timeout}), bmcp.highlight(el, {ms,color}), bmcp.state (persistent object across calls). "+
					"Return any JSON-serializable value; DOM nodes are auto-converted to metadata. If the site has a strict CSP, execution auto-falls back to the isolated world.",
			),
			mcp.WithString("code",
				mcp.Required(),
				mcp.Description("JavaScript source to run in the page. `args` and `bmcp` are in scope; the code runs in an async function so `await` is available."),
			),
			mcp.WithObject("args",
				mcp.Description("JSON-serializable object passed to your code as the `args` variable. Prefer this over string-interpolating values into `code`."),
			),
			mcp.WithString("world",
				mcp.Description("Which JS world to run in: \"main\" (default, sees page globals and page frameworks) or \"isolated\" (immune to page CSP). Falls back to isolated automatically if the page CSP blocks main."),
			),
			mcp.WithNumber("timeoutMs",
				mcp.Description("Milliseconds to wait for the script to finish. Default 60000. Chrome force-kills long-running service-worker work around 5 minutes."),
			),
			mcp.WithNumber("tabId",
				mcp.Description("Chrome tab id to target. Omit to use the active tab of the focused window."),
			),
		),
		s.handleExecuteJS,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolGetTab,
			mcp.WithDescription("Return the current tab's URL, title, favicon and tab id. Also reports whether the extension is connected. Call this first to orient yourself, then use browser_execute_js to inspect or drive the page."),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleGetTab,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolListFields,
			mcp.WithDescription("List every interactive form field on the page (inputs, textareas, selects, buttons, contenteditable). Each entry includes a stable CSS `selector`, `label` (visible label text), `name`, `id`, `type`, `placeholder`, `required`, `value`, and `options` for selects. Delegates to bmcp.fields()."),
			mcp.WithString("selector", mcp.Description("Optional CSS selector to scope the search to a container/form.")),
			mcp.WithBoolean("includeHidden", mcp.Description("Include type=hidden inputs. Default false.")),
			mcp.WithBoolean("includeDisabled", mcp.Description("Include disabled fields. Default false.")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleListFields,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolTypeText,
			mcp.WithDescription("Type text into a field by selector, simulating a user (execCommand insertText by default, which fires proper input events so React/Angular state updates). Prefer this over browser_set_value when the framework needs to track the keystrokes."),
			mcp.WithString("selector", mcp.Required(), mcp.Description("CSS selector for the field.")),
			mcp.WithString("text", mcp.Required(), mcp.Description("Text to type.")),
			mcp.WithString("mode", mcp.Description("\"insertText\" (default), \"keyboard\" (per-character keydown/keypress/input simulation), or \"native\" (value setter only).")),
			mcp.WithBoolean("clearFirst", mcp.Description("Select and replace any existing value. Default false.")),
			mcp.WithBoolean("pressEnter", mcp.Description("Press Enter after typing. Default false.")),
			mcp.WithNumber("delay", mcp.Description("Per-character delay in ms (keyboard mode only). Default 0.")),
			mcp.WithBoolean("returnSnapshot", mcp.Description("Also return a page snapshot after filling. Default false.")),
			mcp.WithNumber("snapshotFields", mcp.Description("Max fields in the returned snapshot. Default 100.")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleTypeText,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolSetValue,
			mcp.WithDescription("Set a field's value directly using the native value setter and dispatch input/change (bypasses React's value-tracking). For selects and checkboxes it sets the value/checked state."),
			mcp.WithString("selector", mcp.Required(), mcp.Description("CSS selector for the field.")),
			mcp.WithString("value", mcp.Required(), mcp.Description("Value to set.")),
			mcp.WithBoolean("returnSnapshot", mcp.Description("Also return a page snapshot after setting. Default false.")),
			mcp.WithNumber("snapshotFields", mcp.Description("Max fields in the returned snapshot. Default 100.")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleSetValue,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolGetValue,
			mcp.WithDescription("Read the current value of a field (input value, textarea value, select value, checkbox checked, or contenteditable text)."),
			mcp.WithString("selector", mcp.Required(), mcp.Description("CSS selector for the field.")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleGetValue,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolReadText,
			mcp.WithDescription("Read the visible text of the page or a container (innerText). Useful for extracting a job description or verifying page state after an action."),
			mcp.WithString("selector", mcp.Description("Optional CSS selector; omit for the whole page body.")),
			mcp.WithNumber("maxChars", mcp.Description("Cap the returned text length. Default unlimited.")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleReadText,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolClick,
			mcp.WithDescription("Click an element (button, link, checkbox) by selector using el.click(). Optionally highlight it first so the user sees what is happening."),
			mcp.WithString("selector", mcp.Required(), mcp.Description("CSS selector for the element to click.")),
			mcp.WithNumber("highlightMs", mcp.Description("Highlight the element for this many ms before clicking (0 to skip). Default 0.")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleClick,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolSubmit,
			mcp.WithDescription("Submit a form: finds the form (the one containing `selector`, or the given form selector, or the first form on the page), clicks its submit button, or calls form.submit() as a fallback."),
			mcp.WithString("selector", mcp.Description("Optional selector for the form, or an element inside it.")),
			mcp.WithNumber("highlightMs", mcp.Description("Highlight the submit button for this many ms before clicking. Default 0.")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleSubmit,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolWait,
			mcp.WithDescription("Wait a fixed number of ms, or poll until a selector appears (and is visible). Use after clicks or navigation before filling fields."),
			mcp.WithNumber("ms", mcp.Description("Milliseconds to sleep (used when selector is not given).")),
			mcp.WithString("selector", mcp.Description("Selector to wait for.")),
			mcp.WithNumber("timeoutMs", mcp.Description("Max wait for the selector. Default 10000.")),
			mcp.WithNumber("intervalMs", mcp.Description("Poll interval. Default 150.")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleWait,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolHighlight,
			mcp.WithDescription("Flash a colored outline around an element so the user can see what the agent is about to touch. Call before filling or clicking for good UX."),
			mcp.WithString("selector", mcp.Required(), mcp.Description("CSS selector for the element.")),
			mcp.WithNumber("ms", mcp.Description("Duration in ms. Default 1500.")),
			mcp.WithString("color", mcp.Description("Outline color. Default \"#ff3b30\".")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleHighlight,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolSnapshot,
			mcp.WithDescription("One-call snapshot of the page's interactive surface: url, title, headings, every form field (selector, label, type, required, options), every button with its visible text + selector, and truncated visible text. Use this to orient yourself quickly instead of several separate calls."),
			mcp.WithNumber("maxFields", mcp.Description("Max fields to return. Default 100.")),
			mcp.WithNumber("maxButtons", mcp.Description("Max buttons to return. Default 100.")),
			mcp.WithNumber("maxText", mcp.Description("Max characters of visible text. Default 4000.")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleSnapshot,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolFind,
			mcp.WithDescription("Resolve an element in one call and return its selector + metadata. Provide exactly one of: `selector` (CSS), `label` (a field's visible label text, e.g. \"Manager phone number\"), or `text` (visible text of a button/link, e.g. \"Save changes\"; searches buttons/links unless `scope` narrows it)."),
			mcp.WithString("selector", mcp.Description("CSS selector to resolve.")),
			mcp.WithString("label", mcp.Description("Visible label text of a form field to resolve.")),
			mcp.WithString("text", mcp.Description("Visible text of a button/link to resolve (regex allowed).")),
			mcp.WithString("scope", mcp.Description("CSS selector scoping the `text` search. Default: buttons, links and submit inputs.")),
			mcp.WithNumber("highlightMs", mcp.Description("Highlight the found element for this many ms.")),
			mcp.WithString("color", mcp.Description("Highlight color. Default \"#22c55e\".")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleFind,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolClickButton,
			mcp.WithDescription("Click a button or link by its visible text (regex allowed), e.g. \"Generate\" or \"Save changes\". Uses bmcp.findByText + bmcp.click."),
			mcp.WithString("text", mcp.Required(), mcp.Description("Visible text of the button/link to click (regex allowed).")),
			mcp.WithString("scope", mcp.Description("CSS selector scoping the search. Default: buttons, links and submit inputs.")),
			mcp.WithNumber("highlightMs", mcp.Description("Highlight the button for this many ms before clicking.")),
			mcp.WithString("color", mcp.Description("Highlight color. Default \"#22c55e\".")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleClickButton,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolScreenshot,
			mcp.WithDescription("Capture the visible part of the tab and return it as a PNG image. Useful for verifying the page looks right or orienting on an unfamiliar site."),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleScreenshot,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolRunSnippet,
			mcp.WithDescription("Run a reusable, pre-confirmed page script by name (see browser_list_snippets). Snippets are plain JS files that use the same runner as browser_execute_js, so `args` and `bmcp` are in scope and `await` works. Lookup order: $BROWSER_MCP_SNIPPETS, ~/.config/browser-mcp/snippets/, the snippets/ dir next to the binary, ./snippets."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Snippet name, e.g. \"atproto-qr.app/save-dynamic\". Site-scoped snippets are named after the host.")),
			mcp.WithObject("args", mcp.Description("JSON object passed to the snippet as `args`.")),
			mcp.WithString("world", mcp.Description("JS world: \"main\" (default) or \"isolated\".")),
			mcp.WithNumber("timeoutMs", mcp.Description("Milliseconds to wait. Default 60000.")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleRunSnippet,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolListSnippets,
			mcp.WithDescription("List every available reusable page script: name + one-line description. See browser_run_snippet."),
		),
		s.handleListSnippets,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolListWindows,
			mcp.WithDescription("List Chrome windows: id, whether focused, window type, and the active tab of each. Use this to discover which window to target, then pin this session with browser_set_target."),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleListWindows,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolListTabs,
			mcp.WithDescription("List tabs (all windows, or one window): tabId, url, title, active. Use with browser_list_windows to pick a target tab/window."),
			mcp.WithNumber("windowId", mcp.Description("Restrict to this window. Omit for all windows.")),
			mcp.WithNumber("tabId", mcp.Description("Chrome tab id. Omit to use the active tab.")),
		),
		s.handleListTabs,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolSetTarget,
			mcp.WithDescription("Pin this agent session to a specific tab or window. Once pinned, the session is restricted to that target: other tabs are off-limits until you re-pin or clear. WORKFLOW: before starting browser work, confirm with the user which tab you'll operate on (browser_get_tab shows it), then pin it here — so the user can browse elsewhere without you following the focus. Pass {tabId} to pin a tab, {windowId} to pin a window (its active tab), or {\"clear\": true} to unpin back to dynamic active."),
			mcp.WithNumber("tabId", mcp.Description("Pin to this exact tab.")),
			mcp.WithNumber("windowId", mcp.Description("Pin to the active tab of this window.")),
			mcp.WithBoolean("clear", mcp.Description("Clear the pin and return to dynamic active-tab targeting.")),
		),
		s.handleSetTarget,
	)

	mcpServer.AddTool(
		mcp.NewTool(toolGetTarget,
			mcp.WithDescription("Show this session's current pin: {\"kind\":\"active\"} (dynamic, follows the focused window), {\"kind\":\"tab\",\"id\":N}, or {\"kind\":\"window\",\"id\":N}."),
		),
		s.handleGetTarget,
	)
}

// ---- handlers ----

func (s *Server) handleExecuteJS(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	code, err := req.RequireString("code")
	if err != nil {
		return mcp.NewToolResultError("code is required"), nil
	}
	params := map[string]any{
		"code":  code,
		"world": req.GetString("world", "main"),
	}
	if args, ok := req.GetArguments()["args"].(map[string]any); ok {
		params["args"] = args
	}
	if ms := req.GetInt("timeoutMs", 0); ms > 0 {
		params["timeoutMs"] = ms
	}
	if tabID := req.GetInt("tabId", 0); tabID > 0 {
		params["tabId"] = tabID
	}
	return s.call(ctx, "executeScript", params)
}

func (s *Server) handleGetTab(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.callTab(ctx, "getTabInfo", req, nil)
}

func (s *Server) handleListFields(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := map[string]any{}
	if sel := req.GetString("selector", ""); sel != "" {
		params["selector"] = sel
	}
	params["includeHidden"] = req.GetBool("includeHidden", false)
	params["includeDisabled"] = req.GetBool("includeDisabled", false)
	params["code"] = snippetListFields
	return s.callTab(ctx, "executeScript", req, params)
}

func (s *Server) handleTypeText(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sel, err := req.RequireString("selector")
	if err != nil {
		return mcp.NewToolResultError("selector is required"), nil
	}
	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError("text is required"), nil
	}
	params := map[string]any{
		"code": snippetTypeText,
		"args": map[string]any{
			"selector":       sel,
			"text":           text,
			"mode":           req.GetString("mode", "insertText"),
			"clearFirst":     req.GetBool("clearFirst", false),
			"pressEnter":     req.GetBool("pressEnter", false),
			"delay":          req.GetInt("delay", 0),
			"returnSnapshot": req.GetBool("returnSnapshot", false),
			"snapshotFields": req.GetInt("snapshotFields", 100),
		},
	}
	return s.callTab(ctx, "executeScript", req, params)
}

func (s *Server) handleSetValue(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sel, err := req.RequireString("selector")
	if err != nil {
		return mcp.NewToolResultError("selector is required"), nil
	}
	value, err := req.RequireString("value")
	if err != nil {
		return mcp.NewToolResultError("value is required"), nil
	}
	params := map[string]any{
		"code": snippetSetValue,
		"args": map[string]any{
			"selector":       sel,
			"value":          value,
			"returnSnapshot": req.GetBool("returnSnapshot", false),
			"snapshotFields": req.GetInt("snapshotFields", 100),
		},
	}
	return s.callTab(ctx, "executeScript", req, params)
}

func (s *Server) handleGetValue(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sel, err := req.RequireString("selector")
	if err != nil {
		return mcp.NewToolResultError("selector is required"), nil
	}
	params := map[string]any{
		"code": snippetGetValue,
		"args": map[string]any{"selector": sel},
	}
	return s.callTab(ctx, "executeScript", req, params)
}

func (s *Server) handleReadText(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := map[string]any{}
	if sel := req.GetString("selector", ""); sel != "" {
		args["selector"] = sel
	}
	if mc := req.GetInt("maxChars", 0); mc > 0 {
		args["maxChars"] = mc
	}
	params := map[string]any{"code": snippetReadText, "args": args}
	return s.callTab(ctx, "executeScript", req, params)
}

func (s *Server) handleClick(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sel, err := req.RequireString("selector")
	if err != nil {
		return mcp.NewToolResultError("selector is required"), nil
	}
	params := map[string]any{
		"code": snippetClick,
		"args": map[string]any{"selector": sel, "highlightMs": req.GetInt("highlightMs", 0)},
	}
	return s.callTab(ctx, "executeScript", req, params)
}

func (s *Server) handleSubmit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := map[string]any{"highlightMs": req.GetInt("highlightMs", 0)}
	if sel := req.GetString("selector", ""); sel != "" {
		args["selector"] = sel
	}
	params := map[string]any{"code": snippetSubmit, "args": args}
	return s.callTab(ctx, "executeScript", req, params)
}

func (s *Server) handleWait(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := map[string]any{}
	if sel := req.GetString("selector", ""); sel != "" {
		args["selector"] = sel
	}
	if ms := req.GetInt("ms", 0); ms > 0 {
		args["ms"] = ms
	}
	if tm := req.GetInt("timeoutMs", 0); tm > 0 {
		args["timeoutMs"] = tm
	}
	if im := req.GetInt("intervalMs", 0); im > 0 {
		args["intervalMs"] = im
	}
	params := map[string]any{"code": snippetWait, "args": args}
	return s.callTab(ctx, "executeScript", req, params)
}

func (s *Server) handleHighlight(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sel, err := req.RequireString("selector")
	if err != nil {
		return mcp.NewToolResultError("selector is required"), nil
	}
	params := map[string]any{
		"code": snippetHighlight,
		"args": map[string]any{
			"selector": sel,
			"ms":       req.GetInt("ms", 1500),
			"color":    req.GetString("color", "#ff3b30"),
		},
	}
	return s.callTab(ctx, "executeScript", req, params)
}

func (s *Server) handleSnapshot(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := map[string]any{}
	if v := req.GetInt("maxFields", 0); v > 0 {
		args["maxFields"] = v
	}
	if v := req.GetInt("maxButtons", 0); v > 0 {
		args["maxButtons"] = v
	}
	if v := req.GetInt("maxText", 0); v > 0 {
		args["maxText"] = v
	}
	return s.callTab(ctx, "executeScript", req, map[string]any{"code": snippetSnapshot, "args": args})
}

func (s *Server) handleFind(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := map[string]any{}
	if v := req.GetString("selector", ""); v != "" {
		args["selector"] = v
	}
	if v := req.GetString("label", ""); v != "" {
		args["label"] = v
	}
	if v := req.GetString("text", ""); v != "" {
		args["text"] = v
	}
	if v := req.GetString("scope", ""); v != "" {
		args["scope"] = v
	}
	if v := req.GetInt("highlightMs", 0); v > 0 {
		args["highlightMs"] = v
	}
	if v := req.GetString("color", ""); v != "" {
		args["color"] = v
	}
	if len(args) == 0 {
		return mcp.NewToolResultError("provide one of selector, label, or text"), nil
	}
	return s.callTab(ctx, "executeScript", req, map[string]any{"code": snippetFind, "args": args})
}

func (s *Server) handleClickButton(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError("text is required"), nil
	}
	args := map[string]any{"text": text}
	if v := req.GetString("scope", ""); v != "" {
		args["scope"] = v
	}
	if v := req.GetInt("highlightMs", 0); v > 0 {
		args["highlightMs"] = v
	}
	if v := req.GetString("color", ""); v != "" {
		args["color"] = v
	}
	return s.callTab(ctx, "executeScript", req, map[string]any{"code": snippetClickButton, "args": args})
}

func (s *Server) handleScreenshot(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := map[string]any{}
	if tabID := req.GetInt("tabId", 0); tabID > 0 {
		params["tabId"] = tabID
	}
	cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	reply, err := s.br.Request(cctx, "captureTab", params)
	if err != nil {
		if err == bridge.ErrNotConnected {
			return notConnectedResult(), nil
		}
		return mcp.NewToolResultError("Browser bridge error: " + err.Error()), nil
	}
	if !reply.OK {
		return mcp.NewToolResultError(reply.Error), nil
	}
	dataURL, _ := reply.Data["dataUrl"].(string)
	b64 := strings.TrimPrefix(dataURL, "data:image/png;base64,")
	if _, err := base64.StdEncoding.DecodeString(b64); err != nil {
		return mcp.NewToolResultError("capture failed: " + err.Error()), nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{mcp.ImageContent{Type: "image", Data: b64, MIMEType: "image/png"}}}, nil
}

func (s *Server) handleRunSnippet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name is required"), nil
	}
	code, path, err := resolveSnippet(name)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	params := map[string]any{"code": code, "world": req.GetString("world", "main")}
	if args, ok := req.GetArguments()["args"].(map[string]any); ok {
		params["args"] = args
	}
	if ms := req.GetInt("timeoutMs", 0); ms > 0 {
		params["timeoutMs"] = ms
	}
	_ = path
	return s.callTab(ctx, "executeScript", req, params)
}

func (s *Server) handleListSnippets(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	snips, err := listSnippets()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(snips) == 0 {
		return mcp.NewToolResultText("No snippets found."), nil
	}
	b, err := json.MarshalIndent(snips, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

func (s *Server) handleListWindows(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.callTab(ctx, "listWindows", req, nil)
}

func (s *Server) handleListTabs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := map[string]any{}
	if w := req.GetInt("windowId", 0); w > 0 {
		params["windowId"] = w
	}
	return s.callTab(ctx, "listTabs", req, params)
}

func (s *Server) handleSetTarget(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := map[string]any{}
	tabID := req.GetInt("tabId", 0)
	windowID := req.GetInt("windowId", 0)
	if req.GetBool("clear", false) {
		params["clear"] = true
	} else if tabID > 0 && windowID > 0 {
		return mcp.NewToolResultError("provide tabId or windowId, not both"), nil
	} else if tabID > 0 {
		params["tabId"] = tabID
	} else if windowID > 0 {
		params["windowId"] = windowID
	} else {
		return mcp.NewToolResultError("provide tabId, windowId, or clear"), nil
	}
	return s.callControl(ctx, "setTarget", params)
}

func (s *Server) handleGetTarget(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.callControl(ctx, "getTarget", nil)
}

func notConnectedResult() *mcp.CallToolResult {
	return mcp.NewToolResultError("No browser extension is connected. Open the extension popup in Chrome and click Connect (default ws://127.0.0.1:18765/ws), then try again.")
}

// call sends an action to the extension and formats the reply for MCP.
func (s *Server) call(ctx context.Context, action string, params map[string]any) (*mcp.CallToolResult, error) {
	cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	reply, err := s.br.Request(cctx, action, params)
	if err != nil {
		return s.mapError(err), nil
	}
	if !reply.OK {
		if reply.Error == bridge.ErrNoExtension.Error() {
			return notConnectedResult(), nil
		}
		return mcp.NewToolResultError(reply.Error), nil
	}
	return renderResult(reply.Data), nil
}

// callTab merges tabId/windowId params into params before calling.
func (s *Server) callTab(ctx context.Context, action string, req mcp.CallToolRequest, params map[string]any) (*mcp.CallToolResult, error) {
	if params == nil {
		params = map[string]any{}
	}
	if tabID := req.GetInt("tabId", 0); tabID > 0 {
		params["tabId"] = tabID
	}
	if windowID := req.GetInt("windowId", 0); windowID > 0 {
		params["windowId"] = windowID
	}
	return s.call(ctx, action, params)
}

// callControl sends a hub-side control message.
func (s *Server) callControl(ctx context.Context, ctrl string, params map[string]any) (*mcp.CallToolResult, error) {
	cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	reply, err := s.br.Control(cctx, ctrl, params)
	if err != nil {
		return s.mapError(err), nil
	}
	if !reply.OK {
		if reply.Error == bridge.ErrNoExtension.Error() {
			return notConnectedResult(), nil
		}
		return mcp.NewToolResultError(reply.Error), nil
	}
	return renderResult(reply.Data), nil
}

// mapError translates client errors into friendly tool results.
func (s *Server) mapError(err error) *mcp.CallToolResult {
	switch {
	case errors.Is(err, bridge.ErrNotConnected):
		return notConnectedResult()
	case errors.Is(err, bridge.ErrNoHub):
		return mcp.NewToolResultError("The browser-mcp hub is not reachable and could not be started. Run `browser-mcp daemon` or check port 18765, then retry.")
	case errors.Is(err, context.DeadlineExceeded):
		return mcp.NewToolResultError("The browser did not respond in time (timed out). The extension may be waking up — retry the call.")
	default:
		return mcp.NewToolResultError("Browser bridge error: " + err.Error())
	}
}

func renderResult(data map[string]any) *mcp.CallToolResult {
	if data == nil {
		return mcp.NewToolResultText("ok")
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("%v", data))
	}
	return mcp.NewToolResultText(string(b))
}
