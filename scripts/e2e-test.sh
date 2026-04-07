#!/bin/sh

API="${API:-http://go-mls:8080}"
RTMP_HUB="${RTMP_HUB:-rtmp://go-mls:1935}"
HARNESS_RTMP_LOCAL="${HARNESS_RTMP_LOCAL:-rtmp://127.0.0.1:1935}"
HARNESS_RTMP_REMOTE="${HARNESS_RTMP_REMOTE:-rtmp://qa-harness:1935}"
SOURCE_STREAM_PATH="${SOURCE_STREAM_PATH:-live/testsrc}"
SOURCE_RTMP_LOCAL="${SOURCE_RTMP_LOCAL:-${HARNESS_RTMP_LOCAL}/${SOURCE_STREAM_PATH}}"
SOURCE_RTMP_FOR_GOMLS="${SOURCE_RTMP_FOR_GOMLS:-${HARNESS_RTMP_REMOTE}/${SOURCE_STREAM_PATH}}"
OUTPUT_RTMP_FOR_GOMLS="${OUTPUT_RTMP_FOR_GOMLS:-${HARNESS_RTMP_REMOTE}}"
OUTPUT_RTMP_LOCAL="${OUTPUT_RTMP_LOCAL:-${HARNESS_RTMP_LOCAL}}"
RELAY_CONFIG="${RELAY_CONFIG:-/relay_config.json}"
RESULTS_DIR="${RESULTS_DIR:-/results}"
RECORDINGS_DIR="${RECORDINGS_DIR:-/recordings}"
HLS_DIR="${HLS_DIR:-/hls}"
TEST_SOURCE_FILE="${TEST_SOURCE_FILE:-/testdata/testsrc.mp4}"
FFPROBE_TIMEOUT_S="${FFPROBE_TIMEOUT_S:-4}"
FFPROBE_ANALYZE_US="${FFPROBE_ANALYZE_US:-2000000}"
FFPROBE_PROBESIZE="${FFPROBE_PROBESIZE:-1000000}"
FFPROBE_RW_TIMEOUT_US="${FFPROBE_RW_TIMEOUT_US:-3000000}"

NGINX_PID=""
PULL_SRC_PID=""
PUSH_SRC_PID=""
PULL_VIEWER_ID=""
PUSH_VIEWER_ID=""

results_path() {
    printf '%s/%s' "${RESULTS_DIR}" "$1"
}

e2e_init() {
    mkdir -p "${RESULTS_DIR}"
    chmod 777 "${RESULTS_DIR}" 2>/dev/null || true
}

e2e_banner() {
    echo "=========================================="
    echo "Go-MLS E2E Test (QA Harness Consolidated)"
    echo "HTTP API: ${API}"
    echo "RTMP Hub: ${RTMP_HUB}"
    echo "Harness Local RTMP: ${HARNESS_RTMP_LOCAL}"
    echo "Results Dir: ${RESULTS_DIR}"
    echo "=========================================="
    echo ""
}

cleanup() {
    set +e
    if [ -n "${PUSH_SRC_PID}" ] && kill -0 "${PUSH_SRC_PID}" 2>/dev/null; then
        kill "${PUSH_SRC_PID}" 2>/dev/null || true
        wait "${PUSH_SRC_PID}" 2>/dev/null || true
    fi
    if [ -n "${PULL_SRC_PID}" ] && kill -0 "${PULL_SRC_PID}" 2>/dev/null; then
        kill "${PULL_SRC_PID}" 2>/dev/null || true
        wait "${PULL_SRC_PID}" 2>/dev/null || true
    fi
    if [ -n "${NGINX_PID}" ] && kill -0 "${NGINX_PID}" 2>/dev/null; then
        kill "${NGINX_PID}" 2>/dev/null || true
        wait "${NGINX_PID}" 2>/dev/null || true
    fi
}

e2e_install_traps() {
    trap cleanup EXIT INT TERM
}

wait_for_go_mls_api() {
    echo "Waiting for go-mls API..."
    for i in $(seq 1 30); do
        if curl -sf "${API}/stats" >/dev/null 2>&1; then
            echo "OK: go-mls API ready"
            echo ""
            return 0
        fi
        sleep 1
    done
    echo "FAIL: go-mls API not ready"
    return 1
}

