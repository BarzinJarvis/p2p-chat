package main

import (
	"embed"
	"encoding/json"
	"flag"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

//go:embed static/*
var staticFiles embed.FS

const maxUpload = 100 << 20 // 100 MB

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
			http.Error(w, "File too large (max 100 MB)", 400)
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

	// ── Serve uploaded files from disk ──
	http.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir("uploads"))))

	// ── WebSocket ──
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})

	// ── Static files (embedded) ──
	http.Handle("/", http.FileServer(http.FS(sub)))

	log.Printf("P2P Chat server listening on :%s  (pass -p PORT to change)", listenPort)
	log.Fatal(http.ListenAndServe(":"+listenPort, nil))
}
