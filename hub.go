package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// ──────────────────────────────────────────────────────────────────────────────
// Types
// ──────────────────────────────────────────────────────────────────────────────

type PeerInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	IP   string `json:"ip,omitempty"`
}

type Client struct {
	hub      *Hub
	conn     *websocket.Conn
	send     chan []byte
	mu       sync.Mutex
	id       string
	name     string
	room     string
	password string
	ip       string
}

type Hub struct {
	mu            sync.RWMutex
	rooms         map[string]map[string]*Client // room → peerId → Client
	roomFiles     map[string][]string           // room → []local file paths
	roomPasswords map[string]string             // room → password ("" = open)
	register      chan *Client
	unregister    chan *Client
	signal        chan envelope
}

type envelope struct {
	sender *Client
	raw    json.RawMessage
}

// Wire shapes

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
		rooms:         make(map[string]map[string]*Client),
		roomFiles:     make(map[string][]string),
		roomPasswords: make(map[string]string),
		register:      make(chan *Client, 64),
		unregister:    make(chan *Client, 64),
		signal:        make(chan envelope, 512),
	}
}

func (h *Hub) run() {
	for {
		select {

		case c := <-h.register:
			h.mu.Lock()

			members := h.rooms[c.room]
			if len(members) > 0 {
				// Room exists — validate password
				if pw := h.roomPasswords[c.room]; pw != "" && c.password != pw {
					h.mu.Unlock()
					h.rejectClient(c, "wrong-password", "Wrong room password")
					continue
				}
			} else {
				// New room — set password if provided
				if c.password != "" {
					h.roomPasswords[c.room] = c.password
				}
			}

			// Admit
			if h.rooms[c.room] == nil {
				h.rooms[c.room] = make(map[string]*Client)
			}
			existing := h.peersInRoom(c.room)
			h.rooms[c.room][c.id] = c
			h.mu.Unlock()

			c.sendJSON(outMsg{Type: "welcome", From: c.id, Peers: existing})
			h.broadcastToRoom(c.room, c.id, outMsg{
				Type: "peer-joined",
				Peer: &PeerInfo{ID: c.id, Name: c.name, IP: c.ip},
			})
			log.Printf("[join] %s (%s) → room %s  (%d peers)", c.name, c.id, c.room, len(existing)+1)

		case c := <-h.unregister:
			var toDelete []string

			h.mu.Lock()
			if _, ok := h.rooms[c.room][c.id]; ok {
				delete(h.rooms[c.room], c.id)
				if len(h.rooms[c.room]) == 0 {
					// Room is now empty — destroy everything
					delete(h.rooms, c.room)
					delete(h.roomPasswords, c.room) // destroy password
					toDelete = h.roomFiles[c.room]
					delete(h.roomFiles, c.room)
				}
			}
			h.mu.Unlock()
			c.conn.Close()

			h.broadcastToRoom(c.room, c.id, outMsg{Type: "peer-left", From: c.id})
			log.Printf("[left] %s (%s) ← room %s", c.name, c.id, c.room)

			// Delete uploaded files in background
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
					Peer: &PeerInfo{ID: env.sender.id, Name: env.sender.name, IP: env.sender.ip},
					Data: msg.Data,
				})
				if msg.Type == "file" {
					var fd struct {
						URL string `json:"url"`
					}
					if json.Unmarshal(msg.Data, &fd) == nil && fd.URL != "" {
						fname := path.Base(fd.URL)
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
					Peer: &PeerInfo{ID: env.sender.id, Name: env.sender.name, IP: env.sender.ip},
					Data: msg.Data,
				})

			case "uploading":
				// Broadcast file-upload progress notification to all peers in room.
				// Sender sets isUploading:true on start, false on finish/fail.
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: "uploading",
					From: env.sender.id,
					Peer: &PeerInfo{ID: env.sender.id, Name: env.sender.name, IP: env.sender.ip},
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
							Peer: &PeerInfo{ID: env.sender.id, Name: env.sender.name, IP: env.sender.ip},
							Data: msg.Data,
						})
					}
				}

			case "voice-status":
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: "voice-status",
					From: env.sender.id,
					Peer: &PeerInfo{ID: env.sender.id, Name: env.sender.name, IP: env.sender.ip},
					Data: msg.Data,
				})

			case "call-invite", "call-cancel":
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: msg.Type,
					From: env.sender.id,
					Peer: &PeerInfo{ID: env.sender.id, Name: env.sender.name, IP: env.sender.ip},
					Data: msg.Data,
				})

			case "call-response":
				if msg.To != "" {
					h.mu.RLock()
					target := h.rooms[env.sender.room][msg.To]
					h.mu.RUnlock()
					if target != nil {
						target.sendJSON(outMsg{
							Type: "call-response",
							From: env.sender.id,
							Peer: &PeerInfo{ID: env.sender.id, Name: env.sender.name, IP: env.sender.ip},
							Data: msg.Data,
						})
					}
				}

			case "user-status":
				// Broadcast away/online status to all peers
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: "user-status",
					From: env.sender.id,
					Data: msg.Data,
				})

			case "msg-read":
				// Broadcast read receipt to everyone else in room (incl. original sender)
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: "msg-read",
					From: env.sender.id,
					Peer: &PeerInfo{ID: env.sender.id, Name: env.sender.name, IP: env.sender.ip},
					Data: msg.Data,
				})

			case "file-deleted":
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: "file-deleted",
					From: env.sender.id,
					Data: msg.Data,
				})

			case "edit-msg":
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: "edit-msg",
					From: env.sender.id,
					Data: msg.Data,
				})

			case "delete-msg":
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: "delete-msg",
					From: env.sender.id,
					Data: msg.Data,
				})

			case "admin-info":
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: "admin-info",
					From: env.sender.id,
					Data: msg.Data,
				})
			}
		}
	}
}

