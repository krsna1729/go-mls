# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./

# Enable auto toolchain to download required Go version
ENV GOTOOLCHAIN=auto

RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o go-mls .

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache \
    ffmpeg \
    curl \
    ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/go-mls .

# Copy web assets
COPY web/ ./web/

# Create directories
RUN mkdir -p /recordings /hls

EXPOSE 8080 1935

# Health check
HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/stats || exit 1

ENTRYPOINT ["./go-mls"]
CMD ["-config", "config.json"]
