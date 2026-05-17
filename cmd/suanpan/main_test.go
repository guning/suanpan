package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var suanpanBin string

// TestMain builds the suanpan binary once, shared across all tests in this package.
// The build dir is removed in all exit paths — note `os.Exit` skips deferred
// funcs, so setup+cleanup live inside a helper that returns the exit code.
func TestMain(m *testing.M) { os.Exit(runTests(m)) }

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "suanpan-bin-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	suanpanBin = filepath.Join(dir, "suanpan")
	build := exec.Command("go", "build", "-o", suanpanBin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	// Also build suanpan-mcp into the same dir so install-skill's sibling
	// auto-detection sees it.
	mcp := exec.Command("go", "build", "-o", filepath.Join(dir, "suanpan-mcp"), "../suanpan-mcp")
	mcp.Stderr = os.Stderr
	if err := mcp.Run(); err != nil {
		panic(err)
	}
	return m.Run()
}

type cliResult struct {
	stdout string
	stderr string
	code   int
}

func runCLI(t *testing.T, dbPath string, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command(suanpanBin, args...)
	cmd.Env = append(os.Environ(), "SUANPAN_DB="+dbPath)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return cliResult{out.String(), errb.String(), code}
}

func mustRun(t *testing.T, dbPath string, args ...string) cliResult {
	t.Helper()
	r := runCLI(t, dbPath, args...)
	if r.code != 0 {
		t.Fatalf("cli %v exit=%d stderr=%s", args, r.code, r.stderr)
	}
	return r
}

func TestCLI_EndToEnd(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")

	r := mustRun(t, db, "init")
	if !strings.Contains(r.stdout, "initialized DB") {
		t.Errorf("init stdout: %q", r.stdout)
	}

	mustRun(t, db, "family", "add", "-name", "张家")
	mustRun(t, db, "person", "add", "-name", "张三", "-family", "1")
	mustRun(t, db, "account", "add", "-name", "招行", "-type", "bank", "-owner", "person:1", "-initial", "5000")
	mustRun(t, db, "account", "add", "-name", "现金", "-type", "cash", "-owner", "person:1")
	mustRun(t, db, "txn", "add", "-amount", "28.50", "-account", "1", "-kind", "expense", "-category-name", "餐饮", "-person", "1")
	mustRun(t, db, "txn", "add", "-amount", "12000", "-account", "1", "-kind", "income", "-category-name", "工资", "-person", "1")
	mustRun(t, db, "txn", "transfer", "-from", "1", "-to", "2", "-amount", "100")

	// Balances via JSON. Expected:
	//   acct 1: 500000 + 1200000 - 2850 - 10000 = 1687150
	//   acct 2:                    + 10000      =   10000
	r = mustRun(t, db, "account", "balance", "-owner", "person:1", "-json")
	if !strings.Contains(r.stdout, `"balance": 1687150`) {
		t.Errorf("expected balance 1687150 in %q", r.stdout)
	}
	if !strings.Contains(r.stdout, `"balance": 10000`) {
		t.Errorf("expected balance 10000 in %q", r.stdout)
	}

	// Report for a wide window.
	r = mustRun(t, db, "report", "-since", "2026-01-01", "-until", "2026-12-31")
	for _, want := range []string{"Income", "Expense", "12000.00", "28.50"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("report missing %q: %s", want, r.stdout)
		}
	}
}

func TestCLI_BudgetStatus(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	mustRun(t, db, "init")
	mustRun(t, db, "person", "add", "-name", "u")
	mustRun(t, db, "account", "add", "-name", "A", "-type", "cash", "-owner", "person:1")
	mustRun(t, db, "txn", "add", "-amount", "300", "-account", "1", "-kind", "expense", "-category-name", "餐饮", "-date", "2026-04-10")
	mustRun(t, db, "budget", "add", "-name", "月度", "-period", "monthly", "-amount", "1000", "-owner", "person:1", "-start", "2026-01-01")

	r := mustRun(t, db, "budget", "status", "-owner", "person:1", "-date", "2026-04-15", "-json")
	if !strings.Contains(r.stdout, `"spent": 30000`) {
		t.Errorf("spent not 30000: %s", r.stdout)
	}
	if !strings.Contains(r.stdout, `"remaining": 70000`) {
		t.Errorf("remaining not 70000: %s", r.stdout)
	}
}

