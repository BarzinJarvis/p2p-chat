package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
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

type BanEntry struct {
	IP   string `json:"ip"`
	Name string `json:"name"`
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
	roomBans      map[string]map[string]string  // room → IP → banned username
	roomAdminID   map[string]string             // room → admin peer ID
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
		roomBans:      make(map[string]map[string]string),
		roomAdminID:   make(map[string]string),
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
				// Check IP ban
				if bans := h.roomBans[c.room]; bans != nil {
					if _, banned := bans[c.ip]; banned {
						h.mu.Unlock()
						h.rejectClient(c, "banned", "You have been banned from this room")
						continue
					}
				}
			} else {
				// New room — set password if provided
				if c.password != "" {
					h.roomPasswords[c.room] = c.password
				}
			}

			// Evict existing connections from the SAME IP **and** NAME.
			// This handles reconnects (page refresh, app restart) without
			// preventing two different users behind the same NAT/device.
			if h.rooms[c.room] == nil {
				h.rooms[c.room] = make(map[string]*Client)
			}
			var evicted []*Client
			for eid, ec := range h.rooms[c.room] {
				if ec.ip == c.ip && ec.name == c.name {
					evicted = append(evicted, ec)
					delete(h.rooms[c.room], eid)
					// Transfer admin to new connection if needed
					if h.roomAdminID[c.room] == ec.id {
						h.roomAdminID[c.room] = c.id
					}
					log.Printf("[evict] %s (%s) from IP %s replaced in room %s", ec.name, ec.id, ec.ip, c.room)
				}
			}

			// Room capacity check (after eviction, so reconnects don't get rejected)
			if len(h.rooms[c.room]) >= maxPeersPerRoom {
				h.mu.Unlock()
				h.rejectClient(c, "room-full", "Room is full (max 50 peers)")
				continue
			}

			existing := h.peersInRoom(c.room)
			h.rooms[c.room][c.id] = c

			// First client in room becomes admin
			if len(existing) == 0 {
				h.roomAdminID[c.room] = c.id
			}

			h.mu.Unlock()

			// Notify room about evictions and close old connections.
			// Send a TEXT "evicted" message first so the client can show a
			// meaningful UI instead of the generic reconnect overlay.
			// Delay conn.Close() in a goroutine so the TCP send buffer has time
			// to flush the text frame before the connection is torn down.
			for _, old := range evicted {
				h.broadcastToRoom(c.room, old.id, outMsg{Type: "peer-left", From: old.id})
				evictBytes, _ := json.Marshal(outMsg{Type: "evicted"})
				old.mu.Lock()
				_ = old.conn.WriteMessage(websocket.TextMessage, evictBytes)
				old.mu.Unlock()
				go func(c2 *Client) {
					time.Sleep(200 * time.Millisecond)
					c2.conn.Close()
				}(old)
			}

			c.sendJSON(outMsg{Type: "welcome", From: c.id, Peers: existing})
			h.broadcastToRoom(c.room, c.id, outMsg{
				Type: "peer-joined",
				Peer: &PeerInfo{ID: c.id, Name: c.name, IP: c.ip},
			})
			// Broadcast current admin to the whole room (incl. new joiner)
			h.broadcastAdminInfo(c.room)
			log.Printf("[join] %s (%s) ip=%s → room %s  (%d peers)", c.name, c.id, c.ip, c.room, len(existing)+1)

		case c := <-h.unregister:
			var toDelete []string
			wasInRoom := false

			h.mu.Lock()
			if _, ok := h.rooms[c.room][c.id]; ok {
				wasInRoom = true
				delete(h.rooms[c.room], c.id)
				if len(h.rooms[c.room]) == 0 {
					// Room empty — destroy everything including bans and IP names
					delete(h.rooms, c.room)
					delete(h.roomPasswords, c.room)
					delete(h.roomBans, c.room)
					delete(h.roomAdminID, c.room)
					toDelete = h.roomFiles[c.room]
					delete(h.roomFiles, c.room)
				} else if h.roomAdminID[c.room] == c.id {
					// Admin left — promote the first remaining peer
					for _, remaining := range h.rooms[c.room] {
						h.roomAdminID[c.room] = remaining.id
						break
					}
				}
			}
			h.mu.Unlock()
			c.conn.Close()

			if wasInRoom {
				h.broadcastToRoom(c.room, c.id, outMsg{Type: "peer-left", From: c.id})
				h.broadcastAdminInfo(c.room) // notify room if admin changed
				log.Printf("[left] %s (%s) ← room %s", c.name, c.id, c.room)
			}

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
				h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
					Type: "user-status",
					From: env.sender.id,
					Data: msg.Data,
				})

			case "msg-read":
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
				// Only relay if the sender is actually the server-verified admin.
				// This blocks any non-admin from forging fake admin-info broadcasts
				// that would trick other clients into showing wrong admin badges.
				h.mu.RLock()
				senderIsAdmin := h.roomAdminID[env.sender.room] == env.sender.id
				h.mu.RUnlock()
				if senderIsAdmin {
					h.broadcastToRoom(env.sender.room, env.sender.id, outMsg{
						Type: "admin-info",
						From: env.sender.id,
						Data: msg.Data,
					})
				}

			// ── Ban / Unban (admin only) ──────────────────────────────────────

			case "ban":
				h.mu.RLock()
				isAdm := h.roomAdminID[env.sender.room] == env.sender.id
				h.mu.RUnlock()
				if !isAdm {
					continue
				}
				var bd struct {
					PeerID string `json:"peerId"`
				}
				if json.Unmarshal(msg.Data, &bd) != nil || bd.PeerID == "" {
					continue
				}
				// Admin cannot ban themselves
				if bd.PeerID == env.sender.id {
					continue
				}
				h.mu.Lock()
				target := h.rooms[env.sender.room][bd.PeerID]
				if target != nil {
					if h.roomBans[env.sender.room] == nil {
						h.roomBans[env.sender.room] = make(map[string]string)
					}
					h.roomBans[env.sender.room][target.ip] = target.name
					delete(h.rooms[env.sender.room], bd.PeerID)
				}
				h.mu.Unlock()
				if target != nil {
					// Write the "banned" TEXT message synchronously then delay-close
					// in a goroutine. This ensures the TCP send buffer has time to
					// flush the frame before the connection is torn down, which is
					// more reliable than WS close codes (which can be dropped by RST).
					banData, _ := json.Marshal(map[string]string{"reason": "Banned by admin"})
					banBytes, _ := json.Marshal(outMsg{Type: "banned", Data: json.RawMessage(banData)})
					target.mu.Lock()
					_ = target.conn.WriteMessage(websocket.TextMessage, banBytes)
					target.mu.Unlock()
					go func() {
						time.Sleep(200 * time.Millisecond)
						target.conn.Close()
					}()
					h.broadcastToRoom(env.sender.room, bd.PeerID, outMsg{Type: "peer-left", From: bd.PeerID})
					log.Printf("[ban] %s ip=%s banned from room %s", target.name, target.ip, env.sender.room)
				}
				h.sendBanList(env.sender)

			case "unban":
				h.mu.RLock()
				isAdm := h.roomAdminID[env.sender.room] == env.sender.id
				h.mu.RUnlock()
				if !isAdm {
					continue
				}
				var ud struct {
					IP string `json:"ip"`
				}
				if json.Unmarshal(msg.Data, &ud) != nil || ud.IP == "" {
					continue
				}
				h.mu.Lock()
				if h.roomBans[env.sender.room] != nil {
					delete(h.roomBans[env.sender.room], ud.IP)
				}
				h.mu.Unlock()
				h.sendBanList(env.sender)
				log.Printf("[unban] ip=%s unbanned from room %s", ud.IP, env.sender.room)

			case "get-ban-list":
				h.mu.RLock()
				isAdm := h.roomAdminID[env.sender.room] == env.sender.id
				h.mu.RUnlock()
				if isAdm {
					h.sendBanList(env.sender)
				}
			}
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func (h *Hub) rejectClient(c *Client, code, message string) {
	data, _ := json.Marshal(map[string]string{"code": code, "message": message})
	errMsg, _ := json.Marshal(outMsg{Type: "error", Data: json.RawMessage(data)})

	c.mu.Lock()
	_ = c.conn.WriteMessage(websocket.TextMessage, errMsg)
	c.mu.Unlock()

	go func() {
		time.Sleep(300 * time.Millisecond)
		defer func() { recover() }()
		close(c.send)
	}()
	log.Printf("[reject] %s → room %s (%s)", c.name, c.room, code)
}

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
			defer func() { recover() }()
			select {
			case c.send <- b:
			default:
			}
		}()
	}
}

