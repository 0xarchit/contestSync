# ContestSync

> **Sync contests to your calendar, beautifully and securely.**

ContestSync is a robust, high-performance web application and background worker system designed to automatically synchronize competitive programming contests from major platforms directly to Google Calendar. It supports LeetCode, Codeforces, CodeChef, AtCoder, HackerRank, GeeksforGeeks, and Naukri Code360.

---

## High-Level System Architecture

The following diagram illustrates the upgraded high-level system architecture of ContestSync, demonstrating the separation of concerns between the API Web Server, the Background Worker, and the shared database, caching, locking, and queueing tiers.

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

## Component-Specific Architectures

### 1. API Web Server (`cmd/server`)
The API Server handles frontend client requests, user authentication, profile settings, manual synchronization triggers, and administrative overrides.
* **Routing**: Built using the Go Chi Router.
* **Middleware Pipeline**:
  1. **Request ID Injection**: Generates a unique UUIDv4 for tracing requests.
  2. **Security Headers**: Injecting Content-Security-Policy (CSP), HSTS, X-Content-Type-Options, X-Frame-Options, X-XSS-Protection, Referrer-Policy, and Permissions-Policy.
  3. **Rate Limiting**: Distributed rate-limiting backed by Valkey, falling back automatically to a thread-safe local LRU cache (10,000 capacity) to prevent memory exhaustion.
  4. **Request Logging & Recovery**: Captures request metadata and recovers from panic scenarios.
  5. **Authentication Verification**: Ensures session presence for protected resources.
  6. **CSRF Validation**: Double-submit token check using token matches stored securely in the user's session values (preventing token size mismatch panics).
* **State Management**: Uses Gorilla Sessions, dynamically backing session stores with Valkey (using JSON decoders with numbers to avoid type conversion bugs) or cookie stores.

### 2. Standalone Background Worker (`cmd/worker`)
The Background Worker consumes tasks published to the queue (Kafka/In-Memory) to offload heavy operations from the web server.
* **Task Processing**: Executes contest extractions and user calendar synchronizations concurrently.
* **Backpressure Control**: Restricts concurrency using channel semaphores (bounded to 10 concurrent user syncs and 3 platform extractions).
* **Worker Health Check**: Runs a minimal HTTP server exposing `GET /health` returning status JSON for orchestration/health checks.

### 3. Queue System (`internal/queue`)
ContestSync uses a dual-mode message broker abstraction.
* **Kafka Mode**: Uses `segmentio/kafka-go` with SASL/TLS. Features configurable partition counts via environment settings.
* **In-Memory Mode**: If no Kafka host is configured, the queue automatically falls back to in-memory channels (`extractionCh` and `syncCh`) with background consumer goroutines.
* **Task Types**:
  - `ExtractionTask`: Contains the target platform string to fetch new contests.
  - `SyncTask`: Contains the target user ID to synchronize calendar events.

### 4. Cache & Distributed Locking (`Valkey`)
Valkey serves as the central hub for distributed states.
* **Concurrency Locking**: Acquires keyspace locks (`lock:sync:userID` with 5-minute TTL) via `SetNX` during user synchronization tasks to prevent race conditions and duplicate Google Calendar events.
* **Session Storage**: Holds user session JSON records.
* **Limiter State**: Tracks rate-limiting IP hits.

### 5. Automated Scheduler (`internal/scheduler`)
The Scheduler manages cron tasks via `robfig/cron/v3` inside the server context:
* **`@daily` Platform Extraction**: Queues extraction tasks for all platforms.
* **`@daily` User Synchronization**: Queues sync tasks for all users.
* **`@daily` Database Pruning**: Deletes contests older than 30 days (cascading to delete synced events).
* **`@every 15m` OAuth State Cleanup**: Clears expired transient OAuth states older than 10 minutes.

### 6. Scrapers & Extractors (`internal/extractor`)
Each platform has a dedicated fetcher mapping to a shared `Contest` struct:
* **LeetCode**: GraphQL queries sent to `leetcode.com/graphql`.
* **Codeforces**: API fetches from `codeforces.com/api/contest.list` filtering pre-contests.
* **CodeChef**: JSON API parsing of future contests.
* **AtCoder**: HTML table parser extraction.
* **HackerRank**: HTTP request API integration.
* **GeeksforGeeks**: REST API content parsing.
* **Naukri Code360**: JSON API fetches using `event_start_time` mapping for start times and durations.

