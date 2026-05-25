<p align="center">
  <img src="assets/contestSync.webp" alt="ContestSync Logo" width="240" style="border-radius: 24px; box-shadow: 0 10px 40px rgba(0,0,0,0.4);" />
</p>

<h1 align="center">ContestSync</h1>

<p align="center">
  <strong>Enterprise-Grade SaaS & Open-Source Synchronization Engine for Competitive Programming Calendars.</strong>
</p>

<p align="center">
  <a href="https://github.com/0xarchit/contestSync/releases"><img src="https://img.shields.io/github/v/release/0xarchit/contestSync?style=for-the-badge&logo=github&logoColor=white&labelColor=000000&color=000000" alt="Version" /></a>
  <a href="https://github.com/0xarchit/contestSync/actions"><img src="https://img.shields.io/github/actions/workflow/status/0xarchit/contestSync/release.yml?style=for-the-badge&logo=githubactions&logoColor=white&labelColor=000000&color=000000" alt="Build Status" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-000000.svg?style=for-the-badge&logo=opensourceinitiative&logoColor=white&labelColor=000000&color=000000" alt="License" /></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.26+-000000.svg?style=for-the-badge&logo=go&logoColor=white&labelColor=000000&color=000000" alt="Go" /></a>
</p>

---

ContestSync is a robust, high-performance web platform and background worker ecosystem designed to automatically synchronize competitive programming contests from major platforms directly to Google Calendar. By integrating advanced caching, distributed queues, secure cryptography, and low-latency asset rendering, ContestSync provides developers and competitive programmers worldwide with a flawless, automated scheduling interface.

## Supported Platforms

* **LeetCode** (GraphQL Fetcher)
* **Codeforces** (Active Contest API Filter)
* **CodeChef** (REST API Fetcher)
* **AtCoder** (HTML Scraper)
* **HackerRank** (REST API Fetcher)
* **GeeksforGeeks** (REST API Parser)
* **Naukri Code360** (Active Event API)

---

## Architecture Highlights

ContestSync utilizes a decoupled, resilient architecture dividing responsibilities between the Web Client, the API Web Server, a Standalone Background Worker, PostgreSQL, Valkey, and Apache Kafka.

### High-Level System Design

```mermaid
graph TD
    subgraph Client Tier
        Browser["Web Browser (Vanilla JS, GSAP, Lenis)"]
    end

    subgraph Service Tier
        Server["Server Binary (Chi Router, Handlers, Scheduler)"]
        Worker["Worker Binary (Queue Consumers, Health Server)"]
    end

    subgraph Queueing & Event Hub
        Kafka["Apache Kafka (Topics: sync-tasks, extraction-tasks)"]
        InMem["In-Memory Channels (Local Queue Fallback)"]
    end

    subgraph Cache & Lock Tier
        Valkey["Valkey Store (Distributed Locks, Sessions, Rate Limits)"]
    end

    subgraph Database Tier
        DB["PostgreSQL (Users, Contests, Synced Events, OAuth States)"]
    end

    subgraph External Services
        Google["Google Calendar API (v3)"]
        LeetCode["LeetCode GraphQL"]
        Codeforces["Codeforces API"]
        CodeChef["CodeChef API"]
        AtCoder["AtCoder Web"]
        HackerRank["HackerRank API"]
        GFG["GeeksforGeeks API"]
        Code360["Code360 API"]
    end

    Browser <-->|HTTP / JSON / HTML| Server
    Server -->|Publish Tasks| Kafka
    Server -.->|Local Channels| InMem
    Worker -->|Consume Tasks| Kafka
    Worker -.->|Local Channels| InMem

    Server -->|Session & Rate Limits| Valkey
    Worker -->|Sync Locks| Valkey

    Server -->|Read / Write| DB
    Worker -->|Read / Write| DB

    Worker -->|Google Calendar OAuth| Google
    Worker -->|Fetch Contests| LeetCode
    Worker -->|Fetch Contests| Codeforces
    Worker -->|Fetch Contests| CodeChef
    Worker -->|Fetch Contests| AtCoder
    Worker -->|Fetch Contests| HackerRank
    Worker -->|Fetch Contests| GFG
    Worker -->|Fetch Contests| Code360
```

---

## Core SaaS & Open-Source Capabilities

