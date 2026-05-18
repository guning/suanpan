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
	// Tests must set SUANPAN_AS_PERSON explicitly via runCLI calls that pass
	// -as-person, or the read commands will refuse. Wipe any inherited value.
	cmd.Env = append(cmd.Env, "SUANPAN_AS_PERSON=")
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
	mustRun(t, db, "txn", "add", "-amount", "28.50", "-kind", "expense", "-category-name", "餐饮", "-person", "1")
	mustRun(t, db, "txn", "add", "-amount", "12000", "-kind", "income", "-category-name", "工资", "-person", "1")

	// Report for a wide window — scoped to person 1.
	r = mustRun(t, db, "report", "-as-person", "1", "-since", "2026-01-01", "-until", "2026-12-31")
	for _, want := range []string{"Income", "Expense", "12000.00", "28.50"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("report missing %q: %s", want, r.stdout)
		}
	}

	// Verify txn list also respects scope.
	r = mustRun(t, db, "txn", "list", "-as-person", "1", "-since", "2026-01-01", "-limit", "10", "-json")
	if !strings.Contains(r.stdout, `"amount": 2850`) {
		t.Errorf("expected 2850 expense in scoped list: %s", r.stdout)
	}
	if !strings.Contains(r.stdout, `"amount": 1200000`) {
		t.Errorf("expected 1200000 income in scoped list: %s", r.stdout)
	}
}

func TestCLI_BudgetStatus(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	mustRun(t, db, "init")
	mustRun(t, db, "person", "add", "-name", "u")
	mustRun(t, db, "txn", "add", "-amount", "300", "-kind", "expense", "-category-name", "餐饮", "-person", "1", "-date", "2026-04-10")
	mustRun(t, db, "budget", "add", "-name", "月度", "-period", "monthly", "-amount", "1000", "-owner", "person:1", "-start", "2026-01-01")

	r := mustRun(t, db, "budget", "status", "-as-person", "1", "-date", "2026-04-15", "-json")
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

	r := runCLI(t, db, "txn", "add", "-amount", "0", "-kind", "expense", "-person", "1")
	if r.code == 0 {
		t.Errorf("expected non-zero exit for zero amount, got stdout=%q", r.stdout)
	}
	if !strings.Contains(r.stderr, "positive") {
		t.Errorf("stderr should complain about positive amount: %q", r.stderr)
	}
}

// TestCLI_ReportRequiresScope verifies that scoped reads error out when
// neither -as-person nor $SUANPAN_AS_PERSON is provided.
func TestCLI_ReportRequiresScope(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	mustRun(t, db, "init")
	r := runCLI(t, db, "report", "-since", "2026-01-01", "-until", "2026-12-31")
	if r.code == 0 {
		t.Errorf("expected non-zero exit when scope missing; stdout=%q stderr=%q", r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "as-person") {
		t.Errorf("stderr should mention -as-person: %q", r.stderr)
	}
}

// TestCLI_ScopeHidesOtherFamily verifies the security gate end-to-end: alice
// in family A cannot see carl's (family B) spending.
func TestCLI_ScopeHidesOtherFamily(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	mustRun(t, db, "init")
	mustRun(t, db, "family", "add", "-name", "A家")
	mustRun(t, db, "family", "add", "-name", "B家")
	mustRun(t, db, "person", "add", "-name", "alice", "-family", "1")
	mustRun(t, db, "person", "add", "-name", "carl", "-family", "2")
	mustRun(t, db, "txn", "add", "-amount", "11.11", "-kind", "expense", "-person", "1", "-date", "2026-04-10")
	mustRun(t, db, "txn", "add", "-amount", "99.99", "-kind", "expense", "-person", "2", "-date", "2026-04-10")

	// Alice's report should see her 11.11 but NOT carl's 99.99.
	r := mustRun(t, db, "report", "-as-person", "1", "-since", "2026-04-01", "-until", "2026-04-30", "-json")
	if !strings.Contains(r.stdout, `"expense": 1111`) {
		t.Errorf("alice should see her expense (1111 minor units): %s", r.stdout)
	}
	if strings.Contains(r.stdout, "9999") {
		t.Errorf("alice should NOT see carl's expense (9999): %s", r.stdout)
	}

	// `txn list` must apply the same scope. JSON output makes the assertion
	// stable: alice sees only her 1111, carl sees only his 9999.
	r = mustRun(t, db, "txn", "list", "-as-person", "1", "-since", "2026-04-01", "-json")
	if !strings.Contains(r.stdout, `"amount": 1111`) {
		t.Errorf("alice's txn list should include 1111: %s", r.stdout)
	}
	if strings.Contains(r.stdout, `"amount": 9999`) {
		t.Errorf("alice's txn list should NOT leak carl's 9999: %s", r.stdout)
	}

	r = mustRun(t, db, "txn", "list", "-as-person", "2", "-since", "2026-04-01", "-json")
	if !strings.Contains(r.stdout, `"amount": 9999`) {
		t.Errorf("carl's txn list should include 9999: %s", r.stdout)
	}
	if strings.Contains(r.stdout, `"amount": 1111`) {
		t.Errorf("carl's txn list should NOT leak alice's 1111: %s", r.stdout)
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

	// Second run without -force at the same version should skip (and still
	// exit 0). The message includes the version so users see what's installed.
	r2 := runCLIWithHome(t, home, db, "install-skill")
	if r2.code != 0 {
		t.Errorf("second run exit=%d", r2.code)
	}
	if !strings.Contains(r2.stderr, "already at v") {
		t.Errorf("expected 'already at v' warning, got: %q", r2.stderr)
	}
}

// TestCLI_InstallSkill_AutoUpgrade verifies the version-bump contract: an
// installed SKILL.md older than the bundled one is silently upgraded without
// requiring -force. Covers the v0-style pre-versioning install (treated as v0)
// upgrading to whatever the embedded binary ships.
func TestCLI_InstallSkill_AutoUpgrade(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(t.TempDir(), "s.db")
	skillFile := filepath.Join(home, ".claude", "skills", "accounting", "SKILL.md")

	// Plant a stale skill — no `version:` field at all, so the parser reads it
	// as v0 (older than anything we ship now).
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := "---\nname: accounting\ndescription: old\n---\nstale body\n"
	if err := os.WriteFile(skillFile, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	r := runCLIWithHome(t, home, db, "install-skill")
	if r.code != 0 {
		t.Fatalf("exit=%d stderr=%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "upgraded skill v0") {
		t.Errorf("expected 'upgraded skill v0' message, got stdout=%q stderr=%q", r.stdout, r.stderr)
	}

	body, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "stale body") {
		t.Errorf("stale content not replaced: %s", body)
	}
	if !strings.Contains(string(body), "version:") {
		t.Errorf("upgraded skill should have a version line: %s", body)
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
