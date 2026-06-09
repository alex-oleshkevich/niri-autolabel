package main

import (
	"strconv"
	"strings"
)

const maxLabelLen = 12

// uniqueLabel returns base, or base with the smallest numeric suffix that is
// not already taken. niri can only reference a workspace by its (per-output,
// shifting) index or its name, so names we manage must stay globally unique to
// be addressable unambiguously.
func uniqueLabel(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for n := 2; n < 1000; n++ {
		suffix := strconv.Itoa(n)
		stem := base
		if len(stem)+len(suffix) > maxLabelLen {
			stem = stem[:maxLabelLen-len(suffix)]
		}
		cand := stem + suffix
		if !taken[cand] {
			return cand
		}
	}
	return base
}

// sanitize reduces a raw codex response to a single niri-safe workspace label:
// lowercase, first whitespace token, alphanumeric only, capped at maxLabelLen.
// ok is false when nothing usable remains.
func sanitize(raw string) (string, bool) {
	fields := strings.Fields(strings.ToLower(raw))
	if len(fields) == 0 {
		return "", false
	}

	var b strings.Builder
	for _, r := range fields[0] {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
		if b.Len() == maxLabelLen {
			break
		}
	}

	label := b.String()
	if label == "" {
		return "", false
	}
	return label, true
}