### 1. In-Memory Static Asset Compiler & Minifier
* **Zero Disk Overhead**: Static assets (HTML, CSS, JS) are read, parsed, and compiled into memory exactly once at server boot.
* **On-the-Fly Minification**: Strips comments, collapses whitespaces, and removes excessive formatting programmatically to optimize payload sizes without local npm compilation chains.
* **Aggressive Revalidation Caching**: Assets are served with deterministic SHA256 ETags and maximum lifetime cache directives (`Cache-Control: public, max-age=31536000, must-revalidate`), assuring lightning-fast load times.

### 2. Distributed Valkey Cache-Aside & Coherence Tier
* **High-Speed Cache-Aside**: Fetches active platforms and user preferences directly from Valkey, reducing PostgreSQL read pressure.
* **O(1) Cold Starts**: Merges multiple platform cache misses into a single batched database query using array matching.
* **Instant Invalidation**: Scrapers and preference changes immediately purge invalid caches (`cache:contests:<platform>` and `cache:user:<userID>`), keeping backend states perfectly coherent.

### 3. Queue-Driven Concurrency & Backpressure control
* **Dual-Mode Queue Broker**: Supports a distributed Apache Kafka cluster for horizontal scalability, or dynamically falls back to optimized, thread-safe in-memory channels when no Kafka host is configured.
* **Semaphore-Gated Concurrency**: Restricts active goroutines (max 10 concurrent user syncs and 3 platform extractions) to prevent memory spikes and API rate limit locks.

### 4. Zero-Trust Security Gating
* **At-Rest AES-256-GCM Encryption**: Encrypts and decrypts Google Calendar OAuth2 Refresh Tokens securely using standard cryptographic keys.
* **Cookie-less Session-Bound CSRF validation**: State-modifying requests verify CSRF signatures embedded strictly inside secure session keys, eliminating double-submit cookie exposure.
* **Session Regeneration**: Session IDs are fully cleared and rotated upon successful login callbacks to safeguard users against session fixation exploits.
* **Payload Gating**: Request bodies are capped at 1MB and admin authentication headers are bounded to 256 bytes to prevent buffer and memory exhaustion attempts.

### 5. Deterministic Idempotency & Conflict Resolution
* **Deterministic Event IDs**: Hashes the combination of user credentials and contest metadata into a base32hex string passed directly to the Google Calendar API as the event's unique identifier.
* **409 Conflict Reconciliation**: Catches and processes duplicate calendar sync triggers gracefully, maintaining sync integrity without duplicate event pollution.

---

## Deep-Dive Component Architecture

### 1. API Web Server (`cmd/server`)
* **Routing**: Driven by Go Chi Router with structured log tracing.
* **State Management**: Uses Gorilla Sessions, dynamically choosing Valkey or fallback Cookie Stores.
* **Rate Limiting**: Distributed IP rate-limiting powered by Valkey with a dynamic retry interval header. Auto-falls back to a thread-safe local LRU cache (10,000 size capacity) on redis connection interruptions.

### 2. Background Worker (`cmd/worker`)
* **Task Consumers**: Consumes and processes background workloads.
* **Health Probes**: Runs a dedicated micro-server providing a `/health` endpoint for Kubernetes/orchestration validation.

### 3. Automated Cron Scheduler (`internal/scheduler`)
* **Extractions**: Daily trigger to fetch contest lists.
* **Syncs**: Daily trigger to update all active user calendars.
* **Data Pruning**: Daily trigger that purges events older than 30 days.
* **OAuth Cleans**: Ticker running every 15 minutes to purge stale transient states.

---

## System Flowcharts

### 1. Authentication Lifecycle

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant Browser
    participant Server
    participant GoogleAuth as Google OAuth
    participant DB as PostgreSQL
    participant Valkey

    User->>Browser: Click "Sign in with Google"
    Browser->>Server: GET /auth/google
    Server->>DB: INSERT INTO oauth_states (state)
    Server->>Browser: Redirect to Google Consent Screen
    Browser->>GoogleAuth: Request authorization
    GoogleAuth->>User: Display consent prompt
    User->>GoogleAuth: Approve permissions
    GoogleAuth->>Browser: Redirect to /auth/google/callback?code=CODE&state=STATE
    Browser->>Server: GET /auth/google/callback?code=CODE&state=STATE
    Server->>DB: Verify and delete state from oauth_states
    Server->>GoogleAuth: Exchange CODE for Access & Refresh Tokens
    GoogleAuth->>Server: Return tokens
    Server->>Server: Encrypt Refresh Token (AES-256-GCM)
    Server->>DB: INSERT/UPDATE user details
    Server->>Valkey: Create Session (session_id -> user_id, csrf_token)
    Server->>Browser: Set HTTP-Only Session Cookie
    Browser->>User: Show Preferences Dashboard
