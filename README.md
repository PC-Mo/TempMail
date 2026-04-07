# TempMail

A lightweight, self-hosted temporary email service built with Go. No database required, single binary deployment.

## Features

- **Zero-config Setup** — Auto-generate disposable email addresses on page load, no registration needed
- **Real-time Notifications** — WebSocket-based instant mail push with zero latency
- **Custom Addresses** — Support for custom email prefixes
- **Auto-expiration** — Configurable retention period with automatic cleanup
- **Admin Dashboard** — Web UI for configuration, prefix blocking, credential management
- **Single Binary** — Static Go compilation, ~20MB Alpine image, no external runtimes
- **In-memory Store** — Fast, zero dependencies, data cleared on restart (correct for ephemeral mail)

## Quick Start

### Docker (Recommended)

```bash
docker run -d \
  --name tempmail \
  --restart unless-stopped \
  -p 80:80 \
  -p 25:25 \
  -e BASE_URL=https://mail.example.com \
  -e ADMIN_PASSWORD=secure_password \
  -e SESSION_SECRET=random_secret \
  tempmail:latest
```

### Docker Compose

```bash
docker compose up -d
```

### Build from Source (Go 1.22+)

```bash
go build -o server .
./server
```

## Configuration

Configuration via `config.json` or environment variables (env vars take precedence).

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `80` | HTTP listen port |
| `SMTP_PORT` | `25` | SMTP listen port |
| `SMTP_HOST` | `0.0.0.0` | SMTP bind address |
| `BASE_URL` | — | Public access URL (for domain extraction) |
| `MAX_MAILS` | `50` | Max emails per mailbox |
| `MAIL_EXPIRE_MINUTES` | `10` | Email retention duration (minutes) |
| `ADMIN_USER` | `admin` | Admin username |
| `ADMIN_PASSWORD` | `password` | Admin password (**must be changed**) |
| `FORBIDDEN_PREFIXES` | `admin,root,support,test` | Blocked email prefixes (comma-separated) |
| `SESSION_SECRET` | `change_me_in_production` | Session encryption key (**must be changed**) |

### Example config.json

```json
{
  "PORT": 80,
  "SMTP_PORT": 25,
  "SMTP_HOST": "0.0.0.0",
  "BASE_URL": "https://mail.example.com",
  "MAX_MAILS": 50,
  "MAIL_EXPIRE_MINUTES": 10,
  "ADMIN_USER": "admin",
  "ADMIN_PASSWORD": "secure_password",
  "FORBIDDEN_PREFIXES": ["admin", "root", "support"],
  "SESSION_SECRET": "random_secret_key_here"
}
```

## DNS Setup

To receive emails, add MX and A records to your domain DNS:

```
mail.example.com.  IN  MX  10  mail.example.com.
mail.example.com.  IN  A   203.0.113.1
```

**Note:** Cloud providers typically block port 25 by default. Request unblocking from your provider.

## API Reference

### REST Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/config` | Get domain configuration |
| `GET` | `/api/mails/:addr` | List all mails for address |
| `GET` | `/api/mails/:addr/:idx` | Retrieve specific email |
| `DELETE` | `/api/mails/:addr/:idx` | Delete specific email |
| `GET` | `/health` | Health check endpoint |

### WebSocket (`/ws`)

**Client Message:**
```json
{ "type": "request_mailbox" }
{ "type": "set_mailbox", "id": "custom_prefix" }
```

**Server Push:**
```json
{ "type": "mailbox", "id": "generated_id" }
{ "type": "mail", "mail": { "from": "...", "subject": "...", ... } }
{ "type": "mailbox_error", "code": "forbidden_prefix" }
```

## Admin Dashboard

Access `/admin` after login to manage:

- Email retention duration
- Max emails per mailbox
- Blocked email prefixes
- Admin credentials

## Tech Stack

| Component | Technology |
|-----------|------------|
| Backend | Go 1.22, net/http, gorilla/websocket, gorilla/sessions |
| SMTP | emersion/go-smtp, emersion/go-message |
| Frontend | Vanilla JS, TailwindCSS (CDN), Native WebSocket |
| Deployment | Docker, Alpine 3.19, Single binary |

## Security Considerations

- Change default `ADMIN_PASSWORD` and `SESSION_SECRET` in production
- Use HTTPS reverse proxy (e.g., nginx, Caddy) in production
- Restrict MX record to trusted IPs if needed
- All admin endpoints require valid session
- CSRF protection on configuration changes
- WebSocket origin validation enabled

## License

MIT