start_local_rtmp_server() {
    echo "Starting local nginx-rtmp in qa-harness..."
    nginx &
    NGINX_PID=$!

    for i in $(seq 1 30); do
        if pgrep -f "nginx: master process" >/dev/null 2>&1 && nc -z 127.0.0.1 1935 >/dev/null 2>&1; then
            echo "  OK: local RTMP server ready"
            return 0
        fi
        sleep 1
    done

    echo "  FAIL: local RTMP server not ready"
    return 1
}

start_pull_source_publisher() {
    echo "Starting pull-source publisher to ${SOURCE_RTMP_LOCAL}"
    ffmpeg -re -stream_loop -1 -i "${TEST_SOURCE_FILE}" -c copy -f flv "${SOURCE_RTMP_LOCAL}" >/tmp/pull-source.log 2>&1 &
    PULL_SRC_PID=$!
}

stop_push_source() {
    if [ -n "${PUSH_SRC_PID}" ] && kill -0 "${PUSH_SRC_PID}" 2>/dev/null; then
        kill "${PUSH_SRC_PID}" 2>/dev/null || true
        wait "${PUSH_SRC_PID}" 2>/dev/null || true
    fi
    PUSH_SRC_PID=""
}

start_push_source() {
    stop_push_source
    echo "Starting push-source publisher to ${RTMP_HUB}/push-stream"
    ffmpeg -re -stream_loop -1 -i "${TEST_SOURCE_FILE}" -c copy -f flv "${RTMP_HUB}/push-stream" >/tmp/push-source.log 2>&1 &
    PUSH_SRC_PID=$!
}

wait_for_stream() {
    url=$1
    timeout=$2
    name=$3
    echo "Waiting for stream ${name} (timeout: ${timeout}s)..."
    for i in $(seq 1 "${timeout}"); do
        if fast_ffprobe -v error -hide_banner \
            -analyzeduration "${FFPROBE_ANALYZE_US}" \
            -probesize "${FFPROBE_PROBESIZE}" \
            -rw_timeout "${FFPROBE_RW_TIMEOUT_US}" \
            -select_streams v:0 \
            -show_entries stream=codec_type \
            -of default=nw=1:nk=1 \
            "${url}" 2>/dev/null | grep -q '^video$'; then
            echo "  OK: Stream ${name} is active!"
            return 0
        fi
        sleep 1
    done
    echo "  FAIL: Stream ${name} timed out"
    return 1
}

verify_stream_active() {
    wait_for_stream "$1" "$2" "$3"
}

wait_for_hls() {
    stream=$1
    timeout=$2
    echo "Waiting for HLS playlist ${stream} (timeout: ${timeout}s)..."
    for i in $(seq 1 "${timeout}"); do
        content=$(curl -s "${API}/hls/${stream}/index.m3u8" 2>/dev/null)
        if echo "${content}" | grep -q "EXTM3U"; then
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
    grep -q '"input_name"[[:space:]]*:[[:space:]]*"push-stream"' "${cfg}" || {
        echo "  FAIL: relay config missing push-stream input_name"
        return 1
    }
    grep -q '"input_url"[[:space:]]*:[[:space:]]*""' "${cfg}" || {
        echo "  FAIL: relay config push input_url must be empty (accept mode)"
        return 1
    }
    grep -q 'qa-harness:1935/live/push-' "${cfg}" || {
        echo "  FAIL: relay config missing push output URLs on qa-harness"
        return 1
    }
    echo "  OK: relay config push path is aligned"
}

wait_for_input_status() {
    stream=$1
    status=$2
    timeout=$3
    echo "Waiting for input ${stream} status=${status} (timeout: ${timeout}s)..."
    for i in $(seq 1 "${timeout}"); do
        stats=$(curl -s "${API}/stats")
        if echo "${stats}" | tr -d '\n' | grep -q "\"stream_path\":\"${stream}\".*\"status\":\"${status}\""; then
            echo "  OK: input ${stream} is ${status}"
            return 0
        fi
        sleep 1
    done
    echo "  FAIL: input ${stream} did not reach ${status}"
    return 1
}

register_pull_input() {
    stream=${1:-pull-stream}
    remote_url=${2:-${SOURCE_RTMP_FOR_GOMLS}}
    result_file=${3:-$(results_path step1_pull_input.json)}
    payload=$(printf '{"stream_path":"%s","remote_url":"%s","mode":"pull"}' "${stream}" "${remote_url}")
    curl -s -X POST "${API}/inputs" -H "Content-Type: application/json" -d "${payload}" | tee "${result_file}"
}

