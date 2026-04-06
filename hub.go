package main

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

const (
	pollFast = 200 * time.Millisecond
	pollSlow = 2 * time.Second
)

// Hub manages all WebSocket clients connected to a specific session.
type Hub struct {
	session *Session
	clients map[*Client]bool
	mu      sync.RWMutex
	stopCh  chan struct{}
	inputCh chan string // signals that new input was sent
}

var (
	hubs   = make(map[string]*Hub)
	hubsMu sync.Mutex
)

// getOrCreateHub returns the hub for a session, creating it if needed.
func getOrCreateHub(session *Session) *Hub {
	hubsMu.Lock()
	defer hubsMu.Unlock()

	if h, ok := hubs[session.Name]; ok {
		return h
	}

	h := &Hub{
		session: session,
		clients: make(map[*Client]bool),
		stopCh:  make(chan struct{}),
		inputCh: make(chan string, 16),
	}
	hubs[session.Name] = h
	go h.pollLoop()
	return h
}

func removeHub(name string) {
	hubsMu.Lock()
	defer hubsMu.Unlock()
	if h, ok := hubs[name]; ok {
		close(h.stopCh)
		delete(hubs, name)
	}
}

func (h *Hub) addClient(c *Client) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()

	// Send initial snapshot
	snap, err := h.session.Capture()
	if err != nil {
		log.Printf("capture error on connect: %v", err)
		return
	}
	h.session.mu.Lock()
	h.session.lastSnap = snap
	h.session.mu.Unlock()

	msg := OutMessage{
		Type: "snapshot",
		Text: snap,
	}
	data, _ := json.Marshal(msg)
	c.send <- data
}

func (h *Hub) removeClient(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	empty := len(h.clients) == 0
	h.mu.Unlock()

	if empty {
		removeHub(h.session.Name)
	}
}

func (h *Hub) broadcast(data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- data:
		default:
			// Client too slow, skip
		}
	}
}

func (h *Hub) notifyInput() {
	select {
	case h.inputCh <- "":
	default:
	}
}

func (h *Hub) pollLoop() {
	ticker := time.NewTicker(pollFast)
	defer ticker.Stop()

	currentInterval := pollFast

	for {
		select {
		case <-h.stopCh:
			return
		case <-h.inputCh:
			// New input — switch to fast polling
			if currentInterval != pollFast {
				currentInterval = pollFast
				ticker.Reset(currentInterval)
			}
		case <-ticker.C:
			snap, err := h.session.Capture()
			if err != nil {
				log.Printf("capture error: %v", err)
				continue
			}

			h.session.mu.Lock()
			oldSnap := h.session.lastSnap
			h.session.lastSnap = snap
			h.session.mu.Unlock()

			if !hasChanged(oldSnap, snap) {
				continue
			}

			// Send full snapshot as replacement
			msg := OutMessage{
				Type: "update",
				Text: snap,
				Ready: isReady(snap),
			}
			data, _ := json.Marshal(msg)
			h.broadcast(data)

			if msg.Ready {
				// Slow down polling
				if currentInterval != pollSlow {
					currentInterval = pollSlow
					ticker.Reset(currentInterval)
				}
			}
		}
	}
}
