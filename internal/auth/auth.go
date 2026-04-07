// Package auth implements OAuth2/OIDC login via an external provider (e.g. Authelia).
//
// Flow:
//   1. Any protected request without a valid session → redirect to /auth/login
//   2. /auth/login  → redirect to provider's authorize endpoint (with state + PKCE)
//   3. Provider     → redirects back to /auth/callback?code=...&state=...
//   4. /auth/callback exchanges code for token, fetches userinfo, writes session
//   5. All subsequent requests: middleware checks session cookie (local, sub-ms)
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/sessions"

	"tempmail/internal/config"
)

const sessionName = "mm_auth"

var (
	store     *sessions.CookieStore
	storeOnce sync.Once
)

func getStore() *sessions.CookieStore {
	storeOnce.Do(func() {
		secret := config.Get().SessionSecret
		store = sessions.NewCookieStore([]byte(secret))
		store.Options = &sessions.Options{
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400 * 7, // 7 days
			Path:     "/",
		}
	})
	return store
}

// OAuthConfig holds OIDC/OAuth2 provider settings, read from env/config at startup.
type OAuthConfig struct {
	Enabled       bool
	IssuerURL     string // e.g. https://auth.example.com
	ClientID      string
	ClientSecret  string
	RedirectURI   string // e.g. https://mail.example.com/auth/callback
	PostLogoutURI string // where to redirect after logout (default: BASE_URL or "/")
	EndSessionURL string // RP-initiated logout endpoint (optional, e.g. https://auth.example.com/logout)
}

var oauthCfg OAuthConfig

// Init loads OAuth2 config from environment variables.
// Call once at startup before registering routes.
func Init() {
	getEnv := func(key string) string { return getEnvVal(key) }

	oauthCfg = OAuthConfig{
		IssuerURL:    getEnv("OIDC_ISSUER_URL"),
		ClientID:     getEnv("OIDC_CLIENT_ID"),
		ClientSecret: getEnv("OIDC_CLIENT_SECRET"),
		RedirectURI:  getEnv("OIDC_REDIRECT_URI"),
		EndSessionURL: getEnv("OIDC_END_SESSION_URL"), // optional RP-initiated logout
	}
	// PostLogoutURI: OIDC_POST_LOGOUT_URI → BASE_URL → "/"
	if v := getEnv("OIDC_POST_LOGOUT_URI"); v != "" {
		oauthCfg.PostLogoutURI = v
	} else if v := getEnv("BASE_URL"); v != "" {
		oauthCfg.PostLogoutURI = v
	} else {
		oauthCfg.PostLogoutURI = "/"
	}
	oauthCfg.Enabled = oauthCfg.IssuerURL != "" && oauthCfg.ClientID != "" && oauthCfg.ClientSecret != ""

	if oauthCfg.Enabled {
		log.Printf("auth: OIDC enabled, issuer=%s client=%s", oauthCfg.IssuerURL, oauthCfg.ClientID)
	} else {
		log.Printf("auth: OIDC disabled (set OIDC_ISSUER_URL, OIDC_CLIENT_ID, OIDC_CLIENT_SECRET to enable)")
	}
}

// IsEnabled reports whether OIDC auth is configured and active.
func IsEnabled() bool {
	return oauthCfg.Enabled
}

// Middleware wraps a handler, requiring a valid auth session.
// Static assets (css/js/images) are always allowed through.
// If OAuth is disabled, all requests pass through.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !oauthCfg.Enabled || isStaticAsset(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// Skip auth routes themselves
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			next.ServeHTTP(w, r)
			return
		}

		sess, _ := getStore().Get(r, sessionName)
		if authed, _ := sess.Values["authenticated"].(bool); authed {
			// USER_GROUP check: if configured, only members may access
			userGroup := config.Get().UserGroup
			if userGroup != "" && !isInGroupFromSession(sess, userGroup) {
				http.Error(w, "403 Forbidden: not in required group", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// WebSocket and API calls: return 401 instead of redirecting
		if r.Header.Get("Upgrade") == "websocket" ||
			strings.HasPrefix(r.URL.Path, "/api/") ||
			r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// Save the original URL so we can redirect back after login
		returnTo := r.URL.RequestURI()
		http.Redirect(w, r, "/auth/login?return_to="+url.QueryEscape(returnTo), http.StatusFound)
	})
}

