package main

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

const (
	pollFast = 200 * time.Millisecond
	pollSlow = 2 * time.Second
)

// Hub manages all WebSocket clients connected to a specific session and runs
// the capture-pane polling loop that feeds them.
type Hub struct {
	session *Session
	clients map[*Client]bool
	mu      sync.RWMutex
	stopCh  chan struct{}
	inputCh chan string // signals that new input was sent
	pauseCh chan bool   // true = pause polling, false = resume
	paused  atomic.Bool // mirrors the loop's pause state, readable elsewhere
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
		pauseCh: make(chan bool, 4),
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

	// Send initial snapshot.
	snap, err := h.session.Capture()
	if err != nil {
		log.Printf("capture error on connect: %v", err)
		return
	}
	h.session.mu.Lock()
	h.session.lastSnap = snap
	h.session.mu.Unlock()

	data, _ := json.Marshal(OutMessage{Type: "snapshot", Text: snap})
	c.send <- data

	// Let a fresh client know if the sync is currently paused.
	if h.paused.Load() {
		syncData, _ := json.Marshal(OutMessage{Type: "sync", Paused: true})
		c.send <- syncData
	}
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
			// Client too slow, skip.
		}
	}
}

func (h *Hub) notifyInput() {
	select {
	case h.inputCh <- "":
	default:
	}
}

// setPaused asks the poll loop to pause (true) or resume (false) syncing.
func (h *Hub) setPaused(p bool) {
	select {
	case h.pauseCh <- p:
	default:
	}
}

// sendSnapshot captures the pane and broadcasts it as a full snapshot,
// regardless of whether it changed. Used when resuming a paused sync.
func (h *Hub) sendSnapshot() {
	snap, err := h.session.Capture()
	if err != nil {
		log.Printf("capture error: %v", err)
		return
	}
	h.session.mu.Lock()
	h.session.lastSnap = snap
	h.session.mu.Unlock()

	data, _ := json.Marshal(OutMessage{Type: "snapshot", Text: snap})
	h.broadcast(data)
}

// captureAndBroadcast polls the pane and, if it changed, broadcasts an update.
// Returns whether the harness looks ready for input.
func (h *Hub) captureAndBroadcast() (ready bool) {
	snap, err := h.session.Capture()
	if err != nil {
		log.Printf("capture error: %v", err)
		return false
	}

	h.session.mu.Lock()
	oldSnap := h.session.lastSnap
	h.session.lastSnap = snap
	h.session.mu.Unlock()

	if !hasChanged(oldSnap, snap) {
		return false
	}

	ready = isReady(snap)
	data, _ := json.Marshal(OutMessage{Type: "update", Text: snap, Ready: ready})
	h.broadcast(data)
	return ready
}

func (h *Hub) pollLoop() {
	ticker := time.NewTicker(pollFast)
	defer ticker.Stop()

	currentInterval := pollFast

	for {
		select {
		case <-h.stopCh:
			return

		case p := <-h.pauseCh:
			h.paused.Store(p)
			data, _ := json.Marshal(OutMessage{Type: "sync", Paused: p})
			h.broadcast(data)
			if !p {
				// Resumed — repaint immediately and poll fast again.
				h.sendSnapshot()
				currentInterval = pollFast
				ticker.Reset(currentInterval)
			}

		case <-h.inputCh:
			if h.paused.Load() {
				continue
			}
			// New input — switch to fast polling.
			if currentInterval != pollFast {
				currentInterval = pollFast
				ticker.Reset(currentInterval)
			}

		case <-ticker.C:
			if h.paused.Load() {
				continue
			}
			if h.captureAndBroadcast() {
				// Harness idle — slow down polling.
				if currentInterval != pollSlow {
					currentInterval = pollSlow
					ticker.Reset(currentInterval)
				}
			}
		}
	}
}
