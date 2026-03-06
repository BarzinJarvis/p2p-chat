package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type testMsg struct {
	Type  string          `json:"type"`
	From  string          `json:"from,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Peers []interface{}   `json:"peers,omitempty"`
}

// dialWSWithIP dials a WebSocket connection setting X-Real-Ip header so
// the server treats the connection as coming from a specific IP address.
// This lets us give Admin and Victim different IPs in the test environment
// (where all connections otherwise appear as 127.0.0.1).
func dialWSWithIP(t *testing.T, baseURL, room, name, fakeIP string) (*websocket.Conn, string) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws?room=" + room + "&name=" + name
	hdr := http.Header{}
	if fakeIP != "" {
		hdr.Set("X-Real-Ip", fakeIP)
	}
	c, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("dial %s (ip=%s): %v", name, fakeIP, err)
	}
	// Drain until welcome (skip peer-joined, admin-info, etc.)
	var myID string
	for {
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, raw, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for welcome (%s): %v", name, err)
		}
		var m testMsg
		json.Unmarshal(raw, &m)
		if m.Type == "welcome" {
			myID = m.From
			break
		}
	}
	c.SetReadDeadline(time.Time{})
	return c, myID
}

func drainBg(c *websocket.Conn, ch chan testMsg) {
	go func() {
		for {
			_, raw, err := c.ReadMessage()
			if err != nil {
				return
			}
			var m testMsg
			json.Unmarshal(raw, &m)
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
		}
	}()
}

// ─────────────────────────────────────────────────────────────────────────────
// TestBan: admin bans victim → victim gets kicked + banned message + can't rejoin
// ─────────────────────────────────────────────────────────────────────────────
func TestBan(t *testing.T) {
	hub := newHub()
	go hub.run()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	const (
		adminIP  = "10.0.0.1"
		victimIP = "10.0.0.2"
		room     = "testroom"
	)

	// ── 1. Connect admin ──────────────────────────────────────────────────────
	admin, adminID := dialWSWithIP(t, srv.URL, room, "Admin", adminIP)
	defer admin.Close()
	adminCh := make(chan testMsg, 32)
	drainBg(admin, adminCh)
	t.Logf("Admin connected: id=%s ip=%s", adminID, adminIP)

	// ── 2. Connect victim ──────────────────────────────────────────────────────
	victim, victimID := dialWSWithIP(t, srv.URL, room, "Victim", victimIP)
	t.Logf("Victim connected: id=%s ip=%s", victimID, victimIP)

	// Let peer-joined / admin-info settle
	time.Sleep(200 * time.Millisecond)

	// ── 3. Admin sends ban ────────────────────────────────────────────────────
	banPkt, _ := json.Marshal(map[string]interface{}{
		"type": "ban",
		"data": map[string]string{"peerId": victimID},
	})
	if err := admin.WriteMessage(websocket.TextMessage, banPkt); err != nil {
		t.Fatalf("admin send ban: %v", err)
	}
	t.Log("Admin sent ban")

	// ── 4. Victim must receive 'banned' TEXT message OR WS close code 4002 ───
	// Drain all pending messages; admin-info, peer-joined etc. may arrive first.
	bannedByMsg := false
	bannedByCode := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		victim.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		_, raw, err := victim.ReadMessage()
		victim.SetReadDeadline(time.Time{})
		if err == nil {
			var m testMsg
			json.Unmarshal(raw, &m)
			if m.Type == "banned" {
				bannedByMsg = true
				t.Logf("✅ PASS: Victim received 'banned' TEXT message: data=%s", string(m.Data))
				break
			}
			t.Logf("(Victim drained: type=%s, waiting for banned...)", m.Type)
			continue
		}
		if websocket.IsCloseError(err, 4002) {
			bannedByCode = true
			t.Log("✅ PASS: Victim received WS close code 4002 (banned)")
		} else if !strings.Contains(err.Error(), "i/o timeout") {
			t.Logf("Victim close/err: %v", err)
		}
		break
	}

	if !bannedByMsg && !bannedByCode {
		t.Fatal("❌ FAIL: Victim never received any ban notification")
	}

	// ── 5. Victim is kicked — admin should see peer-left ─────────────────────
	time.Sleep(400 * time.Millisecond)
	seenPeerLeft := false
	for {
		select {
		case m := <-adminCh:
			if m.Type == "peer-left" && m.From == victimID {
				seenPeerLeft = true
			}
		default:
			goto doneAdminDrain
		}
	}
doneAdminDrain:
	if seenPeerLeft {
		t.Log("✅ PASS: Admin received peer-left for banned victim")
	} else {
		t.Error("❌ FAIL: Admin never received peer-left for banned victim")
	}

	// ── 6. Victim tries to rejoin — must be blocked ───────────────────────────
	time.Sleep(200 * time.Millisecond)
	rejoinURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?room=" + room + "&name=Victim"
	rejoinHdr := http.Header{"X-Real-Ip": []string{victimIP}}
	rejoin, _, dialErr := websocket.DefaultDialer.Dial(rejoinURL, rejoinHdr)
	if dialErr != nil {
		t.Logf("✅ PASS: Rejoin connection refused at dial: %v", dialErr)
		return
	}
	defer rejoin.Close()

	rejoin.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw2, err2 := rejoin.ReadMessage()
	rejoin.SetReadDeadline(time.Time{})

	if err2 != nil {
		t.Logf("✅ PASS: Rejoin got error before welcome: %v", err2)
		return
	}
	var m2 testMsg
	json.Unmarshal(raw2, &m2)
	if m2.Type == "error" {
		var d map[string]string
		json.Unmarshal(m2.Data, &d)
		if d["code"] == "banned" {
			t.Logf("✅ PASS: Rejoin correctly blocked with code='banned': %s", d["message"])
		} else {
			t.Errorf("❌ FAIL: Rejoin got error but wrong code '%s' (expected 'banned')", d["code"])
		}
	} else {
		t.Errorf("❌ FAIL: Rejoin was allowed! Got type=%s (expected error)", m2.Type)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestEviction: same IP + same name connecting twice → first gets evicted
// ─────────────────────────────────────────────────────────────────────────────
func TestEviction(t *testing.T) {
	hub := newHub()
	go hub.run()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	const ip = "10.0.0.5"

	// Connect tab1
	tab1, tab1ID := dialWSWithIP(t, srv.URL, "evictroom", "User", ip)
	t.Logf("Tab1: id=%s", tab1ID)

	// Connect tab2 (same IP + same name) — should evict tab1
	tab2, tab2ID := dialWSWithIP(t, srv.URL, "evictroom", "User", ip)
	t.Logf("Tab2: id=%s", tab2ID)

	// Tab1 should get a TEXT "evicted" message, then be closed
	deadline := time.Now().Add(3 * time.Second)
	gotEvicted := false
	for time.Now().Before(deadline) {
		tab1.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		_, raw, err := tab1.ReadMessage()
		tab1.SetReadDeadline(time.Time{})
		if err != nil {
			t.Logf("tab1 closed (err=%v) after eviction", err)
			break
		}
		var m testMsg
		json.Unmarshal(raw, &m)
		if m.Type == "evicted" {
			gotEvicted = true
			break
		}
		t.Logf("(tab1 drained: type=%s)", m.Type)
	}

	if gotEvicted {
		t.Log("✅ PASS: Tab1 received 'evicted' TEXT message")
	} else {
		t.Error("❌ FAIL: Tab1 did not receive 'evicted' message")
	}

	// Tab2 should still be alive
	if tab2.WriteMessage(websocket.PingMessage, nil) == nil {
		t.Log("✅ PASS: Tab2 still connected after eviction")
	} else {
		t.Error("❌ FAIL: Tab2 was also disconnected")
	}
	tab2.Close()
	_ = fmt.Sprintf("") // suppress unused import
}

// ─────────────────────────────────────────────────────────────────────────────
// TestUnban: admin bans, then unbans → banned user can rejoin
// ─────────────────────────────────────────────────────────────────────────────
func TestUnban(t *testing.T) {
	hub := newHub()
	go hub.run()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	const (
		adminIP  = "10.1.0.1"
		victimIP = "10.1.0.2"
		room     = "unbanroom"
	)

	admin, adminID := dialWSWithIP(t, srv.URL, room, "Admin", adminIP)
	defer admin.Close()
	adminCh := make(chan testMsg, 32)
	drainBg(admin, adminCh)
	t.Logf("Admin: id=%s", adminID)

	victim, victimID := dialWSWithIP(t, srv.URL, room, "Victim", victimIP)
	t.Logf("Victim: id=%s", victimID)

	time.Sleep(200 * time.Millisecond)

	// Ban victim
	banPkt, _ := json.Marshal(map[string]interface{}{"type": "ban", "data": map[string]string{"peerId": victimID}})
	admin.WriteMessage(websocket.TextMessage, banPkt)

	// Wait for victim to be kicked
	time.Sleep(500 * time.Millisecond)
	victim.Close()
	time.Sleep(100 * time.Millisecond)

	// Admin should get a ban-list message
	banListReceived := false
	for i := 0; i < 20; i++ {
		select {
		case m := <-adminCh:
			if m.Type == "ban-list" {
				banListReceived = true
				var list []map[string]string
				json.Unmarshal(m.Data, &list)
				t.Logf("✅ Ban list received (%d entries): %v", len(list), list)
			}
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !banListReceived {
		t.Error("❌ Admin never received ban-list update after ban")
	}

	// Unban victim
	unbanPkt, _ := json.Marshal(map[string]interface{}{"type": "unban", "data": map[string]string{"ip": victimIP}})
	admin.WriteMessage(websocket.TextMessage, unbanPkt)
	time.Sleep(200 * time.Millisecond)

	// Victim tries to rejoin — should succeed now
	rejoinURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?room=" + room + "&name=Victim"
	rejoinHdr := http.Header{"X-Real-Ip": []string{victimIP}}
	rejoin, _, dialErr := websocket.DefaultDialer.Dial(rejoinURL, rejoinHdr)
	if dialErr != nil {
		t.Fatalf("❌ Rejoin after unban failed: %v", dialErr)
	}
	defer rejoin.Close()

	rejoin.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := rejoin.ReadMessage()
	rejoin.SetReadDeadline(time.Time{})
	if err != nil {
		t.Fatalf("❌ Rejoin got no response after unban: %v", err)
	}
	var m testMsg
	json.Unmarshal(raw, &m)
	if m.Type == "welcome" {
		t.Log("✅ PASS: Victim successfully rejoined after unban")
	} else {
		t.Errorf("❌ FAIL: Expected welcome after unban, got type=%s data=%s", m.Type, string(m.Data))
	}
}
