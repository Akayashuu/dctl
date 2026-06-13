// Package service installs the `dctl serve` daemon as a native, boot-started
// background service on Linux (systemd user unit), macOS (launchd LaunchAgent),
// and Windows (Task Scheduler onlogon task).
//
// The design separates a pure planner (BuildPlan / BuildUninstall, testable on
// any OS) from the executor (Install / Uninstall, which writes files and runs
// the platform commands). Secrets never live in the generated unit: every
// platform sources an env file (mode 0600) that holds DISCORD_BOT_TOKEN et al.,
// and the planner only ever creates that file as an empty template — it never
// overwrites an existing one and never echoes a token.
package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// label / on-disk names shared by the planner and the docs.
const (
	linuxUnitName = "dctl.service"
	macLabel      = "com.vskstudio.dctl"
	winTaskName   = "dctl"
)

// Config describes the service to install.
type Config struct {
	GOOS       string   // target OS; "" => runtime.GOOS
	BinPath    string   // absolute path to the dctl binary
	Home       string   // user home dir
	User       string   // username (for loginctl enable-linger)
	EnvFile    string   // path to the secrets env file (mode 0600)
	HealthAddr string   // --health-addr value; "" omits the flag
	ExtraArgs  []string // extra args appended to `dctl serve`
	SkipStart  bool     // configure boot-start but don't start now (e.g. token not set yet)
}

// FileWrite is one file the plan writes. Template files are written only when
// missing (so an install never clobbers the user's secrets).
type FileWrite struct {
	Path     string
	Content  string
	Mode     os.FileMode
	Template bool
}

// Command is one shell command the plan runs. IgnoreErr commands are best-effort
// (e.g. unloading a service that isn't loaded yet).
type Command struct {
	Argv      []string
	IgnoreErr bool
}

// Plan is the full set of side effects for an install or uninstall.
type Plan struct {
	Files    []FileWrite
	Commands []Command
	Notes    []string // human-facing follow-ups (shown after a successful run)
}

func goos(c Config) string {
	if c.GOOS != "" {
		return c.GOOS
	}
	return runtime.GOOS
}

// serveArgs builds the `serve …` argv the service runs.
func serveArgs(c Config) []string {
	args := []string{"serve"}
	if c.HealthAddr != "" {
		args = append(args, "--health-addr", c.HealthAddr)
	}
	return append(args, c.ExtraArgs...)
}

const envTemplate = `# dctl daemon secrets — keep private (chmod 600), never commit.
# Fill these in, then restart the service.
DISCORD_BOT_TOKEN=
DISCORD_CHANNEL_ID=
DCTL_OWNER_ID=
`

// envFileWrite is the (always template, never-overwrite) secrets file shared by
// every platform.
func envFileWrite(c Config) FileWrite {
	return FileWrite{Path: c.EnvFile, Content: envTemplate, Mode: 0o600, Template: true}
}

// BuildPlan returns the install plan for c's target OS.
func BuildPlan(c Config) (Plan, error) {
	switch goos(c) {
	case "linux":
		return linuxPlan(c), nil
	case "darwin":
		return macPlan(c), nil
	case "windows":
		return windowsPlan(c), nil
	default:
		return Plan{}, fmt.Errorf("unsupported OS %q", goos(c))
	}
}

// BuildUninstall returns the uninstall plan for c's target OS.
func BuildUninstall(c Config) (Plan, error) {
	switch goos(c) {
	case "linux":
		unit := filepath.Join(c.Home, ".config", "systemd", "user", linuxUnitName)
		return Plan{Commands: []Command{
			{Argv: []string{"systemctl", "--user", "disable", "--now", linuxUnitName}, IgnoreErr: true},
			{Argv: []string{"rm", "-f", unit}},
			{Argv: []string{"systemctl", "--user", "daemon-reload"}, IgnoreErr: true},
		}}, nil
	case "darwin":
		plist := filepath.Join(c.Home, "Library", "LaunchAgents", macLabel+".plist")
		return Plan{Commands: []Command{
			{Argv: []string{"launchctl", "unload", "-w", plist}, IgnoreErr: true},
			{Argv: []string{"rm", "-f", plist}},
		}}, nil
	case "windows":
		return Plan{Commands: []Command{
			{Argv: []string{"schtasks", "/delete", "/tn", winTaskName, "/f"}},
		}}, nil
	default:
		return Plan{}, fmt.Errorf("unsupported OS %q", goos(c))
	}
}

