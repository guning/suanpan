package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/goose/suanpan/internal/skill"
	"github.com/goose/suanpan/internal/store"
)

// cmdInstallSkill writes the bundled SKILL.md to Claude Code's skills
// directory and emits the command needed to register the MCP server.
// With --mcp-add, it also executes `claude mcp add` if the `claude` CLI is
// available.
func cmdInstallSkill(args []string) error {
	fs := flag.NewFlagSet("install-skill", flag.ExitOnError)
	scope := fs.String("scope", "user", "user|project — where to install the skill")
	name := fs.String("name", skill.Name, "skill directory name")
	force := fs.Bool("force", false, "overwrite existing SKILL.md")
	skillOnly := fs.Bool("skill-only", false, "skip the MCP registration command")
	mcpAdd := fs.Bool("mcp-add", false, "also run `claude mcp add` (requires claude CLI on PATH)")
	mcpName := fs.String("mcp-name", "suanpan", "MCP server name to register")
	mcpBinFlag := fs.String("mcp-bin", "", "path to suanpan-mcp binary (default: auto-detect alongside suanpan)")
	dbPathFlag := fs.String("db", "", "DB path to embed in the MCP env (default: $SUANPAN_DB or ~/.suanpan/suanpan.db)")
	dry := fs.Bool("dry-run", false, "print what would happen without writing or executing")
	fs.Parse(args)

	skillDir, err := resolveSkillDir(*scope, *name)
	if err != nil {
		return err
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")

	// 1. Install SKILL.md. Auto-upgrade when the bundled version is newer than
	// the installed one; otherwise leave hand-edits alone unless -force is set.
	embedVer := skill.Version()
	if *dry {
		fmt.Printf("[dry-run] would write v%d skill (%d bytes) -> %s\n", embedVer, len(skill.Content), skillFile)
	} else if err := writeSkill(skillFile, skillDir, embedVer, *force); err != nil {
		return err
	}

	if *skillOnly {
		return nil
	}

	// 2. Resolve the MCP binary path and DB path.
	mcpBin, err := resolveMCPBin(*mcpBinFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: %v — skipping MCP registration\n", err)
		return nil
	}
	db := *dbPathFlag
	if db == "" {
		db = store.DefaultPath()
	}

	// 3. Emit (and optionally execute) `claude mcp add`.
	claudeScope := *scope
	if claudeScope != "user" && claudeScope != "project" {
		claudeScope = "user"
	}
	cmd := []string{
		"claude", "mcp", "add",
		"--scope", claudeScope,
		"--env", "SUANPAN_DB=" + db,
		*mcpName, mcpBin,
	}
	fmt.Println()
	fmt.Println("register the MCP server with Claude Code:")
	fmt.Println("  " + shellJoin(cmd))

	if !*mcpAdd {
		return nil
	}
	if *dry {
		fmt.Println("[dry-run] not executing")
		return nil
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("`claude` CLI not found on PATH; copy the command above and run it manually")
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("claude mcp add: %w", err)
	}
	return nil
}

// writeSkill installs or upgrades the SKILL.md file. The version-comparison
// branch is the heart of the auto-upgrade contract:
//   - file missing → fresh install
//   - installed < embedded → upgrade (silently, this is the upgrade path)
//   - installed ≥ embedded → skip unless -force (preserves hand-edits)
func writeSkill(skillFile, skillDir string, embedVer int, force bool) error {
	existing, err := os.ReadFile(skillFile)
	switch {
	case err == nil && !force:
		instVer := skill.ParseVersion(string(existing))
		if instVer >= embedVer {
			fmt.Fprintf(os.Stderr, "%s already at v%d (use -force to overwrite)\n", skillFile, instVer)
			return nil
		}
		if err := os.WriteFile(skillFile, []byte(skill.Content), 0o644); err != nil {
			return fmt.Errorf("write skill: %w", err)
		}
		fmt.Printf("upgraded skill v%d → v%d → %s\n", instVer, embedVer, skillFile)
		return nil
	case err == nil && force, os.IsNotExist(err):
		// fresh install (or forced overwrite)
	default:
		return fmt.Errorf("read skill: %w", err)
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(skillFile, []byte(skill.Content), 0o644); err != nil {
		return fmt.Errorf("write skill: %w", err)
	}
	fmt.Printf("installed skill → %s\n", skillFile)
	return nil
}

func resolveSkillDir(scope, name string) (string, error) {
	switch scope {
	case "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".claude", "skills", name), nil
	case "project":
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(cwd, ".claude", "skills", name), nil
	default:
		return "", fmt.Errorf("scope must be 'user' or 'project', got %q", scope)
	}
}

// resolveMCPBin picks the suanpan-mcp binary. Priority: explicit flag, sibling
// of the running suanpan binary, then $PATH.
func resolveMCPBin(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("mcp binary %s: %w", explicit, err)
		}
		abs, _ := filepath.Abs(explicit)
		return abs, nil
	}
	self, err := os.Executable()
	if err == nil {
		sibling := filepath.Join(filepath.Dir(self), "suanpan-mcp")
		if _, err := os.Stat(sibling); err == nil {
			return sibling, nil
		}
	}
	if p, err := exec.LookPath("suanpan-mcp"); err == nil {
		return p, nil
	}
	return "", errors.New("suanpan-mcp not found (install it via `make install` or pass -mcp-bin)")
}

// shellJoin quotes each arg the way a shell would accept and joins with spaces.
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$`*?[]{}|<>();&#") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}