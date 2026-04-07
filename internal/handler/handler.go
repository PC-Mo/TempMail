package handler

import (
	"crypto/rand"
	"encoding/json"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/sessions"
	"github.com/gorilla/websocket"

	"tempmail/internal/auth"
	"tempmail/internal/config"
	"tempmail/internal/mailbox"
	"tempmail/internal/mailstore"
)

var (
	store     *sessions.CookieStore
	storeOnce sync.Once
)

func sessionStore() *sessions.CookieStore {
	storeOnce.Do(func() {
		cfg := config.Get()
		store = sessions.NewCookieStore([]byte(cfg.SessionSecret))
		store.Options = &sessions.Options{
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400,
			// Secure is not forced here to support plain-HTTP deployments;
			// set it via a reverse proxy that terminates TLS.
		}
	})
	return store
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Only allow same-origin WebSocket connections
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // browser requests without Origin header
		}
		return isSameOrigin(r, origin)
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func isSameOrigin(r *http.Request, origin string) bool {
	// Extract host from request
	requestHost := r.Host
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		// Safe to assume HTTPS if TLS is active or reverse proxy indicates it
	}
	// Parse origin URL
	if idx := strings.Index(origin, "://"); idx >= 0 {
		origin = origin[idx+3:]
	}
	if idx := strings.Index(origin, "/"); idx >= 0 {
		origin = origin[:idx]
	}
	return origin == requestHost
}

// Register mounts all routes on the given mux.
func Register(mux *http.ServeMux, staticDir string) {
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/api/config", handleAPIConfig)
	mux.HandleFunc("/api/mails/", handleMails)
	mux.HandleFunc("/ws", handleWS)
	// Admin SPA entry (served as admin.html)
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, staticDir+"/admin.html")
	})
	// Admin API (shared by both modes)
	mux.HandleFunc("/admin/config", handleAdminConfig)
	// Password-mode only routes
	mux.HandleFunc("/admin/login", handleAdminLogin)
	mux.HandleFunc("/admin/logout", handleAdminLogout)
}

// --- Health ---

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	jsonOK(w, map[string]string{"status": "ok"})
}

// --- API Config ---

func handleAPIConfig(w http.ResponseWriter, _ *http.Request) {
	jsonOK(w, map[string]string{"domain": config.Get().GetDomain()})
}

// --- Mails API ---

func handleMails(w http.ResponseWriter, r *http.Request) {
	// Path: /api/mails/{addr} or /api/mails/{addr}/{idx}
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/mails/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "bad request", 400)
		return
	}

	addr := strings.ToLower(parts[0])
	prefix := strings.Split(addr, "@")[0]

	if config.Get().IsForbidden(prefix) {
		jsonErr(w, "不允许使用该邮箱前缀", "forbidden_prefix", 403)
		return
	}

	if len(parts) == 1 {
		// GET /api/mails/:addr
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		mails := mailstore.GetAll(addr)
		if mails == nil {
			mails = []*mailstore.Mail{}
		}
		jsonOK(w, map[string]any{"mails": mails})
		return
	}

	// /api/mails/:addr/:idx
	idx, err := strconv.Atoi(parts[1])
	if err != nil {
		http.Error(w, "bad index", 400)
		return
	}

	switch r.Method {
	case http.MethodGet:
		mail := mailstore.GetByIdx(addr, idx)
		if mail == nil {
			jsonErr(w, "邮件不存在或已过期", "", 404)
			return
		}
		jsonOK(w, map[string]any{"mail": mail})
	case http.MethodDelete:
		ok := mailstore.Delete(addr, idx)
		jsonOK(w, map[string]bool{"success": ok})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// --- WebSocket ---

const (
	wsPingInterval = 30 * time.Second
	wsReadDeadline = 60 * time.Second
)

func handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}

	// mailboxID is written by the read loop and read by the ping goroutine.
	// Use atomic.Value to avoid a data race between the two goroutines.
	var mailboxID atomic.Value
	mailboxID.Store("")

	getID := func() string {
		if v := mailboxID.Load(); v != nil {
			return v.(string)
		}
		return ""
	}

	defer func() {
		conn.Close()
		if id := getID(); id != "" {
			mailbox.GetHub().Unregister(id)
		}
	}()

	conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
		return nil
	})

	// Ping goroutine: keeps the connection alive and detects dead peers.
	// All writes go through the hub's per-connection mutex to avoid races with Push.
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				id := getID()
				var pingErr error
				if id != "" {
					// After registration: use the hub's serialised ping.
					pingErr = mailbox.GetHub().Ping(id)
				} else {
					// Before registration: no concurrent writers yet, safe to write directly.
					conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
					pingErr = conn.WriteMessage(websocket.PingMessage, nil)
					conn.SetWriteDeadline(time.Time{})
				}
				if pingErr != nil {
					return
				}
			case <-stopPing:
				return
			}
		}
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		conn.SetReadDeadline(time.Now().Add(wsReadDeadline))

		var req map[string]string
		if err := json.Unmarshal(msg, &req); err != nil {
			continue
		}

		curID := getID()

		switch req["type"] {
		case "request_mailbox":
			id := newMailboxID()
			if curID != "" && curID != id {
				mailbox.GetHub().Unregister(curID)
			}
			mailboxID.Store(id)
			mailbox.GetHub().Register(id, conn)
			if err := mailbox.GetHub().Send(id, map[string]string{"type": "mailbox", "id": id}); err != nil {
				log.Printf("ws send mailbox: %v", err)
			}

		case "set_mailbox":
			id := strings.ToLower(strings.TrimSpace(req["id"]))
			if id == "" || len(id) > 64 {
				continue
			}
			prefix := strings.Split(id, "@")[0]
			if config.Get().IsForbidden(prefix) {
				errMsg := map[string]string{"type": "mailbox_error", "code": "forbidden_prefix"}
				if curID != "" {
					if err := mailbox.GetHub().Send(curID, errMsg); err != nil {
						writeJSON(conn, errMsg)
					}
				} else {
					writeJSON(conn, errMsg)
				}
				continue
			}
			if curID != "" && curID != id {
				mailbox.GetHub().Unregister(curID)
			}
			mailboxID.Store(id)
			mailbox.GetHub().Register(id, conn)
			if err := mailbox.GetHub().Send(id, map[string]string{"type": "mailbox", "id": id}); err != nil {
				log.Printf("ws send mailbox: %v", err)
			}
		}
	}
}

