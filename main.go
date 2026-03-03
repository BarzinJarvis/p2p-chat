package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

//go:embed all:static
var staticFiles embed.FS

const maxUpload = 200 << 20 // 200 MB

func main() {
	port := flag.String("p", "", "Port to listen on (default: 8080, overrides PORT env var)")
	flag.Parse()

	listenPort := *port
	if listenPort == "" {
		listenPort = os.Getenv("PORT")
	}
	if listenPort == "" {
		listenPort = "8080"
	}

	os.MkdirAll("uploads", 0755)

	hub := newHub()
	go hub.run()

	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}

	// ── File upload ──
	http.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
		if err := r.ParseMultipartForm(maxUpload); err != nil {
			http.Error(w, "File too large (max 200 MB)", 400)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "Bad request", 400)
			return
		}
		defer file.Close()

		ext := filepath.Ext(header.Filename)
		filename := uuid.New().String() + ext
		dst, err := os.Create("uploads/" + filename)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		defer dst.Close()
		if _, err = io.Copy(dst, file); err != nil {
			http.Error(w, "Write error", 500)
			return
		}

		mime := header.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"url":  "/uploads/" + filename,
			"name": header.Filename,
			"mime": mime,
		})
		log.Printf("[upload] %s (%s) → %s", header.Filename, mime, filename)
	})

	// ── Serve uploaded files from disk (also handle DELETE) ──
	http.HandleFunc("/uploads/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			filename := strings.TrimPrefix(r.URL.Path, "/uploads/")
			// Sanitize: no path traversal
			if strings.Contains(filename, "/") || strings.Contains(filename, "..") {
				http.Error(w, "Invalid", 400)
				return
			}
			os.Remove("uploads/" + filename)
			w.WriteHeader(204)
			log.Printf("[delete] uploads/%s", filename)
			return
		}
		// Force download with original filename so browser doesn't try to preview
		filename := filepath.Base(r.URL.Path)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		http.StripPrefix("/uploads/", http.FileServer(http.Dir("uploads"))).ServeHTTP(w, r)
	})

	// ── WebSocket ──
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})

	// ── Static files (embedded) ──
	http.Handle("/", http.FileServer(http.FS(sub)))

	log.Printf("P2P Chat server listening on :%s", listenPort)
	log.Fatal(http.ListenAndServe(":"+listenPort, nil))
}
