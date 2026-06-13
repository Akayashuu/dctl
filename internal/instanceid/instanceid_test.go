package instanceid

import "testing"

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		id   string
		ok   bool
	}{
		{"simple", "alice", true},
		{"with-digits", "u12345678", true},
		{"with-hyphen", "team-a", true},
		{"max-len-16", "abcdefghijklmnop", true},
		{"single-char", "a", true},
		{"empty", "", false},
		{"too-long-17", "abcdefghijklmnopq", false},
		{"uppercase", "Alice", false},
		{"leading-hyphen", "-alice", false},
		{"double-underscore", "ali__ce", false},
		{"single-underscore", "ali_ce", false},
		{"slash", "ali/ce", false},
		{"dotdot", "..", false},
		{"space", "ali ce", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Validate(tt.id); got != tt.ok {
				t.Fatalf("Validate(%q) = %v, want %v", tt.id, got, tt.ok)
			}
		})
	}
}
