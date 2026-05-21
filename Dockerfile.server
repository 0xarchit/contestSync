FROM golang:alpine AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -tags "netgo osusergo" -trimpath -buildvcs=false \
    -ldflags="-s -w -extldflags -static" \
    -o server ./cmd/server/main.go

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=builder /app/server /server

EXPOSE 8080

USER 65532:65532

ENTRYPOINT ["/server"]
