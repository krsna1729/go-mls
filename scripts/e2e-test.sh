#!/bin/sh
set -e

mkdir -p /results
chmod 777 /results

API="http://go-mls:8080"
RTMP_HUB="rtmp://go-mls:1935"
OUTPUT_RTMP="rtmp://output-rtmp:1935"
SOURCE_RTMP="rtmp://source-rtmp:1935/live/testsrc"
RELAY_CONFIG="/relay_config.json"

echo "=========================================="
echo "Go-MLS E2E Test: Pull + Push Simultaneous"
echo "HTTP API: ${API}"
echo "RTMP Hub: ${RTMP_HUB}"
echo "=========================================="
echo ""

wait_for_stream() {
    url=$1
    timeout=$2
    name=$3
    echo "Waiting for stream $name (timeout: ${timeout}s)..."
    for i in $(seq 1 $timeout); do
        if ffprobe -v quiet -show_entries stream=codec_type "$url" 2>/dev/null | grep -q video; then
            echo "  OK: Stream $name is active!"
            return 0
        fi
        sleep 1
    done
    echo "  FAIL: Stream $name timed out"
    return 1
}

wait_for_hls() {
    stream=$1
    timeout=$2
    echo "Waiting for HLS playlist $stream (timeout: ${timeout}s)..."
    for i in $(seq 1 $timeout); do
        content=$(curl -s "${API}/hls/${stream}/index.m3u8" 2>/dev/null)
        if echo "$content" | grep -q "EXTM3U"; then
            echo "  OK: HLS playlist ready!"
            return 0
        fi
        sleep 1
    done
    echo "  FAIL: HLS playlist timed out"
    return 1
}

assert_relay_config_push_path() {
    cfg=$1
    echo "Validating relay config push path expectations..."
    grep -q '"input_name"[[:space:]]*:[[:space:]]*"push-stream"' "$cfg" || {
        echo "  FAIL: relay config missing push-stream input_name"
        return 1
    }
    grep -q '"input_url"[[:space:]]*:[[:space:]]*""' "$cfg" || {
        echo "  FAIL: relay config push input_url must be empty (accept mode)"
        return 1
    }
    grep -q 'output-rtmp:1935/live/push-' "$cfg" || {
        echo "  FAIL: relay config missing push output URLs on output-rtmp"
        return 1
    }
    echo "  OK: relay config push path is aligned"
}

wait_for_input_status() {
    stream=$1
    status=$2
    timeout=$3
    echo "Waiting for input ${stream} status=${status} (timeout: ${timeout}s)..."
    for i in $(seq 1 $timeout); do
        stats=$(curl -s "${API}/stats")
        if echo "$stats" | tr -d '\n' | grep -q "\"stream_path\":\"${stream}\".*\"status\":\"${status}\""; then
            echo "  OK: input ${stream} is ${status}"
            return 0
        fi
        sleep 1
    done
    echo "  FAIL: input ${stream} did not reach ${status}"
    return 1
}

probe_profile() {
    url=$1
    ffprobe -v error -select_streams v:0 \
        -show_entries stream=width,height,r_frame_rate \
        -of default=nw=1:nk=1 "$url" 2>/dev/null | head -3 | tr '\n' ' '
}

wait_for_profile() {
    url=$1
    timeout=$2
    for i in $(seq 1 $timeout); do
        p=$(probe_profile "$url")
        set -- $p
        if [ -n "$1" ] && [ -n "$2" ] && [ -n "$3" ]; then
            echo "$1 $2 $3"
            return 0
        fi
        sleep 1
    done
    return 1
}

verify_profile_exact() {
    url=$1
    expected_w=$2
    expected_h=$3
    expected_fps=$4
    name=$5

    profile=$(wait_for_profile "$url" 30) || {
        echo "  FAIL: ${name} profile unavailable"
        return 1
    }

    set -- $profile
    w=$1
    h=$2
    rate=$3
    fps=$(echo "$rate" | awk -F/ '{ if ($2 > 0) printf("%d", $1 / $2); else printf("%d", $1) }')

    if [ "$w" = "$expected_w" ] && [ "$h" = "$expected_h" ] && [ "$fps" = "$expected_fps" ]; then
        echo "  OK: ${name} profile ${w}x${h}@${fps}"
        return 0
    fi

    echo "  FAIL: ${name} expected ${expected_w}x${expected_h}@${expected_fps}, got ${w}x${h}@${fps}"
    return 1
}

