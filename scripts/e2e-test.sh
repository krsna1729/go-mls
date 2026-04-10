#!/bin/sh

API="${API:-http://go-mls:8080}"
RTMP_HUB="${RTMP_HUB:-rtmp://go-mls:1935}"
RTSP_PUSH_BASE="${RTSP_PUSH_BASE:-rtsp://go-mls:8554}"
SRT_PUSH_BASE="${SRT_PUSH_BASE:-srt://go-mls:9000}"
HARNESS_RTMP_LOCAL="${HARNESS_RTMP_LOCAL:-rtmp://127.0.0.1:1935}"
HARNESS_RTMP_REMOTE="${HARNESS_RTMP_REMOTE:-rtmp://qa-harness:1935}"
SOURCE_STREAM_PATH="${SOURCE_STREAM_PATH:-live/testsrc}"
SOURCE_RTMP_LOCAL="${SOURCE_RTMP_LOCAL:-${HARNESS_RTMP_LOCAL}/${SOURCE_STREAM_PATH}}"
SOURCE_RTMP_FOR_GOMLS="${SOURCE_RTMP_FOR_GOMLS:-${HARNESS_RTMP_REMOTE}/${SOURCE_STREAM_PATH}}"
RELAY_CONFIG="${RELAY_CONFIG:-/relay_config.json}"
RESULTS_DIR="${RESULTS_DIR:-/results}"
RECORDINGS_DIR="${RECORDINGS_DIR:-/recordings}"
HLS_DIR="${HLS_DIR:-/hls}"
TEST_SOURCE_FILE="${TEST_SOURCE_FILE:-/testdata/testsrc.mp4}"
FFPROBE_TIMEOUT_S="${FFPROBE_TIMEOUT_S:-4}"
FFPROBE_ANALYZE_US="${FFPROBE_ANALYZE_US:-2000000}"
FFPROBE_PROBESIZE="${FFPROBE_PROBESIZE:-1000000}"
FFPROBE_RW_TIMEOUT_US="${FFPROBE_RW_TIMEOUT_US:-3000000}"

PULL_STREAM="pull-rtmp"
PUSH_RTMP_STREAM="push-rtmp"
PUSH_RTSP_STREAM="push-rtsp"
PUSH_SRT_STREAM="push-srt"

NGINX_PID=""
PULL_SRC_PID=""
RTMP_PUSH_SRC_PID=""
RTSP_PUSH_SRC_PID=""
SRT_PUSH_SRC_PID=""
COMBINED_PUSH_SRC_PID=""

PULL_VIEWER_ID=""
PUSH_RTMP_VIEWER_ID=""
PUSH_RTSP_VIEWER_ID=""
PUSH_SRT_VIEWER_ID=""

SOURCE_W=""
SOURCE_H=""
SOURCE_FPS=""

results_path() {
    printf '%s/%s' "${RESULTS_DIR}" "$1"
}

e2e_init() {
    mkdir -p "${RESULTS_DIR}"
    chmod 777 "${RESULTS_DIR}" 2>/dev/null || true
}

e2e_banner() {
    echo "=========================================="
    echo "Go-MLS E2E Test (4 Inputs / 4 Outputs)"
    echo "HTTP API: ${API}"
    echo "RTMP Hub: ${RTMP_HUB}"
    echo "RTSP Push Base: ${RTSP_PUSH_BASE}"
    echo "SRT Push Base: ${SRT_PUSH_BASE}"
    echo "Harness Local RTMP: ${HARNESS_RTMP_LOCAL}"
    echo "Relay Config: ${RELAY_CONFIG}"
    echo "Results Dir: ${RESULTS_DIR}"
    echo "=========================================="
    echo ""
}

stop_pid() {
    pid=$1
    if [ -n "${pid}" ] && kill -0 "${pid}" 2>/dev/null; then
        kill "${pid}" 2>/dev/null || true
        wait "${pid}" 2>/dev/null || true
    fi
}

stop_rtmp_push_source() {
    stop_pid "${RTMP_PUSH_SRC_PID}"
    RTMP_PUSH_SRC_PID=""
}

stop_rtsp_push_source() {
    stop_pid "${RTSP_PUSH_SRC_PID}"
    RTSP_PUSH_SRC_PID=""
}

