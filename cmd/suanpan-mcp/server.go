package main

import (
	"encoding/json"
	"sync"

	"github.com/goose/suanpan/internal/store"
)

const protocolVersion = "2025-06-18"

// ---- JSON-RPC envelope ----

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ---- tool registry ----

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	handler     func(args json.RawMessage) (any, error)
}

// contentItem is an MCP content block in tool results.
type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func textResult(v any) map[string]any {
	b, _ := json.MarshalIndent(v, "", "  ")
	return map[string]any{
		"content": []contentItem{{Type: "text", Text: string(b)}},
	}
}

func errResult(err error) map[string]any {
	return map[string]any{
		"content": []contentItem{{Type: "text", Text: "error: " + err.Error()}},
		"isError": true,
	}
}

// ---- server ----

// server bundles the store, tool registry, and shared mutex. Methods are safe
// for concurrent use; the SQLite store serializes writes internally
// (SetMaxOpenConns(1)), and the tool registry is read-only after init.
type server struct {
	store     *store.Store
	tools     []toolDef
	toolIndex map[string]*toolDef

	// dispatchMu serializes tool invocations so tools that read-then-write
	// (e.g. `BudgetStatusAt` which walks budgets and reads txns) do not
	// interleave across concurrent HTTP clients.
	dispatchMu sync.Mutex
}

func newServer(s *store.Store) *server {
	tools := buildTools(s)
	idx := make(map[string]*toolDef, len(tools))
	for i := range tools {
		idx[tools[i].Name] = &tools[i]
	}
	return &server{store: s, tools: tools, toolIndex: idx}
}

// dispatch handles one request and returns a response. Returns nil for
// notifications (requests without an id). Safe for concurrent invocation.
func (sv *server) dispatch(req *rpcRequest) *rpcResponse {
	resp := func(result any) *rpcResponse {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	}
	rpcErr := func(code int, msg string) *rpcResponse {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: msg}}
	}

	switch req.Method {
	case "initialize":
		return resp(map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    "suanpan-mcp",
				"version": "0.1.0",
			},
			"instructions": "Local-first accounting. Entities: family, person, account, category, txn, budget. Money is decimal (e.g. 12.34); internally stored as minor units.",
		})

	case "notifications/initialized", "initialized":
		return nil

	case "ping":
		return resp(map[string]any{})

	case "tools/list":
		out := make([]map[string]any, 0, len(sv.tools))
		for _, t := range sv.tools {
			out = append(out, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
			})
		}
		return resp(map[string]any{"tools": out})

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return rpcErr(-32602, err.Error())
		}
		t, ok := sv.toolIndex[p.Name]
		if !ok {
			return rpcErr(-32601, "unknown tool: "+p.Name)
		}
		sv.dispatchMu.Lock()
		res, err := t.handler(p.Arguments)
		sv.dispatchMu.Unlock()
		if err != nil {
			return resp(errResult(err))
		}
		return resp(textResult(res))

	case "shutdown":
		return resp(map[string]any{})

	default:
		if len(req.ID) == 0 { // notification
			return nil
		}
		return rpcErr(-32601, "method not found: "+req.Method)
	}
}
