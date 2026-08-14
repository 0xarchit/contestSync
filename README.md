<p align="center">
  <img src="assets/contestSync.webp" alt="ContestSync Logo" width="240" style="border-radius: 24px; box-shadow: 0 10px 40px rgba(0,0,0,0.4);" />
</p>

<h1 align="center">ContestSync</h1>

<p align="center">
  <strong>Enterprise-Grade Open-Source Synchronization Engine & SaaS Platform for Competitive Programming Calendars.</strong>
</p>

<p align="center">
  <a href="https://github.com/0xarchit/contestSync/releases"><img src="https://img.shields.io/github/v/release/0xarchit/contestSync?style=for-the-badge&logo=github&logoColor=white&labelColor=000000&color=000000" alt="Version" /></a>
  <a href="https://github.com/0xarchit/contestSync/actions"><img src="https://img.shields.io/github/actions/workflow/status/0xarchit/contestSync/release.yml?style=for-the-badge&logo=githubactions&logoColor=white&labelColor=000000&color=000000" alt="Build Status" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-000000.svg?style=for-the-badge&logo=opensourceinitiative&logoColor=white&labelColor=000000&color=000000" alt="License" /></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.26+-000000.svg?style=for-the-badge&logo=go&logoColor=white&labelColor=000000&color=000000" alt="Go" /></a>
</p>

---

ContestSync is a robust, highly optimized web platform and distributed worker system designed to automatically synchronize competitive programming contests from major platforms directly to Google Calendar. By integrating advanced replication pooling, distributed caching, secure cryptography, and low-latency asset rendering, ContestSync provides developers and competitive programmers with an automated, zero-maintenance scheduling interface.

---

## Supported Platforms & Integration Specs

| Platform | Scraper/API Type | Fetch Payload Format | Update Frequency | Rate Limit Strategy |
| :--- | :--- | :--- | :--- | :--- |
| **LeetCode** | GraphQL Endpoint | JSON Query Payload | On startup, then every 30m | Dynamic Backoff + Retries |
| **Codeforces** | REST API Filter | HTTP JSON Response | On startup, then every 30m | Local Token Bucket Gating |
| **CodeChef** | REST API Fetcher | Nested JSON Payload | On startup, then every 30m | Session Connection Pool |
| **AtCoder** | HTML Scraper | DOM Node Parsing | On startup, then every 30m | Strict User Agent Gating |
| **HackerRank** | REST API Fetcher | Flat JSON Document | On startup, then every 30m | Secure Host Verification |
| **GeeksforGeeks**| REST API Parser | Raw JSON Payload | On startup, then every 30m | Bounded Payload Capping |
| **Naukri Code360**| REST API Parser | Structured JSON Array | On startup, then every 30m | Dynamic Event Mapping |

---

## Topological System Architecture

<p align="center">
  <img src="assets/contestSync_Diagram.gif" alt="ContestSync System Architecture" width="1080" style="border-radius: 24px; box-shadow: 0 10px 40px rgba(0,0,0,0.4);" />
</p>

---

## Core SaaS & Enterprise Capabilities

### 1. High-Concurrency CloudAMQP LavinMQ Queue Engine

* **Ultra-Light & Blazing Fast**: Powered by CloudAMQP LavinMQ (AMQP 0.9.1), consuming extraction tasks (concurrency limit 3) and user sync tasks (concurrency limit 10) with near-zero idle memory footprint (~15MB RAM).
* **Transparent Fallback**: Automatic, zero-configuration fallback to thread-safe in-memory channels (`chan`) when neither `AMQP_URL` nor `CLOUDAMQP_URL` is configured, or if AMQP connection initialization fails. When operating in-memory, unconsumed in-flight tasks may be lost on process restart.
* **Persistent Task Queuing**: Manages persistent `extraction-tasks` and `sync-tasks` queues with automatic retry handling.

### 2. High-Concurrency Neon DB Read/Write Split Engine

* **Isolation of Concerns**: Database operations are split into separate Write Primary and multiple Read Replica connection pools.
* **Atomic Round-Robin Distribution**: Reads are distributed across replicas using lock-free atomic round-robin counters, utilizing the Neon DB pool endpoints for high performance.
* **Massive Concurrency Support**: Integrates PgBouncer configurations, allowing up to 10,000 concurrent pooled connections per read replica, while direct connections are preserved for write paths and migrations.
* **In-Memory CA Validation**: Implements in-memory PEM certificate loading for database connections, avoiding disk leakage issues.

