#!/bin/sh
set -e

API="http://go-mls:8080"
RTMP_HUB="rtmp://go-mls:1935"
STREAM_PATH="push-stream"
MAX_RETRIES=120

echo "source-push: Waiting for go-mls API (max ${MAX_RETRIES}s)..."
i=0
while [ $i -lt $MAX_RETRIES ]; do
    if curl -sf "${API}/stats" > /dev/null 2>&1; then
        echo "source-push: go-mls API ready"
        break
    fi
    i=$((i+1))
    sleep 1
done

if [ $i -eq $MAX_RETRIES ]; then
    echo "source-push: FAIL - go-mls API not ready after ${MAX_RETRIES}s"
    exit 1
fi

echo "source-push: Waiting for ${STREAM_PATH} to be registered..."
while true; do
    inputs=$(curl -s "${API}/inputs" 2>/dev/null)
    if echo "$inputs" | grep -q "\"stream_path\":\"${STREAM_PATH}\""; then
        echo "source-push: ${STREAM_PATH} registered, ready to push"
        break
    fi
    echo "source-push: Waiting for ${STREAM_PATH} registration..."
    sleep 1
done

echo "source-push: Starting ffmpeg push to ${RTMP_HUB}/${STREAM_PATH}"
exec ffmpeg -re -stream_loop -1 -i /testdata/testsrc.mp4 -c copy -f flv "${RTMP_HUB}/${STREAM_PATH}"
