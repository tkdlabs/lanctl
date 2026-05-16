package frontend

import "net/http"

// Attach registers routes for the frontend static files on mux.
func Attach(mux *http.ServeMux, indexPath string, logPath string) {
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, indexPath)
	})
	mux.HandleFunc("GET /log", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, logPath)
	})
}