func TestCLI_RejectsInvalidAmount(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	mustRun(t, db, "init")
	mustRun(t, db, "person", "add", "-name", "u")
	mustRun(t, db, "account", "add", "-name", "A", "-type", "cash", "-owner", "person:1")

	r := runCLI(t, db, "txn", "add", "-amount", "0", "-account", "1", "-kind", "expense")
	if r.code == 0 {
		t.Errorf("expected non-zero exit for zero amount, got stdout=%q", r.stdout)
	}
	if !strings.Contains(r.stderr, "positive") {
		t.Errorf("stderr should complain about positive amount: %q", r.stderr)
	}
}

func TestCLI_UnknownCommand(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	r := runCLI(t, db, "foobar")
	if r.code == 0 {
		t.Errorf("expected non-zero exit")
	}
	if !strings.Contains(r.stderr, "unknown command") {
		t.Errorf("stderr: %q", r.stderr)
	}
}

// runCLIWithHome is like runCLI but lets the test pick a sandboxed $HOME so
// install-skill won't touch the developer's real ~/.claude/skills dir.
func runCLIWithHome(t *testing.T, home, db string, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command(suanpanBin, args...)
	env := append(os.Environ(), "HOME="+home, "SUANPAN_DB="+db)
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return cliResult{out.String(), errb.String(), code}
}

func TestCLI_InstallSkill_User(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(t.TempDir(), "s.db")

	r := runCLIWithHome(t, home, db, "install-skill")
	if r.code != 0 {
		t.Fatalf("install-skill exit=%d stderr=%s", r.code, r.stderr)
	}

	dest := filepath.Join(home, ".claude", "skills", "accounting", "SKILL.md")
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("skill not written: %v", err)
	}
	if !strings.Contains(string(body), "suanpan") {
		t.Errorf("skill content looks wrong (len=%d)", len(body))
	}
	if !strings.Contains(r.stdout, "installed skill") {
		t.Errorf("stdout: %q", r.stdout)
	}
	if !strings.Contains(r.stdout, "claude mcp add") {
		t.Errorf("stdout should print the mcp add command: %q", r.stdout)
	}

	// Second run without -force should skip (and still exit 0).
	r2 := runCLIWithHome(t, home, db, "install-skill")
	if r2.code != 0 {
		t.Errorf("second run exit=%d", r2.code)
	}
	if !strings.Contains(r2.stderr, "already exists") {
		t.Errorf("expected 'already exists' warning, got: %q", r2.stderr)
	}
}

func TestCLI_InstallSkill_DryRun(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(t.TempDir(), "s.db")
	r := runCLIWithHome(t, home, db, "install-skill", "-dry-run")
	if r.code != 0 {
		t.Fatalf("exit=%d stderr=%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "[dry-run]") {
		t.Errorf("stdout: %q", r.stdout)
	}
	// File must not exist.
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "accounting", "SKILL.md")); err == nil {
		t.Error("dry-run should not have written the skill")
	}
}

func TestCLI_InstallSkill_SkillOnly(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(t.TempDir(), "s.db")
	r := runCLIWithHome(t, home, db, "install-skill", "-skill-only")
	if r.code != 0 {
		t.Fatalf("exit=%d stderr=%s", r.code, r.stderr)
	}
	if strings.Contains(r.stdout, "claude mcp add") {
		t.Errorf("skill-only should not print mcp command: %q", r.stdout)
	}
}