### 7. Calendar Synchronization Engine (`internal/sync`)
Performs the actual calendar writing operations:
* **Distributed Lock Check**: Rejects concurrent requests if a Valkey or `sync.Map` lock is already active for the user.
* **Token Decryption**: Decrypts the Google OAuth refresh token using AES-256-GCM.
* **Calendar Resolution**: Detects if the user selected a dedicated calendar. Creates a separate "ContestSync" calendar if required, otherwise defaults to the primary calendar.
* **Google API Level Idempotency**: Generates a deterministic base32hex(md5(userID + "_" + contestID)) string as the Google Calendar Event ID.
* **Exponential Backoff**: Retries calendar event inserts up to 3 times (500ms, 1000ms delay) on transient errors.
* **Conflict Reconciliation**: Catches `409 Conflict` duplicate responses and reconciles them into the database `synced_events` mapping.

---

## Core System Flows

### 1. User Authentication & Registration Sequence

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

### 2. Contest Synchronization Sequence

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

### 3. Contest Extraction Sequence

```mermaid
sequenceDiagram
    autonumber
    participant Scheduler as Scheduler (Cron)
    participant Queue as Queue (Kafka / In-Memory)
    participant Worker
    participant Platform as Contest Platform API/Web
    participant DB as PostgreSQL

    Scheduler->>Queue: Publish ExtractionTask (platform)
    Queue->>Worker: Consume ExtractionTask (platform)
    Worker->>Platform: Fetch API or Scrape HTML
    Platform-->>Worker: Contest list
    loop For each extracted contest
        Worker->>DB: Batch INSERT/UPDATE contests (ON CONFLICT update details)
    end
```

### 4. General System Flowchart

```mermaid
flowchart TD
    Start([System Start]) --> InitConfig[Load Config & env]
    InitConfig --> InitDB[Initialize pgx Connection Pool]
    InitConfig --> InitValkey{VALKEY_URI configured?}
    
    InitValkey -->|Yes| ConnectValkey[Connect to Valkey & Init ValkeySessionStore]
    InitValkey -->|No| ConnectCookie[Initialize CookieStore]
    
    ConnectValkey --> InitQueue
    ConnectCookie --> InitQueue
    
    InitQueue{KAFKA_HOST configured?}
    InitQueue -->|Yes| ConnectKafka[Initialize Kafka Writer & topics]
    InitQueue -->|No| ConnectInMem[Initialize In-Memory Channels]
    
    ConnectKafka --> StartApp
    ConnectInMem --> StartApp

    subgraph Server Runtime
        StartApp --> StartHTTP[Start HTTP Server on PORT]
        StartApp --> StartSched[Start Cron Scheduler]
        StartApp --> ConsumeLocal[Start Queue Consumers]
    end

    subgraph Cron Jobs
        StartSched -->|Daily| CronExtract[RunExtraction: Queue platform tasks]
        StartSched -->|Daily| CronSync[SyncAllUsers: Queue user sync tasks]
        StartSched -->|Daily| CronPrune[PruneOldData: Delete contests > 30 days old]
        StartSched -->|Every 15m| CronOAuth[CleanupOAuthStates: Prune states > 10m old]
    end

    subgraph Task Processing
        ConsumeLocal -->|Consume Extraction Task| FetchPlat[Fetch contests from platform]
        FetchPlat --> DBBatch[Batch Insert/Update contests in PostgreSQL]
        
        ConsumeLocal -->|Consume Sync Task| CheckLock{Valkey/sync.Map Lock active?}
        CheckLock -->|Yes| LockActive[Log warning & abort task]
        CheckLock -->|No| AcquireLock[Acquire user sync lock & set status = syncing]
        
        AcquireLock --> DecryptToken[Decrypt Google Refresh Token via AES-256-GCM]
        DecryptToken --> CalResolution{use_dedicated enabled?}
        
        CalResolution -->|Yes| CheckDed[Check/Create Dedicated ContestSync Calendar]
        CalResolution -->|No| UsePrimary[Use primary Calendar]
        
        CheckDed --> QueryContests[Query future contests matching user's platforms]
        UsePrimary --> QueryContests
        
        QueryContests --> LoopContests{For each unsynced contest}
        LoopContests -->|Next| DetID[Compute MD5 + Base32 deterministic Event ID]
        DetID --> InsertCal{Insert Event to Calendar API}
        
        InsertCal -->|Success| SaveSync[Insert record into synced_events]
        InsertCal -->|409 Conflict| SaveSyncConflict[Insert record into synced_events ON CONFLICT DO NOTHING]
        InsertCal -->|Transient Failure| RetryInsert{Retry < 3 times with backoff?}
        
        RetryInsert -->|Yes| InsertCal
        RetryInsert -->|No| LogErr[Log failure and continue]
        
        SaveSync --> LoopContests
        SaveSyncConflict --> LoopContests
        LogErr --> LoopContests
        
        LoopContests -->|Done| SetSuccessStatus[Set user status = success, update last_sync_at]
        SetSuccessStatus --> ReleaseLock[Release lock & end task]
    end
```