// rejectClient sends an error then gracefully tears down the client.
func (h *Hub) rejectClient(c *Client, code, message string) {
	data, _ := json.Marshal(map[string]string{"code": code, "message": message})
	errMsg, _ := json.Marshal(outMsg{Type: "error", Data: json.RawMessage(data)})

	// Write directly to conn (writePump is blocked on empty send channel)
	c.mu.Lock()
	_ = c.conn.WriteMessage(websocket.TextMessage, errMsg)
	c.mu.Unlock()

	// After a brief flush window, close send channel and connection
	go func() {
		time.Sleep(300 * time.Millisecond)
		defer func() { recover() }() // guard against double-close
		close(c.send)                // exits writePump's range loop
	}()
	log.Printf("[reject] %s → room %s (%s)", c.name, c.room, code)
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func (h *Hub) peersInRoom(room string) []PeerInfo {
	var peers []PeerInfo
	for _, c := range h.rooms[room] {
		peers = append(peers, PeerInfo{ID: c.id, Name: c.name, IP: c.ip})
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
		func() {
			defer func() { recover() }() // guard send on closed channel
			select {
			case c.send <- b:
			default:
			}
		}()
	}
}

func (c *Client) sendJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	defer func() { recover() }() // guard send on closed channel
	select {
	case c.send <- b:
	default:
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// WebSocket upgrade + pumps
// ──────────────────────────────────────────────────────────────────────────────

const (
	// How often the server pings each client.
	pingInterval = 25 * time.Second
	// How long the server waits for a pong before killing the connection.
	// Ghost clients (abruptly dropped connections) are detected within
	// pingInterval + pongWait = ~35 seconds.
	pongWait = 10 * time.Second
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	name := r.URL.Query().Get("name")
	pass := r.URL.Query().Get("pass")
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

	// Get real IP from headers or RemoteAddr
	ip := r.Header.Get("X-Real-Ip")
	if ip == "" {
		ip = r.Header.Get("X-Forwarded-For")
	}
	if ip == "" {
		ip = r.RemoteAddr
		if h, _, err := net.SplitHostPort(ip); err == nil {
			ip = h
		}
	}

	c := &Client{
		hub:      hub,
		conn:     conn,
		send:     make(chan []byte, 256),
		id:       uuid.New().String(),
		name:     name,
		room:     room,
		password: pass,
		ip:       ip,
	}
	hub.register <- c
	go c.writePump()
	go c.readPump()
}

func (c *Client) readPump() {
	defer func() { c.hub.unregister <- c }()
	c.conn.SetReadLimit(128 * 1024)

	// Set initial read deadline; reset on every pong received.
	// If no pong arrives within pongWait after a ping, ReadMessage returns an
	// error and the ghost client is immediately unregistered.
	c.conn.SetReadDeadline(time.Now().Add(pingInterval + pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pingInterval + pongWait))
		return nil
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		c.hub.signal <- envelope{sender: c, raw: raw}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				// send channel was closed — write a close frame and exit
				c.mu.Lock()
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				c.mu.Unlock()
				return
			}
			c.mu.Lock()
			err := c.conn.WriteMessage(websocket.TextMessage, msg)
			c.mu.Unlock()
			if err != nil {
				return
			}

		case <-ticker.C:
			// Send a ping frame; if the client is gone it won't pong back and
			// readPump's deadline will fire, unregistering the ghost.
			c.mu.Lock()
			err := c.conn.WriteMessage(websocket.PingMessage, nil)
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}
