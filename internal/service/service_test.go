package service

import (
	"strings"
	"testing"
)

func testConfig(goos string) Config {
	return Config{
		GOOS:       goos,
		BinPath:    "/home/me/.local/bin/dctl",
		Home:       "/home/me",
		User:       "me",
		EnvFile:    "/home/me/.config/dctl/dctl.env",
		HealthAddr: "127.0.0.1:8787",
	}
}

func TestServeArgs(t *testing.T) {
	got := serveArgs(Config{HealthAddr: "127.0.0.1:8787", ExtraArgs: []string{"--status-channel", "42"}})
	want := "serve --health-addr 127.0.0.1:8787 --status-channel 42"
	if strings.Join(got, " ") != want {
		t.Fatalf("serveArgs = %q, want %q", strings.Join(got, " "), want)
	}
	// No health addr → flag omitted.
	if strings.Join(serveArgs(Config{}), " ") != "serve" {
		t.Fatalf("bare serveArgs should be just 'serve', got %v", serveArgs(Config{}))
	}
}

func TestLinuxPlan(t *testing.T) {
	p, err := BuildPlan(testConfig("linux"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 2 {
		t.Fatalf("want unit + env file, got %d files", len(p.Files))
	}
	unit := p.Files[0]
	if !strings.HasSuffix(unit.Path, "/.config/systemd/user/dctl.service") {
		t.Fatalf("unit path = %s", unit.Path)
	}
	for _, want := range []string{
		"EnvironmentFile=/home/me/.config/dctl/dctl.env",
		"ExecStart=/home/me/.local/bin/dctl serve --health-addr 127.0.0.1:8787",
		"Restart=always",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit.Content, want) {
			t.Errorf("unit missing %q\n---\n%s", want, unit.Content)
		}
	}
	// The token must never appear in a generated unit.
	if strings.Contains(unit.Content, "TOKEN=") {
		t.Error("unit file embeds a secret value")
	}
	// Env file is a never-overwrite 0600 template with no values.
	env := p.Files[1]
	if !env.Template || env.Mode != 0o600 {
		t.Fatalf("env file should be a 0600 template, got mode=%o template=%v", env.Mode, env.Template)
	}
	if strings.Contains(env.Content, "DISCORD_BOT_TOKEN=x") || !strings.Contains(env.Content, "DISCORD_BOT_TOKEN=") {
		t.Errorf("env template should list the var with no value:\n%s", env.Content)
	}
	assertCmd(t, p, "systemctl --user enable --now dctl.service")
	assertCmd(t, p, "loginctl enable-linger me")
}

func TestMacPlan(t *testing.T) {
	p, err := BuildPlan(testConfig("darwin"))
	if err != nil {
		t.Fatal(err)
	}
	plist := p.Files[0]
	if !strings.HasSuffix(plist.Path, "/Library/LaunchAgents/com.vskstudio.dctl.plist") {
		t.Fatalf("plist path = %s", plist.Path)
	}
	for _, want := range []string{
		"<key>Label</key><string>com.vskstudio.dctl</string>",
		"<key>RunAtLoad</key><true/>",
		". '/home/me/.config/dctl/dctl.env'", // sources the env file
		"exec /home/me/.local/bin/dctl serve --health-addr 127.0.0.1:8787",
	} {
		if !strings.Contains(plist.Content, want) {
			t.Errorf("plist missing %q\n---\n%s", want, plist.Content)
		}
	}
	assertCmd(t, p, "launchctl load -w")
}

func TestWindowsPlan(t *testing.T) {
	c := testConfig("windows")
	c.BinPath = `C:\Users\me\dctl.exe`
	c.EnvFile = `C:\Users\me\.config\dctl\dctl.env`
	c.Home = `C:\Users\me`
	p, err := BuildPlan(c)
	if err != nil {
		t.Fatal(err)
	}
	launcher := p.Files[0]
	if !strings.HasSuffix(launcher.Path, "dctl-serve.cmd") {
		t.Fatalf("launcher path = %s", launcher.Path)
	}
	if !strings.Contains(launcher.Content, `"C:\Users\me\dctl.exe" serve`) {
		t.Errorf("launcher missing dctl invocation:\n%s", launcher.Content)
	}
	assertCmd(t, p, "schtasks /create /tn dctl /tr")
}

func TestUnsupportedOS(t *testing.T) {
	if _, err := BuildPlan(testConfig("plan9")); err == nil {
		t.Fatal("expected error for unsupported OS")
	}
}

func TestQuoteArgvSpaces(t *testing.T) {
	if got := quoteArgv("/opt/My Apps/dctl", []string{"serve"}); got != `"/opt/My Apps/dctl" serve` {
		t.Fatalf("quoteArgv = %q", got)
	}
	if got := quoteArgv("/usr/bin/dctl", nil); got != "/usr/bin/dctl" {
		t.Fatalf("quoteArgv = %q", got)
	}
}

func TestUninstallAndStatus(t *testing.T) {
	for _, os := range []string{"linux", "darwin", "windows"} {
		if _, err := BuildUninstall(testConfig(os)); err != nil {
			t.Errorf("uninstall %s: %v", os, err)
		}
		if _, err := StatusCommand(testConfig(os)); err != nil {
			t.Errorf("status %s: %v", os, err)
		}
	}
}

func assertCmd(t *testing.T, p Plan, want string) {
	t.Helper()
	for _, c := range p.Commands {
		if strings.Contains(strings.Join(c.Argv, " "), want) {
			return
		}
	}
	t.Errorf("no command matching %q in plan", want)
}