stop_srt_push_source() {
    stop_pid "${SRT_PUSH_SRC_PID}"
    SRT_PUSH_SRC_PID=""
}

stop_combined_push_source() {
    stop_pid "${COMBINED_PUSH_SRC_PID}"
    COMBINED_PUSH_SRC_PID=""
}

stop_all_push_sources() {
    stop_rtmp_push_source
    stop_rtsp_push_source
    stop_srt_push_source
    stop_combined_push_source
}

cleanup() {
    set +e
    stop_all_push_sources
    stop_pid "${PULL_SRC_PID}"
    PULL_SRC_PID=""
    stop_pid "${NGINX_PID}"
    NGINX_PID=""
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

start_rtmp_push_source() {
    start_combined_push_sources
}

start_rtsp_push_source() {
    start_combined_push_sources
}

start_srt_push_source() {
    start_combined_push_sources
}

start_combined_push_sources() {
    if [ -n "${COMBINED_PUSH_SRC_PID}" ] && kill -0 "${COMBINED_PUSH_SRC_PID}" 2>/dev/null; then
        return 0
    fi

    stop_all_push_sources

    streamid="publish:${PUSH_SRT_STREAM}"
    rtmp_url="${RTMP_HUB}/${PUSH_RTMP_STREAM}"
    rtsp_url="${RTSP_PUSH_BASE}/${PUSH_RTSP_STREAM}"
    srt_url="${SRT_PUSH_BASE}?mode=caller&streamid=${streamid}&transtype=live"

    echo "Starting combined push-source publisher (RTMP/RTSP/SRT)"
    ffmpeg -re -stream_loop -1 -i "${TEST_SOURCE_FILE}" \
        -map 0:v:0 -map 0:a:0? -c copy \
        -f flv "${rtmp_url}" \
        -map 0:v:0 -map 0:a:0? -c copy -rtsp_transport tcp -f rtsp "${rtsp_url}" \
        -map 0:v:0 -map 0:a:0? -c copy -f mpegts "${srt_url}" \
        >/tmp/push-source-combined.log 2>&1 &
    COMBINED_PUSH_SRC_PID=$!
}

start_push_source_for_stream() {
    stream=$1
    case "${stream}" in
        "${PUSH_RTMP_STREAM}") start_rtmp_push_source ;;
        "${PUSH_RTSP_STREAM}") start_rtsp_push_source ;;
        "${PUSH_SRT_STREAM}") start_srt_push_source ;;
        *)
            echo "FAIL: unknown push stream ${stream}"
            return 1
            ;;
    esac
}

fast_ffprobe() {
    if command -v timeout >/dev/null 2>&1; then
        timeout "${FFPROBE_TIMEOUT_S}" ffprobe "$@"
    else
        ffprobe "$@"
    fi
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
    stream=$1
    remote_url=$2
    result_file=$3
    payload=$(printf '{"stream_path":"%s","remote_url":"%s","mode":"pull"}' "${stream}" "${remote_url}")
    curl -s -X POST "${API}/inputs" -H "Content-Type: application/json" -d "${payload}" | tee "${result_file}"
}

register_accept_input() {
    stream=$1
    accept_protocol=$2
    result_file=$3
    payload=$(printf '{"stream_path":"%s","accept_protocol":"%s"}' "${stream}" "${accept_protocol}")
    curl -s -X POST "${API}/inputs" -H "Content-Type: application/json" -d "${payload}" | tee "${result_file}"
}

list_inputs() {
    result_file=$1
    curl -s "${API}/inputs" | tee "${result_file}"
}

configured_output_specs() {
    jq -r '.[] as $relay | $relay.outputs[] | [$relay.input_name, .output_name, .output_url] | @tsv' "${RELAY_CONFIG}"
}

configured_output_payloads() {
    jq -c '.[] as $relay | $relay.outputs[] |
        {
            stream_path: $relay.input_name,
            output_id: .output_name,
            remote_url: .output_url
        }
        + (if .platform_preset then {preset: .platform_preset} else {} end)
        + (if .ffmpeg_options then .ffmpeg_options else {} end)' "${RELAY_CONFIG}"
}

