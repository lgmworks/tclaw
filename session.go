package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type Session struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`

	mu       sync.Mutex
	lastSnap string
}

var (
	sessions   = make(map[string]*Session)
	sessionsMu sync.RWMutex
)

// adoptExistingSessions finds running tmux sessions and registers them in tclaw.
// This is what lets sessions survive a tclaw restart: the tmux server keeps the
// harness alive, and on startup tclaw re-attaches to whatever is still running.
func adoptExistingSessions() {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}:#{pane_current_path}")
	out, err := cmd.Output()
	if err != nil {
		return // no tmux server or no sessions
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		name := parts[0]
		dir := ""
		if len(parts) > 1 {
			dir = parts[1]
		}

		sessionsMu.Lock()
		if _, exists := sessions[name]; !exists {
			sessions[name] = &Session{Name: name, Dir: dir}
		}
		sessionsMu.Unlock()
	}
}

// sanitizeName converts a directory path into a safe tmux session name.
// "/home/user/mi proyecto ñoño" → "home-user-mi-proyecto-nono"
func sanitizeName(dir string) string {
	// Normalize unicode and strip accents/tildes
	t := norm.NFD.String(dir)
	var b strings.Builder
	for _, r := range t {
		if unicode.Is(unicode.Mn, r) {
			continue // skip combining marks (accents, tildes)
		}
		b.WriteRune(r)
	}
	s := b.String()

	// Replace path separators and spaces with hyphens
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\\", "-")
	s = strings.ReplaceAll(s, " ", "-")

	// Keep only alphanumeric and hyphens
	re := regexp.MustCompile(`[^a-zA-Z0-9-]`)
	s = re.ReplaceAllString(s, "")

	// Collapse multiple hyphens
	re = regexp.MustCompile(`-{2,}`)
	s = re.ReplaceAllString(s, "-")

	// Trim leading/trailing hyphens
	s = strings.Trim(s, "-")

	// Lowercase
	s = strings.ToLower(s)

	if s == "" {
		s = "session"
	}
	return s
}

// resolveDir makes the dir relative to CWD, stripping leading slashes,
// and creates it if it doesn't exist.
func resolveDir(dir string) (string, error) {
	// Strip leading slashes — "/Users/private" becomes "Users/private"
	dir = strings.TrimLeft(dir, "/\\")
	if dir == "" {
		dir = "."
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	if err := os.MkdirAll(abs, 0755); err != nil {
		return "", fmt.Errorf("cannot create directory: %w", err)
	}

	return abs, nil
}

// harnessCommandParts returns the command + args for the configured harness.
func harnessCommandParts() []string {
	switch getConfig().Harness {
	case "codex":
		return []string{"codex", "--dangerously-bypass-approvals-and-sandbox"}
	case "claude":
		fallthrough
	default:
		return []string{"claude", "--dangerously-skip-permissions"}
	}
}

// tmuxKeysForCommand turns a command into send-keys arguments, inserting an
// explicit "Space" key between parts so the whole line is typed literally.
func tmuxKeysForCommand(parts []string) []string {
	if len(parts) == 0 {
		return nil
	}

	keys := make([]string, 0, len(parts)*2-1)
	for i, part := range parts {
		if i > 0 {
			keys = append(keys, "Space")
		}
		keys = append(keys, part)
	}
	return keys
}

func createSession(dir string) (*Session, error) {
	absDir, err := resolveDir(dir)
	if err != nil {
		return nil, err
	}

	name := sanitizeName(dir)

	sessionsMu.Lock()
	if _, exists := sessions[name]; exists {
		sessionsMu.Unlock()
		return nil, fmt.Errorf("session %q already exists", name)
	}
	sessionsMu.Unlock()

	// Create the tmux session detached, starting in the resolved directory.
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name, "-c", absDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux new-session: %s: %w", string(out), err)
	}

	// window-size manual keeps the pane at whatever size we set via
	// resize-window, instead of tmux snapping it back. Best-effort: older
	// tmux versions lack the option, and that is fine.
	_ = exec.Command("tmux", "set-option", "-t", name, "window-size", "manual").Run()

	// Start the configured harness inside the session.
	args := append([]string{"send-keys", "-t", name}, tmuxKeysForCommand(harnessCommandParts())...)
	args = append(args, "Enter")
	cmd = exec.Command("tmux", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux send-keys harness: %s: %w", string(out), err)
	}

	s := &Session{Name: name, Dir: absDir}
	sessionsMu.Lock()
	sessions[name] = s
	sessionsMu.Unlock()

	return s, nil
}

func getSession(name string) *Session {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	return sessions[name]
}

func listSessions() []*Session {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	result := make([]*Session, 0, len(sessions))
	for _, s := range sessions {
		result = append(result, s)
	}
	return result
}

// liveSessionNames returns the set of registered session names. Used by
// /api/dirs to colour folders that already have a session.
func liveSessionNames() map[string]bool {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	m := make(map[string]bool, len(sessions))
	for name := range sessions {
		m[name] = true
	}
	return m
}

func deleteSession(name string) error {
	sessionsMu.Lock()
	_, exists := sessions[name]
	if !exists {
		sessionsMu.Unlock()
		return fmt.Errorf("session %q not found", name)
	}
	delete(sessions, name)
	sessionsMu.Unlock()

	cmd := exec.Command("tmux", "kill-session", "-t", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-session: %s: %w", string(out), err)
	}
	return nil
}

func (s *Session) SendInput(text, submitKey string) error {
	if text != "" {
		if err := s.PasteText(text); err != nil {
			return err
		}
		time.Sleep(300 * time.Millisecond)
	}

	// "none" pastes the text without submitting (used by the NL button).
	if submitKey == "none" {
		return nil
	}
	if submitKey == "" {
		submitKey = "Enter"
	}

	cmd := exec.Command("tmux", "send-keys", "-t", s.Name, submitKey)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys submit key: %s: %w", string(out), err)
	}
	return nil
}

func (s *Session) PasteText(text string) error {
	bufferName := "tclaw-input"

	setCmd := exec.Command("tmux", "set-buffer", "-b", bufferName, "--", text)
	if out, err := setCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux set-buffer: %s: %w", string(out), err)
	}

	pasteCmd := exec.Command("tmux", "paste-buffer", "-b", bufferName, "-t", s.Name)
	if out, err := pasteCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux paste-buffer: %s: %w", string(out), err)
	}

	return nil
}

func (s *Session) SendText(text string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", s.Name, "-l", text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys text: %s: %w", string(out), err)
	}
	return nil
}

// SendKey sends a raw tmux key (e.g. "Up", "Down", "Enter", "Escape", "C-c").
func (s *Session) SendKey(key string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", s.Name, key)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys: %s: %w", string(out), err)
	}
	return nil
}

// Resize sets the tmux window size so the harness TUI reflows to the client's
// real screen width. capturePane then returns lines at that width.
func (s *Session) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	cmd := exec.Command("tmux", "resize-window", "-t", s.Name,
		"-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux resize-window: %s: %w", string(out), err)
	}
	return nil
}

// capturePane captures the tmux pane content (screen + 1000 lines scrollback).
func capturePane(name string) (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-t", name, "-p", "-e", "-J", "-S", "-1000")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(lines, "\n"), nil
}

func (s *Session) Capture() (string, error) {
	return capturePane(s.Name)
}
