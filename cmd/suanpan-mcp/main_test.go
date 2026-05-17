package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

var mcpBin string

// os.Exit bypasses defers, so cleanup lives in a helper that returns the code.
func TestMain(m *testing.M) { os.Exit(runTests(m)) }

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "suanpan-mcp-bin-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	mcpBin = filepath.Join(dir, "suanpan-mcp")
	build := exec.Command("go", "build", "-o", mcpBin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	return m.Run()
}

type client struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID atomic.Int64
}

func newClient(t *testing.T, dbPath string) *client {
	t.Helper()
	cmd := exec.Command(mcpBin)
	cmd.Env = append(os.Environ(), "SUANPAN_DB="+dbPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 1<<20)}
	t.Cleanup(func() {
		stdin.Close()
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	})
	return c
}

type rpcResp struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *client) call(method string, params any) rpcResp {
	c.t.Helper()
	id := c.nextID.Add(1)
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	b, _ := json.Marshal(req)
	if _, err := c.stdin.Write(append(b, '\n')); err != nil {
		c.t.Fatalf("write: %v", err)
	}
	line, err := c.stdout.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	var resp rpcResp
	if err := json.Unmarshal(line, &resp); err != nil {
		c.t.Fatalf("parse %q: %v", line, err)
	}
	return resp
}

// callTool invokes tools/call and decodes the single-text-content payload as JSON.
func (c *client) callTool(name string, args any, out any) {
	c.t.Helper()
	res := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	if res.Error != nil {
		c.t.Fatalf("rpc error: %+v", res.Error)
	}
	var wrap struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res.Result, &wrap); err != nil {
		c.t.Fatalf("tool result parse: %v", err)
	}
	if wrap.IsError {
		c.t.Fatalf("tool %s returned error: %s", name, wrap.Content[0].Text)
	}
	if len(wrap.Content) == 0 {
		c.t.Fatalf("empty content from %s", name)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(wrap.Content[0].Text), out); err != nil {
			c.t.Fatalf("payload parse from %s: %v (raw=%s)", name, err, wrap.Content[0].Text)
		}
	}
}

func TestMCP_InitializeAndToolList(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	c := newClient(t, db)

	res := c.call("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1"},
	})
	if res.Error != nil {
		t.Fatalf("initialize: %+v", res.Error)
	}
	var initOut struct {
		ServerInfo struct{ Name string } `json:"serverInfo"`
	}
	if err := json.Unmarshal(res.Result, &initOut); err != nil {
		t.Fatal(err)
	}
	if initOut.ServerInfo.Name != "suanpan-mcp" {
		t.Errorf("serverInfo.name=%q", initOut.ServerInfo.Name)
	}

	res = c.call("tools/list", nil)
	var tl struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(res.Result, &tl); err != nil {
		t.Fatal(err)
	}
	if len(tl.Tools) != 24 {
		t.Errorf("tools=%d, want 24", len(tl.Tools))
	}
	// Spot-check a few must-have tools.
	names := map[string]bool{}
	for _, tool := range tl.Tools {
		names[tool["name"].(string)] = true
	}
	for _, want := range []string{"add_expense", "add_income", "add_transfer", "summarize", "account_balances", "budget_status"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
}

func TestMCP_AddExpenseAndSummarize(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	c := newClient(t, db)

	c.call("initialize", map[string]any{})
	// Scenario setup.
	c.callTool("create_family", map[string]any{"name": "家", "currency": "CNY"}, nil)
	c.callTool("create_person", map[string]any{"name": "Z", "family_id": 1}, nil)
	c.callTool("create_account", map[string]any{
		"name": "A", "type": "cash", "currency": "CNY",
		"initial_balance": "100.00", "owner_kind": "person", "owner_id": 1,
	}, nil)

	var txn struct {
		ID     int64  `json:"id"`
		Amount int64  `json:"amount"`
		Kind   string `json:"kind"`
	}
	c.callTool("add_expense", map[string]any{
		"amount":        "28.50",
		"account_id":    1,
		"category_name": "餐饮",
		"date":          "2026-04-15",
	}, &txn)
	if txn.Amount != 2850 {
		t.Errorf("amount=%d, want 2850 (minor units)", txn.Amount)
	}
	if txn.Kind != "expense" {
		t.Errorf("kind=%q", txn.Kind)
	}

	// add_income with numeric amount (float64 in JSON) — moneyArg must handle it.
	c.callTool("add_income", map[string]any{
		"amount":        100.5,
		"account_id":    1,
		"category_name": "工资",
		"date":          "2026-04-05",
	}, nil)

	var sum struct {
		Income   int64 `json:"income"`
		Expense  int64 `json:"expense"`
		Net      int64 `json:"net"`
		TxnCount int   `json:"txn_count"`
	}
	c.callTool("summarize", map[string]any{"since": "2026-04-01", "until": "2026-04-30"}, &sum)
	if sum.Expense != 2850 {
		t.Errorf("expense=%d, want 2850", sum.Expense)
	}
	if sum.Income != 10050 {
		t.Errorf("income=%d, want 10050", sum.Income)
	}
	if sum.Net != 10050-2850 {
		t.Errorf("net=%d", sum.Net)
	}
	if sum.TxnCount != 2 {
		t.Errorf("txn_count=%d", sum.TxnCount)
	}

	// account_balances sanity: 10000 (init) + 10050 (income) - 2850 (expense) = 17200
	var bals []struct {
		Balance int64 `json:"balance"`
	}
	c.callTool("account_balances", map[string]any{"owner_kind": "person", "owner_id": 1}, &bals)
	if len(bals) != 1 || bals[0].Balance != 17200 {
		t.Errorf("balances=%+v, want one with 17200", bals)
	}
}

func TestMCP_UnknownTool(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	c := newClient(t, db)
	c.call("initialize", map[string]any{})
	res := c.call("tools/call", map[string]any{"name": "does-not-exist", "arguments": map[string]any{}})
	if res.Error == nil {
		t.Errorf("expected rpc error, got result=%s", res.Result)
	}
}

func TestMCP_EOFExitsCleanly(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	cmd := exec.Command(mcpBin)
	cmd.Env = append(os.Environ(), "SUANPAN_DB="+dbPath(db))
	stdin, _ := cmd.StdinPipe()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("wait err=%v", err)
		}
	case <-time.After(2 * time.Second):
		cmd.Process.Kill()
		t.Error("server did not exit within 2s of stdin close")
	}
}

func dbPath(p string) string { return p }
