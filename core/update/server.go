package update

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// NewHandler serves a release directory written by Publish:
//
//	GET /healthz
//	GET /v1/{platform}/latest              the signed manifest
//	GET /v1/{platform}/files/{name}        a package; Range supported
//
// prefix, if set, is stripped from request paths (for a reverse proxy that
// keeps its mount point). The handler is read-only.
func NewHandler(dir, prefix string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /v1/{platform}/latest", func(w http.ResponseWriter, r *http.Request) {
		platform := r.PathValue("platform")
		if !ValidPlatform(platform) {
			http.NotFound(w, r)
			return
		}
		raw, err := os.ReadFile(filepath.Join(dir, platform, "latest.json"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("GET /v1/{platform}/files/{name}", func(w http.ResponseWriter, r *http.Request) {
		platform, name := r.PathValue("platform"), r.PathValue("name")
		if !ValidPlatform(platform) || !ValidFileName(name) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(filepath.Join(dir, platform, "files", name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		// The file name embeds the version, so its bytes never change.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("Content-Type", "application/octet-stream")
		if m := currentManifest(dir, platform); m != nil && m.File == name {
			w.Header().Set("X-Content-SHA256", m.SHA256)
		}
		http.ServeContent(w, r, name, st.ModTime(), f)
	})
	var h http.Handler = mux
	if prefix = strings.TrimRight(prefix, "/"); prefix != "" {
		h = http.StripPrefix(prefix, mux)
	}
	return h
}

func currentManifest(dir, platform string) *Manifest {
	raw, err := os.ReadFile(filepath.Join(dir, platform, "latest.json"))
	if err != nil {
		return nil
	}
	var m Manifest
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return &m
}
