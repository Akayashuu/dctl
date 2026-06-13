package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/vskstudio/dctl"
)

// runChannel manages channels: list / create / delete / ensure. Guild defaults
// to the bot's sole server (mono-server); override with --guild.
func runChannel(ctx context.Context, c *dctl.Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: dctl channel <list|create|delete|ensure> [args]")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("channel", flag.ExitOnError)
	guild := fs.String("guild", "", "guild id (default: the bot's sole server)")
	fs.Parse(rest)
	pos := fs.Args()

	switch sub {
	case "list":
		chs, err := c.Channels(ctx, *guild)
		if err != nil {
			return err
		}
		for _, ch := range chs {
			fmt.Printf("%s\t%d\t%s\n", ch.ID, ch.Type, ch.Name)
		}
		return nil
	case "create":
		if len(pos) < 1 {
			return fmt.Errorf("usage: dctl channel create <name>")
		}
		ch, err := c.CreateChannel(ctx, *guild, pos[0])
		if err != nil {
			return err
		}
		fmt.Println(ch.ID)
		return nil
	case "ensure":
		if len(pos) < 1 {
			return fmt.Errorf("usage: dctl channel ensure <name>")
		}
		ch, err := c.EnsureChannel(ctx, *guild, pos[0])
		if err != nil {
			return err
		}
		fmt.Println(ch.ID)
		return nil
	case "delete":
		if len(pos) < 1 {
			return fmt.Errorf("usage: dctl channel delete <channel_id>")
		}
		if err := c.DeleteChannel(ctx, pos[0]); err != nil {
			return err
		}
		fmt.Println("deleted")
		return nil
	default:
		return fmt.Errorf("unknown channel subcommand %q (list|create|delete|ensure)", sub)
	}
}
