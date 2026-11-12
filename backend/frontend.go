package main

import (
	"net/http"
	"os"
	"path/filepath"
)

func registerFrontends(mux *http.ServeMux) {
	dir := "../frontend/dist"
	if _, err := os.Stat(dir); err != nil {
		dir = "/frontend"
	}
	mux.Handle("GET /assets/", http.FileServer(http.Dir(dir)))
	portal := func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, filepath.Join(dir, "index.html")) }
	mux.HandleFunc("GET /admin/{$}", portal)
	mux.HandleFunc("GET /warden/{$}", portal)
	mux.HandleFunc("GET /laundromat/{$}", portal)
	mux.HandleFunc("GET /handler/{$}", portal)
	mux.HandleFunc("GET /student/{$}", portal)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/admin/", http.StatusSeeOther) })
}
