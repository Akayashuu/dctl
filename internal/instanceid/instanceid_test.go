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

func TestSlugify(t *testing.T) {
	tests := []struct {
		name  string
		owner string
		want  string
	}{
		{"snowflake-18-digits", "343535234303787009", "u03787009"},
		{"short-snowflake-5", "12345", "u12345"},
		{"exactly-8", "12345678", "u12345678"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Slugify(tt.owner)
			if got != tt.want {
				t.Fatalf("Slugify(%q) = %q, want %q", tt.owner, got, tt.want)
			}
			if got != "" && !Validate(got) {
				t.Fatalf("Slugify(%q) = %q which fails Validate", tt.owner, got)
			}
		})
	}
}
