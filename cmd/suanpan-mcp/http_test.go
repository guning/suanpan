package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pickFreeAddr reserves a loopback port by briefly binding to :0. There's a
// tiny race between Close and the subprocess bind; good enough for tests.
func pickFreeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// startHTTPServer launches suanpan-mcp with -listen <addr> and waits for
// /healthz. The returned cleanup function terminates the process.
func startHTTPServer(t *testing.T, dbPath, addr string, extraEnv ...string) func() {
	t.Helper()
	cmd := exec.Command(mcpBin, "-listen", addr)
	cmd.Env = append(append(os.Environ(), "SUANPAN_DB="+dbPath), extraEnv...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if time.Now().After(deadline) {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("server at %s never became healthy", addr)
	}

	return func() {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

// httpRPC posts one JSON-RPC request and decodes the response. headers is
// merged onto Authorization etc.
func httpRPC(t *testing.T, addr, method string, params any, id int64, headers map[string]string) rpcResp {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "method": method, "id": id}
	if params != nil {
		req["params"] = params
	}
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", "http://"+addr+"/mcp", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer httpResp.Body.Close()
	b, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", httpResp.StatusCode, string(b))
	}
	var resp rpcResp
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("parse: %v (%s)", err, string(b))
	}
	return resp
}

func httpCallTool(t *testing.T, addr, name string, args any, id int64, out any, headers map[string]string) {
	t.Helper()
	r := httpRPC(t, addr, "tools/call", map[string]any{"name": name, "arguments": args}, id, headers)
	if r.Error != nil {
		t.Fatalf("rpc error on %s: %+v", name, r.Error)
	}
	var wrap struct {
		Content []struct{ Text string } `json:"content"`
		IsError bool                    `json:"isError"`
	}
	if err := json.Unmarshal(r.Result, &wrap); err != nil {
		t.Fatalf("wrap parse: %v", err)
	}
	if wrap.IsError {
		t.Fatalf("tool %s error: %s", name, wrap.Content[0].Text)
	}
	if out != nil && len(wrap.Content) > 0 {
		if err := json.Unmarshal([]byte(wrap.Content[0].Text), out); err != nil {
			t.Fatalf("payload: %v (%s)", err, wrap.Content[0].Text)
		}
	}
}

func TestMCP_HTTP_Basic(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	addr := pickFreeAddr(t)
	defer startHTTPServer(t, db, addr)()

	// initialize
	r := httpRPC(t, addr, "initialize", nil, 1, nil)
	if r.Error != nil {
		t.Fatalf("initialize: %+v", r.Error)
	}
	var init struct {
		ServerInfo struct{ Name string } `json:"serverInfo"`
	}
	json.Unmarshal(r.Result, &init)
	if init.ServerInfo.Name != "suanpan-mcp" {
		t.Errorf("serverInfo.name=%q", init.ServerInfo.Name)
	}

	// tools/list
	r = httpRPC(t, addr, "tools/list", nil, 2, nil)
	var tl struct {
		Tools []map[string]any `json:"tools"`
	}
	json.Unmarshal(r.Result, &tl)
	if len(tl.Tools) != 24 {
		t.Errorf("tools=%d, want 24", len(tl.Tools))
	}

	// basic scenario
	httpCallTool(t, addr, "create_person", map[string]any{"name": "u"}, 3, nil, nil)
	httpCallTool(t, addr, "create_account", map[string]any{
		"name": "A", "type": "cash", "owner_kind": "person", "owner_id": 1,
	}, 4, nil, nil)
	var txn struct {
		Amount int64 `json:"amount"`
	}
	httpCallTool(t, addr, "add_expense", map[string]any{
		"amount": "12.34", "account_id": 1, "date": "2026-04-15",
	}, 5, &txn, nil)
	if txn.Amount != 1234 {
		t.Errorf("amount=%d, want 1234", txn.Amount)
	}
}

// Many clients hammering the server simultaneously. Each client runs its own
// initialize + creates a labeled person + creates an account + records 10
// expenses. At the end the total txn count must equal N*10.
func TestMCP_HTTP_ConcurrentClients(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	addr := pickFreeAddr(t)
	defer startHTTPServer(t, db, addr)()

	const clients = 10
	const txnsPerClient = 10

	var id atomic.Int64
	nextID := func() int64 { return id.Add(1) }

	var wg sync.WaitGroup
	errCh := make(chan error, clients)
	wg.Add(clients)
	for i := 0; i < clients; i++ {
		i := i
		go func() {
			defer wg.Done()
			// Each client initializes independently (stateless server).
			httpRPC(t, addr, "initialize", nil, nextID(), nil)

			var p struct{ ID int64 }
			httpCallTool(t, addr, "create_person", map[string]any{
				"name": fmt.Sprintf("u%d", i),
			}, nextID(), &p, nil)
			if p.ID == 0 {
				errCh <- fmt.Errorf("client %d got zero person id", i)
				return
			}

			var a struct{ ID int64 }
			httpCallTool(t, addr, "create_account", map[string]any{
				"name": fmt.Sprintf("acct%d", i), "type": "cash",
				"owner_kind": "person", "owner_id": p.ID,
			}, nextID(), &a, nil)

			for j := 0; j < txnsPerClient; j++ {
				httpCallTool(t, addr, "add_expense", map[string]any{
					"amount":     fmt.Sprintf("%d.%02d", j+1, j),
					"account_id": a.ID,
					"date":       "2026-04-15",
				}, nextID(), nil, nil)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	// All writes should be durable and queryable.
	var txns []map[string]any
	httpCallTool(t, addr, "list_transactions", map[string]any{
		"since": "2026-04-01", "until": "2026-04-30", "limit": 10000,
	}, nextID(), &txns, nil)
	if got, want := len(txns), clients*txnsPerClient; got != want {
		t.Errorf("persisted txns=%d, want %d", got, want)
	}
}

func TestMCP_HTTP_Auth(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	addr := pickFreeAddr(t)
	defer startHTTPServer(t, db, addr, "SUANPAN_TOKEN=secret-xyz")()

	// No token → 401.
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	r, err := http.Post("http://"+addr+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-token status=%d, want 401", r.StatusCode)
	}

	// Wrong token → 401.
	req, _ := http.NewRequest("POST", "http://"+addr+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong")
	r, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong-token status=%d, want 401", r.StatusCode)
	}

	// Correct token → 200.
	resp := httpRPC(t, addr, "ping", nil, 1, map[string]string{
		"Authorization": "Bearer secret-xyz",
	})
	if resp.Error != nil {
		t.Errorf("authorized ping error: %+v", resp.Error)
	}
}

func TestMCP_HTTP_SSEKeepsOpen(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	addr := pickFreeAddr(t)
	defer startHTTPServer(t, db, addr)()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/mcp", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got, want := resp.Header.Get("Content-Type"), "text/event-stream"; got != want {
		t.Errorf("content-type=%q, want %q", got, want)
	}

	buf := make([]byte, 128)
	n, err := resp.Body.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "stream-open") {
		t.Errorf("expected stream-open comment, got %q", buf[:n])
	}
}

func TestMCP_HTTP_BadHost(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	addr := pickFreeAddr(t)
	defer startHTTPServer(t, db, addr)()

	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	req, _ := http.NewRequest("POST", "http://"+addr+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "evil.example.com" // DNS-rebinding probe
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("rebind probe status=%d, want 403", r.StatusCode)
	}
}
