// Package skill carries the Claude Code skill content bundled into the
// suanpan CLI so `suanpan install-skill` can drop it into the user's skills
// directory without reading any companion files from disk.
package skill

import (
	_ "embed"
	"strconv"
	"strings"
)

//go:embed SKILL.md
var Content string

// Name is the default skill directory name used when installing.
const Name = "accounting"

// Version returns the integer version declared in the bundled SKILL.md
// frontmatter (`version: N`). Bump it whenever the skill content changes in a
// way you want existing installs to pick up.
func Version() int { return ParseVersion(Content) }

// ParseVersion extracts `version: N` from a SKILL.md-style YAML frontmatter
// block. Returns 0 if the file has no frontmatter, no version field, or an
// unparseable value — callers treat 0 as "older than anything we ship", so a
// pre-versioning install gets upgraded on the next run.
func ParseVersion(content string) int {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	for _, line := range lines[1:] {
		trim := strings.TrimSpace(line)
		if trim == "---" {
			return 0
		}
		if rest, ok := strings.CutPrefix(trim, "version:"); ok {
			v, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil {
				return 0
			}
			return v
		}
	}
	return 0
}
