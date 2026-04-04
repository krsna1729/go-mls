# ============================================================================
# Build stage
# ============================================================================
FROM golang:1.23-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./

ENV GOTOOLCHAIN=auto

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o go-mls .

# ============================================================================
# Runtime stage (go-mls)
# ============================================================================
FROM alpine:3.19 AS runtime

RUN apk add --no-cache \
    ffmpeg \
    curl \
    ca-certificates

WORKDIR /app

COPY --from=builder /app/go-mls .
COPY web/ ./web/

RUN mkdir -p /recordings /hls

EXPOSE 8080 1935

HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/stats || exit 1

ENTRYPOINT ["./go-mls"]
CMD ["-config", "config.json"]

# ============================================================================
# Source push stage (FFmpeg pushing to RTMP)
# ============================================================================
FROM jrottenberg/ffmpeg:4.4-alpine AS source-push

RUN apk add --no-cache curl bash

# ============================================================================
# Test runner stage
# ============================================================================
FROM jrottenberg/ffmpeg:4.4-alpine AS test-runner

RUN apk add --no-cache curl bash
