package update

import (
	"encoding/json"
	"fmt"
	"html/template"
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
//	GET /v1/{platform}/download            the current package at a stable URL
//	GET /                                  a download page for people
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
		serveFile(w, r, dir, r.PathValue("platform"), r.PathValue("name"))
	})
	// Same bytes as files/{name} for the current release, so a bookmarked link never goes stale.
	mux.HandleFunc("GET /v1/{platform}/download", func(w http.ResponseWriter, r *http.Request) {
		platform := r.PathValue("platform")
		m := currentManifest(dir, platform)
		if !ValidPlatform(platform) || m == nil {
			http.NotFound(w, r)
			return
		}
		serveFile(w, r, dir, platform, m.File)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { serveIndex(w, dir) })
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

func serveFile(w http.ResponseWriter, r *http.Request, dir, platform, name string) {
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
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	if m := currentManifest(dir, platform); m != nil && m.File == name {
		w.Header().Set("X-Content-SHA256", m.SHA256)
	}
	http.ServeContent(w, r, name, st.ModTime(), f)
}

var indexTmpl = template.Must(template.New("index").Parse(`<!doctype html>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>ClipBridge downloads</title>
<style>body{font:16px system-ui,sans-serif;max-width:40rem;margin:2rem auto;padding:0 1rem}
code{word-break:break-all;font-size:.8em}li{margin:1rem 0}</style>
<h1>ClipBridge</h1>
{{range .}}<h2>{{.Title}}</h2>
{{if .M}}<ul><li><a href="v1/{{.M.Platform}}/download">{{.M.File}}</a><br>
version {{.M.VersionName}} ({{.M.VersionCode}}) &middot; {{.Size}} &middot; {{.M.PublishedAt}}<br>
SHA-256 <code>{{.M.SHA256}}</code>{{if .M.Notes}}<br>{{.M.Notes}}{{end}}</li></ul>
{{else}}<p>Not published yet.</p>{{end}}{{end}}`))

func serveIndex(w http.ResponseWriter, dir string) {
	type row struct {
		Title string
		M     *Manifest
		Size  string
	}
	var rows []row
	for _, p := range []struct{ id, title string }{{PlatformAndroid, "Android"}, {PlatformWindows, "Windows"}} {
		r := row{Title: p.title, M: currentManifest(dir, p.id)}
		if r.M != nil {
			r.Size = humanSize(r.M.Size)
		}
		rows = append(rows, r)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = indexTmpl.Execute(w, rows)
}

func humanSize(n int64) string {
	if n >= 1<<20 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/(1<<20)), ".0") + " MiB"
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1024), ".0") + " KiB"
}
