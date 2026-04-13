# ── Build stage ────────────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Cache dependencies separately for faster rebuilds
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" -o /gateway ./cmd/gateway/main.go

# ── Runtime stage ───────────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /gateway .

EXPOSE 8080

CMD ["./gateway"]
