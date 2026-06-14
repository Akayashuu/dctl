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
	const usage = "usage: dctl service <install|uninstall|status> [--health-addr ADDR] [--env-file PATH]"
	if len(args) == 0 {
		return errors.New(usage)
	}
	sub := args[0]
	switch sub {
	case "install", "uninstall", "status":
	default:
		return fmt.Errorf("dctl service: unknown subcommand %q (want install|uninstall|status)", sub)
	}

	cfg, err := service.DefaultConfig()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	healthAddr := fs.String("health-addr", cfg.HealthAddr, "value for serve --health-addr (empty disables the health endpoint)")
	envFile := fs.String("env-file", cfg.EnvFile, "path to the 0600 secrets file the service sources")
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
