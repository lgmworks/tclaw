package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

type CreateSessionRequest struct {
	Dir string `json:"dir"`
}

type SessionInfo struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

func handleListSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	list := listSessions()
	infos := make([]SessionInfo, len(list))
	for i, s := range list {
		infos[i] = SessionInfo{Name: s.Name, Dir: s.Dir}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(infos)
}

func handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if req.Dir == "" {
		http.Error(w, "dir is required", http.StatusBadRequest)
		return
	}

	s, err := createSession(req.Dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(SessionInfo{Name: s.Name, Dir: s.Dir})
}

func handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract session name from path: /api/sessions/{name}
	name := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if name == "" {
		http.Error(w, "session name required", http.StatusBadRequest)
		return
	}

	removeHub(name)

	if err := deleteSession(name); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract session name from path: /ws/{name}
	name := strings.TrimPrefix(r.URL.Path, "/ws/")
	if name == "" {
		http.Error(w, "session name required", http.StatusBadRequest)
		return
	}

	session := getSession(name)
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	hub := getOrCreateHub(session)
	client := &Client{
		hub:  hub,
		conn: conn,
		send: make(chan []byte, 64),
	}

	hub.addClient(client)
	go client.writePump()
	go client.readPump()
}