create_output_payload() {
    payload=$1
    result_file=$2
    curl -s -X POST "${API}/outputs" -H "Content-Type: application/json" -d "${payload}" | tee "${result_file}"
}

list_outputs() {
    result_file=$1
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

stop_output() {
    stream=$1
    output_id=$2
    result_file=$3
    payload=$(printf '{"stream_path":"%s","output_id":"%s"}' "${stream}" "${output_id}")
    response=$(curl -s --max-time 12 -X POST "${API}/outputs/stop" -H "Content-Type: application/json" -d "${payload}" || true)
    if [ -z "${response}" ]; then
        response=$(printf '{"status":"timeout","stream_path":"%s","output_id":"%s"}' "${stream}" "${output_id}")
    fi
    echo "${response}" | tee "${result_file}"
}

restart_output() {
    stream=$1
    output_id=$2
    prefix=$3

    stop_output "${stream}" "${output_id}" "$(results_path ${prefix}_${output_id}_stop.json)" >/dev/null 2>&1 || true
    start_output "${stream}" "${output_id}" "$(results_path ${prefix}_${output_id}_start.json)"
}

restart_failed_outputs_from_stats() {
    stats_json=$1
    prefix=$2

    echo "${stats_json}" | jq -r '.outputs[] | select(.status == "Error") | [.stream_path, .output_id] | @tsv' | \
        while IFS="$(printf '\t')" read -r stream output_id; do
            [ -n "${stream}" ] || continue
            echo "  WARN: restarting failed output ${stream}/${output_id}"
            restart_output "${stream}" "${output_id}" "${prefix}"
        done
}

start_non_running_outputs_from_stats() {
    stats_json=$1
    prefix=$2

    echo "${stats_json}" | jq -r '.outputs[] | select(.status != "Running") | [.stream_path, .output_id] | @tsv' | \
        while IFS="$(printf '\t')" read -r stream output_id; do
            [ -n "${stream}" ] || continue
            start_output "${stream}" "${output_id}" "$(results_path ${prefix}_${output_id}.json)"
        done
}

create_outputs_from_config() {
    prefix=$1
    configured_output_payloads | while IFS= read -r payload; do
        output_id=$(printf '%s' "${payload}" | jq -r '.output_id')
        create_output_payload "${payload}" "$(results_path ${prefix}_${output_id}.json)"
    done
}

start_outputs_from_config() {
    prefix=$1
    configured_output_specs | while IFS="$(printf '\t')" read -r stream output_id remote_url; do
        start_output "${stream}" "${output_id}" "$(results_path ${prefix}_${output_id}.json)"
    done
}

stop_outputs_from_config() {
    prefix=$1
    configured_output_specs | while IFS="$(printf '\t')" read -r stream output_id remote_url; do
        stop_output "${stream}" "${output_id}" "$(results_path ${prefix}_${output_id}.json)"
    done
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
    config_file=$1
    result_file=$2
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

capture_source_profile() {
    profile=$(wait_for_profile "${TEST_SOURCE_FILE}" 5) || {
        echo "FAIL: unable to determine source profile from ${TEST_SOURCE_FILE}"
        return 1
    }

    set -- ${profile}
    SOURCE_W=$1
    SOURCE_H=$2
    rate=$3
    SOURCE_FPS=$(echo "${rate}" | awk -F/ '{ if ($2 > 0) printf("%d", $1 / $2); else printf("%d", $1) }')
    echo "Source profile detected: ${SOURCE_W}x${SOURCE_H}@${SOURCE_FPS}"
}

verify_output_profile_by_name() {
    output_name=$1
    output_url=$2

    case "${output_name}" in
        pull-copy)
            verify_profile_exact "${output_url}" "${SOURCE_W}" "${SOURCE_H}" "${SOURCE_FPS}" "${output_name}"
            ;;
        push-rtmp-facebook)
            verify_profile_exact "${output_url}" "1280" "720" "30" "${output_name}"
            ;;
        push-rtsp-instagram)
            verify_profile_dims_any_order "${output_url}" "720" "1280" "30" "${output_name}"
            ;;
        push-srt-twitch)
            verify_profile_exact "${output_url}" "1920" "1080" "60" "${output_name}"
            ;;
        *)
            echo "  FAIL: no profile verification rule for ${output_name}"
            return 1
            ;;
    esac
}

