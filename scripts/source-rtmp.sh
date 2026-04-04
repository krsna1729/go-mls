#!/bin/sh
set -e

STREAM_PATH="testsrc"
RTMP_HOST="127.0.0.1"
RTMP_APP="live"
RTMP_URL="rtmp://${RTMP_HOST}:1935/${RTMP_APP}/${STREAM_PATH}"
MAX_RETRIES=60

echo "source-rtmp: Starting nginx-rtmp..."
nginx &

echo "source-rtmp: Waiting for nginx to be ready (max ${MAX_RETRIES}s)..."
i=0
while [ $i -lt $MAX_RETRIES ]; do
    if pgrep -f "nginx: master process" > /dev/null 2>&1; then
        echo "source-rtmp: nginx ready"
        break
    fi
    i=$((i+1))
    sleep 1
done

if [ $i -eq $MAX_RETRIES ]; then
    echo "source-rtmp: FAIL - nginx not ready after ${MAX_RETRIES}s"
    exit 1
fi

echo "source-rtmp: Waiting for RTMP port 1935 to accept connections..."
i=0
while [ $i -lt $MAX_RETRIES ]; do
    if nc -z "${RTMP_HOST}" 1935 > /dev/null 2>&1; then
        echo "source-rtmp: RTMP port ready"
        break
    fi
    i=$((i+1))
    sleep 1
done

if [ $i -eq $MAX_RETRIES ]; then
    echo "source-rtmp: FAIL - RTMP port 1935 not ready after ${MAX_RETRIES}s"
    exit 1
fi

if [ -f /var/log/nginx/error.log ]; then
    tail -n 20 /var/log/nginx/error.log
else
    echo "source-rtmp: nginx error log not present, continuing"
fi

echo "source-rtmp: Starting ffmpeg to push test video to ${RTMP_URL}"
exec ffmpeg -re -stream_loop -1 -i /testdata/testsrc.mp4 -c copy -f flv "${RTMP_URL}"
