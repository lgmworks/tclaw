package main

import (
	"fmt"
	"os/exec"
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

func createSession(dir string) (*Session, error) {
	name := sanitizeName(dir)

	sessionsMu.Lock()
	if _, exists := sessions[name]; exists {
		sessionsMu.Unlock()
		return nil, fmt.Errorf("session %q already exists", name)
	}
	sessionsMu.Unlock()

	// Create tmux session in background, starting in the given directory
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name, "-c", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux new-session: %s: %w", string(out), err)
	}

	// Start claude inside the session
	cmd = exec.Command("tmux", "send-keys", "-t", name, "claude", "Enter")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux send-keys claude: %s: %w", string(out), err)
	}

	s := &Session{Name: name, Dir: dir}
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

func (s *Session) Capture() (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-t", s.Name, "-p", "-S", "-1000")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}

	// Strip trailing blank lines (like sed '/^$/d' but only trailing)
	lines := strings.Split(string(out), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(lines, "\n"), nil
}