### 3. Multi-Tier Distributed Valkey Caching Model

| Caching Area | Cache Key Format | Time To Live (TTL) | Eviction Trigger | Storage Format |
| :--- | :--- | :--- | :--- | :--- |
| **Contests List** | `cache:contests:<platform>` | 12 Hours | Crawler batch finished | JSON Contest Array |
| **User Preferences** | `cache:user:v2:<userID>` | 24 Hours | Google OAuth callback / Preferences save / Account deletion | JSON Profile Struct |
| **Calendar Validation**| `user:cal_val:<userID>` | 15 Min (Success/Credential) / 1 Min (Operational) | Google OAuth login / Token Revocation | JSON Status Struct |
| **Synced Events** | `cache:synced_events:<userID>` | 24 Hours | End of SyncUser run (if new sync recorded) | JSON String Array |
| **Platforms List**| `cache:platforms` | 24 Hours | Static Compile (None) | Pre-serialized JSON |
| **IP Rate Limiter** | `ratelimit:<route_pattern>:<ip>` | Variable (Dynamic) | Auto-Expires on window end | Numeric string counter |
| **User Sessions** | `session:<session_id>` | 7 Days | Account logout | Encoded Gorilla Session |

### 4. Cached Calendar Scope Validation & 24h Ungranted User Pruning

* **Cached Scope Verification**: `GET /auth/calendar/validate` checks user Google Calendar access and returns cached validation status from Valkey (`user:cal_val:<userID>`, 15m for valid/credential failure, 1m for operational failure), invalidated on OAuth login or token revoke.
* **Frontend Warning State**: Displays explicit **Google Calendar Access Missing** alerts for missing/revoked tokens and **Temporary Calendar Service Issues** for API outages.
* **24-Hour Automated Cleanup**: Background cron task automatically deletes accounts with ungranted tokens (`refresh_token IS NULL`) after 24 hours to prevent orphan DB records.

### 5. Scheduled Contest Overwrite & Obsolete Contest Pruning

* **Dynamic Pruning**: When scrapers run, a cleanup operation is executed: `DELETE FROM contests WHERE platform = $1 AND start_time > NOW() AND id != ALL($2)`. This deletes upcoming contests that were rescheduled or cancelled on the host platform.
* **Boundary Integrity**: The prune query is locked to upcoming contests (`start_time > NOW()`). This preserves past contests and their synced events, preventing redundant event syncs or double-syncing.
* **Safe Overwrite**: If a scrape returns zero upcoming contests, future scheduled entries for that platform are preserved to protect against API blocks or temporary empty responses.

---

## Detailed Environment Configurations