// sendBanList sends the current ban list to the requesting admin client.
func (h *Hub) sendBanList(admin *Client) {
	h.mu.RLock()
	bans := h.roomBans[admin.room]
	var list []BanEntry
	for ip, name := range bans {
		list = append(list, BanEntry{IP: ip, Name: name})
	}
	h.mu.RUnlock()
	if list == nil {
		list = []BanEntry{}
	}
	data, _ := json.Marshal(list)
	admin.sendJSON(outMsg{Type: "ban-list", Data: json.RawMessage(data)})
}

// broadcastAdminInfo sends the current admin ID to all clients in the room.
func (h *Hub) broadcastAdminInfo(room string) {
	h.mu.RLock()
	adminId := h.roomAdminID[room]
	var clients []*Client
	for _, c := range h.rooms[room] {
		clients = append(clients, c)
	}
	h.mu.RUnlock()
	if adminId == "" || len(clients) == 0 {
		return
	}
	data, _ := json.Marshal(map[string]string{"adminId": adminId})
	msg := outMsg{Type: "admin-info", Data: json.RawMessage(data)}
	for _, c := range clients {
		c.sendJSON(msg)
	}
}

// realIP extracts the client IP from the request.
// X-Real-Ip / X-Forwarded-For are trusted when present (reverse-proxy setup).
// When X-Forwarded-For contains multiple comma-separated addresses (client,
// proxy1, proxy2…) we take only the first (the originating client).
func realIP(r *http.Request) string {
	if v := r.Header.Get("X-Real-Ip"); v != "" {
		return strings.TrimSpace(v)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first address before any comma
		if idx := strings.IndexByte(xff, ','); idx != -1 {
			xff = xff[:idx]
		}
		return strings.TrimSpace(xff)
	}
	ip := r.RemoteAddr
	if h, _, err := net.SplitHostPort(ip); err == nil {
		return h
	}
	return ip
}

func (c *Client) sendJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	defer func() { recover() }()
	select {
	case c.send <- b:
	default:
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// WebSocket upgrade + pumps
// ──────────────────────────────────────────────────────────────────────────────

const (
	pingInterval    = 25 * time.Second
	pongWait        = 10 * time.Second
	maxPeersPerRoom = 50  // DoS guard
	maxNameLen      = 50  // max username length (runes)
	maxRoomLen      = 64  // max room name length (runes)
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
	// Enforce length limits to prevent log/memory abuse
	if runes := []rune(room); len(runes) > maxRoomLen {
		room = string(runes[:maxRoomLen])
	}
	if runes := []rune(name); len(runes) > maxNameLen {
		name = string(runes[:maxNameLen])
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("ws upgrade:", err)
		return
	}

	ip := realIP(r)

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
			c.mu.Lock()
			err := c.conn.WriteMessage(websocket.PingMessage, nil)
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}
