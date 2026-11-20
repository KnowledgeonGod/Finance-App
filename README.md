# Personal Finance Tracker API

A modular Go REST API for managing personal income, expenses, categories, monthly budgets, and reports. PostgreSQL stores account data; all user-owned records are scoped to the authenticated account.

## Code map

| Location | Responsibility |
|---|---|
| `cmd/api/` | Start the HTTP API and background session cleanup |
| `cmd/migrate/` | Apply database migrations as a separate deployment step |
| `internal/config/` | Read and validate environment configuration |
| `internal/database/postgres.go` | Configure the PostgreSQL connection pool and TLS |
| `internal/httpapi/server.go` | Register routes and configure HTTP middleware |
| `internal/httpapi/authentication_handlers.go` | Registration, login, logout, and password reset |
| `internal/httpapi/financial_handlers.go` | Category, transaction, report, and budget endpoints |
| `internal/httpapi/request_validation.go` | Validate request values before database access |
| `internal/httpapi/in_memory_rate_limiter.go` | Bounded development-only rate limiter |
| `internal/ratelimit/redis.go` | Shared Redis rate limiter for production |
| `internal/store/database_migrations.go` | Run/check schema versions and clean up expired sessions |
| `internal/store/migrations/` | Versioned PostgreSQL schema files |
| `internal/httpapi/templates/` | Server-rendered page templates and the frontend's CSS/JavaScript assets |
| `*_test.go` beside each package | Unit tests for that package’s behavior and error cases |

## Requirements and local startup

- Go 1.26+
- PostgreSQL 14+
- Redis 6+ for shared rate limiting (optional in development)

For local development, copy `.env.example` to `.env` and set `DATABASE_URL` in that file. Both Go commands load `.env` from the project directory; environment variables already set in the process take precedence. `.env` is excluded from Git and Docker build contexts.

Apply the schema once, using a migration-capable database role:

```powershell
if (-not (Test-Path .env)) { Copy-Item .env.example .env }
# Edit .env and set DATABASE_URL for your database.
go run ./cmd/migrate
```

Then start the API:

```powershell
go run ./cmd/api
```

For production, do not deploy `.env`; provide secrets through your deployment platform. Prefer a least-privilege API database role separate from the migration role.

For local development, `HTTP_ADDR` defaults to `:8080`; API rate limiting uses a bounded in-process limiter unless `RATE_LIMIT_REDIS_URL` is set. In production, `APP_ENV=production` requires verified PostgreSQL TLS (`sslmode=verify-full`), a TLS Redis URL (`rediss://...`), an HTTPS `PUBLIC_BASE_URL`, and authenticated SMTP. The API refuses to start until the latest migration is applied. Migrations are not run automatically by API instances; rerun the migration command after application upgrades.

Configuration: `APP_ENV` (`development`, `test`, or `production`), `HTTP_ADDR`, `DATABASE_URL`, `PUBLIC_BASE_URL`, `RATE_LIMIT_REDIS_URL`, `SMTP_HOST`, `SMTP_PORT` (default `587` when SMTP is configured), `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`, `DB_MAX_CONNS` (default 20), `DB_MIN_CONNS` (default 2), `DB_CONNECT_TIMEOUT` (default `5s`), and `DB_STATEMENT_TIMEOUT` (default `10s`). New accounts must verify their email before signing in. Verification links expire after 24 hours; in development, links are emailed when SMTP is configured and otherwise printed to the API terminal. Password-reset links are emailed when SMTP is configured; otherwise they are printed to the API terminal in development. Neither kind of link is included in HTTP responses. Production requires an HTTPS `PUBLIC_BASE_URL` and authenticated SMTP with STARTTLS. Production secrets belong in your deployment platform's secret manager, not in source control or container images.

## Deployment

The multi-stage `Dockerfile` builds a small, non-root runtime image containing both the API and a separate migration executable. Build it with:

```powershell
docker build -t finance-tracker-api .
```

Run the migration as a one-off deployment job using a migration-only database role before rolling out API replicas. For example, provide `DATABASE_URL` through your orchestrator's secret mechanism and run the image with `/usr/local/bin/migrate` as its command. Run API replicas with a separate role that has only the required application data permissions. Use a TLS-terminating ingress, configure per-client rate limiting there, and monitor `/livez` and `/readyz`. Do not expose PostgreSQL or Redis publicly.

The in-process limiter is for local development only and does not coordinate across instances. Production requires shared Redis rate limiting; limiter failures reject requests with `503` (fail closed). Forwarded client-IP headers are deliberately not trusted by the API, so configure ingress-level client-IP-aware limits as well.

CI runs tests, `go vet`, and a full build on pushes and pull requests. Before deployment, also run the security review, database migration checks, backup/restore drills, and deployment-specific integration tests.

## API