| Environment Variable | Description | Default Value | Example Value |
| :--- | :--- | :--- | :--- |
| `POSTGRES_DB` | Connection URL for Primary Write Postgres Database | None (Required) | `postgres://user:pass@host:port/db?sslmode=require` |
| `POSTGRES_READ_DB` | Comma-separated Connection URLs for Read Replica Databases | None | `postgres://user:pass@rep1:port/db,postgres://user:pass@rep2:port/db` |
| `CONNECTION_LIMIT` | Maximum connections allowed in Primary Write pool | `800` | `20` |
| `CONNECTION_POOL_LIMIT`| Maximum connections allowed per Read Replica Pool | `10000` | `100` |
| `VALKEY_URI` | Connection URI string for Valkey instance | None | `rediss://default:password@host:port` |
| `CLOUDAMQP_URL` / `AMQP_URL` | AMQP 0.9.1 connection URL for CloudAMQP LavinMQ (fallback: in-memory) | None | `amqps://user:pass@lemming.rmq.cloudamqp.com/vhost` |
| `GOOGLE_CLIENT_ID` | OAuth 2.0 Web Application Client ID from Google Cloud Console | None | `abc-123.apps.googleusercontent.com` |
| `GOOGLE_CLIENT_SECRET` | OAuth 2.0 Web Application Client Secret from Google Cloud Console | None | `sec_code_xyz` |
| `GOOGLE_REDIRECT_URL` | Redirect Callback URL registered in Google API credentials | None | `http://localhost:8080/auth/google/callback` |
| `SESSION_SECRET` | 32-byte (64 hex characters) key for Gorilla Cookie / Valkey Sessions | None | `<64-hex-character-secret>` (generate via `openssl rand -hex 32`) |
| `ENCRYPTION_KEY` | 32-byte (64 hex characters) key for AES Refresh Token encryption | None | `<64-hex-character-secret>` (generate via `openssl rand -hex 32`) |
| `ADMIN_PASSWORD` | Standard password for admin panel authentication | None | `adm_pwd_secure` |
| `TRUST_PROXY` | Gated verification trust toggle for client IP forwarding | `false` | `true` |
| `TELEGRAM_PROXY_URL` | Outbound Proxy target for forwarding warnings/errors | None | `https://tg-proxy.myorg.workers.dev` |
| `PROXY_SECRET_KEY` | Token key for proxy HTTP authorization | None | `sec_key_abc` |
| `TELEGRAM_GROUP_ID` | Group Identifier target for slog alerts | None | `-1002938475` |
| `TELEGRAM_GROUP_TOPIC_ID`| Topic Thread ID inside Telemetry Group | None | `12` |
| `FROM_ENV` | Application node identifier string for diagnostics | None | `Server-Node-Staging` |
| `PORT` | API Web Server HTTP listener port | `8080` | `7860` |
| `WORKER_PORT` | Background Worker Micro-Health server HTTP port | `8081` | `8082` |
| `ENV` | System execution environment gating flag | `production` | `development` |

---

## Detailed System Workflows

### 1. Authentication Lifecycle

```mermaid
sequenceDiagram
    autonumber
    actor User as Competitive Programmer
    participant Browser as Web Browser
    participant Server as Server Binary (cmd/server)
    participant GoogleAuth as Google OAuth Service
    participant DB_Write as Neon DB Primary
    participant Valkey as Valkey Distributed Cache

    User->>Browser: Click 'Sign in with Google'
    Browser->>Server: GET /auth/google
    Server->>Server: Generate secure random state token
    Server->>DB_Write: Store state: INSERT INTO oauth_states (state)
    Server->>Browser: Set secure HttpOnly 'oauth_state' cookie
    Server->>Browser: Redirect to Google OAuth consent page
    Browser->>GoogleAuth: Direct browser request with client_id, scopes, and state
    GoogleAuth->>User: Render permissions consent dialog
    User->>GoogleAuth: Authorize request (Calendar & Profile scopes)
    GoogleAuth->>Browser: Redirect callback: /auth/google/callback?code=CODE&state=STATE
    Browser->>Server: GET /auth/google/callback?code=CODE&state=STATE
    Server->>Server: Validate state cookie match
    Server->>DB_Write: Revalidate & purge state: DELETE FROM oauth_states WHERE state = STATE
    DB_Write-->>Server: Return deleted state status
    Server->>GoogleAuth: Request tokens: Exchange authorization CODE
    GoogleAuth-->>Server: Return Access Token & Refresh Token
    Server->>Server: Encrypt Refresh Token using AES-256-GCM
    Server->>DB_Write: Upsert User Profile: INSERT/UPDATE users table
    DB_Write-->>Server: Return active user_id
    Server->>Valkey: Evict stale profile: DEL cache:user:id
    Server->>Server: Rotate and regenerate Session ID
    Server->>Valkey: Write dynamic session payload: SET session:session_id
    Server->>Browser: Set secure HttpOnly Session cookie
    Browser->>User: Redirect & render Preferences Dashboard
```

### 2. Background Synchronization & Caching Layer