register_push_input() {
    stream=${1:-push-stream}
    result_file=${2:-$(results_path step1_push_input.json)}
    payload=$(printf '{"stream_path":"%s"}' "${stream}")
    curl -s -X POST "${API}/inputs" -H "Content-Type: application/json" -d "${payload}" | tee "${result_file}"
}

list_inputs() {
    result_file=${1:-$(results_path step1_inputs.json)}
    curl -s "${API}/inputs" | tee "${result_file}"
}

create_output() {
    stream=$1
    output_id=$2
    remote_url=$3
    result_file=$4
    payload=$(printf '{"stream_path":"%s","output_id":"%s","remote_url":"%s"}' "${stream}" "${output_id}" "${remote_url}")
    curl -s -X POST "${API}/outputs" -H "Content-Type: application/json" -d "${payload}" | tee "${result_file}"
}

list_outputs() {
    result_file=${1:-$(results_path step3_outputs.json)}
    curl -s "${API}/outputs" | tee "${result_file}"
}

start_output() {
    stream=$1
    output_id=$2
    result_file=$3
    payload=$(printf '{"stream_path":"%s","output_id":"%s"}' "${stream}" "${output_id}")

    max_attempts=8
    attempt=1
    while [ "${attempt}" -le "${max_attempts}" ]; do
        response=$(curl -s -X POST "${API}/outputs/start" -H "Content-Type: application/json" -d "${payload}")
        echo "${response}" | tee "${result_file}"

        if echo "${response}" | grep -q '"status":"ok"'; then
            return 0
        fi

        if [ "${attempt}" -lt "${max_attempts}" ]; then
            sleep 2
        fi
        attempt=$((attempt + 1))
    done

    echo "FAIL: unable to start output ${stream}/${output_id} after ${max_attempts} attempts"
    return 1
}

start_recording() {
    stream=$1
    result_file=$2
    curl -s -X POST "${API}/record?stream=${stream}" | tee "${result_file}"
}

start_hls_viewer() {
    stream=$1
    result_file=$2
    response=$(curl -s -X POST "${API}/hls/start?stream=${stream}")
    echo "${response}" | tee "${result_file}" >&2
    echo "${response}" | sed -n 's/.*"viewer_id":"\([^"]*\)".*/\1/p'
}

stop_hls_viewer() {
    stream=$1
    viewer_id=$2
    result_file=$3
    payload=$(printf '{"stream":"%s","viewer_id":"%s"}' "${stream}" "${viewer_id}")
    curl -s -X POST "${API}/hls/stop" -H "Content-Type: application/json" -d "${payload}" | tee "${result_file}"
}

export_config() {
    result_file=$1
    curl -s "${API}/system/export" | tee "${result_file}"
}

import_config() {
    config_file=${1:-${RELAY_CONFIG}}
    result_file=${2:-$(results_path step7_import_response.json)}
    curl -s -X POST "${API}/system/import" -H "Content-Type: application/json" --data-binary @"${config_file}" | tee "${result_file}"
}

stop_recording_if_active() {
    stream=$1
    result_file=$2
    if curl -s "${API}/recordings" | jq -e --arg stream "${stream}" '.[] | select(.stream_path == $stream and .active == true)' >/dev/null; then
        response=$(curl -s -X DELETE "${API}/record?stream=${stream}")
        if echo "${response}" | grep -q '"error":"no active recording"'; then
            echo '{"status":"skipped","reason":"no active recording"}' | tee "${result_file}"
        else
            echo "${response}" | tee "${result_file}"
        fi
    else
        echo '{"status":"skipped","reason":"no active recording"}' | tee "${result_file}"
    fi
}

delete_all_current_outputs() {
    curl -s "${API}/outputs" | jq -r '.[] | [.stream_path, .output_id] | @tsv' | while IFS="$(printf '\t')" read -r stream output_id; do
        [ -n "${stream}" ] || continue
        result_suffix=$(printf '%s_%s' "${stream}" "${output_id}" | tr '/-' '__')
        curl -s -X DELETE "${API}/outputs?stream=${stream}&id=${output_id}" | tee "$(results_path step8_out_${result_suffix}.json)"
    done
}

delete_input() {
    stream=$1
    result_file=$2
    curl -s -X DELETE "${API}/inputs?stream=${stream}" | tee "${result_file}"
}

