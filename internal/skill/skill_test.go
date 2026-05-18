package skill

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"no frontmatter", "no leading dashes here\nversion: 5\n", 0},
		{"unterminated frontmatter", "---\nname: x\nversion: 3\n", 3}, // still finds it
		{"happy path", "---\nname: x\nversion: 7\n---\nbody\n", 7},
		{"version missing", "---\nname: x\ndescription: y\n---\n", 0},
		{"unparseable version", "---\nversion: not-a-number\n---\n", 0},
		{"whitespace around value", "---\nversion:   42   \n---\n", 42},
	}
	for _, c := range cases {
		if got := ParseVersion(c.in); got != c.want {
			t.Errorf("%s: ParseVersion = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestEmbeddedVersion guards against the bundled SKILL.md losing its version
// field — without it, every install would forever be treated as v0.
func TestEmbeddedVersion(t *testing.T) {
	if v := Version(); v < 1 {
		t.Errorf("bundled SKILL.md has version=%d; must be ≥1", v)
	}
}