verify_outputs_from_config() {
    configured_output_specs | while IFS="$(printf '\t')" read -r stream output_id remote_url; do
        wait_for_stream "${remote_url}" 30 "${output_id}"
    done
}

verify_output_profiles_from_config() {
    configured_output_specs | while IFS="$(printf '\t')" read -r stream output_id remote_url; do
        verify_output_profile_by_name "${output_id}" "${remote_url}"
    done
}

assert_relay_config_matrix() {
    relays=$(jq 'length' "${RELAY_CONFIG}")
    outputs=$(jq '[.[].outputs[]] | length' "${RELAY_CONFIG}")
    if [ "${relays}" != "4" ] || [ "${outputs}" != "4" ]; then
        echo "FAIL: expected relay config to define 4 inputs and 4 outputs, got ${relays} inputs and ${outputs} outputs"
        return 1
    fi
    echo "  OK: relay config defines 4 inputs and 4 outputs"
}

ensure_push_input_active() {
    stream=$1
    wait_for_input_status "${stream}" "Starting" 20 || wait_for_input_status "${stream}" "Active" 20
    start_push_source_for_stream "${stream}"
    wait_for_input_status "${stream}" "Active" 45
}

ensure_all_push_inputs_active() {
    start_combined_push_sources
    wait_for_input_status "${PUSH_RTMP_STREAM}" "Active" 45
    wait_for_input_status "${PUSH_RTSP_STREAM}" "Active" 45
    wait_for_input_status "${PUSH_SRT_STREAM}" "Active" 45
}

wait_for_import_apply_ready() {
    expected_inputs=$1
    expected_outputs=$2
    timeout=$3

    echo "Waiting for import apply to converge (inputs=${expected_inputs}, outputs=${expected_outputs}, timeout: ${timeout}s)..."
    for i in $(seq 1 "${timeout}"); do
        stats=$(curl -s "${API}/stats")
        inputs=$(echo "${stats}" | jq '.inputs | length')
        outputs=$(echo "${stats}" | jq '.outputs | length')
        if [ "${inputs}" = "${expected_inputs}" ] && [ "${outputs}" = "${expected_outputs}" ]; then
            echo "  OK: import apply converged"
            return 0
        fi
        sleep 1
    done
    echo "  FAIL: import apply did not converge"
    return 1
}

wait_for_push_inputs_active_with_restarts() {
    timeout=$1
    restart_every=${2:-10}
    restart_count=0

    echo "Waiting for push inputs to become active (timeout: ${timeout}s)..."
    start_combined_push_sources

    for i in $(seq 1 "${timeout}"); do
        stats=$(curl -s "${API}/stats")
        rtmp_status=$(echo "${stats}" | jq -r --arg s "${PUSH_RTMP_STREAM}" '[.inputs[] | select(.stream_path == $s) | .status][0] // "Missing"')
        rtsp_status=$(echo "${stats}" | jq -r --arg s "${PUSH_RTSP_STREAM}" '[.inputs[] | select(.stream_path == $s) | .status][0] // "Missing"')
        srt_status=$(echo "${stats}" | jq -r --arg s "${PUSH_SRT_STREAM}" '[.inputs[] | select(.stream_path == $s) | .status][0] // "Missing"')

        if [ "${rtmp_status}" = "Active" ] && [ "${rtsp_status}" = "Active" ] && [ "${srt_status}" = "Active" ]; then
            echo "  OK: all push inputs are active"
            return 0
        fi

        if [ $((i % restart_every)) -eq 0 ]; then
            restart_count=$((restart_count + 1))
            echo "  WARN: push inputs not all active (rtmp=${rtmp_status}, rtsp=${rtsp_status}, srt=${srt_status}); restarting combined publisher (attempt ${restart_count})"
            tail -n 20 /tmp/push-source-combined.log 2>/dev/null || true
            stop_combined_push_source
            start_combined_push_sources
        fi

        sleep 1
    done

    echo "  FAIL: push inputs did not all become active in time"
    curl -s "${API}/stats" | tee "$(results_path step7_push_inputs_timeout.json)"
    return 1
}