probe_profile() {
    url=$1
    fast_ffprobe -v error -hide_banner \
        -analyzeduration "${FFPROBE_ANALYZE_US}" \
        -probesize "${FFPROBE_PROBESIZE}" \
        -rw_timeout "${FFPROBE_RW_TIMEOUT_US}" \
        -select_streams v:0 \
        -show_entries stream=width,height,r_frame_rate \
        -of default=nw=1:nk=1 \
        "${url}" 2>/dev/null | head -3 | tr '\n' ' '
}

# Run ffprobe with a hard wall-clock timeout when available to prevent hangs.
fast_ffprobe() {
    if command -v timeout >/dev/null 2>&1; then
        timeout "${FFPROBE_TIMEOUT_S}" ffprobe "$@"
    else
        ffprobe "$@"
    fi
}

wait_for_profile() {
    url=$1
    timeout=$2
    for i in $(seq 1 "${timeout}"); do
        p=$(probe_profile "${url}")
        set -- ${p}
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

    profile=$(wait_for_profile "${url}" 30) || {
        echo "  FAIL: ${name} profile unavailable"
        return 1
    }

    set -- ${profile}
    w=$1
    h=$2
    rate=$3
    fps=$(echo "${rate}" | awk -F/ '{ if ($2 > 0) printf("%d", $1 / $2); else printf("%d", $1) }')

    if [ "${w}" = "${expected_w}" ] && [ "${h}" = "${expected_h}" ] && [ "${fps}" = "${expected_fps}" ]; then
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

    profile=$(wait_for_profile "${url}" 30) || {
        echo "  FAIL: ${name} profile unavailable"
        return 1
    }

    set -- ${profile}
    w=$1
    h=$2
    rate=$3
    fps=$(echo "${rate}" | awk -F/ '{ if ($2 > 0) printf("%d", $1 / $2); else printf("%d", $1) }')

    if [ "${fps}" != "${expected_fps}" ]; then
        echo "  FAIL: ${name} expected fps ${expected_fps}, got ${fps}"
        return 1
    fi

    if { [ "${w}" = "${dim_a}" ] && [ "${h}" = "${dim_b}" ]; } || { [ "${w}" = "${dim_b}" ] && [ "${h}" = "${dim_a}" ]; }; then
        echo "  OK: ${name} profile ${w}x${h}@${fps}"
        return 0
    fi

    echo "  FAIL: ${name} expected dimensions ${dim_a}x${dim_b} (any order), got ${w}x${h}"
    return 1
}

e2e_prepare_harness() {
    e2e_init
    e2e_install_traps
    e2e_banner
    wait_for_go_mls_api
    start_local_rtmp_server
    start_pull_source_publisher
    echo "Waiting for RTMP source stream..."
    wait_for_stream "${SOURCE_RTMP_LOCAL}" 30 "harness/testsrc"
    echo ""
}

ensure_push_input_active() {
    stream=${1:-push-stream}
    wait_for_input_status "${stream}" "Starting" 20 || wait_for_input_status "${stream}" "Active" 20
    start_push_source
    wait_for_input_status "${stream}" "Active" 30
}

e2e_phase_inputs() {
    echo "=== STEP 1: Register Inputs ==="
    echo "1.1: Register pull input"
    register_pull_input "pull-stream" "${SOURCE_RTMP_FOR_GOMLS}" "$(results_path step1_pull_input.json)"
    echo ""

    echo "1.2: Register push input (acceptor)"
    register_push_input "push-stream" "$(results_path step1_push_input.json)"
    echo ""

    echo "1.3: Verify both inputs registered"
    list_inputs "$(results_path step1_inputs.json)"
    echo ""

    echo "1.4: Verify push acceptor path is push-stream"
    ensure_push_input_active "push-stream"
    echo ""
}

e2e_phase_stream_checks() {
    echo "=== STEP 2: Start Streams ==="
    echo "2.1: Push stream should be active from qa-harness publisher"
    echo "  (qa-harness starts pushing after push acceptor registration)"
    sleep 5
    echo ""

    echo "2.2: Pull stream already started by go-mls puller"
    sleep 3
    echo ""

    echo "2.3: Verify inputs status"
    STATS=$(curl -s "${API}/stats")
    echo "${STATS}" | tee "$(results_path step2_stats.json)"
    echo ""

    echo "2.4: Verify streams with ffprobe"
    wait_for_stream "${RTMP_HUB}/push-stream" 10 "push-stream" || echo "  Note: push-stream may need more time"
    wait_for_stream "${RTMP_HUB}/pull-stream" 10 "pull-stream" || echo "  Note: pull-stream may need more time"
    echo ""
}

