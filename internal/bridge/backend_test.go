package bridge

import "testing"

func TestResolveBackendDefaultsToTmux(t *testing.T) {
	cases := []struct {
		backend string
		stream  bool
		want    string
	}{
		{"", true, "tmux"},           // default: nothing set → tmux
		{"", false, "oneshot"},       // legacy --stream=false → oneshot
		{"stream", true, "stream"},   // explicit wins over the default
		{"stream", false, "stream"},  // explicit wins over --stream
		{"oneshot", true, "oneshot"}, // explicit wins
		{"tmux", false, "tmux"},      // explicit wins
	}
	for _, c := range cases {
		if got := resolveBackend(c.backend, c.stream); got != c.want {
			t.Errorf("resolveBackend(%q, %v) = %q, want %q", c.backend, c.stream, got, c.want)
		}
	}
}
