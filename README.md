# TempMail

A lightweight, self-hosted temporary email service built with Go. No database required, single binary deployment.

## Features

- **Zero-config Setup** — Auto-generate disposable email addresses on page load, no registration needed
- **Real-time Notifications** — WebSocket-based instant mail push with zero latency
- **Custom Addresses** — Support for custom email prefixes
- **Auto-expiration** — Configurable retention period with automatic cleanup
- **Admin Dashboard** — Hidden `/admin` route for managing config; supports both password and OIDC auth
- **Dual Auth Modes** — Run standalone with username/password, or integrate with any OIDC provider (e.g. Authelia, Keycloak)
- **Single Binary** — Static Go compilation, ~20MB Alpine image, no external runtimes
- **In-memory Store** — Fast, zero dependencies, data cleared on restart (by design for ephemeral mail)

## Quick Start

```bash
# 1. Copy and edit the environment file
cp .env.example .env
vi .env

# 2. Start
docker compose up -d
```

Then point your DNS MX record at the server (see [DNS Setup](#dns-setup)).

## Configuration

All variables can be set in `.env` (recommended) or `config.json`. **Environment variables always take precedence.**

### Core Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `BASE_URL` | — | Public URL, e.g. `https://mail.example.com` (required) |
| `PORT` | `80` | HTTP listen port |
| `SMTP_PORT` | `25` | SMTP listen port |
| `SMTP_HOST` | `0.0.0.0` | SMTP bind address |
| `MAX_MAILS` | `50` | Max emails per mailbox |
| `MAIL_EXPIRE_MINUTES` | `10` | Email retention duration (minutes) |
| `FORBIDDEN_PREFIXES` | `admin,root,support` | Blocked email prefixes (comma-separated) |
| `SESSION_SECRET` | — | Session encryption key (**required**, generate with `openssl rand -hex 32`) |

### Auth Mode: Password (default)

Used when `OIDC_ISSUER_URL` is **not** set.

| Variable | Default | Description |
|----------|---------|-------------|
| `ADMIN_USER` | `admin` | Admin username |
| `ADMIN_PASSWORD` | `password` | Admin password (**must be changed**) |

Access `/admin` → enter credentials → manage settings.

### Auth Mode: OIDC

Used when `OIDC_ISSUER_URL` is set. All users must authenticate via the OIDC provider before accessing the site.

| Variable | Default | Description |
|----------|---------|-------------|
| `OIDC_ISSUER_URL` | — | OIDC issuer base URL, e.g. `https://auth.example.com` |
| `OIDC_CLIENT_ID` | — | Client ID registered in your OIDC provider |
| `OIDC_CLIENT_SECRET` | — | Client secret |
| `OIDC_REDIRECT_URI` | — | Must match redirect URI in provider config, e.g. `https://mail.example.com/auth/callback` |
| `USER_GROUP` | _(empty)_ | Group required to access the main site. Empty = any authenticated user |
| `ADMIN_GROUP` | _(empty)_ | Group required to access `/admin`. Empty = nobody can access admin |

> **OIDC provider setup**: register a confidential client with scopes `openid profile groups` and redirect URI pointing to `/auth/callback`. The `groups` claim must be returned by the userinfo endpoint.

#### Example: Authelia

```yaml
# authelia configuration.yml — identity_providers.oidc.clients
- client_id: tempmail
  client_name: TempMail
  client_secret: '$pbkdf2-sha512$...'   # use authelia crypto hash generate
  authorization_policy: one_factor
  consent_mode: implicit
  redirect_uris:
    - https://mail.example.com/auth/callback
  scopes:
    - openid
    - profile
    - groups
```

### Example .env (OIDC mode)

```env
BASE_URL=https://mail.example.com
SESSION_SECRET=<openssl rand -hex 32>

OIDC_ISSUER_URL=https://auth.example.com
OIDC_CLIENT_ID=tempmail
OIDC_CLIENT_SECRET=your_client_secret
OIDC_REDIRECT_URI=https://mail.example.com/auth/callback

USER_GROUP=users
ADMIN_GROUP=admins
```

## DNS Setup

To receive emails, add MX and A records:

```
mail.example.com.  IN  A   203.0.113.1
mail.example.com.  IN  MX  10  mail.example.com.
```

> Cloud providers typically block inbound port 25 by default. You may need to request unblocking.

## Admin Dashboard

Navigate to `/admin` (the path is not linked anywhere in the UI).

- **Password mode**: login form is shown; enter `ADMIN_USER` / `ADMIN_PASSWORD`
- **OIDC mode**: redirected through OIDC login; access granted only if your account is in `ADMIN_GROUP`

Settings available in admin:
- Email retention duration
- Max emails per mailbox
- Blocked email prefixes
- Admin credentials (password mode only)

## API Reference

### REST

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/config` | Get domain and expiry config |
| `GET` | `/api/mails/:addr` | List all mails for address |
| `GET` | `/api/mails/:addr/:idx` | Retrieve a specific email |
| `DELETE` | `/api/mails/:addr/:idx` | Delete a specific email |
| `GET` | `/health` | Health check |

### WebSocket (`/ws`)

**Client → Server:**
```json
{ "type": "request_mailbox" }
{ "type": "set_mailbox", "id": "custom_prefix" }
```

**Server → Client:**
```json
{ "type": "mailbox", "id": "abc123" }
{ "type": "mail", "mail": { "from": "...", "subject": "...", "body": "..." } }
{ "type": "mailbox_error", "code": "forbidden_prefix" }
```

## Tech Stack

| Component | Technology |
|-----------|------------|
| Backend | Go 1.22, `net/http`, `gorilla/websocket`, `gorilla/sessions` |
| SMTP | `emersion/go-smtp`, `emersion/go-message` |
| Auth | OIDC (authorization code + PKCE) or username/password |
| Frontend | Vanilla JS, TailwindCSS (bundled), Native WebSocket |
| Deployment | Docker, Alpine 3.19, single static binary |

## Security Notes

- Always set a strong `SESSION_SECRET` and `ADMIN_PASSWORD` in production
- Use HTTPS via a reverse proxy (Caddy, nginx) — never expose HTTP directly
- In OIDC mode, set `ADMIN_GROUP` to restrict `/admin` access; leave `USER_GROUP` empty only if all authenticated users should have access
- WebSocket origin validation is enabled
- Restrict inbound port 25 to trusted sources if your provider allows it

## License

MIT
