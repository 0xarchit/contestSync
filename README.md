# ContestSync

> **Sync contests to your calendar, beautifully.**

ContestSync is a web application designed to automatically sync competitive programming contests from major platforms (LeetCode, Codeforces, AtCoder, etc.) directly to your Google Calendar.

---

## Features

- **Automated Sync:** Scrapes and updates contest data every 6 hours.
- **Cinematic UI:** Smooth scrolling (Lenis) and interactive animations (GSAP).
- **Security First:** AES-256-GCM encryption for tokens, CSRF protection, and secure session management.
- **Platform Support:** 
  - LeetCode
  - Codeforces
  - CodeChef
  - AtCoder
  - HackerRank
  - GeeksforGeeks
  - Code360

---

## Tech Stack

- **Frontend:** Pure HTML, CSS, Vanilla JavaScript, Lenis, GSAP.
- **Backend:** Golang (Chi Router, pgx/v5).
- **Database:** PostgreSQL.
- **API:** Google Calendar API v3.

---

## Getting Started

### 1. Prerequisites
- Go 1.26+
- PostgreSQL
- Google Cloud Console Project (with Calendar API enabled)

### 2. Environment Setup
Create a `.env` file in the root:

```env
POSTGRES_DB=postgres://user:pass@host:port/db?sslmode=require
GOOGLE_CLIENT_ID=your_id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your_secret
GOOGLE_REDIRECT_URL=http://localhost:8080/auth/google/callback
SESSION_SECRET=your_32_byte_hex_secret
CSRF_SECRET=your_32_byte_hex_secret
ADMIN_PASSWORD=your_admin_pass
PORT=8080
ENV=development
ALLOWED_ORIGIN=http://localhost:8080
```

### 3. Run the Application
```bash
$env:CGO_ENABLED=0; $env:GOOS="windows"; $env:GOARCH="amd64"; $env:GOAMD64="v3"; go build -tags "netgo osusergo" -trimpath -buildvcs=false -ldflags="-s -w" -o server.exe ./cmd/server/main.go 2>&1
./server.exe
```

---

## Security

ContestSync implements best-in-class security practices:
- **AES-256-GCM** encryption for Google Refresh Tokens at rest.
- **Double Submit Cookie** pattern for CSRF protection.
- **Strict Content-Security-Policy (CSP)**.
- **Session ID regeneration** post-login to prevent fixation.

---

## License

MIT © 2026 ContestSync. See [LICENSE](LICENSE) for details.
