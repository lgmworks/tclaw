package main

import "strings"

// DiffResult holds the result of comparing two pane captures.
type DiffResult struct {
	NewLines      []string `json:"lines,omitempty"`
	FullLineCount int      `json:"full_line_count"`
	Ready         bool     `json:"ready"`
}

// diffCaptures compares old and new capture-pane output.
// Returns only the new lines that appeared since the last capture.
func diffCaptures(oldSnap, newSnap string) DiffResult {
	newLines := strings.Split(newSnap, "\n")
	result := DiffResult{
		FullLineCount: len(newLines),
		Ready:         isReady(newSnap),
	}

	if oldSnap == "" {
		// First capture — everything is new
		result.NewLines = newLines
		return result
	}

	oldLines := strings.Split(oldSnap, "\n")

	// Find where the new content starts by matching from the end of old
	// We look for the longest suffix of oldLines that matches a prefix of newLines
	// Simple approach: find how many lines at the start are the same
	commonPrefix := 0
	minLen := len(oldLines)
	if len(newLines) < minLen {
		minLen = len(newLines)
	}
	for i := 0; i < minLen; i++ {
		if oldLines[i] != newLines[i] {
			break
		}
		commonPrefix = i + 1
	}

	if commonPrefix < len(newLines) {
		result.NewLines = newLines[commonPrefix:]
	}

	return result
}

// isReady checks if Claude Code is waiting for input.
// It looks for the ❯ prompt as the last meaningful line.
func isReady(snap string) bool {
	lines := strings.Split(snap, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		// Claude Code prompt patterns
		if trimmed == "❯" || trimmed == ">" {
			return true
		}
		// Also check for the shortcuts hint line which appears below the prompt
		if strings.Contains(trimmed, "? for shortcuts") {
			return true
		}
		return false
	}
	return false
}