---

## Security Framework

ContestSync enforces a stringent, zero-trust security architecture:
1. **At-Rest Token Encryption**: Credentials and Google Calendar OAuth2 Refresh Tokens are encrypted in the PostgreSQL database using AES-256-GCM with a dedicated 32-byte encryption key.
2. **Session Hijacking Defenses**: Session identifiers are generated securely using high-entropy random keys. Session IDs are fully regenerated (`session.ID = ""`) upon successful login callbacks to eliminate session fixation vulnerabilities.
3. **Session-Bound CSRF Token validation**: Action-modifying requests (`POST /preferences`, `POST /sync`, `DELETE /account`) require custom `X-CSRF-Token` headers verified directly against session variables, preventing cross-site scripting vulnerabilities.
4. **Denial of Service (DoS) Hardening**:
   - Headers: Admin authentication headers are restricted to a maximum length of 256 bytes.
   - Payloads: Incoming JSON request bodies are restricted to a maximum size of 1MB via `http.MaxBytesReader`.
5. **Rate Limiting Protection**: Distributed IP rate-limiting prevents credential stuffing and endpoint abuse.
6. **Graceful Termination**: Handles OS interrupts and termination signals (SIGINT, SIGTERM), completing ongoing sync jobs and cleaning up connections.

---

## Tech Stack & Dependencies

* **Frontend UI**: HTML5, Vanilla JavaScript, CSS variables, Lenis Smooth Scroll, GSAP & ScrollTrigger.
* **Backend Runtime**: Golang (Go 1.26+), Chi Router, pgx/v5 PostgreSQL pool, Go-Redis/v9 Valkey client, robfig/cron, gorilla/sessions.
* **Broker & Queue**: Apache Kafka (segmentio/kafka-go) or Local Go Channels.
* **Persistence**: PostgreSQL, Valkey.
* **External APIs**: Google Calendar API v3.

---

## Getting Started

### 1. Environment Setup

Configure a `.env` file in the project root:

```env
POSTGRES_DB=postgres://user:pass@host:port/db?sslmode=require
CONNECTION_LIMIT=10
GOOGLE_CLIENT_ID=your_id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your_secret
GOOGLE_REDIRECT_URL=http://localhost:8080/auth/google/callback
SESSION_SECRET=your_32_byte_hex_secret
CSRF_SECRET=your_32_byte_hex_secret
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

### 2. Compilation and Running

#### Native Go Compilation

To compile and launch the server service locally:

```powershell
$env:CGO_ENABLED=0; $env:GOOS="windows"; $env:GOARCH="amd64"; $env:GOAMD64="v3"; go build -tags "netgo osusergo" -trimpath -buildvcs=false -ldflags="-s -w -extldflags -static" -o server.exe ./cmd/server/main.go 2>&1
./server.exe
```

To compile and run the worker service locally:

```powershell
$env:CGO_ENABLED=0; $env:GOOS="windows"; $env:GOARCH="amd64"; $env:GOAMD64="v3"; go build -tags "netgo osusergo" -trimpath -buildvcs=false -ldflags="-s -w -extldflags -static" -o worker.exe ./cmd/worker/main.go 2>&1
./worker.exe
```

#### Docker Containerization

To build the two separate Docker images manually:

```powershell
docker build -f Dockerfile.server -t contestsync-server .
docker build -f Dockerfile.worker -t contestsync-worker .
```

To build and launch both services concurrently using Docker Compose:

```powershell
docker compose up --build
```

---

## License

MIT © 2026 ContestSync. See [LICENSE](LICENSE) for details.
