# 💬 P2P Chat

A lightweight, real-time group chat server with file sharing, voice calls, and reply threads — distributed as a **single self-contained binary**.

![Go](https://img.shields.io/badge/Go-1.22-00ADD8?logo=go&logoColor=white)
![License](https://img.shields.io/badge/license-MIT-green)

---

## ✨ Features

| Feature | Details |
|---|---|
| 💬 Real-time messaging | WebSocket relay, works on LAN and internet |
| 📎 File sharing | Images, video, audio, any file — up to 100 MB |
| 🖼 Inline previews | Images (lightbox), video player, audio player |
| 🎙 Voice messages | Record in-browser, upload and send as audio |
| 📞 Voice calls | WebRTC peer-to-peer audio between room members |
| ↩️ Reply threads | Swipe right (mobile) or hover button (desktop) |
| ✓✓ Seen receipts | Single tick = sent, double tick = seen |
| ⌨️ Typing indicators | Animated dots when someone is typing |
| 🔔 Browser notifications | Desktop push when tab is unfocused |
| 🗑 Auto file cleanup | Files deleted when last member leaves the room |
| 🌐 Sub-path proxy ready | Works at `/` or `/chat/` behind Nginx |
| 📦 Single binary | No Docker, no Node, no database — just run it |

---

## 🚀 Quick Start

### Download a release binary

```bash
# Linux x86_64
curl -L https://github.com/BarzinJarvis/p2p-chat/releases/latest/download/chat-linux-amd64 -o p2p-chat
chmod +x p2p-chat
./p2p-chat -p 8080
```

Then open `http://localhost:8080` in your browser.

### Build from source

```bash
git clone https://github.com/BarzinJarvis/p2p-chat
cd p2p-chat
go build -o p2p-chat .
./p2p-chat -p 8080
```

**Requirements:** Go 1.22+

---

## ⚙️ Usage

```
./p2p-chat [OPTIONS]

Options:
  -p PORT    Port to listen on (default: 8080, overrides $PORT env var)
```

```bash
# Default port 8080
./p2p-chat

# Custom port
./p2p-chat -p 3000

# Via environment variable
PORT=3000 ./p2p-chat
```

Uploaded files are stored in `./uploads/` next to the binary and deleted automatically when all room members leave.

---

## 🌐 Nginx Reverse Proxy

### At root domain (`https://chat.example.com`)

```nginx
server {
    listen 443 ssl;
    server_name chat.example.com;

    ssl_certificate     /etc/letsencrypt/live/example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/example.com/privkey.pem;

    client_max_body_size 100m;

    location /ws {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade    $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host       $host;
        proxy_read_timeout 86400;
    }

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host      $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

### At sub-path (`https://example.com/chat/`)

```nginx
location /chat/ws {
    proxy_pass http://127.0.0.1:8080/ws;
    proxy_http_version 1.1;
    proxy_set_header Upgrade    $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 86400;
}

location /chat/ {
    proxy_pass http://127.0.0.1:8080/;
    proxy_set_header Host      $host;
    proxy_set_header X-Real-IP $remote_addr;
}

location = /chat {
    return 301 /chat/;
}
```

---

## 🗓 File Cleanup Cron (optional)

Files are already deleted when a room empties. For extra safety (e.g. server restarts), add a cron job:

```bash
# Delete uploads older than 4 hours
0 */4 * * * find /path/to/uploads -type f -mmin +240 -delete
```

---

## 🔒 Security Notes

- **Transport:** All traffic over `wss://` (TLS) is encrypted in transit — ISPs cannot read messages
- **Server relay:** Text messages pass through the server (not true E2E encrypted). The server operator can read messages in transit.
- **Voice calls:** WebRTC audio is DTLS-SRTP encrypted peer-to-peer — server never sees audio
- **Rooms:** Anyone who knows the room name can join. Use a private room name for sensitive chats.
- **No authentication:** No passwords, no accounts — security comes from room name secrecy

---

## 📁 Project Structure

```
p2p-chat/
├── main.go          # HTTP server, /upload endpoint, file serving
├── hub.go           # WebSocket hub, room management, file cleanup
├── go.mod
├── go.sum
└── static/
    └── index.html   # Full single-page app (embedded in binary)
```

---

## 📜 License

MIT — free to use, modify, and distribute.
