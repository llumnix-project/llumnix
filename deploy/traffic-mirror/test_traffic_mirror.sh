#!/bin/bash
#
# Traffic Mirror Integration Test Script
#
# Tests:
#   1. Mirror enabled  - requests mirrored to mirror-target (verify via logs)
#   2. Hot-reload       - disable mirroring at runtime, verify mirror stops
#   3. Re-enable        - re-enable mirroring, verify mirror resumes
#
# Usage:
#   ./test_traffic_mirror.sh [--gateway-url <url>] [--count <n>]
#
# This script is meant to run inside the gateway pod (or any pod with
# network access to the gateway and mirror-target services).

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

GATEWAY_URL="http://gateway:8089"
MIRROR_TARGET_URL="http://mirror-target:8000"
MIRROR_CONFIG_PATH="/mnt/mirror.json"
REQUEST_COUNT=5
WAIT_RELOAD=15

print_info() { echo -e "${BLUE}ℹ $1${NC}"; }
print_success() { echo -e "${GREEN}✓ $1${NC}"; }
print_error() { echo -e "${RED}✗ $1${NC}"; }
print_warning() { echo -e "${YELLOW}⚠ $1${NC}"; }

parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --gateway-url) GATEWAY_URL="$2"; shift 2 ;;
            --count) REQUEST_COUNT="$2"; shift 2 ;;
            --help|-h) show_usage; exit 0 ;;
            *) echo "Unknown: $1"; show_usage; exit 1 ;;
        esac
    done
}