```

### 2. Background Synchronization

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant Browser
    participant Server
    participant Queue as Queue (Kafka / In-Memory)
    participant Worker
    participant Valkey
    participant DB as PostgreSQL
    participant GoogleCal as Google Calendar API

    User->>Browser: Click "Sync Now"
    Browser->>Server: POST /sync (with X-CSRF-Token header)
    Server->>Server: Validate Session and CSRF Token
    Server->>Queue: Publish SyncTask (user_id)
    Server->>Browser: Return 202 Accepted
    Browser->>User: Show "Syncing..." status

    Queue->>Worker: Consume SyncTask (user_id)
    Worker->>Valkey: SetNX (lock:sync:user_id, TTL=5m)
    alt Lock Acquired
        Worker->>DB: SELECT user profile & encrypted refresh token
        Worker->>Server: Decrypt Refresh Token (AES-256-GCM)
        Worker->>DB: UPDATE user sync_status = 'syncing'
        Worker->>GoogleCal: Get timezone / Resolve dedicated calendar
        Worker->>DB: SELECT synced_events and future contests
        loop For each unsynced contest
            Worker->>Worker: Compute deterministic Event ID
            loop Try up to 3 times (Exponential Backoff)
                Worker->>GoogleCal: Insert Event
                GoogleCal-->>Worker: Status 200 OK / 409 Conflict
            end
            alt Status 200 OK
                Worker->>DB: INSERT INTO synced_events
            else Status 409 Conflict
                Worker->>DB: INSERT INTO synced_events (ON CONFLICT DO NOTHING)
            end
        end
        Worker->>DB: UPDATE user sync_status = 'success', last_sync_at = NOW()
        Worker->>Valkey: Del (lock:sync:user_id)
    else Lock Busy
        Worker-->>Worker: Terminate task
    end
```

---

## Stack Specifications

* **Frontend**: Responsive CSS, Vanilla JS, GSAP Animations, Lenis Smooth Scroll.
* **Backend Core**: Golang (1.26+), Chi Router, pgx/v5 Pool, Gorilla Sessions, robfig/cron/v3.
* **Distributed Engine**: Valkey, Apache Kafka (segmentio/kafka-go).
* **Storage**: PostgreSQL.
* **Integrations**: Google Calendar API v3.

---

## Configuration & Getting Started

### 1. Environment Configurations

Define these environment settings in a `.env` file at the root:

```env
POSTGRES_DB=postgres://user:pass@host:port/db?sslmode=require
CONNECTION_LIMIT=10
GOOGLE_CLIENT_ID=your_id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your_secret
GOOGLE_REDIRECT_URL=http://localhost:8080/auth/google/callback
SESSION_SECRET=your_32_byte_hex_secret
ENCRYPTION_KEY=your_32_byte_hex_secret
PORT=8080
ENV=development
ALLOWED_ORIGIN=http://localhost:8080
ADMIN_PASSWORD=your_admin_pass
KAFKA_HOST=kafka_broker_host
KAFKA_PORT=9092
KAFKA_PARTITIONS=4
VALKEY_URI=rediss://default:password@host:port
```

### 2. Execution and Compilation

#### Compile and Start API Server Locally

```powershell
$env:CGO_ENABLED=0; $env:GOOS="windows"; $env:GOARCH="amd64"; $env:GOAMD64="v3"; go build -tags "netgo osusergo" -trimpath -buildvcs=false -ldflags="-s -w -extldflags -static" -o server.exe ./cmd/server/main.go 2>&1
./server.exe
```

#### Compile and Start Background Worker Locally

```powershell
$env:CGO_ENABLED=0; $env:GOOS="windows"; $env:GOARCH="amd64"; $env:GOAMD64="v3"; go build -tags "netgo osusergo" -trimpath -buildvcs=false -ldflags="-s -w -extldflags -static" -o worker.exe ./cmd/worker/main.go 2>&1
./worker.exe
```

#### Multi-Service Containerized Deployment

To compile images manually:

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
