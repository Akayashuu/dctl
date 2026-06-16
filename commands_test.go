package dctl

import (
	"reflect"
	"testing"
)

func TestCommandBuildFlat(t *testing.T) {
	got := NewCommand("ping", "health check").
		With(Bool("verbose", "extra detail", false)).
		JSON()
	want := map[string]any{
		"name": "ping", "type": 1, "description": "health check",
		"options": []map[string]any{
			{"type": 5, "name": "verbose", "description": "extra detail"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("\ngot  %#v\nwant %#v", got, want)
	}
}

func TestCommandBuildSubAndGroup(t *testing.T) {
	got := NewCommand("set", "dctl settings").
		With(
			Sub("home", "set the category", ChannelOpt("channel", "category", true).ChannelTypes(ChannelCategory, ChannelForum)),
			Group("role", "role ops", Sub("add", "give a role", User("who", "member", true), RoleOpt("role", "role", true))),
		).JSON()

	opts := got["options"].([]map[string]any)
	if opts[0]["type"] != optSubCommand || opts[1]["type"] != optSubCommandGroup {
		t.Fatalf("sub/group types wrong: %#v", opts)
	}
	home := opts[0]["options"].([]map[string]any)[0]
	if home["type"] != optChannel || !reflect.DeepEqual(home["channel_types"], []int{ChannelCategory, ChannelForum}) {
		t.Fatalf("channel option wrong: %#v", home)
	}
	add := opts[1]["options"].([]map[string]any)[0]
	if add["type"] != optSubCommand || len(add["options"].([]map[string]any)) != 2 {
		t.Fatalf("group sub wrong: %#v", add)
	}
}

func TestCommandFullCoverage(t *testing.T) {
	got := NewCommand("cfg", "settings").
		Loc(LocaleFR, "config", "réglages").
		Perms(PermManageGuild, PermManageRoles).
		DMPermission(false).
		NSFW().
		With(
			String("path", "absolute path", true).Len(1, 4000).Loc(LocaleFR, "chemin", "chemin absolu"),
			Int("count", "how many", false).Range(1, 100).Choices(NewChoice("ten", 10).Loc(LocaleFR, "dix")),
			String("mode", "pick", false).Autocomplete(),
		).JSON()

	if got["name_localizations"].(map[string]string)["fr"] != "config" {
		t.Errorf("cmd name loc missing: %#v", got["name_localizations"])
	}
	if got["default_member_permissions"] != "268435488" { // (1<<5)|(1<<28)
		t.Errorf("perms = %v", got["default_member_permissions"])
	}
	if got["dm_permission"] != false || got["nsfw"] != true {
		t.Errorf("dm/nsfw wrong: %#v", got)
	}
	opts := got["options"].([]map[string]any)
	if opts[0]["min_length"] != 1 || opts[0]["max_length"] != 4000 {
		t.Errorf("len wrong: %#v", opts[0])
	}
	if opts[0]["description_localizations"].(map[string]string)["fr"] != "chemin absolu" {
		t.Errorf("opt desc loc missing: %#v", opts[0])
	}
	if opts[1]["min_value"] != 1.0 || opts[1]["max_value"] != 100.0 {
		t.Errorf("range wrong: %#v", opts[1])
	}
	choice := opts[1]["choices"].([]map[string]any)[0]
	if choice["value"] != 10 || choice["name_localizations"].(map[string]string)["fr"] != "dix" {
		t.Errorf("choice wrong: %#v", choice)
	}
	if opts[2]["autocomplete"] != true {
		t.Errorf("autocomplete missing: %#v", opts[2])
	}
}

func TestContextMenuCommandHasNoDescription(t *testing.T) {
	got := NewUserCommand("Report").JSON()
	if _, ok := got["description"]; ok {
		t.Errorf("user command must omit description: %#v", got)
	}
	if got["type"] != cmdUser {
		t.Errorf("type = %v", got["type"])
	}
}

func TestContextMenuCommandOmitsDescriptionLocalizations(t *testing.T) {
	// Loc on a context-menu command may localize the name but must NOT emit
	// description_localizations (Discord rejects it for type 2/3).
	got := NewUserCommand("Report").Loc(LocaleFR, "Signaler", "ignored").JSON()
	if _, ok := got["description_localizations"]; ok {
		t.Errorf("context-menu command must omit description_localizations: %#v", got)
	}
	if got["name_localizations"].(map[string]string)["fr"] != "Signaler" {
		t.Errorf("name localization should still be set: %#v", got["name_localizations"])
	}
}

func TestOptionLocDoesNotMutateShared(t *testing.T) {
	base := String("x", "y", true)
	a := base.Loc(LocaleFR, "fr", "frd")
	b := base.Loc(LocaleDE, "de", "ded")
	if len(a.nameLoc) != 1 || len(b.nameLoc) != 1 {
		t.Fatalf("Loc leaked across copies: a=%v b=%v", a.nameLoc, b.nameLoc)
	}
}
