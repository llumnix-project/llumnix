#!/bin/bash
#
# Service Router Weight Routing Test Script
#
# Tests weight-based routing between internal (50%) and external (50%) services.
#
# Usage:
#   ./test_service_router.sh [--gateway-url <url>] [--count <n>]

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

GATEWAY_URL="http://gateway:8089"
REQUEST_COUNT=10

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
    cat << EOF
Usage: $0 [OPTIONS]
Options:
  --gateway-url <url>    Gateway URL (default: http://gateway:8089)
  --count <n>            Number of requests (default: 10)
  --help                 Show help
EOF
}

check_health() {
    print_info "Checking gateway health..."
    if curl -sf "${GATEWAY_URL}/healthz" > /dev/null 2>&1; then
        print_success "Gateway is healthy"
        return 0
    fi
    print_error "Gateway unhealthy"
    return 1
}

send_requests() {
    local model="$1"
    local count="$2"
    local success=0
    local failed=0

    print_info "Sending $count requests with model=$model..."
    echo ""

    for i in $(seq 1 $count); do
        local payload='{"model":"'$model'","messages":[{"role":"user","content":"Hello"}],"max_tokens":10}'
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

main() {
    parse_args "$@"

    echo ""
    echo "================================================================"
    echo "Service Router Weight Routing Tests"
    echo "================================================================"
    echo "  Gateway URL: $GATEWAY_URL"
    echo "  Request Count: $REQUEST_COUNT per model"
    echo "  Routing: 50% internal, 50% external (weight-based)"
    echo ""

    check_health || exit 1

    echo ""
    echo "================================================================"
    print_info "STEP 1: Send $REQUEST_COUNT requests (weight-based routing)"
    echo "================================================================"
    echo ""
    echo "All requests use the same model (Qwen/Qwen3-8B)."
    echo "Expected: ~50% routed to internal, ~50% to external (random)"
    echo ""
    send_requests "Qwen/Qwen3-8B" $REQUEST_COUNT

    echo ""
    echo "================================================================"
    print_info "STEP 3: Verify routing distribution"
    echo "================================================================"
    echo ""
    echo "To verify that requests were distributed 50/50 between internal"
    echo "and external services, check the gateway logs:"
    echo ""
    echo "  kubectl logs -f deployment/gateway -n <namespace>"
    echo ""
    echo "Expected log patterns:"
    echo "  - type: RouteInternal  (roughly 50% of requests)"
    echo "  - type: RouteExternal  (roughly 50% of requests)"
    echo ""
    echo "Since weight-based routing is random, the actual distribution"
    echo "may vary. With $REQUEST_COUNT requests per model, expect:"
    echo "  - ~$((REQUEST_COUNT/2)) requests to internal"
    echo "  - ~$((REQUEST_COUNT/2)) requests to external"
    echo ""

    print_success "Test completed! Check gateway logs to verify routing."
}

main "$@"
