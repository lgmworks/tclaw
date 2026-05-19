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

	"github.com/creack/pty"
	"golang.org/x/text/unicode/norm"
)

// replayBufferCap is the size of the per-session ring buffer kept so a
// reconnecting client can repaint the terminal from recent output.
const replayBufferCap = 256 * 1024

type Session struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`

	mu      sync.Mutex
	ptmx    *os.File
	cmd     *exec.Cmd
	clients map[*Client]bool
	buf     []byte // ring buffer of recent PTY output
	closed  bool
	rows    uint16
	cols    uint16
}

var (
	sessions   = make(map[string]*Session)
	sessionsMu sync.RWMutex
)

// sanitizeName converts a directory path into a safe session name.
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

// harnessCommand builds the exec.Cmd to launch the configured harness.
// It tries tclaw's own PATH first; if the harness isn't there — e.g. tclaw
// runs under systemd with a minimal PATH that lacks ~/.local/bin or nvm —
// it falls back to a login shell so the user's full environment is loaded.
func harnessCommand() *exec.Cmd {
	parts := harnessCommandParts()

	// 1. On tclaw's own PATH.
	if path, err := exec.LookPath(parts[0]); err == nil {
		return exec.Command(path, parts[1:]...)
	}

	// 2. ~/.local/bin — where the Claude Code installer puts the binary.
	// systemd's minimal PATH (and even login shells on some setups) miss it.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidate := filepath.Join(home, ".local", "bin", parts[0])
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return exec.Command(candidate, parts[1:]...)
		}
	}

	// 3. Login shell — picks up the user's full environment (nvm, etc.).
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	return exec.Command(shell, "-lc", "exec "+strings.Join(parts, " "))
}

// createSession spawns the configured harness inside a PTY.
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

	cmd := harnessCommand()
	cmd.Dir = absDir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

	s := &Session{
		Name:    name,
		Dir:     absDir,
		ptmx:    ptmx,
		cmd:     cmd,
		clients: make(map[*Client]bool),
		buf:     make([]byte, 0, replayBufferCap),
		rows:    24,
		cols:    80,
	}

	sessionsMu.Lock()
	sessions[name] = s
	sessionsMu.Unlock()

	go s.readLoop()
	go s.waitLoop()

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

// liveSessionNames returns the set of session names with a running process.
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
	s := getSession(name)
	if s == nil {
		return fmt.Errorf("session %q not found", name)
	}
	s.Close()
	return nil
}

// readLoop streams PTY output into the ring buffer and broadcasts it to
// every connected client. It runs for the whole life of the session,
// independent of whether any client is connected.
func (s *Session) readLoop() {
	tmp := make([]byte, 8192)
	for {
		n, err := s.ptmx.Read(tmp)
		if n > 0 {
			data := make([]byte, n)
			copy(data, tmp[:n])
			s.appendBuf(data)
			s.broadcast(data)
		}
		if err != nil {
			return
		}
	}
}

// waitLoop reaps the harness process and tears the session down once it exits.
func (s *Session) waitLoop() {
	_ = s.cmd.Wait()

	s.mu.Lock()
	s.closed = true
	_ = s.ptmx.Close()
	clients := make([]*Client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	for _, c := range clients {
		c.close()
	}

	sessionsMu.Lock()
	delete(sessions, s.Name)
	sessionsMu.Unlock()
}

func (s *Session) appendBuf(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, data...)
	if len(s.buf) > replayBufferCap {
		s.buf = append([]byte(nil), s.buf[len(s.buf)-replayBufferCap:]...)
	}
}

// addClient registers a client and primes it with the replay buffer.
// Returns false if the session has already ended.
func (s *Session) addClient(c *Client) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.clients[c] = true
	if len(s.buf) > 0 {
		replay := make([]byte, len(s.buf))
		copy(replay, s.buf)
		select {
		case c.send <- replay:
		default:
		}
	}
	return true
}

func (s *Session) removeClient(c *Client) {
	s.mu.Lock()
	delete(s.clients, c)
	s.mu.Unlock()
}

func (s *Session) broadcast(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		select {
		case c.send <- data:
		default:
			// Client too slow — drop the chunk for that client.
		}
	}
}

// Write forwards raw bytes from a client straight into the PTY.
func (s *Session) Write(data []byte) error {
	s.mu.Lock()
	closed := s.closed
	ptmx := s.ptmx
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("session %q closed", s.Name)
	}
	_, err := ptmx.Write(data)
	return err
}

// Resize applies a new terminal size to the PTY.
func (s *Session) Resize(rows, cols uint16) {
	if rows == 0 || cols == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.rows, s.cols = rows, cols
	_ = pty.Setsize(s.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

// Close kills the harness process; waitLoop handles the rest of teardown.
func (s *Session) Close() {
	s.mu.Lock()
	closed := s.closed
	cmd := s.cmd
	s.mu.Unlock()
	if closed || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