verify_profile_dims_any_order() {
    url=$1
    dim_a=$2
    dim_b=$3
    expected_fps=$4
    name=$5

    profile=$(wait_for_profile "$url" 30) || {
        echo "  FAIL: ${name} profile unavailable"
        return 1
    }

    set -- $profile
    w=$1
    h=$2
    rate=$3
    fps=$(echo "$rate" | awk -F/ '{ if ($2 > 0) printf("%d", $1 / $2); else printf("%d", $1) }')

    if [ "$fps" != "$expected_fps" ]; then
        echo "  FAIL: ${name} expected fps ${expected_fps}, got ${fps}"
        return 1
    fi

    if { [ "$w" = "$dim_a" ] && [ "$h" = "$dim_b" ]; } || { [ "$w" = "$dim_b" ] && [ "$h" = "$dim_a" ]; }; then
        echo "  OK: ${name} profile ${w}x${h}@${fps}"
        return 0
    fi

    echo "  FAIL: ${name} expected dimensions ${dim_a}x${dim_b} (any order), got ${w}x${h}"
    return 1
}

echo "Waiting for go-mls API..."
for i in $(seq 1 30); do
    if curl -sf "${API}/stats" > /dev/null 2>&1; then
        echo "OK: go-mls API ready"
        break
    fi
    if [ $i -eq 30 ]; then
        echo "FAIL: go-mls API not ready"
        exit 1
    fi
    sleep 1
done
echo ""

echo "Waiting for RTMP source stream..."
wait_for_stream "${SOURCE_RTMP}" 30 "source-rtmp/testsrc"
echo ""

echo "=== STEP 1: Register Inputs ==="
echo "1.1: Register pull input"
curl -s -X POST "${API}/inputs" \
    -H "Content-Type: application/json" \
    -d '{"stream_path": "pull-stream", "remote_url": "'"${SOURCE_RTMP}"'", "mode": "pull"}' | tee /results/step1_pull_input.json
echo ""

echo "1.2: Register push input (acceptor)"
curl -s -X POST "${API}/inputs" \
    -H "Content-Type: application/json" \
    -d '{"stream_path": "push-stream"}' | tee /results/step1_push_input.json
echo ""

echo "1.3: Verify both inputs registered"
curl -s "${API}/inputs" | tee /results/step1_inputs.json
echo ""

echo "1.4: Verify push acceptor path is push-stream"
wait_for_input_status "push-stream" "Starting" 20 || wait_for_input_status "push-stream" "Active" 20
echo ""

echo "=== STEP 2: Start Streams ==="
echo "2.1: Push stream should auto-start from source-push container"
echo "  (source-push auto-registers and starts pushing on startup)"
sleep 5
echo ""

echo "2.2: Pull stream already started by go-mls puller"
sleep 3
echo ""

echo "2.3: Verify inputs status"
STATS=$(curl -s "${API}/stats")
echo "$STATS" | tee /results/step2_stats.json
echo ""

echo "2.4: Verify streams with ffprobe"
wait_for_stream "${RTMP_HUB}/push-stream" 10 "push-stream" || echo "  Note: push-stream may need more time"
wait_for_stream "${RTMP_HUB}/pull-stream" 10 "pull-stream" || echo "  Note: pull-stream may need more time"
echo ""

echo "=== STEP 3: Create Outputs (2 per input) ==="
echo "3.1: Create outputs for pull-stream"
curl -s -X POST "${API}/outputs" \
    -H "Content-Type: application/json" \
    -d '{"stream_path": "pull-stream", "output_id": "pull-to-rtmp1", "remote_url": "'"${OUTPUT_RTMP}/live/pull1"'"}' | tee /results/step3_pull_out1.json
curl -s -X POST "${API}/outputs" \
    -H "Content-Type: application/json" \
    -d '{"stream_path": "pull-stream", "output_id": "pull-to-rtmp2", "remote_url": "'"${OUTPUT_RTMP}/live/pull2"'"}' | tee /results/step3_pull_out2.json
