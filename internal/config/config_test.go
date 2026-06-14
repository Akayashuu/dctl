package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMissingFileIsZero(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file should be no error, got %v", err)
	}
	if (c != Config{}) {
		t.Fatalf("missing file should yield zero Config, got %+v", c)
	}
}

func TestLoadStripsFullLineComments(t *testing.T) {
	c, err := Load(writeTemp(t, `// leading comment
{
  // the base command
  "cmd": "claude --effort low",
  "healthAddr": "127.0.0.1:8787"
}
// trailing comment
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cmd != "claude --effort low" {
		t.Errorf("cmd = %q", c.Cmd)
	}
	if c.HealthAddr != "127.0.0.1:8787" {
		t.Errorf("healthAddr = %q", c.HealthAddr)
	}
}

func TestLoadKeepsInlineSlashesInValues(t *testing.T) {
	// A value containing // (e.g. a URL) must survive: only full-line comments
	// are stripped.
	c, err := Load(writeTemp(t, `{
  "source": "https://example.com/repo"
}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "https://example.com/repo" {
		t.Errorf("source = %q (inline // wrongly stripped)", c.Source)
	}
}

func TestLoadHome(t *testing.T) {
	c, err := Load(writeTemp(t, `{
  "home": { "id": "123", "type": "category" }
}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Home == nil || c.Home.ID != "123" || c.Home.Type != "category" {
		t.Fatalf("home = %+v", c.Home)
	}
}

func TestLoadBadJSONErrors(t *testing.T) {
	if _, err := Load(writeTemp(t, `{ "cmd": }`)); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestDefaultPathHonorsStateDir(t *testing.T) {
	t.Setenv("DCTL_STATE_DIR", "/tmp/dctlcfg")
	if got, want := DefaultPath(), "/tmp/dctlcfg/config.json"; got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestTemplatePrefillsCmdAndParses(t *testing.T) {
	tmpl := Template("claude --model claude-opus-4-8 --effort low", "127.0.0.1:8787")
	p := writeTemp(t, tmpl)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("scaffold must be valid JSONC: %v", err)
	}
	if c.Cmd != "claude --model claude-opus-4-8 --effort low" {
		t.Errorf("template cmd = %q", c.Cmd)
	}
	if c.HealthAddr != "127.0.0.1:8787" {
		t.Errorf("template healthAddr = %q", c.HealthAddr)
	}
}

func TestTemplateEmptyParses(t *testing.T) {
	c, err := Load(writeTemp(t, Template("", "")))
	if err != nil {
		t.Fatalf("empty scaffold must parse: %v", err)
	}
	if c.Cmd != "" || c.HealthAddr != "" {
		t.Errorf("cmd=%q healthAddr=%q, want both empty", c.Cmd, c.HealthAddr)
	}
}