All API routes are under `/api/v1`. Register or log in to receive a bearer token; send it as `Authorization: Bearer <access_token>`. Tokens are random, expire after 24 hours, and only their SHA-256 hashes are stored. Passwords are hashed with bcrypt.

The root URL (`/`) serves a responsive browser dashboard built with HTML templates, CSS, JavaScript modules, and Tailwind utility classes. It supports account registration and login, password reset, logout, transaction creation/editing/deletion, categories, monthly budgets, alerts, reports, filtering, and pagination. The page and API share the same origin; the access token stays in tab-scoped `sessionStorage` and is cleared on logout or an expired session. Tailwind currently uses its CDN Play script for convenience; compile Tailwind to a local static stylesheet before a production deployment.

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/v1/auth/register` | Register with `email` and a `password` of at least 8 characters, including an uppercase letter and special character (maximum 72 bytes) |
| `POST` | `/api/v1/auth/login` | Authenticate |
| `POST` | `/api/v1/auth/logout` | Revoke the current token |
| `POST` | `/api/v1/auth/password-reset/request` | Request a one-time password-reset link (expires in 30 minutes) |
| `POST` | `/api/v1/auth/password-reset/complete` | Set a new password with a valid one-time reset token; revokes all active sessions |
| `GET`, `POST` | `/api/v1/categories` | List or create categories |
| `GET`, `POST` | `/api/v1/transactions` | List/filter or create transactions |
| `PUT`, `DELETE` | `/api/v1/transactions/{id}` | Update or delete a transaction |
| `GET` | `/api/v1/reports/monthly?month=YYYY-MM` | Monthly totals by transaction kind and currency |
| `GET`, `POST` | `/api/v1/budgets` | List or create monthly budgets |
| `GET` | `/api/v1/budgets/alerts?month=YYYY-MM` | Budget progress and alerts (80% threshold) |
| `GET` | `/livez` | Process liveness check |
| `GET` | `/readyz` | PostgreSQL and Redis readiness check |
| `GET` | `/healthz` | Legacy alias for readiness |

Amounts are positive integer minor units (`amount_cents`); currencies are three-letter uppercase codes. Transactions require `kind` (`income` or `expense`) and `occurred_on` (`YYYY-MM-DD`). `category_id` is optional and must belong to the authenticated user. Budgets have a month (`YYYY-MM`), an amount, and a currency; an optional category scopes a budget to that category. A monthly budget is unique per account, currency, and category scope.

Transaction list filters: `kind`, `category_id`, `start_date`, and `end_date`. Pagination uses `limit` (default 50, maximum 100) and `offset` (maximum 1,000,000).

Example transaction:

```json
{
  "kind": "expense",
  "amount_cents": 2599,
  "currency": "USD",
  "description": "Groceries",
  "occurred_on": "2026-11-18",
  "category_id": null
}
```

## Security and tests

- SQL statements use PostgreSQL parameters; client input is never interpolated into SQL.
- Login and registration have a stricter rate limit (10 per 15 minutes); other routes are limited to 120 requests per minute. The development limiter has a bounded key table. Behind a reverse proxy, configure network-level rate limiting as well; the API deliberately does not trust spoofable forwarded-IP headers.
- Production rate limits are stored in Redis so they are shared across API instances. The development in-memory limiter is bounded and intentionally not suitable for multiple replicas.
- Every response includes a generated `X-Request-ID`; structured access logs record the ID, method, path, status, and duration, without query strings or request bodies. Panics are recovered and do not expose panic details to clients.
- Schema migrations are versioned, serialized with a PostgreSQL advisory lock, and run as a separate deployment step; the API verifies the expected schema version on startup. A bounded background cleanup removes expired sessions.
- Password-reset tokens are random, stored only as SHA-256 hashes, single-use, expire after 30 minutes, and are cleaned up in bounded batches. Resetting a password revokes all account sessions. Reset requests return the same acknowledgement whether or not an account exists.
- Production configuration requires verified PostgreSQL TLS and TLS-enabled Redis. The API has bounded connection and request timeouts, graceful shutdown, and distinct liveness/readiness checks.
- JSON request bodies are capped at 1 MiB, unknown JSON fields are rejected, and output uses Go's HTML-safe JSON encoding. Responses include `nosniff`, frame denial, and no-store headers.
- Passwords, bearer tokens, and session records are not returned in errors; session tokens are stored only as hashes.
- Use HTTPS, secure database credentials, backups, and monitoring in production. Add a trusted-origin CORS policy only if a browser client needs cross-origin access.

Run unit and security-focused tests with:

```powershell
go test ./...
go test -cover ./...
```

`go vet ./...` and `go build ./...` are also run by CI. The current unit tests do not replace integration tests against PostgreSQL and Redis; run those in a deployment pipeline before promoting a release.
