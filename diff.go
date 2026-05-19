package main

import "strings"

const tailLines = 30

// hasChanged compares the last N lines of two snapshots.
// Returns true if they differ.
func hasChanged(oldSnap, newSnap string) bool {
	return tail(oldSnap, tailLines) != tail(newSnap, tailLines)
}

// tail returns the last n lines of a string.
func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// isReady checks if Claude Code is waiting for input.
func isReady(snap string) bool {
	lines := strings.Split(snap, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if trimmed == "❯" || trimmed == ">" {
			return true
		}
		if strings.Contains(trimmed, "? for shortcuts") {
			return true
		}
		return false
	}
	return false
}
