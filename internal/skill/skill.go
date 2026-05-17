// Package skill carries the Claude Code skill content bundled into the
// suanpan CLI so `suanpan install-skill` can drop it into the user's skills
// directory without reading any companion files from disk.
package skill

import _ "embed"

//go:embed SKILL.md
var Content string

// Name is the default skill directory name used when installing.
const Name = "accounting"