show_usage() {
    cat << 'EOF'
Usage: ./test_traffic_mirror.sh [OPTIONS]
Options:
  --gateway-url <url>    Gateway URL (default: http://gateway:8089)
  --count <n>            Number of requests per step (default: 5)
  --help                 Show help
EOF
}

check_health() {
    print_info "Checking gateway health..."
    if curl -sf "${GATEWAY_URL}/healthz" > /dev/null 2>&1; then
        print_success "Gateway is healthy"
        return 0
    fi
    print_error "Gateway unhealthy at ${GATEWAY_URL}/healthz"
    return 1
}

check_mirror_target() {
    print_info "Checking mirror-target health..."
    if curl -sf "${MIRROR_TARGET_URL}/" > /dev/null 2>&1; then
        print_success "Mirror target is reachable"
        return 0
    fi
    print_error "Mirror target unreachable at ${MIRROR_TARGET_URL}"
    return 1
}

send_requests() {
    local count="$1"
    local label="$2"
    local success=0
    local failed=0

    print_info "Sending $count requests ($label)..."
    echo ""

    for i in $(seq 1 "$count"); do
        local payload='{"model":"Qwen/Qwen3-8B","messages":[{"role":"user","content":"Hello"}],"max_tokens":10}'
        local http_code
        http_code=$(curl -s -o /dev/null -w "%{http_code}" \
            -X POST "${GATEWAY_URL}/v1/chat/completions" \
            -H "Content-Type: application/json" \
            -d "$payload" \
            --max-time 30 2>/dev/null || echo "000")

        if [[ "$http_code" == "200" ]]; then
            ((success++)) || :
            echo "  Request $i: HTTP 200 ✓"
        else
            ((failed++)) || :
            echo "  Request $i: HTTP $http_code ✗"
        fi
    done

    echo ""
    print_success "Completed: $success success, $failed failed"
    return $failed
}

write_mirror_config() {
    local enable="$1"
    local target="$2"
    local ratio="$3"

    cat > "${MIRROR_CONFIG_PATH}" << EOJSON
{
  "Enable": ${enable},
  "Target": "${target}",
  "Ratio": ${ratio},
  "Timeout": 5000,
  "Authorization": "",
  "EnableLog": true
}
EOJSON
    print_info "Wrote mirror config: Enable=${enable}, Target=${target}, Ratio=${ratio}"
}

show_current_config() {
    if [[ -f "${MIRROR_CONFIG_PATH}" ]]; then
        print_info "Current mirror config:"
        cat "${MIRROR_CONFIG_PATH}"
        echo ""
    else
        print_warning "Mirror config file not found at ${MIRROR_CONFIG_PATH}"
    fi
}

main() {
    parse_args "$@"

    echo ""
    echo "================================================================"
    echo "  Traffic Mirror Integration Tests"
    echo "================================================================"
    echo "  Gateway URL:       $GATEWAY_URL"
    echo "  Mirror Target URL: $MIRROR_TARGET_URL"
    echo "  Requests per step: $REQUEST_COUNT"
    echo "  Config path:       $MIRROR_CONFIG_PATH"
    echo ""

    check_health || exit 1
    check_mirror_target || exit 1

    echo ""
    show_current_config

    # ------------------------------------------------------------------
    # TEST 1: Mirror enabled (initial state)
    # ------------------------------------------------------------------
    echo "================================================================"
    print_info "TEST 1: Mirror Enabled - requests should be mirrored"
    echo "================================================================"
    echo ""
    echo "Ensure mirror config has Enable=true, Ratio=100."
    echo "After sending requests, check mirror-target logs for access entries."
    echo ""

    write_mirror_config "true" "${MIRROR_TARGET_URL}" "100"
    print_info "Waiting ${WAIT_RELOAD}s for gateway to pick up new config..."
    sleep "${WAIT_RELOAD}"

    send_requests "$REQUEST_COUNT" "mirror enabled"

    echo ""
    print_info ">>> Check mirror-target logs for $REQUEST_COUNT access entries:"
    echo "    kubectl logs deployment/mirror-target -n <namespace> | tail -${REQUEST_COUNT}"
    echo ""

    # ------------------------------------------------------------------
    # TEST 2: Hot-reload - disable mirroring
    # ------------------------------------------------------------------
    echo "================================================================"
    print_info "TEST 2: Hot-Reload - disable mirroring"
    echo "================================================================"
    echo ""

    write_mirror_config "false" "${MIRROR_TARGET_URL}" "100"
    print_info "Waiting ${WAIT_RELOAD}s for gateway to pick up new config..."
    sleep "${WAIT_RELOAD}"

    echo ""
    print_info "Mirror is now disabled. Sending requests..."
    echo "These requests should NOT appear in mirror-target logs."
    echo ""

    send_requests "$REQUEST_COUNT" "mirror disabled"

    echo ""
    print_info ">>> Verify mirror-target logs did NOT grow (no new entries):"
    echo "    kubectl logs deployment/mirror-target -n <namespace> | tail -5"
    echo ""

    # ------------------------------------------------------------------
    # TEST 3: Hot-reload - re-enable mirroring
    # ------------------------------------------------------------------
    echo "================================================================"
    print_info "TEST 3: Hot-Reload - re-enable mirroring"
    echo "================================================================"
    echo ""

    write_mirror_config "true" "${MIRROR_TARGET_URL}" "100"
    print_info "Waiting ${WAIT_RELOAD}s for gateway to pick up new config..."
    sleep "${WAIT_RELOAD}"

    echo ""
    print_info "Mirror is re-enabled. Sending requests..."
    echo "These requests SHOULD appear in mirror-target logs."
    echo ""

    send_requests "$REQUEST_COUNT" "mirror re-enabled"

    echo ""
    print_info ">>> Check mirror-target logs for new access entries:"
    echo "    kubectl logs deployment/mirror-target -n <namespace> | tail -${REQUEST_COUNT}"
    echo ""

    # ------------------------------------------------------------------
    # Summary
    # ------------------------------------------------------------------
    echo "================================================================"
    echo "  Test Summary"
    echo "================================================================"
    echo ""
    print_success "All requests sent successfully."
    echo ""
    echo "  Manual verification needed:"
    echo "    1. TEST 1: mirror-target should have $REQUEST_COUNT access log entries"
    echo "    2. TEST 2: mirror-target should have NO new entries during this step"
    echo "    3. TEST 3: mirror-target should have $REQUEST_COUNT new entries"
    echo ""
    echo "  Quick check command:"
    echo "    kubectl logs deployment/mirror-target -n <namespace>"
    echo ""
    print_success "Traffic mirror test completed!"
}

main "$@"