ensure_outputs_running_from_config() {
    prefix=$1
    stats=$(curl -s "${API}/stats")
    restart_failed_outputs_from_stats "${stats}" "${prefix}_restart"
    start_outputs_from_config "${prefix}_start"
}

wait_for_outputs_running_with_restarts() {
    expected_outputs=$1
    timeout=$2
    prefix=$3

    echo "Waiting for outputs to reach Running state (expected=${expected_outputs}, timeout: ${timeout}s)..."
    for i in $(seq 1 "${timeout}"); do
        stats=$(curl -s "${API}/stats")
        running=$(echo "${stats}" | jq '[.outputs[] | select(.status == "Running")] | length')
        failed=$(echo "${stats}" | jq '[.outputs[] | select(.status == "Error")] | length')

        if [ "${running}" = "${expected_outputs}" ] && [ "${failed}" = "0" ]; then
            echo "  OK: all outputs are Running"
            return 0
        fi

        if [ "${failed}" -gt 0 ]; then
            restart_failed_outputs_from_stats "${stats}" "${prefix}_restart"
        fi

        if [ $((i % 5)) -eq 0 ]; then
            start_non_running_outputs_from_stats "${stats}" "${prefix}_start"
        fi

        sleep 1
    done

    echo "  FAIL: outputs did not reach Running state in time"
    curl -s "${API}/stats" | tee "$(results_path ${prefix}_outputs_timeout.json)"
    return 1
}

wait_for_import_and_push_converged() {
    expected_inputs=$1
    expected_outputs=$2
    timeout=$3
    import_ready=0
    push_inactive_streak=0

    echo "Waiting for import+push convergence (inputs=${expected_inputs}, outputs=${expected_outputs}, timeout: ${timeout}s)..."
    for i in $(seq 1 "${timeout}"); do
        stats=$(curl -s "${API}/stats")
        inputs=$(echo "${stats}" | jq '.inputs | length')
        outputs=$(echo "${stats}" | jq '.outputs | length')

        if [ "${inputs}" != "${expected_inputs}" ] || [ "${outputs}" != "${expected_outputs}" ]; then
            sleep 1
            continue
        fi

        if [ "${import_ready}" = "0" ]; then
            echo "  INFO: import apply converged; starting combined push publisher"
            stop_combined_push_source
            start_combined_push_sources
            import_ready=1
            sleep 1
            continue
        fi

        if [ -z "${COMBINED_PUSH_SRC_PID}" ] || ! kill -0 "${COMBINED_PUSH_SRC_PID}" 2>/dev/null; then
            echo "  WARN: combined push publisher not running, restarting"
            start_combined_push_sources
            sleep 1
            continue
        fi

        pull_active=$(echo "${stats}" | jq --arg s "${PULL_STREAM}" '[.inputs[] | select(.stream_path == $s and .status == "Active")] | length')
        rtmp_active=$(echo "${stats}" | jq --arg s "${PUSH_RTMP_STREAM}" '[.inputs[] | select(.stream_path == $s and .status == "Active")] | length')
        rtsp_active=$(echo "${stats}" | jq --arg s "${PUSH_RTSP_STREAM}" '[.inputs[] | select(.stream_path == $s and .status == "Active")] | length')
        srt_active=$(echo "${stats}" | jq --arg s "${PUSH_SRT_STREAM}" '[.inputs[] | select(.stream_path == $s and .status == "Active")] | length')

        rtmp_status=$(echo "${stats}" | jq -r --arg s "${PUSH_RTMP_STREAM}" '[.inputs[] | select(.stream_path == $s) | .status][0] // "Missing"')
        rtsp_status=$(echo "${stats}" | jq -r --arg s "${PUSH_RTSP_STREAM}" '[.inputs[] | select(.stream_path == $s) | .status][0] // "Missing"')
        srt_status=$(echo "${stats}" | jq -r --arg s "${PUSH_SRT_STREAM}" '[.inputs[] | select(.stream_path == $s) | .status][0] // "Missing"')

        failed_outputs=$(echo "${stats}" | jq '[.outputs[] | select(.status == "Error")] | length')
        running_outputs=$(echo "${stats}" | jq '[.outputs[] | select(.status == "Running")] | length')

        if [ "${failed_outputs}" -gt 0 ]; then
            restart_failed_outputs_from_stats "${stats}" "step7_restart"
            sleep 2
            continue
        fi

        if [ "${rtmp_active}" != "1" ] || [ "${rtsp_active}" != "1" ] || [ "${srt_active}" != "1" ]; then
            push_inactive_streak=$((push_inactive_streak + 1))
            if [ $((push_inactive_streak % 10)) -eq 0 ]; then
                echo "  WARN: push inputs not fully active (rtmp=${rtmp_status}, rtsp=${rtsp_status}, srt=${srt_status}); recycling combined push publisher"
                stop_combined_push_source
                start_combined_push_sources
            fi
            sleep 1
            continue
        fi

        push_inactive_streak=0

        if [ "${inputs}" = "${expected_inputs}" ] && [ "${outputs}" = "${expected_outputs}" ] && \
            [ "${pull_active}" = "1" ] && [ "${rtmp_active}" = "1" ] && [ "${rtsp_active}" = "1" ] && [ "${srt_active}" = "1" ] && \
            [ "${running_outputs}" = "${expected_outputs}" ]; then
            echo "  OK: import+push converged with all outputs running"
            return 0
        fi

        sleep 1
    done

    echo "  FAIL: import+push did not converge"
    curl -s "${API}/stats" | tee "$(results_path step7_stats_timeout.json)"
    return 1
}

