# ============================================
# Multi-stage build for the API Gateway
# ============================================

# Stage 1: Build the gateway binary
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy dependency files first (better layer caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the gateway binary — static, no CGO
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /gateway cmd/gateway/main.go

# Build the example backend binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /backend examples/backend/main.go

# ============================================
# Stage 2: Minimal runtime image for gateway
# ============================================
FROM alpine:3.19 AS gateway

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /gateway .
COPY config/ ./config/

EXPOSE 8080

ENTRYPOINT ["./gateway"]
CMD ["-config", "config/gateway.yaml"]

# ============================================
# Stage 3: Minimal runtime image for backend
# ============================================
FROM alpine:3.19 AS backend

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /backend .

EXPOSE 8081

ENTRYPOINT ["./backend"]