e2e_phase_outputs() {
    echo "=== STEP 3: Create Outputs (2 per input) ==="
    echo "3.1: Create outputs for pull-stream"
    create_output "pull-stream" "pull-to-rtmp1" "${OUTPUT_RTMP_FOR_GOMLS}/live/pull1" "$(results_path step3_pull_out1.json)"
    create_output "pull-stream" "pull-to-rtmp2" "${OUTPUT_RTMP_FOR_GOMLS}/live/pull2" "$(results_path step3_pull_out2.json)"
    echo ""

    echo "3.2: Create outputs for push-stream"
    create_output "push-stream" "push-to-rtmp1" "${OUTPUT_RTMP_FOR_GOMLS}/live/push1" "$(results_path step3_push_out1.json)"
    create_output "push-stream" "push-to-rtmp2" "${OUTPUT_RTMP_FOR_GOMLS}/live/push2" "$(results_path step3_push_out2.json)"
    echo ""

    echo "3.3: Verify outputs"
    list_outputs "$(results_path step3_outputs.json)"
    echo ""

    echo "3.4: Verify output streams with ffprobe"
    sleep 3
    wait_for_stream "${OUTPUT_RTMP_LOCAL}/live/pull1" 15 "pull-to-rtmp1" || true
    wait_for_stream "${OUTPUT_RTMP_LOCAL}/live/pull2" 15 "pull-to-rtmp2" || true
    wait_for_stream "${OUTPUT_RTMP_LOCAL}/live/push1" 15 "push-to-rtmp1" || true
    wait_for_stream "${OUTPUT_RTMP_LOCAL}/live/push2" 15 "push-to-rtmp2" || true
    echo ""
}

e2e_phase_recording_and_hls() {
    echo "=== STEP 4: Recording ==="
    echo "4.1: Start recording for pull-stream"
    start_recording "pull-stream" "$(results_path step4_rec_pull.json)"
    echo ""

    echo "4.2: Start recording for push-stream"
    start_recording "push-stream" "$(results_path step4_rec_push.json)"
    echo ""

    echo "4.3: Verify recording status"
    curl -s "${API}/stats" | tee "$(results_path step4_stats.json)"
    echo ""

    echo "=== STEP 5: HLS Generation ==="
    echo "5.1: Start HLS for pull-stream"
    PULL_VIEWER_ID=$(start_hls_viewer "pull-stream" "$(results_path step5_hls_pull.json)")
    echo ""

    echo "5.2: Start HLS for push-stream"
    PUSH_VIEWER_ID=$(start_hls_viewer "push-stream" "$(results_path step5_hls_push.json)")
    echo ""

    wait_for_hls "pull-stream" 20
    wait_for_hls "push-stream" 20
    echo ""
}

e2e_phase_baseline_verify() {
    echo "=== STEP 6: Verification ==="
    echo "6.1: Get final stats"
    STATS=$(curl -s "${API}/stats")
    echo "${STATS}" | tee "$(results_path step6_stats.json)"
    echo ""

    INPUT_COUNT=$(echo "${STATS}" | sed -n 's/.*"inputs":\[\(.*\)\],"outputs".*/\1/p' | grep -o '"stream_path"' | wc -l)
    OUTPUT_COUNT=$(echo "${STATS}" | grep -o '"output_id"' | wc -l)
    echo "  Inputs: ${INPUT_COUNT} (expected: 2)"
    echo "  Outputs: ${OUTPUT_COUNT} (expected: 4)"
    echo ""

    echo "6.2: Check recordings"
    ls -la "${RECORDINGS_DIR}/" 2>/dev/null | grep -E "pull-stream|push-stream" | tee "$(results_path step6_recordings.txt)" || echo "  No recordings yet"
    echo ""

    echo "6.3: Check HLS files"
    find "${HLS_DIR}" -name "*.m3u8" 2>/dev/null | tee "$(results_path step6_hls_files.txt)" || echo "  No HLS files yet"
    echo ""

    echo "6.4: Export baseline config"
    export_config "$(results_path step6_export_baseline.json)"
    echo ""
}