// StatusCommand returns the command that reports whether the service is active.
func StatusCommand(c Config) (Command, error) {
	// IgnoreErr: these report status by exit code (e.g. systemctl exits 3 when
	// the unit is inactive); we still want to print the output without turning
	// "stopped" into a CLI error.
	switch goos(c) {
	case "linux":
		return Command{Argv: []string{"systemctl", "--user", "status", linuxUnitName}, IgnoreErr: true}, nil
	case "darwin":
		return Command{Argv: []string{"launchctl", "list", macLabel}, IgnoreErr: true}, nil
	case "windows":
		return Command{Argv: []string{"schtasks", "/query", "/tn", winTaskName, "/v"}, IgnoreErr: true}, nil
	default:
		return Command{}, fmt.Errorf("unsupported OS %q", goos(c))
	}
}

func linuxPlan(c Config) Plan {
	unit := filepath.Join(c.Home, ".config", "systemd", "user", linuxUnitName)
	content := "[Unit]\n" +
		"Description=dctl Discord daemon\n" +
		"After=network-online.target\n" +
		"Wants=network-online.target\n\n" +
		"[Service]\n" +
		"Type=simple\n" +
		"EnvironmentFile=-" + c.EnvFile + "\n" + // leading '-' => optional (no boot failure if absent)
		"ExecStart=" + quoteArgv(c.BinPath, serveArgs(c)) + "\n" +
		"Restart=always\n" +
		"RestartSec=3\n\n" +
		"[Install]\n" +
		"WantedBy=default.target\n"
	cmds := []Command{
		{Argv: []string{"systemctl", "--user", "daemon-reload"}},
	}
	if c.SkipStart {
		// Enable at boot but don't start now: with no token the daemon exits
		// immediately and Restart=always would crash-loop until it's filled in.
		cmds = append(cmds, Command{Argv: []string{"systemctl", "--user", "enable", linuxUnitName}})
	} else {
		cmds = append(cmds, Command{Argv: []string{"systemctl", "--user", "enable", "--now", linuxUnitName}})
	}
	if c.User != "" {
		// Linger lets the user service keep running after logout / at boot.
		cmds = append(cmds, Command{Argv: []string{"loginctl", "enable-linger", c.User}, IgnoreErr: true})
	}
	return Plan{
		Files:    []FileWrite{{Path: unit, Content: content, Mode: 0o644}, envFileWrite(c)},
		Commands: cmds,
		Notes:    []string{startNote(c, "systemctl --user start "+linuxUnitName)},
	}
}

func macPlan(c Config) Plan {
	plist := filepath.Join(c.Home, "Library", "LaunchAgents", macLabel+".plist")
	logPath := filepath.Join(c.Home, ".local", "state", "dctl", "dctl.log")
	// launchd has no EnvironmentFile, so the program sources the env file in a
	// login shell before exec'ing dctl — keeps the token out of the plist.
	run := "set -a; [ -f '" + c.EnvFile + "' ] && . '" + c.EnvFile + "'; exec " +
		quoteArgv(c.BinPath, serveArgs(c))
	content := xmlHeader +
		"<plist version=\"1.0\">\n<dict>\n" +
		"  <key>Label</key><string>" + macLabel + "</string>\n" +
		"  <key>ProgramArguments</key>\n  <array>\n" +
		"    <string>/bin/sh</string>\n    <string>-lc</string>\n    <string>" + xmlEscape(run) + "</string>\n" +
		"  </array>\n" +
		"  <key>RunAtLoad</key><true/>\n" +
		"  <key>KeepAlive</key><true/>\n" +
		"  <key>StandardOutPath</key><string>" + logPath + "</string>\n" +
		"  <key>StandardErrorPath</key><string>" + logPath + "</string>\n" +
		"</dict>\n</plist>\n"
	cmds := []Command{
		{Argv: []string{"launchctl", "unload", "-w", plist}, IgnoreErr: true},
	}
	if !c.SkipStart {
		// Skipped when no token yet: loading would start the agent, which exits
		// immediately and KeepAlive would respawn it. RunAtLoad still starts it
		// at the next login once the token is in place.
		cmds = append(cmds, Command{Argv: []string{"launchctl", "load", "-w", plist}})
	}
	return Plan{
		Files:    []FileWrite{{Path: plist, Content: content, Mode: 0o644}, envFileWrite(c)},
		Commands: cmds,
		Notes:    []string{startNote(c, "launchctl load -w "+plist)},
	}
}

