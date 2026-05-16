# Stage 1: Build
FROM golang:alpine AS builder

# Install build dependencies
RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy dependency files and download
COPY go.mod go.sum ./
RUN go mod download

# Copy source code (including web/static for embedding)
COPY . .

# Build the binary with optimized flags
# Note: Building for linux/amd64 as it will run in the scratch container
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -tags "netgo osusergo" -trimpath -buildvcs=false \
    -ldflags="-s -w" \
    -o server ./cmd/server/main.go

# Stage 2: Final Image
FROM scratch

# Import certificates for secure HTTPS calls
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the binary (now includes embedded static assets)
COPY --from=builder /app/server /server

# Expose the application port
EXPOSE 8080


USER 65532:65532


# Run the server
ENTRYPOINT ["/server"]
