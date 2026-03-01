package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// ──────────────────────────────────────────────────────────────────────────────
// Types
// ──────────────────────────────────────────────────────────────────────────────

type PeerInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
	mu   sync.Mutex
	id   string
	name string
	room string
}

type Hub struct {
	mu         sync.RWMutex
	rooms      map[string]map[string]*Client // room → peerId → Client
	roomFiles  map[string][]string           // room → []local file paths to delete on empty
	register   chan *Client
	unregister chan *Client
	signal     chan envelope
}

type envelope struct {
	sender *Client
	raw    json.RawMessage
}

// ── Wire shapes ──

type inMsg struct {
	Type string          `json:"type"`
	To   string          `json:"to,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

type outMsg struct {
	Type  string          `json:"type"`
	From  string          `json:"from,omitempty"`
	Peer  *PeerInfo       `json:"peer,omitempty"`
	Peers []PeerInfo      `json:"peers,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// ──────────────────────────────────────────────────────────────────────────────
// Hub lifecycle
// ──────────────────────────────────────────────────────────────────────────────

func newHub() *Hub {
	return &Hub{
		rooms:      make(map[string]map[string]*Client),
		roomFiles:  make(map[string][]string),
		register:   make(chan *Client, 64),
		unregister: make(chan *Client, 64),
		signal:     make(chan envelope, 512),
	}
}

func (h *Hub) run() {
	for {
		select {

		case c := <-h.register:
			h.mu.Lock()
			if h.rooms[c.room] == nil {
				h.rooms[c.room] = make(map[string]*Client)
			}
			existing := h.peersInRoom(c.room)
			h.rooms[c.room][c.id] = c
			h.mu.Unlock()

			c.sendJSON(outMsg{Type: "welcome", From: c.id, Peers: existing})
			h.broadcastToRoom(c.room, c.id, outMsg{
				Type: "peer-joined",
				Peer: &PeerInfo{ID: c.id, Name: c.name},
			})
			log.Printf("[join] %s (%s) → room %s  (%d peers)", c.name, c.id, c.room, len(existing)+1)

		case c := <-h.unregister:
			var toDelete []string

			h.mu.Lock()
			if _, ok := h.rooms[c.room][c.id]; ok {
				delete(h.rooms[c.room], c.id)
				if len(h.rooms[c.room]) == 0 {
					delete(h.rooms, c.room)
					// Collect files to delete — room is now empty
					toDelete = h.roomFiles[c.room]
					delete(h.roomFiles, c.room)
				}
			}
			h.mu.Unlock()
			c.conn.Close()

			h.broadcastToRoom(c.room, c.id, outMsg{Type: "peer-left", From: c.id})
			log.Printf("[left] %s (%s) ← room %s", c.name, c.id, c.room)

			// Delete room's uploaded files in background
			if len(toDelete) > 0 {
				go func(files []string) {
					for _, f := range files {
						if err := os.Remove(f); err == nil {
							log.Printf("[room-cleanup] deleted %s", f)
						}
					}
				}(toDelete)
			}

		case env := <-h.signal:
			var msg inMsg
			if err := json.Unmarshal(env.raw, &msg); err != nil {
				continue
			}

			switch msg.Type {

			case "signal":
				if msg.To != "" {
					h.mu.RLock()
					target := h.rooms[env.sender.room][msg.To]
					h.mu.RUnlock()
					if target != nil {
						target.sendJSON(outMsg{
							Type: "signal",
							From: env.sender.id,
							Data: msg.Data,
						})
					}
				}

			case "chat", "file":
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: msg.Type,
					From: env.sender.id,
					Peer: &PeerInfo{ID: env.sender.id, Name: env.sender.name},
					Data: msg.Data,
				})
				// Track file path for room cleanup
				if msg.Type == "file" {
					var fd struct {
						URL string `json:"url"`
					}
					if json.Unmarshal(msg.Data, &fd) == nil && fd.URL != "" {
						fname := path.Base(fd.URL) // strips any base-path prefix
						if fname != "" && fname != "." && fname != "/" {
							h.mu.Lock()
							h.roomFiles[env.sender.room] = append(
								h.roomFiles[env.sender.room],
								"uploads/"+fname,
							)
							h.mu.Unlock()
						}
					}
				}

			case "typing":
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: "typing",
					From: env.sender.id,
					Peer: &PeerInfo{ID: env.sender.id, Name: env.sender.name},
					Data: msg.Data,
				})

			case "seen":
				if msg.To != "" {
					h.mu.RLock()
					target := h.rooms[env.sender.room][msg.To]
					h.mu.RUnlock()
					if target != nil {
						target.sendJSON(outMsg{
							Type: "seen",
							From: env.sender.id,
							Peer: &PeerInfo{ID: env.sender.id, Name: env.sender.name},
							Data: msg.Data,
						})
					}
				}

			case "voice-status":
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: "voice-status",
					From: env.sender.id,
					Peer: &PeerInfo{ID: env.sender.id, Name: env.sender.name},
					Data: msg.Data,
				})
			}
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func (h *Hub) peersInRoom(room string) []PeerInfo {
	var peers []PeerInfo
	for _, c := range h.rooms[room] {
		peers = append(peers, PeerInfo{ID: c.id, Name: c.name})
	}
	return peers
}

func (h *Hub) broadcastToRoom(room, excludeID string, msg outMsg) {
	b, _ := json.Marshal(msg)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for id, c := range h.rooms[room] {
		if id == excludeID {
			continue
		}
		select {
		case c.send <- b:
		default:
		}
	}
}

func (c *Client) sendJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.send <- b:
	default:
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// WebSocket upgrade + pumps
// ──────────────────────────────────────────────────────────────────────────────

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	name := r.URL.Query().Get("name")
	if room == "" {
		room = "general"
	}
	if name == "" {
		name = "Anonymous"
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("ws upgrade:", err)
		return
	}

	c := &Client{
		hub:  hub,
		conn: conn,
		send: make(chan []byte, 256),
		id:   uuid.New().String(),
		name: name,
		room: room,
	}
	hub.register <- c
	go c.writePump()
	go c.readPump()
}

func (c *Client) readPump() {
	defer func() { c.hub.unregister <- c }()
	c.conn.SetReadLimit(128 * 1024)
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		c.hub.signal <- envelope{sender: c, raw: raw}
	}
}

func (c *Client) writePump() {
	for msg := range c.send {
		c.mu.Lock()
		err := c.conn.WriteMessage(websocket.TextMessage, msg)
		c.mu.Unlock()
		if err != nil {
			break
		}
	}
	c.conn.Close()
}