func windowsPlan(c Config) Plan {
	launcher := filepath.Join(c.Home, "AppData", "Local", "dctl", "dctl-serve.cmd")
	// A .cmd launcher loads the env file (KEY=VALUE lines) then runs dctl, so the
	// scheduled task never carries the token itself.
	content := "@echo off\r\n" +
		"if exist \"" + c.EnvFile + "\" (\r\n" +
		"  for /f \"usebackq eol=# tokens=1,* delims==\" %%a in (\"" + c.EnvFile + "\") do set \"%%a=%%b\"\r\n" +
		")\r\n" +
		"\"" + c.BinPath + "\" " + strings.Join(serveArgs(c), " ") + "\r\n"
	return Plan{
		Files: []FileWrite{{Path: launcher, Content: content, Mode: 0o644}, envFileWrite(c)},
		Commands: []Command{
			{Argv: []string{"schtasks", "/create", "/tn", winTaskName, "/tr", launcher, "/sc", "onlogon", "/rl", "limited", "/f"}},
		},
		Notes: []string{"Edit " + c.EnvFile + " with your token, then re-run or reboot to start the task."},
	}
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
`

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// startNote returns the human-facing follow-up after an install: how to start
// the service once the token is in place (when start was skipped), or where its
// secrets live (when it's already running).
func startNote(c Config, startCmd string) string {
	if c.SkipStart {
		return "installed and enabled at boot, but NOT started — set DISCORD_BOT_TOKEN in " +
			c.EnvFile + ", then: " + startCmd
	}
	return "running. Secrets live in " + c.EnvFile + "; edit it and restart to change them."
}

// envFileHasToken reports whether the env file already carries a non-empty
// DISCORD_BOT_TOKEN, so install can start the service immediately instead of
// configuring a crash-loop with an empty template.
func envFileHasToken(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok &&
			strings.TrimSpace(k) == "DISCORD_BOT_TOKEN" && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// quoteArgv joins a binary path and its args for an ExecStart line, quoting the
// binary path if it contains spaces (systemd/sh both accept double quotes).
func quoteArgv(bin string, args []string) string {
	b := bin
	if strings.ContainsAny(bin, " \t") {
		b = "\"" + bin + "\""
	}
	if len(args) == 0 {
		return b
	}
	return b + " " + strings.Join(args, " ")
}

// DefaultConfig fills a Config from the current environment (binary path, home,
// user, default env-file location and health address).
func DefaultConfig() (Config, error) {
	bin, err := os.Executable()
	if err != nil {
		return Config{}, fmt.Errorf("locate dctl binary: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("locate home dir: %w", err)
	}
	uname := ""
	if u, err := user.Current(); err == nil {
		uname = u.Username
	}
	return Config{
		BinPath:    bin,
		Home:       home,
		User:       uname,
		EnvFile:    filepath.Join(home, ".config", "dctl", "dctl.env"),
		HealthAddr: "127.0.0.1:8787",
	}, nil
}

// Install runs the install plan for the current OS: it writes the unit/launcher
// and secrets template, then enables and starts the service.
func Install(ctx context.Context, c Config) error {
	// Without a token the daemon exits immediately; starting it now would just
	// crash-loop until the user edits the template. Configure boot-start only.
	if !envFileHasToken(c.EnvFile) {
		c.SkipStart = true
	}
	p, err := BuildPlan(c)
	if err != nil {
		return err
	}
	return runPlan(ctx, p)
}

// Uninstall stops and removes the service for the current OS.
func Uninstall(ctx context.Context, c Config) error {
	p, err := BuildUninstall(c)
	if err != nil {
		return err
	}
	return runPlan(ctx, p)
}

// Status prints the platform's service status to stdout/stderr.
func Status(ctx context.Context, c Config) error {
	cmd, err := StatusCommand(c)
	if err != nil {
		return err
	}
	return runCommand(ctx, cmd)
}

func runPlan(ctx context.Context, p Plan) error {
	for _, f := range p.Files {
		if err := writeFile(f); err != nil {
			return err
		}
	}
	for _, cmd := range p.Commands {
		if err := runCommand(ctx, cmd); err != nil {
			return err
		}
	}
	for _, n := range p.Notes {
		fmt.Fprintln(os.Stderr, "dctl service: "+n)
	}
	return nil
}

func writeFile(f FileWrite) error {
	if f.Template {
		// Never overwrite an existing secrets file. Only a definite "not found"
		// permits writing the template; any other stat error is fatal rather
		// than risk clobbering a file we simply couldn't read.
		switch _, err := os.Stat(f.Path); {
		case err == nil:
			return nil
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("stat %s: %w", f.Path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(f.Path), err)
	}
	if err := os.WriteFile(f.Path, []byte(f.Content), f.Mode); err != nil {
		return fmt.Errorf("write %s: %w", f.Path, err)
	}
	return nil
}

func runCommand(ctx context.Context, cmd Command) error {
	if len(cmd.Argv) == 0 {
		return nil
	}
	c := exec.CommandContext(ctx, cmd.Argv[0], cmd.Argv[1:]...)
	c.Stdout, c.Stderr = os.Stderr, os.Stderr
	if err := c.Run(); err != nil && !cmd.IgnoreErr {
		return fmt.Errorf("%s: %w", strings.Join(cmd.Argv, " "), err)
	}
	return nil
}
