package main

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
)

func authToken() string {
	return strings.TrimSpace(os.Getenv("AUTH_TOKEN"))
}

func authEnabled() bool {
	return authToken() != ""
}

func requestToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}

	if token := strings.TrimSpace(r.Header.Get("X-Auth-Token")); token != "" {
		return token
	}

	return strings.TrimSpace(r.URL.Query().Get("token"))
}

func isAuthorized(r *http.Request) bool {
	expected := authToken()
	if expected == "" {
		return true
	}

	actual := requestToken(r)
	if actual == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAuthorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