wait_for_imported_inputs_active() {
    wait_for_input_status "${PULL_STREAM}" "Active" 45
    ensure_all_push_inputs_active
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
    capture_source_profile
    echo ""
}

e2e_phase_inputs() {
    echo "=== STEP 1: Register Inputs ==="
    echo "1.1: Register pull input"
    register_pull_input "${PULL_STREAM}" "${SOURCE_RTMP_FOR_GOMLS}" "$(results_path step1_pull_rtmp_input.json)"
    echo ""

    echo "1.2: Register RTMP push/accept input"
    register_accept_input "${PUSH_RTMP_STREAM}" "rtmp" "$(results_path step1_push_rtmp_input.json)"
    echo ""

    echo "1.3: Register RTSP push/accept input"
    register_accept_input "${PUSH_RTSP_STREAM}" "rtsp" "$(results_path step1_push_rtsp_input.json)"
    echo ""

    echo "1.4: Register SRT push/accept input"
    register_accept_input "${PUSH_SRT_STREAM}" "srt" "$(results_path step1_push_srt_input.json)"
    echo ""

    echo "1.5: Verify all inputs registered"
    list_inputs "$(results_path step1_inputs.json)"
    echo ""

    echo "1.6: Start publishers for all accept-mode inputs"
    ensure_all_push_inputs_active
    echo ""
}

e2e_phase_stream_checks() {
    echo "=== STEP 2: Verify Ingest Streams ==="
    echo "2.1: Collect stats"
    STATS=$(curl -s "${API}/stats")
    echo "${STATS}" | tee "$(results_path step2_stats.json)"
    echo ""

    echo "2.2: Verify all four input streams on internal RTMP backbone"
    wait_for_stream "${RTMP_HUB}/${PULL_STREAM}" 15 "${PULL_STREAM}"
    wait_for_stream "${RTMP_HUB}/${PUSH_RTMP_STREAM}" 20 "${PUSH_RTMP_STREAM}"
    wait_for_stream "${RTMP_HUB}/${PUSH_RTSP_STREAM}" 20 "${PUSH_RTSP_STREAM}"
    wait_for_stream "${RTMP_HUB}/${PUSH_SRT_STREAM}" 20 "${PUSH_SRT_STREAM}"
    echo ""
}

e2e_phase_outputs() {
    echo "=== STEP 3: Create Outputs (1 per input / 4 total) ==="
    echo "3.1: Create all outputs from relay config"
    create_outputs_from_config step3_create
    echo ""

    echo "3.2: Verify output registry"
    list_outputs "$(results_path step3_outputs.json)"
    echo ""

    echo "3.3: Verify all four output streams are active"
    verify_outputs_from_config
    echo ""

    echo "3.4: Verify configured output profiles"
    verify_output_profiles_from_config
    echo ""
}