echo ""

echo "3.2: Create outputs for push-stream"
curl -s -X POST "${API}/outputs" \
    -H "Content-Type: application/json" \
    -d '{"stream_path": "push-stream", "output_id": "push-to-rtmp1", "remote_url": "'"${OUTPUT_RTMP}/live/push1"'"}' | tee /results/step3_push_out1.json
curl -s -X POST "${API}/outputs" \
    -H "Content-Type: application/json" \
    -d '{"stream_path": "push-stream", "output_id": "push-to-rtmp2", "remote_url": "'"${OUTPUT_RTMP}/live/push2"'"}' | tee /results/step3_push_out2.json
echo ""

echo "3.3: Verify outputs"
curl -s "${API}/outputs" | tee /results/step3_outputs.json
echo ""

echo "3.4: Verify output streams with ffprobe"
sleep 3
wait_for_stream "${OUTPUT_RTMP}/live/pull1" 15 "pull-to-rtmp1" || true
wait_for_stream "${OUTPUT_RTMP}/live/pull2" 15 "pull-to-rtmp2" || true
wait_for_stream "${OUTPUT_RTMP}/live/push1" 15 "push-to-rtmp1" || true
wait_for_stream "${OUTPUT_RTMP}/live/push2" 15 "push-to-rtmp2" || true
echo ""

echo "=== STEP 4: Recording ==="
echo "4.1: Start recording for pull-stream"
curl -s -X POST "${API}/record?stream=pull-stream" | tee /results/step4_rec_pull.json
echo ""

echo "4.2: Start recording for push-stream"
curl -s -X POST "${API}/record?stream=push-stream" | tee /results/step4_rec_push.json
echo ""

echo "4.3: Verify recording status"
curl -s "${API}/stats" | tee /results/step4_stats.json
echo ""

echo "=== STEP 5: HLS Generation ==="
echo "5.1: Start HLS for pull-stream"
PULL_HLS=$(curl -s -X POST "${API}/hls/start?stream=pull-stream")
echo "$PULL_HLS" | tee /results/step5_hls_pull.json
PULL_VIEWER_ID=$(echo "$PULL_HLS" | sed -n 's/.*"viewer_id":"\([^"]*\)".*/\1/p')
echo ""

echo "5.2: Start HLS for push-stream"
PUSH_HLS=$(curl -s -X POST "${API}/hls/start?stream=push-stream")
echo "$PUSH_HLS" | tee /results/step5_hls_push.json
PUSH_VIEWER_ID=$(echo "$PUSH_HLS" | sed -n 's/.*"viewer_id":"\([^"]*\)".*/\1/p')
echo ""

wait_for_hls "pull-stream" 20
wait_for_hls "push-stream" 20
echo ""

echo "=== STEP 6: Verification ==="
echo "6.1: Get final stats"
STATS=$(curl -s "${API}/stats")
echo "$STATS" | tee /results/step6_stats.json
echo ""

INPUT_COUNT=$(echo "$STATS" | sed -n 's/.*"inputs":\[\(.*\)\],"outputs".*/\1/p' | grep -o '"stream_path"' | wc -l)
OUTPUT_COUNT=$(echo "$STATS" | grep -o '"output_id"' | wc -l)
echo "  Inputs: $INPUT_COUNT (expected: 2)"
echo "  Outputs: $OUTPUT_COUNT (expected: 4)"
echo ""

echo "6.2: Check recordings"
ls -la /recordings/ 2>/dev/null | grep -E "pull-stream|push-stream" | tee /results/step6_recordings.txt || echo "  No recordings yet"
echo ""

echo "6.3: Check HLS files"
find /hls -name "*.m3u8" 2>/dev/null | tee /results/step6_hls_files.txt || echo "  No HLS files yet"
echo ""

echo "6.4: Export baseline config"
curl -s "${API}/system/export" | tee /results/step6_export_baseline.json
echo ""

echo "=== STEP 7: Bulk Import + Preset Verification ==="
if [ ! -f "${RELAY_CONFIG}" ]; then
    echo "FAIL: relay config file not found at ${RELAY_CONFIG}"
    exit 1
fi

echo "7.0: Validate relay_config push path before import"
assert_relay_config_push_path "${RELAY_CONFIG}"
echo ""