e2e_phase_import_verify() {
    echo "=== STEP 7: Bulk Import + Preset Verification ==="
    if [ ! -f "${RELAY_CONFIG}" ]; then
        echo "FAIL: relay config file not found at ${RELAY_CONFIG}"
        return 1
    fi

    echo "7.0: Validate relay_config push path before import"
    assert_relay_config_push_path "${RELAY_CONFIG}"
    echo ""

    echo "7.1: Import relay_config"
    import_config "${RELAY_CONFIG}" "$(results_path step7_import_response.json)"
    echo ""

    echo "7.2: Wait for imported inputs"
    wait_for_input_status "pull-stream" "Active" 45
    start_push_source
    wait_for_input_status "push-stream" "Active" 60
    echo ""

    echo "7.2b: Ensure imported outputs are started"
    start_output "pull-stream" "pull-youtube" "$(results_path step7_start_pull_youtube.json)"
    start_output "pull-stream" "pull-custom" "$(results_path step7_start_pull_custom.json)"
    start_output "push-stream" "push-instagram" "$(results_path step7_start_push_instagram.json)"
    start_output "push-stream" "push-custom-rot" "$(results_path step7_start_push_custom_rot.json)"
    echo ""

    echo "7.3: Verify imported outputs with ffprobe"
    wait_for_stream "${OUTPUT_RTMP_LOCAL}/live/pull-youtube" 30 "pull-youtube"
    wait_for_stream "${OUTPUT_RTMP_LOCAL}/live/pull-custom" 30 "pull-custom"
    wait_for_stream "${OUTPUT_RTMP_LOCAL}/live/push-instagram" 30 "push-instagram"
    wait_for_stream "${OUTPUT_RTMP_LOCAL}/live/push-custom-rot" 30 "push-custom-rot"
    echo ""

    echo "7.4: Validate preset profiles via ffprobe"
    verify_profile_exact "${OUTPUT_RTMP_LOCAL}/live/pull-youtube" "1920" "1080" "30" "pull-youtube"
    verify_profile_exact "${OUTPUT_RTMP_LOCAL}/live/pull-custom" "1280" "720" "30" "pull-custom"
    verify_profile_dims_any_order "${OUTPUT_RTMP_LOCAL}/live/push-instagram" "720" "1280" "30" "push-instagram"
    verify_profile_dims_any_order "${OUTPUT_RTMP_LOCAL}/live/push-custom-rot" "720" "1280" "30" "push-custom-rot"
    echo ""

    echo "7.5: Export imported config"
    export_config "$(results_path step7_export_after_import.json)"
    echo ""
}

e2e_phase_cleanup() {
    echo "=== STEP 8: Cleanup ==="
    echo "8.1: Stop HLS"
    stop_hls_viewer "pull-stream" "${PULL_VIEWER_ID}" "$(results_path step7_hls_pull.json)"
    stop_hls_viewer "push-stream" "${PUSH_VIEWER_ID}" "$(results_path step7_hls_push.json)"
    echo ""

    echo "8.2: Stop recordings"
    stop_recording_if_active "pull-stream" "$(results_path step7_rec_pull.json)"
    stop_recording_if_active "push-stream" "$(results_path step7_rec_push.json)"
    echo ""

    echo "8.3: Delete all outputs"
    delete_all_current_outputs
    echo ""

    echo "8.4: Delete all inputs"
    delete_input "pull-stream" "$(results_path step7_in_pull.json)"
    delete_input "push-stream" "$(results_path step7_in_push.json)"
    echo ""

    echo "8.5: Export final config"
    export_config "$(results_path step7_export.json)"
    echo ""
}

e2e_finish() {
    echo "=========================================="
    echo "E2E TESTS COMPLETED!"
    echo "Results saved to ${RESULTS_DIR}"
    echo "=========================================="
}

e2e_main() {
    set -e
    e2e_prepare_harness
    e2e_phase_inputs
    e2e_phase_stream_checks
    e2e_phase_outputs
    e2e_phase_recording_and_hls
    e2e_phase_baseline_verify
    e2e_phase_import_verify
    e2e_phase_cleanup
    e2e_finish
}

E2E_AUTO_RUN=1
if [ "${E2E_SOURCE_ONLY:-0}" = "1" ]; then
    E2E_AUTO_RUN=0
fi
if [ -n "${BASH_SOURCE:-}" ] && [ "${BASH_SOURCE[0]}" != "$0" ]; then
    E2E_AUTO_RUN=0
fi

if [ "${E2E_AUTO_RUN}" = "1" ]; then
    if [ $# -eq 0 ]; then
        e2e_main
    else
        if type "$1" >/dev/null 2>&1; then
            cmd=$1
            shift
            "$cmd" "$@"
        else
            echo "Unknown command: $1"
            echo "Run without args for full flow, or pass a function name (e.g. e2e_phase_inputs)."
            exit 2
        fi
    fi
fi
