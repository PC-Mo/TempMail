package main

import (
	"fmt"
	"log"
	"net/http"

	"tempmail/internal/auth"
	"tempmail/internal/config"
	"tempmail/internal/handler"
	"tempmail/internal/mailstore"
	"tempmail/internal/smtp"
)

func main() {
	cfg := config.Load("config.json")

	// Init OIDC auth (reads OIDC_* env vars; no-op if not configured)
	auth.Init()

	mailstore.StartCleanup()

	mux := http.NewServeMux()
	handler.Register(mux, "public")

	// Auth routes (login / callback / logout)
	mux.HandleFunc("/auth/login", auth.HandleLogin)
	mux.HandleFunc("/auth/callback", auth.HandleCallback)
	mux.HandleFunc("/auth/logout", auth.HandleLogout)

	// Static files (fallback)
	mux.Handle("/", handler.StaticHandler("public"))

	// Wrap entire mux with auth middleware
	var root http.Handler = mux
	root = auth.Middleware(root)

	smtpSrv := smtp.NewServer()
	smtpSrv.Addr = fmt.Sprintf("%s:%d", cfg.SMTPHost, cfg.SMTPPort)
	go func() {
		log.Printf("SMTP 服务启动，%s", smtpSrv.Addr)
		if err := smtpSrv.ListenAndServe(); err != nil {
			log.Fatalf("SMTP error: %v", err)
		}
	}()

	httpAddr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("HTTP 服务启动，%s", httpAddr)
	if err := http.ListenAndServe(httpAddr, root); err != nil {
		log.Fatalf("HTTP error: %v", err)
	}
}
