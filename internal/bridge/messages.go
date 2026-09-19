package bridge

// Request is a message the Go MCP server sends to the browser extension
// over the WebSocket: {id, action, params}.
type Request struct {
	ID     uint64         `json:"id"`
	Action string         `json:"action"`
	Params map[string]any `json:"params,omitempty"`
}

// Reply is a correlated response from the extension: {id, ok, data|error}.
type Reply struct {
	ID    uint64         `json:"id"`
	OK    bool           `json:"ok"`
	Data  map[string]any `json:"data,omitempty"`
	Error string         `json:"error,omitempty"`
}
