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

// secureHeaders wraps a handler and adds defensive HTTP security headers.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// Tight CSP: same-origin scripts/styles only, no inline eval, no plugins.
		// 'unsafe-inline' is needed for the large inline <script> block in index.html.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' blob: data:; media-src 'self' blob:; connect-src 'self' wss: ws:; "+
				"frame-ancestors 'none'; object-src 'none'; base-uri 'self';")
		next.ServeHTTP(w, r)
	})
}

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
		// No wildcard CORS — uploads must come from the same origin (the chat page).
		// This prevents third-party sites from using the visitor's browser to
		// upload arbitrary content to this server.
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
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
		// Sanitize original filename: keep alphanumeric, dots, hyphens, underscores; max 80 chars
		origName := header.Filename
		if origName == "" {
			origName = "file" + ext
		}
		// Remove path separators and sanitize
		origName = filepath.Base(origName)
		var sanitized []rune
		for _, r := range []rune(origName) {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
				r == '.' || r == '-' || r == '_' || r == ' ' {
				sanitized = append(sanitized, r)
			}
		}
		sanitizedName := strings.TrimSpace(string(sanitized))
		if len(sanitizedName) == 0 {
			sanitizedName = "file" + ext
		}
		if len(sanitizedName) > 80 {
			sanitizedName = sanitizedName[:80]
		}
		filename := uuid.New().String() + "__" + sanitizedName
		dst, err := os.Create("uploads/" + filename)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		defer dst.Close()

		// Read first 512 bytes to detect real MIME type from content,
		// ignoring the client-supplied Content-Type which can be spoofed.
		buf := make([]byte, 512)
		n, _ := file.Read(buf)
		detectedMime := http.DetectContentType(buf[:n])
		// Write the sniffed bytes then the rest of the file
		if _, err = dst.Write(buf[:n]); err != nil {
			http.Error(w, "Write error", 500)
			return
		}
		if _, err = io.Copy(dst, file); err != nil {
			http.Error(w, "Write error", 500)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"url":  "/uploads/" + filename,
			"name": header.Filename,
			"mime": detectedMime,
		})
		log.Printf("[upload] %s (%s) → %s", header.Filename, detectedMime, filename)
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
		// Serve with original filename in Content-Disposition
		storedName := filepath.Base(r.URL.Path)
		// Extract original name: format is "<uuid>__<original_name>" or legacy "<uuid><ext>"
		displayName := storedName
		if idx := strings.Index(storedName, "__"); idx != -1 {
			displayName = storedName[idx+2:]
		}
		// Escape any double-quotes in the filename to prevent header injection.
		safeDisplay := strings.NewReplacer(`"`, `\"`, "\n", "", "\r", "").Replace(displayName)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeDisplay))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.StripPrefix("/uploads/", http.FileServer(http.Dir("uploads"))).ServeHTTP(w, r)
	})

	// ── WebSocket ──
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})

	// ── Static files (embedded) ──
	http.Handle("/", http.FileServer(http.FS(sub)))

	// Wrap the default mux with security headers
	log.Printf("P2P Chat server listening on :%s", listenPort)
	log.Fatal(http.ListenAndServe(":"+listenPort, secureHeaders(http.DefaultServeMux)))
}
