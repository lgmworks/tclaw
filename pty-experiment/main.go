package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type resizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// Session holds a PTY and broadcasts output to all connected clients.
type Session struct {
	mu      sync.Mutex
	ptmx    *os.File
	cmd     *exec.Cmd
	clients map[*websocket.Conn]bool
	buf     []byte // circular buffer for replay on reconnect
}

var (
	sessionMu sync.Mutex
	session   *Session // single session for now
)

func getOrCreateSession() (*Session, error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()

	if session != nil {
		return session, nil
	}

	cmd := exec.Command("bash")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

	s := &Session{
		ptmx:    ptmx,
		cmd:     cmd,
		clients: make(map[*websocket.Conn]bool),
		buf:     make([]byte, 0, 100_000),
	}

	// PTY → broadcast to all clients
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := ptmx.Read(tmp)
			if err != nil {
				log.Println("pty read:", err)
				return
			}
			data := tmp[:n]

			s.mu.Lock()
			// Append to replay buffer (cap at 100KB)
			if len(s.buf)+n > 100_000 {
				// Keep last 50KB + new data
				keep := 50_000
				if len(s.buf) > keep {
					s.buf = append(s.buf[:0], s.buf[len(s.buf)-keep:]...)
				}
			}
			s.buf = append(s.buf, data...)

			for conn := range s.clients {
				if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
					conn.Close()
					delete(s.clients, conn)
				}
			}
			s.mu.Unlock()
		}
	}()

	// Clean up when process dies
	go func() {
		s.cmd.Wait()
		log.Println("session process ended")
		sessionMu.Lock()
		session = nil
		sessionMu.Unlock()

		s.mu.Lock()
		for conn := range s.clients {
			conn.Close()
		}
		s.mu.Unlock()
	}()

	session = s
	log.Println("session created")
	return s, nil
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade:", err)
		return
	}

	s, err := getOrCreateSession()
	if err != nil {
		log.Println("session:", err)
		conn.Close()
		return
	}

	// Send replay buffer so client sees current state
	s.mu.Lock()
	if len(s.buf) > 0 {
		conn.WriteMessage(websocket.BinaryMessage, s.buf)
	}
	s.clients[conn] = true
	s.mu.Unlock()

	// WebSocket → PTY
	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}

		if msgType == websocket.TextMessage && len(msg) > 0 && msg[0] == '{' {
			var rm resizeMsg
			if json.Unmarshal(msg, &rm) == nil && rm.Type == "resize" {
				pty.Setsize(s.ptmx, &pty.Winsize{Rows: rm.Rows, Cols: rm.Cols})
				continue
			}
		}

		if _, err := s.ptmx.Write(msg); err != nil {
			break
		}
	}

	// Remove client on disconnect
	s.mu.Lock()
	delete(s.clients, conn)
	s.mu.Unlock()
	conn.Close()
	log.Println("client disconnected")
}

func main() {
	http.HandleFunc("/ws", handleWS)
	http.Handle("/", http.FileServer(http.Dir("web")))

	addr := ":9090"
	log.Printf("pty-experiment running on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