e2e_phase_recording_and_hls() {
    echo "=== STEP 4: Recording ==="
    start_recording "${PULL_STREAM}" "$(results_path step4_rec_pull_rtmp.json)"
    start_recording "${PUSH_RTMP_STREAM}" "$(results_path step4_rec_push_rtmp.json)"
    start_recording "${PUSH_RTSP_STREAM}" "$(results_path step4_rec_push_rtsp.json)"
    start_recording "${PUSH_SRT_STREAM}" "$(results_path step4_rec_push_srt.json)"
    echo ""

    echo "4.2: Verify recording stats"
    curl -s "${API}/stats" | tee "$(results_path step4_stats.json)"
    echo ""

    echo "=== STEP 5: HLS Generation ==="
    PULL_VIEWER_ID=$(start_hls_viewer "${PULL_STREAM}" "$(results_path step5_hls_pull_rtmp.json)")
    PUSH_RTMP_VIEWER_ID=$(start_hls_viewer "${PUSH_RTMP_STREAM}" "$(results_path step5_hls_push_rtmp.json)")
    PUSH_RTSP_VIEWER_ID=$(start_hls_viewer "${PUSH_RTSP_STREAM}" "$(results_path step5_hls_push_rtsp.json)")
    PUSH_SRT_VIEWER_ID=$(start_hls_viewer "${PUSH_SRT_STREAM}" "$(results_path step5_hls_push_srt.json)")

    wait_for_hls "${PULL_STREAM}" 20
    wait_for_hls "${PUSH_RTMP_STREAM}" 20
    wait_for_hls "${PUSH_RTSP_STREAM}" 20
    wait_for_hls "${PUSH_SRT_STREAM}" 20
    echo ""
}

e2e_phase_baseline_verify() {
    echo "=== STEP 6: Verification ==="
    echo "6.1: Get final baseline stats"
    STATS=$(curl -s "${API}/stats")
    echo "${STATS}" | tee "$(results_path step6_stats.json)"
    echo ""

    INPUT_COUNT=$(echo "${STATS}" | jq '.inputs | length')
    OUTPUT_COUNT=$(echo "${STATS}" | jq '.outputs | length')
    echo "  Inputs: ${INPUT_COUNT} (expected: 4)"
    echo "  Outputs: ${OUTPUT_COUNT} (expected: 4)"
    [ "${INPUT_COUNT}" = "4" ]
    [ "${OUTPUT_COUNT}" = "4" ]
    echo ""

    echo "6.2: Check recordings"
    ls -la "${RECORDINGS_DIR}/" 2>/dev/null | grep -E "pull-rtmp|push-rtmp|push-rtsp|push-srt" | tee "$(results_path step6_recordings.txt)" || echo "  No recordings yet"
    echo ""

    echo "6.3: Check HLS files"
    find "${HLS_DIR}" -name "*.m3u8" 2>/dev/null | tee "$(results_path step6_hls_files.txt)" || echo "  No HLS files yet"
    echo ""

    echo "6.4: Export baseline config"
    export_config "$(results_path step6_export_baseline.json)"
    echo ""
}