```mermaid
sequenceDiagram
    autonumber
    actor User as Competitive Programmer
    participant Browser as Web Browser
    participant Server as Server Binary (cmd/server)
    participant Queue as Queue Broker (LavinMQ / In-Memory)
    participant Worker as Background Worker (cmd/worker / cmd/server)
    participant Valkey as Valkey Cache
    participant DB_Read as Neon DB Replicas (Round-Robin)
    participant DB_Write as Neon DB Primary
    participant GoogleCal as Google Calendar API

    User->>Browser: Click 'Sync Now'
    Browser->>Server: POST /sync (with X-CSRF-Token header)
    Server->>Server: Verify session & check CSRF signature
    Server->>Queue: Publish sync task: PublishSyncTask(user_id)
    Server-->>Browser: Return 202 Accepted response
    Browser->>User: Display 'Syncing in background' UI status

    Queue->>Worker: Consume sync task: ConsumeSyncTask(user_id)
    Worker->>Valkey: Acquire lock: SetNX lock:sync:user_id (TTL=5m)
    alt Lock Acquired successfully
        Worker->>Valkey: Query cached profile: GET cache:user:user_id
        alt Cache Hit
            Valkey-->>Worker: Return profile data
        else Cache Miss
            Worker->>DB_Read: SELECT profile & encrypted token from Replicas
            DB_Read-->>Worker: Return database user record
            Worker->>Valkey: SET cache:user:user_id (24h TTL)
        end
        Worker->>Worker: Decrypt Google OAuth Refresh Token (AES-256-GCM)
        Worker->>DB_Write: Update sync status: UPDATE users SET sync_status = 'syncing'
        Worker->>GoogleCal: Query primary calendar settings & verify credentials
        Worker->>Valkey: Query synced history: GET cache:synced_events:user_id
        alt Cache Hit
            Valkey-->>Worker: Return synced contest IDs list
        else Cache Miss
            Worker->>DB_Read: SELECT contest_id FROM synced_events
            DB_Read-->>Worker: Return synced records list
            Worker->>Valkey: SET cache:synced_events:user_id (24h TTL)
        end
        Worker->>DB_Read: SELECT future contests: SELECT id FROM contests
        DB_Read-->>Worker: Return contests list
        loop For each unsynced future contest
            Worker->>Worker: Generate deterministic Base32hex Event ID
            loop Retry up to 3 times (Exponential Backoff)
                Worker->>GoogleCal: Send event insert request
                GoogleCal-->>Worker: Return 200 OK / 409 Conflict status
            end
            alt Event Inserted successfully (200 OK)
                Worker->>DB_Write: Log event: INSERT INTO synced_events
                Worker->>Worker: Set anySynced = true
            else Event already exists (409 Conflict)
                Worker->>DB_Write: Reconcile event: INSERT INTO synced_events (ON CONFLICT DO NOTHING)
                Worker->>Worker: Set anySynced = true
            end
        end
        alt if anySynced is true
            Worker->>Valkey: Invalidate synced events cache: DEL cache:synced_events:user_id
        end
        Worker->>DB_Write: Log success: UPDATE users SET sync_status = 'success', last_sync_at = NOW()
        Worker->>Valkey: Release synchronization lock: DEL lock:sync:user_id
    else Lock is Busy
        Worker-->>Worker: Task rejected, exit process early
    end
```

---

## Codebase Component Topology

```
[contestSync]
├── cmd
│   ├── server  ───────── API Server Binary (Routing, Rate limits, Session stores)
│   └── worker  ───────── Background Workflows Executor & Micro-Health Web Server
├── config  ───────────── Unified Config Engine & Environment Gating Layer
├── internal
│   ├── api  ──────────── HTTP Handlers, Admin routes, Security Middlewares, Asset Minifier
│   ├── auth  ─────────── Cryptographic AES-256-GCM Encryption/Decryption Modules
│   ├── db  ───────────── Database Initializer, Pool Splits, Replica Round-Robin counters
│   ├── extractor  ────── Platform-Specific HTML/JSON Fetchers & Active Parsers
│   ├── queue  ────────── CloudAMQP LavinMQ / In-Memory Fallback Queue Broker
│   ├── observability  ── Asynchronous Telegram Warnings and Error Diagnostics Telemetry
│   ├── scheduler  ────── robfig/cron background trigger orchestration
│   └── sync  ─────────── Synchronization Engine & Deterministic Google Calendar Sync
├── migrations  ───────── Database Initialization Schema Definitions
├── models  ───────────── Global Struct Definitions, Key Formatter & Cache constants
└── web  ──────────────── Frontend HTML, GSAP Animations, Lenis, and Custom CSS
```

---

