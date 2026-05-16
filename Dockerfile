# Stage 1: Build
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy dependency files and download
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary with optimized flags for 'scratch'
# CGO_ENABLED=0 creates a statically linked binary
# -ldflags="-s -w" removes symbol tables and debug information to reduce size
# -trimpath removes file system paths from the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -tags "netgo osusergo" -trimpath -buildvcs=false \
    -ldflags="-s -w" \
    -o server ./cmd/server/main.go

# Stage 2: Final Image
FROM scratch

# Import certificates from builder for secure HTTPS calls (Google API)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the binary
COPY --from=builder /app/server /server

# Copy static web assets
COPY --from=builder /app/web/static /web/static

# Expose the application port
EXPOSE 8080

# Run the server
ENTRYPOINT ["/server"]
