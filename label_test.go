package main

import "testing"

func TestSanitize(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"plain word", "code", "code", true},
		{"trailing newline", "gmail\n", "gmail", true},
		{"uppercase", "Slack", "slack", true},
		{"multi word takes first", "code editor", "code", true},
		{"strips punctuation", "**email**", "email", true},
		{"strips quotes", "\"browser\"", "browser", true},
		{"truncates to 12", "documentation", "documentatio", true},
		{"keeps alphanumeric", "web3", "web3", true},
		{"empty", "", "", false},
		{"whitespace only", "   \n", "", false},
		{"only punctuation", "!!!", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := sanitize(tc.raw)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("sanitize(%q) = (%q, %t), want (%q, %t)", tc.raw, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestUniqueLabel(t *testing.T) {
	cases := []struct {
		name  string
		base  string
		taken []string
		want  string
	}{
		{"free", "code", nil, "code"},
		{"one collision", "code", []string{"code"}, "code2"},
		{"two collisions", "code", []string{"code", "code2"}, "code3"},
		{"suffix fits within 12", "development", []string{"development"}, "development2"},
		{"suffix forces trim", "documentatio", []string{"documentatio"}, "documentati2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taken := map[string]bool{}
			for _, s := range tc.taken {
				taken[s] = true
			}
			if got := uniqueLabel(tc.base, taken); got != tc.want {
				t.Fatalf("uniqueLabel(%q) = %q, want %q", tc.base, got, tc.want)
			}
			if len(uniqueLabel(tc.base, taken)) > maxLabelLen {
				t.Fatalf("label exceeds %d chars", maxLabelLen)
			}
		})
	}
}
