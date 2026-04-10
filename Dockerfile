# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /build

ENV GOPROXY=https://goproxy.cn,direct

# Download dependencies (cached layer)
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o server .

# Final stage
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata tini

WORKDIR /app

# Create non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Copy binary and assets from builder
COPY --from=builder /build/server .
COPY --chown=appuser:appgroup public/ ./public/
COPY --chown=appuser:appgroup config.json .

# Security: switch to non-root user
USER appuser

# Expose ports
EXPOSE 80 25

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD nc -z localhost 80 || exit 1

# Set environment defaults
ENV PORT=80 SMTP_PORT=25 SMTP_HOST=0.0.0.0

ENTRYPOINT ["/sbin/tini", "--", "./server"]