// --- Admin ---

func handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	snap := config.Get().Snap()
	username := r.FormValue("username")
	password := r.FormValue("password")

	if username == snap.AdminUser && password == snap.AdminPassword {
		// Invalidate old session
		oldSess, _ := sessionStore().Get(r, "admin")
		if oldSess != nil {
			oldSess.Options.MaxAge = -1
			oldSess.Save(r, w)
		}
		
		// Create new session with secure token
		sess, err := sessionStore().New(r, "admin")
		if err != nil {
			http.Error(w, "session error", 500)
			return
		}
		sess.Values["isAdmin"] = true
		sess.Values["timestamp"] = time.Now().Unix()
		if err := sess.Save(r, w); err != nil {
			log.Printf("session save: %v", err)
			http.Error(w, "session save error", 500)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusFound)
	} else {
		http.Redirect(w, r, "/login.html?error=1", http.StatusFound)
	}
}

func handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if auth.IsEnabled() {
		http.Redirect(w, r, "/auth/logout", http.StatusFound)
		return
	}
	sess, err := sessionStore().Get(r, "admin")
	if err == nil {
		sess.Options.MaxAge = -1
		if err := sess.Save(r, w); err != nil {
			log.Printf("session save on logout: %v", err)
		}
	}
	http.Redirect(w, r, "/login.html", http.StatusFound)
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	cfg := config.Get()

	// OIDC mode: check group membership
	if auth.IsEnabled() {
		group := cfg.AdminGroup
		if group == "" {
			// No admin group configured → nobody gets admin
			http.Error(w, "admin access not configured", http.StatusForbidden)
			return false
		}
		if auth.IsInGroup(r, group) {
			return true
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}

	// Password mode: check local session
	sess, err := sessionStore().Get(r, "admin")
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if v, ok := sess.Values["isAdmin"].(bool); ok && v {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

func handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}

	cfg := config.Get()
	snap := cfg.Snap()

	switch r.Method {
	case http.MethodGet:
		resp := map[string]any{
			"maxMails":          snap.MaxMails,
			"mailExpireMinutes": snap.MailExpireMinutes,
			"forbiddenPrefixes": strings.Join(snap.ForbiddenPrefixes, "\n"),
		}
		// 仅密码模式暴露 adminUser（前端据此判断是否显示账户设置 tab）
		if !auth.IsEnabled() {
			resp["adminUser"] = snap.AdminUser
		}
		jsonOK(w, resp)

	case http.MethodPost:
		var body struct {
			MaxMails          string `json:"maxMails"`
			MailExpireMinutes string `json:"mailExpireMinutes"`
			ForbiddenPrefixes string `json:"forbiddenPrefixes"`
			AdminUser         string `json:"adminUser"`
			AdminPassword     string `json:"adminPassword"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, "bad request", "", 400)
			return
		}
		maxMails, _ := strconv.Atoi(body.MaxMails)
		expireMin, _ := strconv.Atoi(body.MailExpireMinutes)
		forbidden := splitLines(body.ForbiddenPrefixes)

		if err := cfg.Update(maxMails, expireMin, forbidden, body.AdminUser, body.AdminPassword); err != nil {
			jsonErr(w, "configuration update failed", "", 500)
			return
		}
		jsonOK(w, map[string]any{"success": true, "message": "configuration updated"})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

// --- Static files with Cache-Control ---

func StaticHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ext := strings.ToLower(path.Ext(r.URL.Path))
		switch ext {
		case ".html", "":
			w.Header().Set("Cache-Control", "no-cache")
		default:
			w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		}
		fs.ServeHTTP(w, r)
	})
}

// --- Helpers ---

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("jsonOK encode: %v", err)
	}
}

func jsonErr(w http.ResponseWriter, msg, code string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]string{"error": msg}
	if code != "" {
		body["code"] = code
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("jsonErr encode: %v", err)
	}
}

// writeJSON writes directly to a WebSocket connection without going through the hub.
// Only safe to call when the connection is not yet registered in the hub.
func writeJSON(conn *websocket.Conn, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}

func splitLines(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			result = append(result, t)
		}
	}
	return result
}

func newMailboxID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 16) // increased from 8 to 16 for higher entropy
	if _, err := rand.Read(b); err != nil {
		log.Printf("mailbox id generation failed: %v, using fallback", err)
		// Fallback: use time-based (weak but better than crash)
		for i := range b {
			b[i] = chars[i%len(chars)]
		}
		return string(b)
	}
	result := make([]byte, len(b))
	for i, v := range b {
		result[i] = chars[v%byte(len(chars))]
	}
	return string(result)
}
