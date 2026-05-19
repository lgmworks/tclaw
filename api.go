package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type CreateSessionRequest struct {
	Dir string `json:"dir"`
}

type SessionInfo struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

type UpdateConfigRequest struct {
	Harness string `json:"harness"`
}

type UploadResponse struct {
	Path               string `json:"path"`
	FullPath           string `json:"full_path"`
	URL                string `json:"url"`
	Transcript         string `json:"transcript,omitempty"`
	TranscriptionError string `json:"transcription_error,omitempty"`
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

type DirInfo struct {
	Name    string `json:"name"`    // raw directory name (used as dir to create a session)
	Session string `json:"session"` // sanitized session name (used in the URL path)
	Running bool   `json:"running"` // true if a session for this dir is alive
}

// handleListDirs lists the project directories under tclaw's working
// directory, skipping hidden dirs and the configured exclude list.
func handleListDirs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		http.Error(w, "could not read working directory", http.StatusInternalServerError)
		return
	}

	cfg := getConfig()
	exclude := make(map[string]bool, len(cfg.ExcludeDirs))
	for _, d := range cfg.ExcludeDirs {
		exclude[d] = true
	}
	live := liveSessionNames()

	dirs := make([]DirInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || exclude[name] {
			continue
		}
		session := sanitizeName(name)
		dirs = append(dirs, DirInfo{
			Name:    name,
			Session: session,
			Running: live[session],
		})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dirs)
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

	if err := deleteSession(name); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleSessionAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if path == "" {
		http.Error(w, "session name required", http.StatusBadRequest)
		return
	}

	if strings.HasSuffix(path, "/uploads") {
		name := strings.TrimSuffix(path, "/uploads")
		name = strings.TrimSuffix(name, "/")
		handleSessionUpload(w, r, name)
		return
	}

	handleDeleteSession(w, r)
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

	client := &Client{
		session: session,
		conn:    conn,
		send:    make(chan []byte, 256),
	}

	if !session.addClient(client) {
		conn.Close()
		return
	}

	go client.writePump()
	go client.readPump()
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(getConfig())
	case http.MethodPut:
		var req UpdateConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		cfg := getConfig()
		cfg.Harness = req.Harness
		cfg.normalize()

		if err := saveConfig(configPath, cfg); err != nil {
			http.Error(w, "could not save config", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleSessionUpload(w http.ResponseWriter, r *http.Request, sessionName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := getSession(sessionName)
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	if err := r.ParseMultipartForm(25 << 20); err != nil {
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	baseDir := session.Dir
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}

	uploadDir := filepath.Join(baseDir, "uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		http.Error(w, "could not create uploads directory", http.StatusInternalServerError)
		return
	}

	filename := uniqueUploadName(uploadDir, header.Filename)
	absPath, err := filepath.Abs(filepath.Join(uploadDir, filename))
	if err != nil {
		http.Error(w, "could not resolve upload path", http.StatusInternalServerError)
		return
	}
	relPath := filepath.ToSlash(filepath.Join("uploads", filename))
	dst, err := os.Create(absPath)
	if err != nil {
		http.Error(w, "could not create file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "could not save file", http.StatusInternalServerError)
		return
	}

	transcript, transcriptErr := maybeTranscribeUpload(absPath, header.Header.Get("Content-Type"))

	resp := UploadResponse{
		Path:     relPath,
		FullPath: absPath,
		URL:      "",
	}
	if transcriptErr != nil {
		resp.TranscriptionError = transcriptErr.Error()
	}
	if transcript != "" {
		resp.Transcript = transcript
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

func uniqueUploadName(dir, original string) string {
	base := filepath.Base(strings.TrimSpace(original))
	ext := strings.ToLower(filepath.Ext(base))
	name := strings.TrimSuffix(base, filepath.Ext(base))
	name = sanitizeFilename(name)
	if name == "" {
		name = "upload"
	}

	filename := name + ext
	if _, err := os.Stat(filepath.Join(dir, filename)); os.IsNotExist(err) {
		return filename
	}

	for i := 2; ; i++ {
		candidate := sanitizeFilename(name) + "-" + strconv.Itoa(i) + ext
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}

func sanitizeFilename(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	re := regexp.MustCompile(`[^a-z0-9._-]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	return s
}

func isAudioUpload(path, contentType string) bool {
	if strings.HasPrefix(strings.ToLower(contentType), "audio/") {
		return true
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".webm", ".ogg", ".oga", ".mp3", ".m4a", ".wav", ".mp4", ".mpeg", ".mpga":
		return true
	default:
		return false
	}
}