e2e_phase_import_verify() {
    echo "=== STEP 7: Bulk Import Verification ==="
    [ -f "${RELAY_CONFIG}" ] || {
        echo "FAIL: relay config file not found at ${RELAY_CONFIG}"
        return 1
    }

    echo "7.0: Validate relay config matrix"
    assert_relay_config_matrix
    echo ""

    echo "7.1: Quiesce runtime before import"
    stop_hls_viewer "${PULL_STREAM}" "${PULL_VIEWER_ID}" "$(results_path step7_hls_stop_pull_rtmp.json)" || true
    stop_hls_viewer "${PUSH_RTMP_STREAM}" "${PUSH_RTMP_VIEWER_ID}" "$(results_path step7_hls_stop_push_rtmp.json)" || true
    stop_hls_viewer "${PUSH_RTSP_STREAM}" "${PUSH_RTSP_VIEWER_ID}" "$(results_path step7_hls_stop_push_rtsp.json)" || true
    stop_hls_viewer "${PUSH_SRT_STREAM}" "${PUSH_SRT_VIEWER_ID}" "$(results_path step7_hls_stop_push_srt.json)" || true

    stop_recording_if_active "${PULL_STREAM}" "$(results_path step7_rec_stop_pull_rtmp.json)"
    stop_recording_if_active "${PUSH_RTMP_STREAM}" "$(results_path step7_rec_stop_push_rtmp.json)"
    stop_recording_if_active "${PUSH_RTSP_STREAM}" "$(results_path step7_rec_stop_push_rtsp.json)"
    stop_recording_if_active "${PUSH_SRT_STREAM}" "$(results_path step7_rec_stop_push_srt.json)"

    stop_outputs_from_config step7_out_stop_before_import
    stop_all_push_sources
    sleep 2
    echo ""

    echo "7.2: Import relay config"
    import_config "${RELAY_CONFIG}" "$(results_path step7_import_response.json)"
    echo ""

    echo "7.3: Wait for import apply to converge"
    wait_for_import_apply_ready 4 4 90
    echo ""

    echo "7.4: Activate push inputs after import"
    wait_for_push_inputs_active_with_restarts 120 10
    echo ""

    echo "7.5: Recover/start imported outputs and verify"
    wait_for_stream "${RTMP_HUB}/${PULL_STREAM}" 30 "${PULL_STREAM}"
    wait_for_stream "${RTMP_HUB}/${PUSH_RTMP_STREAM}" 30 "${PUSH_RTMP_STREAM}"
    wait_for_stream "${RTMP_HUB}/${PUSH_RTSP_STREAM}" 30 "${PUSH_RTSP_STREAM}"
    wait_for_stream "${RTMP_HUB}/${PUSH_SRT_STREAM}" 30 "${PUSH_SRT_STREAM}"

    ensure_outputs_running_from_config step7
    wait_for_outputs_running_with_restarts 4 75 step7
    verify_output_profiles_from_config
    echo ""

    echo "7.6: Export imported config"
    export_config "$(results_path step7_export_after_import.json)"
    echo ""
}

e2e_phase_cleanup() {
    echo "=== STEP 8: Cleanup ==="
    echo "8.1: Stop HLS"
    stop_hls_viewer "${PULL_STREAM}" "${PULL_VIEWER_ID}" "$(results_path step8_hls_pull_rtmp.json)"
    stop_hls_viewer "${PUSH_RTMP_STREAM}" "${PUSH_RTMP_VIEWER_ID}" "$(results_path step8_hls_push_rtmp.json)"
    stop_hls_viewer "${PUSH_RTSP_STREAM}" "${PUSH_RTSP_VIEWER_ID}" "$(results_path step8_hls_push_rtsp.json)"
    stop_hls_viewer "${PUSH_SRT_STREAM}" "${PUSH_SRT_VIEWER_ID}" "$(results_path step8_hls_push_srt.json)"
    echo ""

    echo "8.2: Stop recordings"
    stop_recording_if_active "${PULL_STREAM}" "$(results_path step8_rec_pull_rtmp.json)"
    stop_recording_if_active "${PUSH_RTMP_STREAM}" "$(results_path step8_rec_push_rtmp.json)"
    stop_recording_if_active "${PUSH_RTSP_STREAM}" "$(results_path step8_rec_push_rtsp.json)"
    stop_recording_if_active "${PUSH_SRT_STREAM}" "$(results_path step8_rec_push_srt.json)"
    echo ""

    echo "8.3: Delete all outputs"
    delete_all_current_outputs
    echo ""

    echo "8.4: Delete all inputs"
    delete_input "${PULL_STREAM}" "$(results_path step8_in_pull_rtmp.json)"
    delete_input "${PUSH_RTMP_STREAM}" "$(results_path step8_in_push_rtmp.json)"
    delete_input "${PUSH_RTSP_STREAM}" "$(results_path step8_in_push_rtsp.json)"
    delete_input "${PUSH_SRT_STREAM}" "$(results_path step8_in_push_srt.json)"
    echo ""

    echo "8.5: Export final config"
    export_config "$(results_path step8_export.json)"
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