echo "7.1: Import relay_config"
curl -s -X POST "${API}/system/import" \
    -H "Content-Type: application/json" \
    --data-binary @"${RELAY_CONFIG}" | tee /results/step7_import_response.json
echo ""

echo "7.2: Wait for imported inputs"
wait_for_input_status "pull-stream" "Active" 45
wait_for_input_status "push-stream" "Active" 60
echo ""

echo "7.3: Verify imported outputs with ffprobe"
wait_for_stream "${OUTPUT_RTMP}/live/pull-youtube" 30 "pull-youtube"
wait_for_stream "${OUTPUT_RTMP}/live/pull-custom" 30 "pull-custom"
wait_for_stream "${OUTPUT_RTMP}/live/push-instagram" 30 "push-instagram"
wait_for_stream "${OUTPUT_RTMP}/live/push-custom-rot" 30 "push-custom-rot"
echo ""

echo "7.4: Validate preset profiles via ffprobe"
verify_profile_exact "${OUTPUT_RTMP}/live/pull-youtube" "1920" "1080" "30" "pull-youtube"
verify_profile_exact "${OUTPUT_RTMP}/live/pull-custom" "1280" "720" "30" "pull-custom"
verify_profile_dims_any_order "${OUTPUT_RTMP}/live/push-instagram" "720" "1280" "30" "push-instagram"
verify_profile_dims_any_order "${OUTPUT_RTMP}/live/push-custom-rot" "720" "1280" "30" "push-custom-rot"
echo ""

echo "7.5: Export imported config"
curl -s "${API}/system/export" | tee /results/step7_export_after_import.json
echo ""

echo "=== STEP 8: Cleanup ==="
echo "8.1: Stop HLS"
curl -s -X POST "${API}/hls/stop" -H "Content-Type: application/json" -d '{"stream": "pull-stream", "viewer_id": "'"${PULL_VIEWER_ID}"'"}' | tee /results/step7_hls_pull.json
curl -s -X POST "${API}/hls/stop" -H "Content-Type: application/json" -d '{"stream": "push-stream", "viewer_id": "'"${PUSH_VIEWER_ID}"'"}' | tee /results/step7_hls_push.json
echo ""

echo "8.2: Stop recordings"
curl -s -X DELETE "${API}/record?stream=pull-stream" | tee /results/step7_rec_pull.json
curl -s -X DELETE "${API}/record?stream=push-stream" | tee /results/step7_rec_push.json
echo ""

echo "8.3: Delete all outputs"
curl -s -X DELETE "${API}/outputs?stream=pull-stream&id=pull-to-rtmp1" | tee /results/step7_out_pull1.json
curl -s -X DELETE "${API}/outputs?stream=pull-stream&id=pull-to-rtmp2" | tee /results/step7_out_pull2.json
curl -s -X DELETE "${API}/outputs?stream=push-stream&id=push-to-rtmp1" | tee /results/step7_out_push1.json
curl -s -X DELETE "${API}/outputs?stream=push-stream&id=push-to-rtmp2" | tee /results/step7_out_push2.json
curl -s -X DELETE "${API}/outputs?stream=pull-stream&id=pull-youtube" | tee /results/step8_out_pull_youtube.json
curl -s -X DELETE "${API}/outputs?stream=pull-stream&id=pull-custom" | tee /results/step8_out_pull_custom.json
curl -s -X DELETE "${API}/outputs?stream=push-stream&id=push-instagram" | tee /results/step8_out_push_instagram.json
curl -s -X DELETE "${API}/outputs?stream=push-stream&id=push-custom-rot" | tee /results/step8_out_push_custom_rot.json
echo ""

echo "8.4: Delete all inputs"
curl -s -X DELETE "${API}/inputs?stream=pull-stream" | tee /results/step7_in_pull.json
curl -s -X DELETE "${API}/inputs?stream=push-stream" | tee /results/step7_in_push.json
echo ""

echo "8.5: Export final config"
curl -s "${API}/system/export" | tee /results/step7_export.json
echo ""

echo "=========================================="
echo "E2E TESTS COMPLETED!"
echo "Results saved to ./test-results/"
echo "=========================================="