## Detailed Environment Configurations

| Environment Variable | Description | Default Value | Example Value |
| :--- | :--- | :--- | :--- |
| `POSTGRES_DB` | Connection URL for Primary Write Postgres Database | None (Required) | `postgres://user:pass@host:port/db?sslmode=require` |
| `POSTGRES_READ_DB` | Comma-separated Connection URLs for Read Replica Databases | None | `postgres://user:pass@rep1:port/db,postgres://user:pass@rep2:port/db` |
| `CONNECTION_LIMIT` | Maximum connections allowed in Primary Write pool | `800` | `20` |
| `CONNECTION_POOL_LIMIT`| Maximum connections allowed per Read Replica Pool | `10000` | `100` |
| `VALKEY_URI` | Connection URI string for Valkey instance | None | `rediss://default:password@host:port` |
| `CLOUDAMQP_URL` / `AMQP_URL` | AMQP 0.9.1 connection URL for CloudAMQP LavinMQ (fallback: in-memory) | None | `amqps://user:pass@lemming.rmq.cloudamqp.com/vhost` |
| `GOOGLE_CLIENT_ID` | OAuth 2.0 Web Application Client ID from Google Cloud Console | None | `abc-123.apps.googleusercontent.com` |
| `GOOGLE_CLIENT_SECRET` | OAuth 2.0 Web Application Client Secret from Google Cloud Console | None | `sec_code_xyz` |
| `GOOGLE_REDIRECT_URL` | Redirect Callback URL registered in Google API credentials | None | `http://localhost:8080/auth/google/callback` |
| `SESSION_SECRET` | 32-byte (64 hex characters) key for Gorilla Cookie / Valkey Sessions | None | `<64-hex-character-secret>` (generate via `openssl rand -hex 32`) |
| `ENCRYPTION_KEY` | 32-byte (64 hex characters) key for AES Refresh Token encryption | None | `<64-hex-character-secret>` (generate via `openssl rand -hex 32`) |
| `ADMIN_PASSWORD` | Standard password for admin panel authentication | None | `adm_pwd_secure` |
| `TRUST_PROXY` | Gated verification trust toggle for client IP forwarding | `false` | `true` |
| `TELEGRAM_PROXY_URL` | Outbound Proxy target for forwarding warnings/errors | None | `https://tg-proxy.myorg.workers.dev` |
| `PROXY_SECRET_KEY` | Token key for proxy HTTP authorization | None | `sec_key_abc` |
| `TELEGRAM_GROUP_ID` | Group Identifier target for slog alerts | None | `-1002938475` |
| `TELEGRAM_GROUP_TOPIC_ID`| Topic Thread ID inside Telemetry Group | None | `12` |
| `FROM_ENV` | Application node identifier string for diagnostics | None | `Server-Node-Staging` |
| `PORT` | API Web Server HTTP listener port | `8080` | `7860` |
| `WORKER_PORT` | Background Worker Micro-Health server HTTP port | `8081` | `8082` |
| `ENV` | System execution environment gating flag | `production` | `development` |

---

## Execution and Compilation

### Compile and Start API Server Locally

```powershell
$env:CGO_ENABLED=0; $env:GOOS="windows"; $env:GOARCH="amd64"; $env:GOAMD64="v3"; go build -tags "netgo osusergo" -trimpath -buildvcs=false -ldflags="-s -w -extldflags -static" -o server.exe ./cmd/server/main.go 2>&1
./server.exe
```

### Compile and Start Background Worker Locally

```powershell
$env:CGO_ENABLED=0; $env:GOOS="windows"; $env:GOARCH="amd64"; $env:GOAMD64="v3"; go build -tags "netgo osusergo" -trimpath -buildvcs=false -ldflags="-s -w -extldflags -static" -o worker.exe ./cmd/worker/main.go 2>&1
./worker.exe
```

### Multi-Service Containerized Deployment

To build Docker images manually:

```powershell
docker build -f Dockerfile.server -t contestsync-server .
docker build -f Dockerfile.worker -t contestsync-worker .
```

To deploy the entire environment via Docker Compose:

```powershell
docker compose up --build
```

---

## License

MIT © 2026 ContestSync. See [LICENSE](LICENSE) for details.
