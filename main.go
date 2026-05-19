package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

func listenAddr() string {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		return ":8080"
	}
	if strings.HasPrefix(port, ":") {
		return port
	}
	return ":" + port
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("warning: could not load .env: %v", err)
	}
	if err := loadConfig(configPath); err != nil {
		log.Printf("warning: could not load config: %v", err)
	}

	mux := http.NewServeMux()

	// REST API
	mux.HandleFunc("/api/sessions", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleListSessions(w, r)
		case http.MethodPost:
			handleCreateSession(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	mux.HandleFunc("/api/sessions/", requireAuth(handleSessionAction))
	mux.HandleFunc("/api/config", requireAuth(handleConfig))
	mux.HandleFunc("/api/dirs", requireAuth(handleListDirs))

	// WebSocket
	mux.HandleFunc("/ws/", requireAuth(handleWebSocket))

	// Frontend — SPA fallback: serve real files from web/, otherwise serve
	// index.html so client-side routes (/agente, /inventario, …) work.
	webDir := http.Dir("web")
	fileServer := http.FileServer(webDir)
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if f, err := webDir.Open(r.URL.Path); err == nil {
			info, statErr := f.Stat()
			f.Close()
			if statErr == nil && !info.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		http.ServeFile(w, r, "web/index.html")
	}))

	addr := listenAddr()
	log.Printf("tclaw listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, corsMiddleware(mux)); err != nil {
		log.Fatal(err)
	}
}
