# ContestSync — File-by-File Lookup

> Comprehensive file-by-file analysis of the `contestSync` repository.
> Generated as a living document while reading each source file.

---

## Project Overview

- **Module**: `github.com/0xarchit/contestsync`
- **Go version**: 1.26.2
- **Purpose**: Sync competitive-programming contests from 7+ platforms into a user's Google Calendar.
- **Binaries**: `cmd/server` (HTTP API), `cmd/worker` (background processor + health server).
- **Stack**: Chi router, pgx/v5, gorilla/sessions, segmentio/kafka-go, robfig/cron, go-redis, Google API, slog, AES-256-GCM, Lenis/GSAP (frontend).
- **External services**: Neon Postgres (1 writer + N read replicas), Aiven Kafka, Aiven Valkey, Google OAuth/Calendar, Telegram proxy, hCaptcha.

---

## Table of Contents

1. [Entry Points](#1-entry-points)
2. [Configuration](#2-configuration)
3. [Build / Deploy / Repo Metadata](#3-build--deploy--repo-metadata)
4. [HTTP API Layer](#4-http-api-layer)
5. [Auth / Crypto](#5-auth--crypto)
6. [Database](#6-database)
7. [Extractors (Platform Scrapers)](#7-extractors-platform-scrapers)
8. [Observability](#8-observability)
9. [Queue (Kafka / In-Memory)](#9-queue-kafka--in-memory)
10. [Scheduler](#10-scheduler)
11. [Sync Engine](#11-sync-engine)
12. [Models](#12-models)
13. [Migrations](#13-migrations)
14. [Web / Frontend](#14-web--frontend)
15. [Hugging Face Spaces](#15-hugging-face-spaces)
16. [GitHub Metadata / CI](#16-github-metadata--ci)
17. [Cross-cutting Concerns & Summary](#17-cross-cutting-concerns--summary)

---

## 1. Entry Points

### `cmd/server/main.go`

| Field | Detail |
|---|---|
| **Responsibility** | HTTP API binary bootstrap, wiring all subsystems, graceful shutdown, and static asset serving. |
| **Key symbols** | `main()` |
| **Dependencies** | `config`, `internal/api`, `internal/auth`, `internal/db`, `internal/observability`, `internal/queue`, `internal/scheduler`, `internal/sync`, `web` (as `ui`), Chi router, gorilla/sessions, redis/go-redis, godotenv. |
| **Interactions** | Loads `.env`, builds `Config`, initializes structured `slog` (JSON in prod, text otherwise), wires the Telegram observability manager, opens Neon DB write/read pool, opens Valkey client, selects session store (Valkey or cookie fallback), constructs `auth.Provider`, `sync.Syncer`, `queue.Queue`, `api.Handlers`, `api.AdminHandlers`, `scheduler.Scheduler`, and the Chi router. Registers global middleware (RequestID, SecurityHeaders, RateLimit, Compress, RequestLogger, Recoverer), public routes, auth-protected group, CSRF-protected group, admin group, and finally a wildcard static handler. Starts `http.Server` in a goroutine, blocks on `shutdownCtx` (SIGINT/SIGTERM), then drains queue and closes DB. |

**Observations / Issues**
- Duplicated bootstrap logic with `cmd/worker/main.go` (config load, slog setup, observability, DB init, Valkey init, syncer build, queue init, shutdown sequence). Should be extracted to a shared `internal/app` package.
- `slog.SetDefault` is called twice — once before observability wires its async Telegram handler, once after. This works but is slightly confusing.
- `staticSub` is loaded from an embedded `embed.FS`; if `ui.StaticFS` is missing or layout changes, the server will fatal at startup (acceptable).
- `adminHandlers.HealthCheck` is exposed at `/health` publicly (no auth). Acceptable for a load-balancer probe, but the worker binary implements a more thorough (auth-protected) health check. Inconsistency between server and worker health checks.
- The shutdown path calls `q.Drain()` but the defer for `q.Close()` runs after `Drain()`. The order is fine, but if `q.Drain()` itself panics, `Close` won't run. (No panic handling here.)
- `api.NewStaticServer` is not referenced elsewhere in the public docs — should be cross-referenced.
- `sched.OnEvent` is wired only on the server binary, but `scheduler` is conceptually independent. Worker should also get the same event hook (or it should be moved to a common place).
- Uses `log.Fatal` rather than `slog.Error` + `os.Exit(1)` in early boot, which bypasses the JSON logger. In production, the boot failure will be plain text while subsequent logs are JSON.
- Race-safe: signal handling uses `signal.NotifyContext`. Good.

**Security**
- TLS not terminated in this binary; assumes a reverse proxy. Documented in README but worth enforcing (e.g., HSTS in `SecurityHeadersMiddleware`).
- `api.RateLimitMiddleware(valkeyClient, 60, time.Minute)` is the global limit; admin group gets a tighter 10/15min. Reasonable.

**Performance**
- `middleware.Compress(5)` enables gzip level 5 globally. Good default.
- `http.Server` does not set read/write timeouts. Could allow slow-client attacks. Minor concern because the rate-limiter caps incoming traffic.

---

### `cmd/worker/main.go`

| Field | Detail |
|---|---|
| **Responsibility** | Background worker binary that consumes sync tasks from Kafka (or in-memory fallback) and exposes a health/admin HTTP server. |
| **Key symbols** | `main()` and two inline `http.HandlerFunc`s (root and `/health`). |
| **Dependencies** | `config`, `internal/auth`, `internal/db`, `internal/observability`, `internal/queue`, `internal/sync`, redis, godotenv. |
| **Interactions** | Same bootstrap pattern as server. Mounts: (a) `GET /` returning a static HTML stub, (b) `POST /health` returning a multi-service health report guarded by `X-Admin-Password` (constant-time compare, max 256 bytes), `GET /health` returning a minimal `{"status":"healthy"}`. Health probes Postgres (read replica `SELECT 1+1` then write `BEGIN/ROLLBACK`), Valkey (PING), and Kafka (`q.Health`). |

**Observations / Issues**
- Health check is duplicated/inconsistent: the GET response is `{"status":"healthy"}` even when downstream services are broken. Operators should rely on POST. Document this.
- The handler uses `tx.Rollback(checkCtx)` and ignores the error — fine for a liveness probe but worth a comment.
- The error path of `pool.WriteDB().Begin(checkCtx)` is assigned back to `pgErr`; the original error from the read replica check is lost. Should use `errors.Join` or chain. (Minor: only the first error matters for "is it healthy".)
- `subtle.ConstantTimeCompare` is used correctly (length-bounded input check first).
- The handler builds a 200+ line closure inside `main`; would be cleaner as a `worker/handlers.go` package.
- `q.Health` is a new contract; check that `queue.Queue` actually implements it (see `internal/queue/kafka.go:139`).
- `WORKER_PORT` defaults to `PORT` then `8081`. Subtle: if both are unset, you get `8081`, but if a user sets `PORT=9090` for the server and forgets `WORKER_PORT`, the worker also binds `9090` in the same Compose file → port collision. Default to `8081` always in worker context, or rename the env var.
- `tgManager` is wired to startup/shutdown events; cron events go to scheduler which is not used here (the worker doesn't run cron). Fine.

**Security**
- POST `/health` requires `X-Admin-Password`; GET `/health` is open. Could be abused for unauth probing. Acceptable for a public liveness endpoint, but `ALLOWED_ORIGIN` is not enforced.
- Plaintext password in header: protected only by TLS at the proxy layer.

**Performance**
- `http.Server` again has no read/write timeouts.
- Each health POST opens a Postgres transaction just to roll it back — adds load. Could use `SELECT 1` on the writer instead.

---

## 2. Configuration

### `config/config.go`

| Field | Detail |
|---|---|
| **Responsibility** | Centralized environment parsing. Builds the `Config` struct used everywhere. |
| **Key symbols** | `type Config struct`, `func Load() *Config` |
| **Dependencies** | `encoding/hex`, `log`, `os`, `strconv`. |
| **Interactions** | Called once from each binary's `main`. Result is passed into `db.Init`, `queue.New`, `api.Handlers`, `api.AdminHandlers`, and so on. |

**Observations / Issues**
- `Load()` calls `log.Fatalf` on bad hex for `SESSION_SECRET` and `ENCRYPTION_KEY`. Acceptable; these are boot-fatal conditions.
- `ENCRYPTION_KEY` is decoded as hex *and* later validated as exactly 32 bytes in `main.go`. The config layer could centralize this validation. The double check is a minor smell.
- The env-var names are inconsistent (`KAFKA_ACCESS_KEY` vs. `ProxySecretKey` vs. `TG_PROXY_URL`). Worth a `envconfig` migration.
- `WORKER_PORT` falls back to `PORT` then to `8081`. As noted above, this can collide.
- **`os.Getenv("KAFKA_ACCESS_KEY")` reads a multi-line PEM key with embedded `\n`**. The raw bytes will contain literal `\n` (two characters), not newlines. `godotenv.Load` typically does *not* interpret escape sequences, so the PEM may not be parseable downstream by `tls.X509KeyPair` → **potential bug**: queue startup will fail with a TLS error.
- `Config.SessionSecret` is `[]byte` but the server treats it both as Gorilla cookie key (in fallback) and as Valkey HMAC key. OK, but means the env var must be ≥ 32/64 bytes depending on the codec.
- No validation of `POSTGRES_DB` non-empty.
- `AllowedOrigin` is loaded but I don't see it enforced anywhere in this file's downstream yet (need to confirm in `internal/api`).
- `Config.Env == "production"` is the only "production" string; the server middleware checks `cfg.Env != "development" && cfg.Env != "dev" && cfg.Env != "local"` for cookie `Secure` flag. Three checks when a single `IsProduction()` helper would be clearer.

**Security**
- `SessionSecret`/`EncryptionKey` are kept in memory as `[]byte` — fine, but a `String()` method on Config would leak them. None defined.
- Defaults (e.g., `ConnLimit=800`, `PoolLimit=10000`) are very high; on a misconfigured deploy this could exhaust Postgres connections.

**Performance / Best-practice**
- Defaults at the call site (binary main) when it should be in `Load()`. E.g., the log-level default is `slog.LevelInfo` in main, not in config.
- Could expose a `Validate()` method instead of using `log.Fatalf` for fail-fast at boot.

---

## 3. Build / Deploy / Repo Metadata

### `Dockerfile.server` (27 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Multi-stage Docker build for the API server binary. |
| **Stages** | `golang:alpine` builder → `scratch` runtime. |
| **Key details** | `CGO_ENABLED=0`, `GOOS=linux`, `GOARCH=amd64`, tags `netgo osusergo`, `-trimpath`, `-buildvcs=false`, `ldflags="-s -w -extldflags -static"`. Copies only `ca-certificates.crt` and the binary. Runs as UID 65532. |

**Observations**
- The "alpine + scratch" combo is the right pattern.
- No `HEALTHCHECK` directive.
- `USER 65532:65532` is correct non-root.
- `EXPOSE 8080` is documentation only.

### `Dockerfile.worker` (27 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Multi-stage Docker build for the background worker binary. |
| **Note** | `EXPOSE 8080` is misleading — the worker listens on `WORKER_PORT` (default `8081`). Should be `EXPOSE 8081`. |

### `docker-compose.yml` (22 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Two-service Compose: `server` (8080) + `worker` (8081), each with `Dockerfile.{server,worker}` and `.env` env file. |
| **Observations** | No network, no volumes, no Postgres/Valkey/Kafka — all external. Reasonable for a SaaS that uses managed services. Missing: `depends_on`, restart policy granularity, healthcheck. |

### `.gitignore` (29 lines)

| Field | Detail |
|---|---|
| **Observations** | Ignores binaries, `.env`, `.antigravitycli`, `.opencode`, and screenshots (`*.png`). Note: `server.exe` and `worker.exe` are *committed* despite `*.exe` being ignored — likely checked in with `git add -f`. Worth cleaning. |

### `LICENSE` (21 lines)

Standard MIT license but with the placeholder `Copyright (c) 2026 Your Name` — should be replaced with the actual author/organization.

### `migrations/001_init.sql` (46 lines)

| Field | Detail |
|---|---|
| **Schema** | `users`, `contests`, `synced_events`, `oauth_states` plus 5 indices. |
| **Type** | `sync_status_type ENUM ('pending', 'syncing', 'success', 'failed')`. |
| **Constraints** | `users.platforms` is a `TEXT[]` with `CHECK (platforms <@ ARRAY[...])` enumerating the 7 platforms. `contests.platform` is a `TEXT CHECK` of the same 7 values. |
| **Cascade** | `synced_events.user_id` and `contest_id` cascade on delete. |
| **Indices** | `oauth_states(created_at)`, `contests(platform, start_time)`, `contests(start_time)`, `synced_events(contest_id)`, `synced_events(user_id)`, `users(google_id)`. |

**Observations**
- The `platforms <@` constraint is a `CHECK` that prevents adding unsupported platforms at the DB level. Good defense in depth.
- The enum and CHECK strings are duplicated in Go code (`extractor.Platforms`, `models`, `migrations`). Should share a constant.
- No `updated_at` on `users`. (Not strictly needed because we only ever insert/update OAuth tokens and preferences.)
- No migration tooling wired in (no `golang-migrate`, no `goose`). Schema must be applied manually.
- `oauth_states` is missing automatic cleanup of states used (i.e., after callback the state is deleted, but stale ones are cleaned by cron — see `scheduler.CleanupOAuthStates`).

### `go.mod` / `go.sum`

| Field | Detail |
|---|---|
| **Module** | `github.com/0xarchit/contestsync` |
| **Go** | `1.26.2` (very recent, aligns with the README badge). |
| **Direct deps** | chi, uuid, gorilla/sessions, pgx/v5, godotenv, robfig/cron, segmentio/kafka-go, oauth2, google.golang.org/api. |
| **Indirect** | redis/go-redis, klauspost/compress, puddle, pgpassfile, pgservicefile, securecookie, xxhash, httpsnoop, lz4, OpenTelemetry stack, gRPC, protobuf. |

**Observations**
- `redis/go-redis` is used in the codebase but is in the `// indirect` block of `go.mod`. Run `go mod tidy` to promote it.
- `golang.org/x/crypto` is in indirect only but is used by `auth`. Promote it.

---

## 4. HTTP API Layer

### `internal/api/handlers.go` (589 lines)

| Field | Detail |
|---|---|
| **Responsibility** | User-facing HTTP handlers for OAuth login/callback, profile, preferences, manual sync, account deletion, and platform list. |
| **Key symbols** | `type Handlers struct`, `ManualSync`, `GoogleLogin`, `GoogleCallback`, `Me`, `DeleteAccount`, `GetPlatforms`, `SavePreferences`, helpers `generateRandomString`, `verifyHCaptcha`. |
| **Dependencies** | pgxpool, redis, gorilla/sessions, oauth2, calendar v3, internal/auth, internal/extractor, internal/queue, models, slog, http. |

**Per-handler analysis**

#### `ManualSync`
- 15-minute per-user rate limit using Valkey cache key `user:last_sync_at:{id}` (TTL 1h) with DB fallback. If `lastSyncAt` < 15min, return `rate_limited`. Else publish a sync task.
- Uses ReadDB → Valkey read-through, WriteDB only for new sync task publication. Correct split.

#### `GoogleLogin`
- Generates a 32-byte hex `state`, inserts it into `oauth_states` table, sets a HttpOnly cookie with `Path="/auth/google/callback"` and `MaxAge=600`.
- Redirects to Google OAuth URL with `AccessTypeOffline` and `ApprovalForce` to force refresh-token issuance.
- **Security**: Cookie `Secure` flag computed via the same 3-way env check. The OAuth state is *only* checked against the DB on the callback; the cookie is a quick filter. Good.

#### `GoogleCallback`
- Validates `oauth_state` cookie matches query, then `DELETE FROM oauth_states WHERE state=$1 AND created_at > NOW() - INTERVAL '10 minutes' RETURNING state` (atomic one-shot).
- Exchanges code for tokens, fetches userinfo, encrypts refresh token with `auth.EncryptToken`, upserts user.
- Session is rotated: old session deleted, new one created, CSRF token stored. Publishes initial sync task.
- **Bug/Smell**: Lines 175-180 — `encryptedRefreshToken` is computed but on encryption failure is left as empty string and the upsert proceeds with an empty token. The `ON CONFLICT DO UPDATE` clause then has `CASE WHEN $3 <> '' THEN $3 ELSE users.refresh_token END`, so the previous token is kept if the new one is empty. Reasonable for backward-compat, but on *first* sign-in this means a user could end up with no refresh token. Should explicitly fail the request.
- **Bug/Smell**: Line 141 uses `created_at > NOW() - INTERVAL '10 minutes'` but the `oauth_states` row's `created_at` is at insert; the check is inverted (should be `> NOW() - INTERVAL '10 minutes'`) — actually it is correct: it accepts only states newer than 10min. Re-reads correctly.
- **Bug/Smell**: Line 215-216 — `session.ID = ""` and then re-assignment of values, but the next `Save` (line 224) is called *without* going through `New`. The gorilla `sessions.Session.Save` regenerates a new ID if empty. OK, but the pattern is non-obvious.
- **Bug/Smell**: Line 233 — publishing the initial sync is best-effort; failure is logged but not surfaced. Acceptable.

#### `Me`
- Cache → DB fallback. Reads from ReadDB, caches in Valkey.
- Includes `csrf_token` in response (used by `app.js`).
- **Smell**: The user struct is built manually instead of using `models.User` directly, because `models.User.RefreshToken` has `json:"-"` but `cachedUser.RefreshToken` does not. The duplicate struct is intentional to avoid leaking the encrypted token. OK, but worth a comment.

#### `DeleteAccount`
- Reads calendar_id + encrypted refresh_token; if `delete_google_data=true`, queries synced event IDs for upcoming contests.
- Spawns a goroutine to (a) delete Google calendar or events, (b) revoke OAuth token.
- Deletes the user (which cascades to `synced_events`).
- Invalidates user cache, expires session.
- **Bug/Smell**: Line 339 — the goroutine uses `context.Background()`, ignoring the request context. After the request returns, the goroutine continues. Good for completing long-running cleanup, but `slog` context won't have `request_id`. Acceptable.
- **Bug/Smell**: Revocation of OAuth via `http.PostForm` does not propagate errors well; an unsuccessful revoke is logged but the user is already deleted. Race window: if the goroutine is still running when the user re-signs in, the old token could be reused. Very minor.
- **Bug/Smell**: The `time.Sleep(50 * time.Millisecond)` between event deletes is a hard-coded rate-limit dodge. Hard-coded magic number. Could use Google batch endpoint or token-bucket.

#### `GetPlatforms`
- Returns the static list from `extractor.Platforms`, with Valkey caching.
- **Smell**: Caches the literal string `models.PlatformsCacheTTL` (24h). Could be served from the in-memory list without a cache; the cache is a "warm" path optimization.

#### `SavePreferences`
- Reads h-captcha if `HCAPTCHA_SECRET` is set.
- Validates platform list against allow-list.
- Cache → DB fallback to load current state, then diffs `req.Platforms` vs `currentPlatforms` (with multiset semantics via the count map). If changed, updates DB and invalidates cache.
- **Smell**: Lines 513-525 — the multiset comparison handles duplicates, but the request `Platforms` is `[]string` and the DB column is `TEXT[]`; a duplicate platform could be persisted. The earlier `allowed` check doesn't dedupe. (Idempotent storage, but worth rejecting.)
- **Smell**: This handler does not call `h.Queue.PublishSyncTask`; the SPA explicitly issues a follow-up POST `/sync`. Fine.

#### Helpers
- `generateRandomString` uses `crypto/rand` (correct).
- `verifyHCaptcha` posts to `https://hcaptcha.com/siteverify` with a 5s timeout. If the secret env is empty, returns `(true, nil)` — disables captcha. **This is a security risk** if the env var is missing in production (the deploy is a single env entry away from bypassing the captcha). Should fail-closed (return `false`) when the secret is empty in production.

**Security / Performance**
- All inputs bounded: `http.MaxBytesReader(w, r.Body, 1048576)` for 1MB JSON bodies. Documented in README. Good.
- `securecookie` codecs use the `SessionSecret`. The fallback cookie store is created with the same secret. Both correctly used.
- CSRF middleware is documented separately.

### `internal/api/admin_handlers.go` (187 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Admin-protected handlers: `UpdateContests`, `SyncAll`, and an enhanced `HealthCheck` that probes Postgres, Valkey, and Kafka. |
| **Key symbols** | `AdminHandlers`, `requireAdmin`, `UpdateContests`, `SyncAll`, `HealthCheck`, `checkPostgres`, `checkValkey`, `measure`, `componentStatus`, `healthResponse`. |

**Observations / Issues**
- `requireAdmin` uses `subtle.ConstantTimeCompare` with a 256-byte length cap. Good.
- `UpdateContests` triggers `Scheduler.RunExtraction`. `SyncAll` triggers `Scheduler.SyncAllUsers`. Both use `context.Background()`, ignoring the request. Acceptable.
- `checkPostgres` does `SELECT 1+1` on read pool, then `BEGIN` + `pg_sleep(0)` + `ROLLBACK` on write pool. `pg_sleep(0)` is a no-op; could just `ROLLBACK` after `BEGIN`. Minor.
- `checkValkey` writes a probe key with a 10s TTL, reads it, then deletes it. Good for a true round-trip test.
- `measure` returns `Microseconds() / 1000` — labeled "latency_ms" but technically it's ms. OK.
- The `HealthCheck` POST returns 503 with `"degraded"` when any sub-check fails. Useful for orchestration.

**Bug/Smell**
- `pgErr` from `checkPostgres` may lose information from both check stages. Should use `errors.Join`.
- The public `GET /health` (line 134-136) returns `200` even when downstream is broken. If the LB uses `GET`, it will keep routing traffic. This is consistent with the worker binary's behavior, but problematic for production. Should at minimum do a lightweight check (e.g., Valkey PING).

### `internal/api/middleware.go` (433 lines)

| Field | Detail |
|---|---|
| **Responsibility** | All cross-cutting middleware: rate limiting (multi-tier), request ID, security headers, request logging, auth, CSRF, and the `ValkeyStore` (Redis-backed gorilla session). |
| **Key symbols** | `rateLimiter`, `EarlyRateLimiter`, `VelocityCounter`, `getClientIP`, `RateLimitMiddleware`, `RequestIDMiddleware`, `SecurityHeadersMiddleware`, `RequestLoggerMiddleware`, `RequireAuth`, `CSRFMiddleware`, `ValkeyStore` (+ `NewValkeyStore`, `Get`, `New`, `Save`). |

**Observations**

#### Rate limiter
- Two-stage: `earlyLimiter` (process-local `EarlyRateLimiter`, default 150/10s) then a `globalLimiter` LRU (10000 entries) and/or a Valkey `TxPipeline` (Incr + Expire) if Valkey is configured. The admin group gets 10/15min.
- **Bug/Smell**: `earlyLimiter.Allow` uses `atomic.AddInt64` on a non-pointer counter that itself sits behind an RWMutex — the atomic and the mutex are redundant. Style smell.
- **Bug/Smell**: `EarlyRateLimiter` never evicts old entries → memory leak under sustained traffic. `globalLimiter` (LRU) does evict.
- **Bug/Smell**: `Retry-After` header is set to `strconv.Itoa(int(duration.Seconds()))` which is wrong for the per-minute `RateLimitMiddleware` if used with a 1-minute duration — it returns "60" only for that specific call. Inconsistent.
- **Bug/Smell**: When `TRUST_PROXY` is true, `getClientIP` reads `CF-Connecting-IP` first, then `X-Forwarded-For`. The first IP in XFF is the client; this is the right behavior. If the proxy chain is untrusted, the env is a footgun.
- The early limiter exists as a DoS guard before hitting Valkey. Good.

#### `RequestIDMiddleware`
- Generates a UUID, sets `X-Request-Id` header, propagates via `context.WithValue(ContextKeyRequestID)`. Logs "request started" — but `RequestLoggerMiddleware` also logs after. Two log lines per request. Minor.

#### `SecurityHeadersMiddleware`
- Sets CSP, X-Content-Type-Options, X-Frame-Options DENY, X-XSS-Protection=0 (correct — modern advice), Referrer-Policy, Permissions-Policy, and HSTS in production. The CSP allows `'unsafe-inline'` for `script-src` and `style-src`, which is needed for inline styles and GSAP. Could use hashes/nonces for stricter CSP, but acceptable for a marketing page.
- **Bug/Smell**: The CSP allows `connect-src https://api.github.com` (for the star counter) — that endpoint doesn't need CORS bypass unless the page is on a different origin. The browser does *not* honor CSP for outbound `fetch` initiated by JS in this case, but documenting.

#### `RequireAuth`
- Loads session "session", checks `user_id` int. On missing, expires the session cookie.
- **Smell**: `session.Save(r, w)` after `MaxAge=-1` is called even when `err != nil`. This is fine for invalidating the bad cookie, but the response is still 401.

#### `CSRFMiddleware`
- Reads `X-CSRF-Token` header, constant-time compare.
- **Smell**: `session, _ := store.Get(r, "session")` ignores the error, so if the session is corrupted, the check silently passes. Should at least log a warning.
- **Smell**: `len(expectedToken) != len(actualToken) || subtle.ConstantTimeCompare(...)` is good (length check first prevents length-based leakage via `subtle.ConstantTimeCompare` — which itself is constant-time regardless of length, so the explicit check is just a micro-optimization).

#### `ValkeyStore`
- Implements `gorilla/sessions.Store`. Uses `securecookie` to encode/decode the session ID, then fetches `session:{id}` from Valkey.
- The session payload is stored as JSON; integer values are encoded via `json.Number` to preserve them through round-trip.
- **Bug/Smell**: `Save` writes the cookie with `Secure = s.options.Secure` for the cleared-cookie path, but a fresh `cookie := sessions.NewCookie(session.Name(), "", session.Options)` already inherits `Options.Secure`. The explicit set is redundant.
- **Bug/Smell**: The session ID is generated using `securecookie.GenerateRandomKey(32)` and hex-encoded. The `codecs` use the same `keyPairs` for both encrypting the ID and HMAC-ing it. OK.

### `internal/api/middleware_test.go` (174 lines)

| Field | Detail |
|---|---|
| **Tests** | `TestRequestIDMiddleware`, `TestSecurityHeadersMiddleware`, `TestRequireAuth`, `TestRequireAuthUnauthorized`, `TestCSRFMiddleware`, `TestCSRFMiddlewareForbidden`, `TestRateLimitMiddleware`. |
| **Observations** | Uses `httptest.NewRecorder` and `gorilla/sessions.NewCookieStore` with a 12-byte secret (works because the cookie store is lenient). The rate-limit test exercises the in-memory fallback by passing `nil` Valkey. |

**Smell**: The cookie store with 12-byte key will likely warn at runtime ("securecookie: key length is too short"). The tests still pass.

### `internal/api/static.go` (294 lines)

| Field | Detail |
|---|---|
| **Responsibility** | In-memory static asset server with on-startup minification (CSS, JS, HTML) and SHA-256 ETag generation. |
| **Key symbols** | `StaticFile`, `MemoryFS`, `NewStaticServer`, `(*MemoryFS).ServeHTTP`, `getContentType`, `MinifyCSS`, `MinifyJS`, `MinifyHTML`, helpers `isCSSWordChar`, `isJSWordChar`. |

**Observations**
- Walks the `fs.FS` at startup, minifies by extension, computes ETags, stores in a `map[string]StaticFile`.
- `ServeHTTP` resolves `/` → `/index.html`, falls back to `path+".html"` for extension-less requests, checks `If-None-Match` → 304.
- Sets `Cache-Control: public, max-age=31536000, must-revalidate` — long cache, which is correct given content-hashed ETags.
- **Bug/Smell**: `MinifyCSS` — `runes[i-1] != '\\'` would panic when `i==0` and a closing quote appears at the very start. Defensive check needed.
- **Bug/Smell**: `MinifyJS` and `MinifyCSS` use rune-level scanning without semantic awareness; e.g., `MinifyJS` does not handle template literals with embedded `${...}` correctly, nor regex literals (`/foo/g`). For the existing app.js (no regexes/templates), it works.
- **Bug/Smell**: `MinifyHTML` strips comments and whitespace but does not collapse `>` `<` boundaries (e.g., `<p> <a>` → `<p><a>`). Standard minifiers do that. Cosmetic.
- **Performance**: A 10MB max static file size is more than enough.
- **Bug/Smell**: `defer f.Close()` inside the `fs.WalkDir` callback closes files eagerly. Not strictly needed because the next iteration opens a new file, but harmless. The linter `errcheck` would flag it.

### `internal/api/static_test.go` (69 lines)

| Field | Detail |
|---|---|
| **Tests** | `TestMinifiers` (CSS/JS/HTML), `TestStaticServer` (uses `fstest.MapFS`, checks 200, ETag, 304). |
| **Observations** | `TestMinifiers` checks the exact minified output, which is fragile if the minifier is improved. |

---

## 5. Auth / Crypto

### `internal/auth/auth.go` (83 lines)

| Field | Detail |
|---|---|
| **Responsibility** | AES-256-GCM token encryption/decryption and the Google OAuth `Provider` constructor. |
| **Key symbols** | `Provider`, `NewProvider`, `EncryptToken`, `DecryptToken`. |
| **Details** | `EncryptToken` generates a 12-byte nonce (GCM default), appends it to the ciphertext, base64-encodes the result. `DecryptToken` reverses. |

**Observations**
- AES-256-GCM is the right choice. The nonce is generated per-encrypt via `crypto/rand`. No nonce reuse possible.
- The key length is *not* validated inside `EncryptToken`/`DecryptToken`; it's validated upstream in `cmd/*/main.go` (`len(cfg.EncryptionKey) != 32`). Defensive `if len(key) != 32` inside the package would be safer.
- The `Provider.Scopes` only requests `userinfo.email` and `calendar.CalendarScope`. The `userinfo.profile` is not requested, but `id` is needed. The callback uses `oauth2/v2/userinfo` which returns `id`, `email`, etc. — works without the profile scope.
- **Smell**: The scope list is hard-coded inside the auth package; the OAuth consent screen won't show the user a "view your email address" permission if Google merges it. Acceptable.

### `internal/auth/auth_test.go` (74 lines)

| Field | Detail |
|---|---|
| **Tests** | `TestEncryptDecryptRoundTrip`, `TestKeySizeBoundary` (verifies invalid AES key size returns error), `TestCryptographicTampering` (flips a byte, expects decrypt to fail), `TestEmptyDecrypt` (empty input fails). |
| **Observations** | Uses a 32-byte ASCII key. Good coverage for the happy path and tampering. No benchmark. |

---

## 6. Database

### `internal/db/db.go` (101 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Postgres pool initialization: one write pool, N read pools, lock-free round-robin read selection. |
| **Key symbols** | `Pool`, `(*Pool).WriteDB`, `(*Pool).ReadDB`, `(*Pool).Close`, `newPool`, `Init`. |
| **Behavior** | `Init` calls `newPool` for the write DSN with `connLimit`. Then iterates comma-separated read DSNs, builds separate pools, and logs how many succeeded. `ReadDB()` uses an `atomic.Uint64` counter to distribute reads. If no reads configured, `ReadDB()` returns the write pool. |

**Observations / Issues**
- **Bug**: `p.readIdx.Add(1) - 1` overflows to `0` after `math.MaxUint64` calls (effectively never for a 64-bit counter). Fine.
- **Bug/Smell**: When no read pools connect, the warning `no read replicas configured, using write pool for reads` is logged at INFO, not WARN. Could mislead.
- **Bug/Smell**: Failed read replica connections are logged at WARN but not retried. If a replica is briefly unavailable, the system will only ever use the write pool.
- **Smell**: `cfg.ConnectionPoolLimit` is applied to *each* read pool. If there are 5 replicas and limit=10000, total = 50000 connections. Could exceed Postgres max_connections.
- **Performance**: Round-robin via atomic counter is excellent. No lock contention.
- **Smell**: `newPool` does not configure `MinConns`, `MaxConnLifetime`, `HealthCheckPeriod`, or `MaxConnIdleTime`. pgx defaults are used, which may not match the README's "3-min idle, 10-min lifetime" claim (those values are set on the Valkey client, not the pgx pool). The docstring and code disagree.

---

## 7. Extractors (Platform Scrapers)

### `internal/extractor/contest.go` (97 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Shared types and HTTP helpers for all scrapers. |
| **Key symbols** | `Contest`, `Platforms`, `Fetcher` type, `Fetchers` map, `httpClient`, `GenerateContestID`, `fetchJSON`, `fetchPage`. |
| **Behavior** | `Contest` uses Unix seconds for start/end/duration. `GenerateContestID(platform, startTime)` returns `"{platform}_{startTime}"` (collision risk if two platforms have the same start time, but the column is unique per id within a platform). The shared `httpClient` has a 10s timeout. `fetchJSON` and `fetchPage` set a 10MB response cap via `io.LimitReader`. |

**Observations / Issues**
- **Bug/Smell**: `User-Agent: Mozilla/5.0` is a default that may be blocked by some platforms (e.g., HackerRank). Each platform has its own strategy in README but the actual UA is uniform.
- **Bug/Smell**: The `Fetcher` type is `func() ([]Contest, error)`. The scheduler invokes them inside a worker goroutine. If a platform is down, the call blocks for 10s. The extraction task is a Kafka message; the consumer semaphore is 3 → at most 3 platforms can block at once.
- **Bug/Smell**: `Platforms` slice is the *only* place the platform list lives. The DB CHECK constraint duplicates it. Three other places (scheduler, API, frontend) consume this. If you add an 8th platform, you must update: (1) this slice, (2) `Fetchers` map, (3) migration CHECK, (4) `web/static/app.js` `colorMap`, (5) any hardcoded UI. Centralize via a single `model.Platforms` constant.
- **Bug/Smell**: `fetchJSON` and `fetchPage` are nearly identical. DRY them up.

### `internal/extractor/leetcode.go` (56 lines)

| Field | Detail |
|---|---|
| **Behavior** | POSTs a GraphQL query `{ allContests { title, titleSlug, startTime, duration } }` to `leetcode.com/graphql`. Filters out contests where `startTime+duration < now`. |

**Observations**
- Uses `float64` for `startTime` and `duration` (LeetCode returns floats). Casts to int64.
- URL is `https://leetcode.com/contest/{slug}`. Good.
- **Smell**: No retry/backoff. The shared `httpClient` has a single 10s timeout.
- **Smell**: Doesn't strip `title` whitespace or handle Unicode in `titleSlug`. Probably fine in practice.

### `internal/extractor/codeforces.go` (52 lines)

| Field | Detail |
|---|---|
| **Behavior** | GETs `https://codeforces.com/api/contest.list`. Requires `Status == "OK"`. Filters `Phase == "BEFORE" && startTimeSec >= now`. URL is `https://codeforces.com/contestRegistration/{id}`. |

**Observations**
- Uses `fmt.Sprintf("%.0f", c.ID)` to convert a float to an integer. Lossy for IDs > 2^53 but Codeforces IDs are < 10^5. OK.
- `float64` for ID is unnecessary; CF returns ints. Could use json.Number or int64.
- **Smell**: URL pattern is `contestRegistration` rather than `contest/{id}`. Bug? The README says it's a registration page.

### `internal/extractor/codechef.go` (59 lines)

| Field | Detail |
|---|---|
| **Behavior** | GETs `https://www.codechef.com/api/list/contests/all?...`. Parses `future_contests`. Tries RFC3339 first, then `2006-01-02T15:04:05` for dates without timezone. URL is `https://www.codechef.com/{code}`. |

**Observations**
- No past-contest filter — only `future_contests` is read. So no `now` check needed.
- The dual parser handles missing timezone in the API response. CodeChef usually sends ISO with TZ, so RFC3339 should always work.

### `internal/extractor/atcoder.go` (169 lines)

| Field | Detail |
|---|---|
| **Behavior** | HTML scraper for `https://atcoder.jp/contests/`. Locates the second `<tbody>` (the upcoming contests table), then iterates `<tr>` rows. Each row's first cell contains an `<a href="/contests/abc?iso=YYYYMMDDTHHMM">`, the second cell has the contest name link, the third has `HH:MM` duration. |

**Observations**
- Pure string parsing — no `golang.org/x/net/html` parser. Will break on any AtCoder redesign.
- The `secondTbody` heuristic is fragile (depends on a "dummy" first tbody existing).
- `time.Parse("20060102T1504", ...)` parses the ISO string from the link.
- **Bug/Smell**: `stripTags` does not decode HTML entities like `&amp;` → `&`. Contest names with ampersands will display wrong.
- **Bug/Smell**: `extractHref` does not handle `href='...'` (single quotes). AtCoder uses double quotes, so OK.
- **Performance**: `strings.Index` is O(n) and called many times per page. For a small page (~20 contests) it's fine. Could be replaced with a proper HTML parser for robustness.
- **Robustness**: `extractCells` returns cells; if the table has `<th>` instead of `<td>`, it skips them. AtCoder uses `<td>` so OK.

### `internal/extractor/hackerrank.go` (71 lines)

| Field | Detail |
|---|---|
| **Behavior** | GETs `https://www.hackerrank.com/community/engage/events`. Parses `data.events.ongoing_events`. **Important security check**: `parsed.Scheme != "https" || !strings.HasSuffix(parsed.Host, "hackerrank.com")` → skip. |

**Observations**
- The host suffix check is a great defense against malicious redirects — HackerRank's API could in theory return any URL.
- **Smell**: `Ongoing` from the JSON is named "ongoing" but the comment in the test says these are upcoming events. Mislabeled in HackerRank's API.
- **Smell**: The URL `microsite_url` is used as-is. Should canonicalize to `https://www.hackerrank.com/{slug}` to avoid storing 3rd-party domains.

### `internal/extractor/geeksforgeeks.go` (55 lines)

| Field | Detail |
|---|---|
| **Behavior** | GETs `https://practiceapi.geeksforgeeks.org/api/vr/events/?...`. Parses `results.upcoming`. URL `https://practice.geeksforgeeks.org/contest/{slug}`. |

**Observations**
- `practiceapi.geeksforgeeks.org` is unofficial; could break.
- No host-suffix check (cf. HackerRank). Trust is implicit.

### `internal/extractor/code360.go` (49 lines)

| Field | Detail |
|---|---|
| **Behavior** | GETs `https://www.naukri.com/code360/api/v4/public_section/contest_list`. Parses `data.events`. Filters events where `registration_end_time >= now`. |

**Observations**
- `event_start_time` and `event_end_time` are float64 (Unix seconds). No timezone info.
- The `registration_end_time` filter is a nice touch — drops events you can no longer join.
- **Smell**: No error if `data.events` is nil/missing.

### `internal/extractor/extractor_test.go` (313 lines)

| Field | Detail |
|---|---|
| **Pattern** | Replaces `httpClient.Transport` with a `mockTransport` that returns canned JSON. Tests every extractor. |
| **Tests** | `TestFetchCodeforces`, `TestFetchCodechef`, `TestFetchLeetcode`, `TestFetchAtcoder`, `TestFetchHackerrank` (with a security test for non-HackerRank URLs), `TestFetchGeeksforgeeks`, `TestFetchCode360`, `TestFetchErrorStatus`. |
| **Observations** | The mock transport pattern is clean. The atcoder test is the most fragile (depends on the exact HTML structure of the test fixture). The hacker rank test verifies the URL validation. |

**Smell**: `setMockTransport` mutates a global `httpClient`. Not safe under `t.Parallel()`. None of the tests use `t.Parallel()` so it's fine.

---

## 8. Observability

### `internal/observability/telegram.go` (321 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Async Telegram error/warning notifier via a worker HTTP proxy. Wraps the standard `slog.Handler` to forward warn/error records. |
| **Key symbols** | `TelegramConfig`, `TelegramClient`, `EscapeHTML`, `SplitMessage`, `TelegramHandler`, `Manager`, `Init`. |
| **Behavior** | `Init` returns `(*Manager, slog.Handler)`. The handler is a `slog.Handler` decorator that calls the next handler and, for `Level >= Warn`, formats the message as HTML and pushes it onto a buffered channel (1000 entries). A goroutine drains the channel and POSTs to the proxy with retry/backoff and 3-strike cooldown. |

**Observations / Issues**
- `EscapeHTML` strips null bytes and escapes `&<>` but not `"` or `'`. Acceptable for Telegram HTML.
- `SplitMessage` splits on the last `\n` within a 4000-byte limit. The `idx == 0` fallback to `1` is a safety net but means the leading character is lost; could index the byte at the limit instead.
- The `TelegramClient` has 3-retry-per-message with exponential backoff plus a 5-min cooldown after 3 consecutive failures. Sends each chunk sequentially. No global rate limit.
- `TelegramHandler.Handle` is goroutine-safe via the channel. The default `select { case ... default: }` silently drops messages if the buffer is full. Better: log a metric, but not critical.
- **Smell**: The 30-second wait at the start of `Manager.Start` is to let the system boot before sending the first message. Magic number, undocumented.
- **Smell**: The `Drain` method uses `m.cancel()` which signals shutdown; the goroutine then drains remaining queue messages with 5s per-message timeout. Total drain time is unbounded.
- **Smell**: The `Manager.Handler` returns a `NewHandler` every call, which is a fresh struct that shares the queue and client. So `slog.SetDefault(slog.New(tgManager.Handler(textHandler)))` works correctly. But `TelegramHandler.WithAttrs` creates a new handler per `WithAttrs` call — fine but could share more state.

**Security**
- Bearer token (`PROXY_SECRET_KEY`) is sent in the Authorization header. Correct.
- HTML injection is escaped. Correct.

**Performance**
- Channel buffer = 1000; on burst this is enough for ~10s of mid-rate errors.
- Single consumer goroutine → if Telegram is slow, the queue fills.

### `internal/observability/telegram_test.go` (36 lines)

| Field | Detail |
|---|---|
| **Test** | `TestIntegrationTelegramSend` — loads `.env` and actually sends a real message. Skips if env vars missing. |
| **Observations** | This is an *integration* test that hits the real proxy. It is currently the only test in this file and not skipped by default — meaning `go test ./internal/observability/...` will hit the network. Add a build tag (`//go:build integration`) or rename to `*_integration_test.go` to keep `go test ./...` hermetic. |

---

## 9. Queue (Kafka / In-Memory)

### `internal/queue/kafka.go` (492 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Dual-mode queue: Kafka (production) or in-memory channels (fallback). Implements publish/consume for extraction and sync tasks, plus cache invalidation, topic auto-creation, and a health check. |
| **Key symbols** | `TopicExtraction`, `TopicSync`, `TopicHealth`, `TaskExtraction`, `TaskSync`, `Queue`, `New`, `ensureTopics`, `Health`, `createTLSConfig`, `PublishExtractionTask`, `PublishSyncTask`, `InvalidateContestsCache`, `StartConsumers`, `consumeExtractionInMemory`, `consumeSyncInMemory`, `consumeExtraction`, `consumeSync`, `Drain`, `handleExtraction`, `logDatabaseContestsTelemetry`, `Close`. |

**Observations / Issues**

#### `New` and `ensureTopics`
- Falls back to in-memory when `KAFKAHost` is empty.
- Builds a `kafka.Writer` with `LeastBytes` balancer and `Async: false`. Synchronous write is correct for durability.
- `ensureTopics` dials, lists partitions, creates missing topics. Topics default to NumPartitions=1 for extraction/health, `cfg.KafkaPartitions` for sync. Replication factor comes from config.

**Smell**: The `KafkaPartitions` default in `config.go` is 2. The README says 4. Mismatch if env is missing.

#### `Health`
- Dials the leader of the `health-check-tasks` topic, reads offsets, writes a probe message, seeks to the read offset, reads, and compares. This is a strong end-to-end probe but **it leaves the probe message in the topic** every time. After many calls the topic will have many probes. The producer should clean up.

#### `createTLSConfig`
- Loads the PEM key/cert pair and CA. If any is missing, returns `nil` → no TLS. **In production with Aiven Kafka, TLS is required, so this fallback is intentional.** OK.

#### `PublishExtractionTask` / `PublishSyncTask`
- Both have in-memory and Kafka paths. In-memory uses `select { case ch <- ...; case <-ctx.Done(); default: return ErrQueueFull }`. The `default` path means the channel is *non-blocking*. A 100-element buffer for extraction tasks is small. The sync channel is 1000.
- Kafka path uses `WriteMessages` which the Writer batches with `BatchSize` (default 100) and `BatchTimeout` (default 1s). The synchronous flag forces per-message flushes. Could be slow under load.
- **Bug/Smell**: When `WriteMessages` returns an error, the caller (e.g., `ManualSync`) returns 500, but the message may have been written successfully if the error is from the close. The kafka-go library returns a slice of errors in the result; the code doesn't check that.

#### Consumers
- `consumeExtraction` / `consumeSync` use a semaphore of 3/10 respectively. Each message is processed in a goroutine that runs `handleExtraction` / `Syncer.SyncUser`.
- **Bug/Smell**: `q.wg.Add(1)` is called inside the loop *after* `sem <- struct{}{}`. If the consumer's goroutine panics before `wg.Done`, the WaitGroup never returns. There's no `recover`. Should be in the consumer's deferred function.
- **Smell**: The consumer reads `cfg` and re-creates the TLS config and dialer. Wasteful — `New` already builds these.

#### `handleExtraction`
- Fetches contests for the platform. If 0 contests, deletes all upcoming contests for that platform and invalidates cache. Otherwise:
  1. Prune obsolete upcoming contests: `DELETE FROM contests WHERE platform=$1 AND start_time > NOW() AND id != ALL($2)`.
  2. Batch insert with `ON CONFLICT (id) DO UPDATE` — uses `pgx.Batch` and `SendBatch`.
  3. Invalidate cache.
  4. Log telemetry.
- **Smell**: The prune query uses `start_time > NOW()`. The migration's `users.last_sync_at` is per-user. The README's section 3 (in the architecture doc) says the same thing — boundary integrity is preserved.
- **Smell**: The batch size is unbounded. If a platform returns 10K contests, we send 10K INSERTs in one batch. pgx's default batch limit is huge but the connection holds a transaction the whole time. Could be split.

#### `logDatabaseContestsTelemetry`
- Logs a `slog.Info` with `platformCounts` as a list of alternating keys/values. This relies on `slog`'s variadic `...any` semantics and `Info(msg, args...)` to interpret them. Clean.

#### `Drain` / `Close`
- `Drain` waits on the WaitGroup. `Close` closes the Kafka producer. The shutdown order in `cmd/server/main.go` and `cmd/worker/main.go` is `Drain` then `Close` (via `defer Close()`), which is correct.

### `internal/queue/queue_test.go` (67 lines)

| Field | Detail |
|---|---|
| **Tests** | `TestInMemoryQueuePublish` (publishes a sync task, reads from channel), `TestQueueDrain` (verifies Drain blocks on the WaitGroup). |
| **Observations** | Tests use a `cfg` with empty `KafkaHost` to force in-memory mode. They don't test the Kafka path. Add a Kafka test that uses `miniredis` or a mock dialer. |

---

## 10. Scheduler

### `internal/scheduler/scheduler.go` (122 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Cron-based orchestration: daily extraction, daily sync-all, daily data prune, every-15-min OAuth state cleanup. |
| **Key symbols** | `Scheduler`, `New`, `Start`, `PruneOldData`, `CleanupOAuthStates`, `SyncAllUsers`, `RunExtraction`, `Stop`, `OnEvent` (callback). |

**Observations / Issues**
- All cron entries are added with `s.Cron.AddFunc(...)` but the `slog.Info` "starting" log is missing — operators can't see what's scheduled.
- The three `@daily` entries fire at slightly different times depending on startup; could be deterministic.
- **Smell**: `PruneOldData` runs `@daily`. Deleting contests older than 30 days is fine, but contests with synced events will leave dangling `synced_events` rows (though `ON DELETE CASCADE` on the foreign key handles this).
- **Smell**: `SyncAllUsers` uses `ReadDB` to list users. If the read replica is lagging, new users won't sync. Acceptable for a daily task.
- **Smell**: The `OnEvent` callback (used to send Telegram notifications) is wired only in `cmd/server/main.go`. In production, the server runs the cron. If the server is down, no cron. Two-process setup: cron should arguably run on the worker.
- **Bug/Smell**: `PruneOldData` invalidates all platform caches but only after the DELETE. The cache may have been just-repopulated by a parallel user sync. The 12h TTL on contests means this is OK; cache races are bounded.

---

## 11. Sync Engine

### `internal/sync/sync.go` (405 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Per-user sync: load user + preferences, decrypt OAuth refresh token, build a Google Calendar client, create/find a dedicated calendar, fetch upcoming contests, and create events with retry+backoff. |
| **Key symbols** | `Syncer`, `(*Syncer).SyncUser`, `GenerateDeterministicEventID`, `(*Syncer).handleSyncError`. |
| **Behavior** | `SyncUser(ctx, userID)` acquires a Valkey `lock:sync:{userID}` (5min TTL) or a `sync.Map` fallback. Loads user from cache or DB. Decrypts token. Creates Calendar service. Gets primary calendar timezone. If `use_dedicated`, creates a "ContestSync" calendar. Loads synced events from cache/DB. Loads contests from cache/DB. For each contest not yet synced, builds a deterministic `event.Id` from `md5(googleID + contestID)`, inserts it via `Events.Insert` with up to 3 retries (handles 409 conflict as "already exists, record it"). Updates `synced_events` table. |

**Observations / Issues**

#### Lock
- `s.Valkey.SetNX(ctx, lockKey, "1", 5*time.Minute)` — good. On unlock, `Del(context.Background(), ...)` ignores request context cancellation. OK.
- In-memory fallback uses `sync.Map.LoadOrStore`. Good.
- **Bug/Smell**: 5min lock TTL is longer than the average sync (1-2min). A crash leaves the lock until TTL. Acceptable for safety.

#### Caching
- Three layers: user cache (24h), synced-events cache (24h), contests cache (12h, per platform). Each loader has the same read-then-cache pattern. Duplicated code (also duplicated in `handlers.go`).
- **Bug/Smell**: When `s.Valkey == nil` AND `s.ReadDB == nil`, the code falls back to `s.DB` (write). So all reads hit the writer. The `db.Pool.ReadDB()` returns the writer if no read replicas. This means the read/write split is broken when there are no replicas. Acceptable.
- **Bug/Smell**: The contests cache uses one Valkey key per platform. The loader iterates platforms and reads each. Could be batched with MGET. Negligible for 7 platforms.
- **Bug/Smell**: If a platform is in the user's list but the cache key is missing, we re-read the platform contests from DB and write to cache. If the DB row count is large, this happens every sync. Mitigation: cron re-populates; manual sync re-uses.

#### Event ID
- `GenerateDeterministicEventID(googleID, contestID) = base32hex(md5(googleID + "_" + contestID))`. Lowercase, no padding, 32 chars.
- The regex test in `sync_test.go` checks `^[a-v0-9]+$` — base32hex uses 0-9a-v. Correct.
- **Bug/Smell**: The separator `_` means `(googleID="a", contestID="bc")` and `(googleID="ab", contestID="c")` produce different IDs. Safe.

#### `Events.Insert`
- Up to 3 attempts. On 409 conflict (already exists), treats it as success and writes the `synced_events` row.
- Backoff is 500ms × attempt. No jitter. (Google's recommended exponential backoff is 2^n seconds with jitter; this is fast and could trigger rate limiting at scale.)
- `handleSyncError` detects `invalid_grant` and deletes the user. Good (the OAuth token is dead, the account is unrecoverable).
- **Bug/Smell**: The retry loop's `s.handleSyncError` is called *during* the loop but the returned `deleted` is discarded. If the user is deleted mid-sync, the loop continues. Minor.
- **Bug/Smell**: The loop uses `time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)` — fixed multiplier, not exponential. Mentioned above.
- **Bug/Smell**: If `res` (the inserted event) is nil after 3 attempts and the error is not 409, the code falls through to `slog.Error` and continues. No insert record → no cache invalidation. Next sync will retry. Correct.

#### Errors
- All `slog.Error` calls swallow the error to logs. There's no surfaced error path back to the user; the user just sees a "queued" response.
- **Bug/Smell**: The deferred function updates `sync_status` and `last_sync_at` using `context.Background()`. If the user closed the connection, the write still happens. Good.

### `internal/sync/sync_test.go` (68 lines)

| Field | Detail |
|---|---|
| **Tests** | `TestGenerateDeterministicEventID` (deterministic, unique per input, regex format), `TestHandleSyncError` (nil, `invalid_grant`, generic error). |
| **Observations** | No integration test for full sync. Adding a test fixture with `httptest.Server` and a fake Calendar API would be valuable. |

---

## 12. Models

### `models/models.go` (73 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Centralized struct definitions and cache key/TTL constants. |
| **Key symbols** | TTL constants (`UserCacheTTL=24h`, `ContestsCacheTTL=12h`, `SyncedEventsCacheTTL=24h`, `PlatformsCacheTTL=24h`), key helpers (`UserCacheKey`, `ContestsCacheKey`, `SyncedEventsCacheKey`, `PlatformsCacheKey`), structs `User`, `CachedUser`, `Contest`, `UserPlatformPreference`, `SyncedEvent`. |

**Observations / Issues**
- `User.RefreshToken` has `json:"-"` (correct, don't leak the encrypted token).
- `CachedUser` does not have `json:"-"` on `RefreshToken` because it is *only* used in cache serialization (not exposed to the API). The `Me` handler manually builds a response struct.
- `Contest` uses `time.Time` (not `int64`) for `StartTime`/`EndTime`. In contrast, the `extractor.Contest` uses `int64` (Unix seconds). There's a translation in `queue.handleExtraction` (`time.Unix(c.StartTime, 0)`).
- `Duration` is `int` in `models.Contest` but `int64` in `extractor.Contest`. Same conversion.
- `UserPlatformPreference` is defined but not used anywhere in the codebase (search confirms). Dead code; remove or implement.
- `UpdatedAt` on `Contest` is set by the DB default `NOW()` on update. The model also has a field for it; OK.

### `models/models_test.go` (41 lines)

| Field | Detail |
|---|---|
| **Test** | `TestCacheKeysAndTTLs` — verifies all four TTLs and all four key formats. |
| **Observations** | Very tight. Could add property-based tests for cache key format (e.g., no special characters in user ID). |

---

## 13. Migrations

See `migrations/001_init.sql` summary in [Section 3](#3-build--deploy--repo-metadata).

**Cross-references**
- `users.refresh_token` is the AES-encrypted blob from `auth.EncryptToken` (`internal/auth/auth.go:35`).
- `users.platforms` is the `TEXT[]` filtered by the `<@` CHECK; the allowed list is duplicated in `extractor.Platforms` and `web/static/app.js` `colorMap`.
- `oauth_states` is populated in `GoogleLogin` (`internal/api/handlers.go:105`) and consumed in `GoogleCallback` (`internal/api/handlers.go:141`); cleaned up by `scheduler.CleanupOAuthStates`.
- `synced_events.user_id` and `.contest_id` are cascade-deleted from `users` and `contests`.

---

## 14. Web / Frontend

### `web/assets.go` (6 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Embeds `web/static/*` into the binary. |
| **Note** | The directive `//go:embed all:static/*` includes hidden files; in practice only the visible static files are present. |

### `web/static/index.html` (792 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Marketing landing page. |
| **Sections** | Header (logo + GitHub star widget), Hero, KPI strip, Problem, Solution (3-step flow), Platforms (7 cards), Calendar preview (decorative), FAQ (6 items), Final CTA, Footer. |
| **Tech** | Loads Google Fonts (IBM Plex Mono, Syne), GSAP + ScrollTrigger via CDN, Lenis (smooth scroll) via unpkg, local `style.css` and `app.js`. Includes `h-captcha` script on preferences page only. |

**Observations**
- `google-site-verification` meta tag is hard-coded — fine for site ownership verification but should not change per environment.
- CSP allows `cdnjs.cloudflare.com` and `unpkg.com`. These CDNs are reliable but a future CSP tightening would need to inline or self-host.
- Inline styles and SVG in the markup are extensive. Fine for a single-page app.

### `web/static/preferences.html` (168 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Preferences dashboard (authenticated users only). |
| **Form** | Checkbox list of platforms (populated by `app.js`), "use dedicated calendar" checkbox, h-captcha widget, submit button. Also a "delete account" section with optional "delete Google data" checkbox. |

**Observations**
- hCaptcha site key is hard-coded in the markup. If the project ever ships multiple environments, the key would need to be templated.
- The `h-captcha` div is always rendered even when `HCAPTCHA_SECRET` is unset on the server. In that case the captcha is a no-op, but the user still sees it. Better to conditionally render.

### `web/static/about.html` (326 lines)

| Field | Detail |
|---|---|
| **Responsibility** | About page. |
| **Observations** | Heavy use of inline styles (similar to other pages). The "Star on GitHub" widget is duplicated from the home page. Could be a partial. |

### `web/static/privacy.html` (261 lines)

| Field | Detail |
|---|---|
| **Responsations** | Privacy policy. |
| **Observations** | **HTML bug at line 254**: `<a href="terms style="text-decoration: none"` is malformed — the `href` attribute is missing a closing quote before `style=`, and the `>` after the href is also missing. Browser parsers will recover (most will treat `terms` as the href value, then `style` as a new attribute), but the "Terms of Service" link will not navigate correctly. **Fix**: `<a href="terms" style="text-decoration: none">Terms of Service</a>`. |

### `web/static/terms.html` (212 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Terms of service page. |
| **Observations** | Footer link uses `terms.html` (the explicit filename), while `privacy.html` and `about.html` use bare names. The static server's fallback to `.html` makes all three work, but the inconsistency should be cleaned up. |

### `web/static/app.js` (666 lines)

| Field | Detail |
|---|---|
| **Responsibility** | All client-side logic. |
| **Key symbols** | `initApp`, `initPreferences`, `initGlobalInteractivity`, `initFAQ`, `fetchGitHubStars`, `securePost`, `getCSRFToken`, `showSuccess`, `showToast`. |

**Observations / Issues**
- Two page-entry points: home page runs `initApp`; preferences page runs `initPreferences`. They share `initGlobalInteractivity` and `initFAQ`.
- **Performance**: Lenis (smooth scroll) is initialized for all pages but only useful on the home page. The `isPreferencesPage` check at line 17 short-circuits before Lenis, so the preference page doesn't pay the cost. Good.
- **Performance**: The custom cursor is added globally via `initGlobalInteractivity`. It's heavy (GSAP animations on every `mousemove`). The 1-second `setInterval` that re-binds hover listeners is a hack to catch dynamically added elements (the success view). Should use a `MutationObserver` instead.
- **Performance**: `gsap.ticker.add((t) => { lenis.raf(t * 1000); })` runs on every frame. Acceptable but expensive on mobile.
- **Bug/Smell**: `securePost` and `getCSRFToken` use a module-level `csrfToken` variable set by `initPreferences`. If the page is opened in two tabs and the user logs out in one, the other still has the old token. (Sessions are per-tab anyway, so this is academic.)
- **Bug/Smell**: `fetchGitHubStars` is called both from `DOMContentLoaded` and from `window.load`. Could double-fire and waste an API call (the GitHub API has rate limits).
- **Accessibility**: The FAQ uses `<details>`/`<summary>` which is accessible by default. The custom FAQ click handler in `initFAQ` is redundant and could break keyboard navigation. Recommend removing `initFAQ` or moving to `<details>` only.
- **Accessibility**: The custom cursor with `cursor: none` on touch devices is correctly disabled via `@media (hover: none)`. Good.
- **Accessibility**: The captcha is required for the preferences form. The error message says "Please complete the CAPTCHA challenge" — accessible.
- **Security**: All POSTs include `X-CSRF-Token` header. Good.
- **Security**: The captcha response is included in the body as `h-captcha-response`. Server validates it. Good.
- **Bug/Smell**: `success-state` shows a hard-coded "Open Google Calendar" link. The link has `target="_blank" rel="noopener noreferrer"`. Good.

### `web/static/style.css` (1688 lines)

| Field | Detail |
|---|---|
| **Responsibility** | All visual styling. |
| **Observations** | Custom design system (no Tailwind/CSS-in-JS). Uses CSS custom properties (`--bg-base`, `--accent-cyan`, etc.) and a Lenis/GSAP-friendly typography scale (`clamp()`). Heavy use of `position: fixed` overlays (noise, scanlines, grid, vignette, custom cursor) for the cyberpunk aesthetic. |
| **Performance** | The `mix-blend-mode` and `position: fixed` overlays can be GPU-expensive on low-end devices. Consider `prefers-reduced-motion` and `prefers-reduced-transparency` queries. |
| **Accessibility** | `cursor: none` for non-touch devices is jarring for users with motor disabilities. Consider a media query to disable. |
| **Bug/Smell** | Several duplicate `html, body { ... }` blocks (lines 58 and 87). Cosmetic. |

### `web/static/icon.webp`

Binary asset (logo). Not analyzable for content; tracked for completeness.

---

## 15. Hugging Face Spaces

### `hf/server/Dockerfile` (9 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Wraps the published server Docker image for HuggingFace Spaces. |
| **Note** | Sets `ENV PORT=7860` and `ENV ENV=production`. The server binary defaults to 8080; this env var is needed for HF's port expectation. The README frontmatter (in `hf/server/README.md`) also sets `app_port: 7860`. |

### `hf/server/README.md` (9 lines)

HuggingFace Spaces metadata: title, emoji, gradient color, `sdk: docker`, `app_port: 7860`.

### `hf/worker/Dockerfile` (9 lines)

Same pattern as server, but copies the worker binary and sets `ENV PORT=7860`.

### `hf/worker/README.md` (9 lines)

Worker space metadata.

**Observations**
- The worker Dockerfile is identical to the server's except for the binary name. The `release.yml` workflow swaps the `:latest` tag with the version tag in-place. A custom ENTRYPOINT in HF Spaces requires the binary to bind to `7860`; both server and worker listen on `cfg.Port`/`cfg.WorkerPort`, which is overridden by `ENV PORT=7860`. The worker will use `cfg.WorkerPort` if set, falling back to `cfg.Port`. So the env var in the Dockerfile must be `PORT=7860` (not `WORKER_PORT=7860`) for the worker to bind correctly. This is a subtle configuration assumption.

---

## 16. GitHub Metadata / CI

### `.github/workflows/codeql.yml` (43 lines)

| Field | Detail |
|---|---|
| **Trigger** | Push, pull request, weekly cron. |
| **Job** | CodeQL analysis on Go. |
| **Observations** | Standard setup. Uses `github/codeql-action@v3`. |

### `.github/workflows/release.yml` (297 lines)

| Field | Detail |
|---|---|
| **Trigger** | Push of a `v*` tag. |
| **Jobs** | (1) `release-notes`: AI-generated via `gh_models_api_key`. (2) `build-and-release`: builds Windows + Linux binaries, builds & pushes Docker images to `ghcr.io`. (3) `deploy-server`: deploys to Hugging Face Spaces. (4) `deploy-worker`: deploys to one or more worker spaces (comma-separated names). (5) `purge-cache`: Cloudflare cache purge. |

**Observations / Issues**
- The deployment to HF Spaces uses `git push origin main --force`. The HF repo's main branch is overwritten. Anyone with write access to the HF space would lose their changes — acceptable for a CI-managed repo but worth documenting.
- The worker deployment iterates over `HF_SPACE_NAME_WORKER` (comma-separated). The bash splits by IFS and iterates.
- The polling loop for HF Space runtime status uses `curl` + `jq` and checks `runtime.stage`. It loops up to 90 × 10s = 15 min, which is the HF Spaces typical cold-start window.
- **Security**: The HF_TOKEN is in `${{ secrets.HF_TOKEN_SERVER }}` etc. Passed as `https://user:token@host` — the URL will be in the action's debug log. Use `git credential helper` or pass via env. Consider masking.
- **Smell**: The `Deploy Server` step does `cd hf/server` and runs `git init` *in the action's checkout*. This relies on the action's `actions/checkout@v4` not removing `.git`. It also re-uses the workflow's existing `.git` if any. The deployment pattern is fragile.
- **Smell**: The build step uses `go build` directly, not via the Dockerfile. The build flags differ slightly from the Dockerfile (`-tags "netgo osusergo" -buildvcs=false` vs. without `-buildvcs=false`). The Linux build is consistent with the Dockerfile; the Windows build omits those flags. Acceptable.

### `.github/scripts/generate_release_notes.py` (175 lines)

| Field | Detail |
|---|---|
| **Responsibility** | Generate a release-notes Markdown by sending commit history to the GitHub Models API. |
| **Behavior** | Reads tags, diffs, chunks, calls `gpt-4o-mini` for summaries, then a final pass for a release description. |

**Observations**
- The script gracefully degrades to a "Maintenance release." message if no `GH_MODELS_API_KEY` is set. Good.
- Uses `subprocess.check_output` with no timeouts. A hang in `git show` would block the action indefinitely.
- Hard-coded `MAX_DIFF_CHARS = 1500` per commit; the script may miss large refactors. The combined chunk limit is 15000.
- The prompt asks for "no emojis", which is in line with the README's engineering tone. Good.
- **Smell**: Sends diffs to a third-party API. The diffs are public-on-GitHub code, but if the repo ever becomes private, this would be a leak. Use a private LLM endpoint.

### `.github/ISSUE_TEMPLATE/bug_report.yml` (27 lines)

Standard bug report template (markdown preamble + what-happened + version + logs). `render: shell` on the logs field gives a monospaced font.

### `.github/ISSUE_TEMPLATE/feature_request.yml` (26 lines)

Standard feature request (description, problem, alternatives). Good structure.

### `.github/SECURITY.md` (21 lines)

Reporting via email; 48h response SLA. Supported version table is minimal (only 1.0.x). Core security mandates listed but no details on threat model.

### `.github/CONTRIBUTING.md` (25 lines)

Standard contributing guide. Notes "no frameworks" for frontend (true: vanilla JS).

### `.github/CODE_OF_CONDUCT.md` (25 lines)

Standard Contributor Covenant-style CoC.

### `.github/FUNDING.yml` (2 lines)

GitHub Sponsors + Buy Me a Coffee.

### `.github/pull_request_template.md` (18 lines)

Standard PR template with type-of-change checkboxes.

---

## 17. Cross-cutting Concerns & Summary

### Architectural Strengths
- **Clear layered design**: `cmd → config → internal/{api,auth,db,extractor,queue,scheduler,sync,observability} → models/web`.
- **Defense in depth**: OAuth state nonce in DB, CSRF token in session, AES-GCM refresh token, hCaptcha on write paths, rate limiting at two tiers (in-memory + Valkey).
- **Cache discipline**: every external system (user, contests, synced events, platforms) has explicit cache key + TTL + invalidation triggers.
- **Read/Write split**: pgx pool per write DSN, round-robin pool per read DSN. Atomic counter for fairness.
- **Graceful shutdown**: signal context + Drain + Close on every component.
- **Session hygiene**: 7-day MaxAge, HttpOnly, SameSite=Lax, Secure in production, ID regeneration on OAuth callback, per-request `Set-Cookie` invalidation on auth failure.
- **Health check**: separate POST endpoint with `X-Admin-Password` for thorough checks; GET for cheap liveness.

### Architectural Smells / Risks

| # | Severity | Issue | Location |
|---|---|---|---|
| 1 | High | `KAFKA_ACCESS_KEY` env with literal `\n` chars may not parse as PEM (godotenv doesn't interpret escapes). | `config/config.go:41`, `.env:21-60` |
| 2 | High | HTML bug on Privacy page: malformed `<a href="terms style="…">` — `Terms of Service` link is broken. | `web/static/privacy.html:254` |
| 3 | High | `verifyHCaptcha` returns `true` when `HCAPTCHA_SECRET` is empty. In production this disables captcha. Should fail-closed. | `internal/api/handlers.go:558-562` |
| 4 | High | On `GoogleCallback`, if AES encryption fails, the user is upserted with an empty refresh token. The `ON CONFLICT DO UPDATE` keeps the *old* token, but a brand-new user could end up without a token. | `internal/api/handlers.go:175-189` |
| 5 | Medium | Duplicated bootstrap (config/slog/observability/db/valkey/queue) between `cmd/server/main.go` and `cmd/worker/main.go`. Extract to `internal/app`. | both `cmd/*/main.go` |
| 6 | Medium | `WorkderPort` falls back to `PORT`, which can collide in compose. | `config/config.go:79-85` |
| 7 | Medium | `db.Pool` doesn't set `MinConns`, `MaxConnLifetime`, `HealthCheckPeriod` — README claims "3-min idle, 10-min lifetime" but those are set on the Valkey client, not pgx. | `internal/db/db.go:41-55` |
| 8 | Medium | `Queue.Health` leaves probe messages in the Kafka topic — accumulates over time. | `internal/queue/kafka.go:139-175` |
| 9 | Medium | `cmd/worker/main.go` GET `/health` always returns 200 with `{"status":"healthy"}` even if Postgres/Valkey/Kafka are down. Operators using GET for LB probes will keep routing traffic. | `cmd/worker/main.go:189-191` |
| 10 | Medium | `EarlyRateLimiter` never evicts old entries → memory growth under sustained traffic. | `internal/api/middleware.go:100-132` |
| 11 | Medium | `http.Server` in both binaries has no Read/Write/Idle timeouts → slow-client attack surface. | `cmd/server/main.go:195-198`, `cmd/worker/main.go:194-197` |
| 12 | Medium | `Extractor.Atcoder` HTML parser is fragile (string-based). Will break on AtCoder redesign. | `internal/extractor/atcoder.go` |
| 13 | Medium | `sync.Syncer` retry uses `attempt * 500ms` (not exponential) with no jitter — can trigger Google API rate limiting. | `internal/sync/sync.go:331-345` |
| 14 | Medium | `scheduler.Scheduler` is only started by the server binary. If the server is down, no cron. | `cmd/server/main.go:140` |
| 15 | Low | `models.UserPlatformPreference` is unused. | `models/models.go:63-66` |
| 16 | Low | `extractor.Platforms` list is duplicated in 5 places (DB CHECK, Go slice, JS colorMap, scheduler, API). Centralize. | see Section 7 |
| 17 | Low | `kafka-go` is a direct dep but its `// indirect` companions in `go.mod` should be tidied. | `go.mod` |
| 18 | Low | LICENSE has placeholder `Copyright (c) 2026 Your Name`. | `LICENSE:3` |
| 19 | Low | Integration Telegram test is not gated by a build tag — `go test ./...` will hit the network. | `internal/observability/telegram_test.go:11` |
| 20 | Low | `intend` for "intent" — typo? (None spotted; ignore.) | n/a |
| 21 | Low | `app.js` calls `fetchGitHubStars` both on `DOMContentLoaded` and `window.load`, doubling the GitHub API call. | `web/static/app.js:654-666` |
| 22 | Low | `app.js` uses a 1-second `setInterval` to re-bind hover listeners; should use `MutationObserver`. | `web/static/app.js:582` |
| 23 | Low | LICENSE template still has "Your Name" placeholder. | `LICENSE:3` |
| 24 | Low | HF Spaces deployment passes token in URL — may appear in action logs. | `.github/workflows/release.yml:143,221` |
| 25 | Low | Worker binary `EXPOSE 8080` but actually listens on `WORKER_PORT` (default 8081) or `PORT` (default 8080). Inconsistency. | `Dockerfile.worker:23` |
| 26 | Low | `Releases.yml` does `git push --force` to HF Spaces — destructive. | `.github/workflows/release.yml:146,224` |
| 27 | Low | `extractor.fetchJSON` and `fetchPage` are near-duplicates. | `internal/extractor/contest.go:51-96` |
| 28 | Low | The fetch helpers don't support context, so request cancellation is impossible from the consumer. | `internal/extractor/contest.go` |
| 29 | Low | `slog.Default` is set twice (before/after observability init) in both binaries. | `cmd/*/main.go` |
| 30 | Low | `slog.Info("telegram api rate limited, waiting to retry"...)` is called inside a `select { ... time.After(retryDelay) ... }` and would block the consumer goroutine for `retry_after` seconds. | `internal/observability/telegram.go:140-147` |

### Performance Considerations
- In-memory queue channels are 100/1000 elements. A burst of 100+ extraction tasks will fail to enqueue.
- The per-platform batch insert in `handleExtraction` uses one transaction per platform. A platform with 10K contests blocks the writer for the duration. Split into batches of 500.
- The frontend's custom cursor and Lenis smooth scroll can tax mobile GPUs. Use `prefers-reduced-motion`.
- The static asset minifier runs at startup; for large asset trees this is a slow boot.

### Security Posture
- ✅ AES-256-GCM for refresh tokens
- ✅ Constant-time compare for admin password and CSRF
- ✅ HttpOnly + SameSite=Lax cookies
- ✅ State nonce in DB (race-free one-shot)
- ✅ Two-tier rate limit
- ✅ CSP, HSTS, X-Content-Type-Options, X-Frame-Options
- ✅ Body size cap (1MB) via `http.MaxBytesReader`
- ⚠️ Captcha can be silently disabled (see #3)
- ⚠️ Refresh-token encryption failure is silently ignored (see #4)
- ⚠️ Logger errors may include user data (no redaction layer)
- ⚠️ No CSP `report-uri`/`report-to` for monitoring
- ⚠️ HTTP server has no timeouts
- ⚠️ Static asset CSP allows `'unsafe-inline'` (necessary for inline styles, but could use hashes)

### Test Coverage
- Unit tests exist for: extractors, auth, queue (in-memory only), sync helpers, models, static server, minifier, middleware (request-id, security headers, rate limit, CSRF, require-auth).
- Missing tests: API handlers (no coverage of `Me`, `DeleteAccount`, `SavePreferences`, `GoogleCallback`), `scheduler`, `db.Pool`, `observability.Manager`, `TelegramHandler`, `queue.Health`, `queue.handleExtraction`, `sync.SyncUser` end-to-end.
- Integration tests: only Telegram (network-gated).
- Frontend: no JS tests; visual regression untested.

### Recommended Next Steps
1. Fix the high-severity issues (KAFKA env parsing, privacy.html link, captcha fail-closed, refresh-token encryption failure).
2. Add timeouts to all `http.Server` instances.
3. Extract the bootstrap to `internal/app`.
4. Centralize the platform list and the cache key/TTL constants.
5. Make the integration Telegram test gated by a build tag.
6. Expand the test suite: `db.Pool`, `scheduler`, `sync.SyncUser` with a `httptest.Server` fake for the Google Calendar API.
7. Document the operator-facing GET vs POST `/health` behavior on the worker.
8. Replace the inline HTML parser for AtCoder with `golang.org/x/net/html`.
9. Move the scheduler to a dedicated binary or to the worker so the server is stateless.

---

*End of lookup.md*