// HandleLogin initiates the OAuth2 authorization code flow.
func HandleLogin(w http.ResponseWriter, r *http.Request) {
	returnTo := r.URL.Query().Get("return_to")
	if returnTo == "" {
		returnTo = "/"
	}

	// Generate state (CSRF) and PKCE verifier
	state := randomBase64(16)
	verifier := randomBase64(32)
	challenge := pkceChallenge(verifier)

	// Store state + verifier + return_to in a short-lived cookie
	sess, _ := getStore().New(r, "mm_oauth_state")
	sess.Options = &sessions.Options{MaxAge: 300, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/"}
	sess.Values["state"] = state
	sess.Values["verifier"] = verifier
	sess.Values["return_to"] = returnTo
	if err := sess.Save(r, w); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}

	authURL := fmt.Sprintf(
		"%s/api/oidc/authorization?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&state=%s&code_challenge=%s&code_challenge_method=S256",
		oauthCfg.IssuerURL,
		url.QueryEscape(oauthCfg.ClientID),
		url.QueryEscape(oauthCfg.RedirectURI),
		url.QueryEscape("openid profile groups"),
		url.QueryEscape(state),
		url.QueryEscape(challenge),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// HandleCallback handles the OAuth2 redirect, exchanges code for token, writes session.
func HandleCallback(w http.ResponseWriter, r *http.Request) {
	// Retrieve state session
	stateSess, err := getStore().Get(r, "mm_oauth_state")
	if err != nil || stateSess.IsNew {
		http.Error(w, "invalid state session", http.StatusBadRequest)
		return
	}

	expectedState, _ := stateSess.Values["state"].(string)
	verifier, _ := stateSess.Values["verifier"].(string)
	returnTo, _ := stateSess.Values["return_to"].(string)

	// Validate state
	if r.URL.Query().Get("state") != expectedState {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	if returnTo == "" {
		returnTo = "/"
	}

	// Invalidate the state cookie
	stateSess.Options.MaxAge = -1
	_ = stateSess.Save(r, w)

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	// Exchange code for token
	tokenResp, err := exchangeCode(code, verifier)
	if err != nil {
		log.Printf("auth: token exchange error: %v", err)
		http.Error(w, "token exchange failed", http.StatusInternalServerError)
		return
	}

	// Fetch userinfo
	userInfo, err := fetchUserInfo(tokenResp.AccessToken)
	if err != nil {
		log.Printf("auth: userinfo error: %v", err)
		http.Error(w, "userinfo failed", http.StatusInternalServerError)
		return
	}

	// Write auth session
	authSess, _ := getStore().New(r, sessionName)
	authSess.Values["authenticated"] = true
	authSess.Values["username"] = userInfo.Username
	authSess.Values["groups"] = strings.Join(userInfo.Groups, ",")
	authSess.Values["at"] = time.Now().Unix()
	log.Printf("auth: login user=%s groups=%v", userInfo.Username, userInfo.Groups)
	if err := authSess.Save(r, w); err != nil {
		log.Printf("auth: session save error: %v", err)
		http.Error(w, "session save failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, returnTo, http.StatusFound)
}

// HandleLogout clears the local auth session and redirects to PostLogoutURI (default "/").
// Note: this does NOT invalidate the IdP session. If the IdP supports
// RP-initiated logout (end_session_endpoint), set OIDC_END_SESSION_URL to
// that endpoint; users will be redirected there after local session is cleared.
func HandleLogout(w http.ResponseWriter, r *http.Request) {
	sess, _ := getStore().Get(r, sessionName)
	sess.Options.MaxAge = -1
	_ = sess.Save(r, w)

	// If provider supports RP-initiated logout, redirect there
	if oauthCfg.Enabled && oauthCfg.EndSessionURL != "" {
		target := fmt.Sprintf(
			"%s?post_logout_redirect_uri=%s&client_id=%s",
			oauthCfg.EndSessionURL,
			url.QueryEscape(oauthCfg.PostLogoutURI),
			url.QueryEscape(oauthCfg.ClientID),
		)
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	http.Redirect(w, r, oauthCfg.PostLogoutURI, http.StatusFound)
}

// --- token exchange ---

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	IDToken     string `json:"id_token"`
}

func exchangeCode(code, verifier string) (*tokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", oauthCfg.RedirectURI)
	data.Set("client_id", oauthCfg.ClientID)
	data.Set("code_verifier", verifier)

	tokenURL := oauthCfg.IssuerURL + "/api/oidc/token"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(oauthCfg.ClientID, oauthCfg.ClientSecret)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint %d: %s", resp.StatusCode, body)
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, err
	}
	return &tr, nil
}

// UserInfo holds identity info from the OIDC userinfo endpoint.
type UserInfo struct {
	Username string
	Groups   []string
}

func isInGroupFromSession(sess *sessions.Session, group string) bool {
	raw, _ := sess.Values["groups"].(string)
	if raw == "" {
		return false
	}
	for _, g := range strings.Split(raw, ",") {
		if strings.TrimSpace(g) == group {
			return true
		}
	}
	return false
}

// IsInGroup reports whether the current request's auth session contains the given group.
func IsInGroup(r *http.Request, group string) bool {
	if group == "" {
		return false
	}
	sess, err := getStore().Get(r, sessionName)
	if err != nil {
		return false
	}
	return isInGroupFromSession(sess, group)
}

func fetchUserInfo(accessToken string) (*UserInfo, error) {
	userinfoURL := oauthCfg.IssuerURL + "/api/oidc/userinfo"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, userinfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo %d: %s", resp.StatusCode, body)
	}

	var raw struct {
		Sub               string          `json:"sub"`
		PreferredUsername string          `json:"preferred_username"`
		Name              string          `json:"name"`
		Groups            json.RawMessage `json:"groups"` // may be []string or string
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	info := &UserInfo{}
	if raw.PreferredUsername != "" {
		info.Username = raw.PreferredUsername
	} else {
		info.Username = raw.Sub
	}

	// groups 可能是 []string 或 string
	if len(raw.Groups) > 0 {
		var arr []string
		if err := json.Unmarshal(raw.Groups, &arr); err == nil {
			info.Groups = arr
		} else {
			var s string
			if err := json.Unmarshal(raw.Groups, &s); err == nil && s != "" {
				info.Groups = []string{s}
			}
		}
	}

	return info, nil
}

// --- helpers ---

func isStaticAsset(path string) bool {
	staticExts := []string{".css", ".js", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".svg",
		".woff", ".woff2", ".ttf", ".eot", ".webp", ".map"}
	lower := strings.ToLower(path)
	for _, ext := range staticExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func randomBase64(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func getEnvVal(key string) string {
	return os.Getenv(key)
}

func lookupEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}
