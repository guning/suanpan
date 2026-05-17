package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// serveHTTP starts an MCP Streamable HTTP server at addr.
//
// Endpoints:
//
//	POST /mcp   — JSON-RPC request, JSON response (or 202 for notifications).
//	GET  /mcp   — opens a text/event-stream for server→client notifications
//	              (we currently only send keep-alives; the stream stays open
//	              until the client disconnects).
//	DELETE /mcp — no-op; session state is not tracked.
//
// Multiple clients may connect concurrently; the backing SQLite store
// serializes writes, and tool dispatch is additionally guarded by a mutex so
// read-then-write tools (e.g. budget_status) observe a consistent snapshot.
//
// Security:
//   - If $SUANPAN_TOKEN is set, all requests must send
//     `Authorization: Bearer <token>`.
//   - When binding to localhost, non-loopback Host headers are rejected
//     (basic DNS-rebinding defense).
//   - Binding to non-loopback without a token logs a warning but proceeds
//     (opt-in).
func (sv *server) serveHTTP(addr string) error {
	token := os.Getenv("SUANPAN_TOKEN")
	listenHost, _, _ := net.SplitHostPort(addr)
	loopback := listenHost == "" || listenHost == "localhost" || listenHost == "127.0.0.1" || listenHost == "::1"
	if !loopback && token == "" {
		logger.Printf("WARNING: binding to non-loopback %q without SUANPAN_TOKEN — anyone on your network can call the server.", listenHost)
	}

	// Only /mcp is auth-gated; /healthz stays open for local monitoring.
	mcpMux := http.NewServeMux()
	mcpMux.HandleFunc("/mcp", sv.mcpHandler)
	mcpMux.HandleFunc("/mcp/", sv.mcpHandler)

	handler := http.NewServeMux()
	handler.Handle("/mcp", sv.applyAuth(mcpMux, token, loopback))
	handler.Handle("/mcp/", sv.applyAuth(mcpMux, token, loopback))
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok\n"))
	})
	logger.Printf("listening on %s (auth=%v, loopback=%v)", addr, token != "", loopback)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

func (sv *server) mcpHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		sv.handleMCPPost(w, r)
	case http.MethodGet:
		sv.handleMCPSSE(w, r)
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case http.MethodOptions:
		w.Header().Set("Allow", "POST, GET, DELETE, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (sv *server) handleMCPPost(w http.ResponseWriter, r *http.Request) {
	ctype := r.Header.Get("Content-Type")
	if ctype != "" && !strings.HasPrefix(ctype, "application/json") {
		http.Error(w, "content-type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	// Cap body size defensively.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	resp := sv.dispatch(&req)
	if resp == nil {
		// Notification — no response body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleMCPSSE holds an SSE stream open for server-initiated messages. We
// don't currently push any, so it only emits an initial comment and a
// keep-alive every 15s until the client disconnects.
func (sv *server) handleMCPSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": stream-open\n\n")
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// applyAuth wraps the handler with bearer-token and Host-header checks.
func (sv *server) applyAuth(next http.Handler, token string, loopback bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// DNS-rebinding defense for loopback binds: require Host to name
		// localhost / 127.0.0.1 / ::1.
		if loopback {
			host := r.Host
			if i := strings.LastIndex(host, ":"); i != -1 {
				host = host[:i]
			}
			host = strings.Trim(host, "[]")
			if host != "" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
				http.Error(w, "unexpected Host header", http.StatusForbidden)
				return
			}
		}
		if token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got != token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
