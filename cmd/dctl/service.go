package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/vskstudio/dctl/internal/service"
)

// runService installs/uninstalls/inspects the `dctl serve` daemon as a native
// boot-started service (systemd user unit on Linux, launchd LaunchAgent on
// macOS, Task Scheduler task on Windows).
func runService(ctx context.Context, args []string) error {
	const usage = "usage: dctl service <install|uninstall|status|restart|update> [--health-addr ADDR] [--env-file PATH] [--cmd 'claude …'] [--source DIR] [--no-pull]"
	if len(args) == 0 {
		return errors.New(usage)
	}
	sub := args[0]
	switch sub {
	case "install", "uninstall", "status", "restart", "update":
	default:
		return fmt.Errorf("dctl service: unknown subcommand %q (want install|uninstall|status|restart|update)", sub)
	}

	cfg, err := service.DefaultConfig()
	if err != nil {
		return err
	}

	// restart/update are operational (no install planning); handle them up front
	// before the install-oriented flag parsing below.
	switch sub {
	case "restart":
		if err := service.Restart(ctx, cfg); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "dctl service: restarted")
		return nil
	case "update":
		return runServiceUpdate(ctx, cfg, args[1:])
	}

	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	healthAddr := fs.String("health-addr", cfg.HealthAddr, "value for serve --health-addr (empty disables the health endpoint)")
	envFile := fs.String("env-file", cfg.EnvFile, "path to the 0600 secrets file the service sources")
	// --cmd bakes a default bridged command into the unit's ExecStart (serve
	// --cmd). It sets the model/effort/etc. for every new session, e.g.
	// 'claude --model claude-opus-4-8 --effort low'. A per-session cmd: still
	// overrides it (see handler.sessionCreate).
	defaultCmd := fs.String("cmd", "", "default bridged command baked into the unit (e.g. 'claude --model claude-opus-4-8 --effort low')")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("dctl service %s: unexpected argument %q\n%s", sub, fs.Arg(0), usage)
	}
	cfg.HealthAddr = *healthAddr
	cfg.EnvFile = *envFile

	switch sub {
	case "install":
		if *defaultCmd != "" {
			// One ExtraArg pair: serve parses "--cmd" then the whole quoted string
			// as a single value (serveArgs appends ExtraArgs verbatim; the unit
			// builder quotes the space-bearing value so it stays one argv token).
			cfg.ExtraArgs = append(cfg.ExtraArgs, "--cmd", *defaultCmd)
		}
		// Install prints a Note describing the exact state (started, or enabled
		// at boot but awaiting a token), so don't assert "started" here.
		return service.Install(ctx, cfg)
	case "uninstall":
		if err := service.Uninstall(ctx, cfg); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "dctl service: removed")
		return nil
	case "status":
		return service.Status(ctx, cfg)
	default:
		return fmt.Errorf("dctl service: unknown subcommand %q (want install|uninstall|status)", sub)
	}
}

// runServiceUpdate handles `dctl service update [--source DIR] [--no-pull]`:
// (git pull +) rebuild the binary from source, then restart the service. The
// source defaults to the current directory — the natural spot to run it right
// after a local merge.
func runServiceUpdate(ctx context.Context, cfg service.Config, args []string) error {
	cwd, _ := os.Getwd()
	fs := flag.NewFlagSet("service update", flag.ContinueOnError)
	source := fs.String("source", cwd, "path to the dctl source checkout to build from")
	noPull := fs.Bool("no-pull", false, "skip `git pull --ff-only` before building")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("dctl service update: unexpected argument %q", fs.Arg(0))
	}
	if err := service.Update(ctx, cfg, *source, !*noPull); err != nil {
		return err
	}
	v := service.SourceVersion(ctx, *source)
	fmt.Fprintf(os.Stderr, "dctl service: rebuilt %s and restarted\n", v)
	return nil
}
