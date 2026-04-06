package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type Session struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`

	mu       sync.Mutex
	lastSnap string
	polling  bool
}

var (
	sessions   = make(map[string]*Session)
	sessionsMu sync.RWMutex
)

// adoptExistingSessions finds running tmux sessions and registers them in tclaw.
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

	// Resolve to absolute path relative to CWD
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(abs, 0755); err != nil {
		return "", fmt.Errorf("cannot create directory: %w", err)
	}

	return abs, nil
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

	// Create tmux session in background, starting in the resolved directory
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name, "-c", absDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux new-session: %s: %w", string(out), err)
	}

	// Start claude inside the session
	cmd = exec.Command("tmux", "send-keys", "-t", name, "claude", "Enter")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux send-keys claude: %s: %w", string(out), err)
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

func deleteSession(name string) error {
	sessionsMu.Lock()
	s, exists := sessions[name]
	if !exists {
		sessionsMu.Unlock()
		return fmt.Errorf("session %q not found", name)
	}
	delete(sessions, name)
	sessionsMu.Unlock()

	_ = s // stop any polling goroutine if needed

	cmd := exec.Command("tmux", "kill-session", "-t", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-session: %s: %w", string(out), err)
	}
	return nil
}

func (s *Session) SendInput(text string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", s.Name, text, "Enter")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys: %s: %w", string(out), err)
	}
	return nil
}

// SendKey sends a raw tmux key (e.g. "Up", "Down", "Enter", "Escape", "y", "n").
func (s *Session) SendKey(key string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", s.Name, key)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys: %s: %w", string(out), err)
	}
	return nil
}

// capturePane captures the tmux pane content by session name.
func capturePane(name string) (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-t", name, "-p", "-S", "-1000")
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
