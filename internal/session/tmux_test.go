package session

import (
	"strings"
	"testing"
)

func TestNewLines(t *testing.T) {
	before := "banner\n> \n"
	after := "banner\nhello from claude\nmore output\n> \n"
	got := strings.Join(newLines(before, after), "\n")
	want := "hello from claude\nmore output\n> "
	if got != want {
		t.Fatalf("newLines =\n%q\nwant\n%q", got, want)
	}
}

func TestStripChrome(t *testing.T) {
	in := []string{
		"╭────────────────────────╮",
		"│ > hello                │",
		"╰────────────────────────╯",
		"",
		"Here is your answer.",
		"It spans two lines.",
		"",
		"",
		"⏵⏵ accept edits on (shift+tab to cycle)",
		"? for shortcuts",
	}
	got := stripChrome(in)
	want := "Here is your answer.\nIt spans two lines."
	if strings.Join(got, "\n") != want {
		t.Fatalf("stripChrome =\n%q\nwant\n%q", strings.Join(got, "\n"), want)
	}
}
