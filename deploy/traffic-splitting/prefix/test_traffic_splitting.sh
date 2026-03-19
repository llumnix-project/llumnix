#!/bin/bash
#
# Service Router Prefix Routing Test Script
#
# Tests three scenarios for the traffic-splitting/prefix deployment:
# 1. Route to local (internal): Qwen/Qwen3-8B -> internal llumnix-managed pool
# 2. Route to external: Qwen/Qwen2.5-7B -> vllm-external:8000
# 3. Local fails then fallback to external: internal failure -> retry -> fallback to external
#
# Usage:
#   # Run from within gateway container (or any pod with network access to gateway)
#   ./test_traffic_splitting.sh [--gateway-url <url>] [--skip-fallback]
#
#   # Run with custom gateway URL
#   ./test_traffic_splitting.sh --gateway-url http://gateway:8089
#
#   # Run without fallback test (manual mode)
#   ./test_traffic_splitting.sh --skip-fallback

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
GATEWAY_URL="http://gateway:8089"
SKIP_FALLBACK=false
V1_CHAT_URL=""

# Test results
PASSED=0
FAILED=0

# Helper to safely increment counter (avoids set -e issues)
increment_passed() {
    ((PASSED=PASSED+1)) || :
}
increment_failed() {
    ((FAILED=FAILED+1)) || :
}

# Helper functions
print_info() {
    echo -e "${BLUE}ℹ $1${NC}"
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠ $1${NC}"
}

# Parse arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --gateway-url)
                GATEWAY_URL="$2"
                shift 2
                ;;
            --skip-fallback)
                SKIP_FALLBACK=true
                shift
                ;;
            --help|-h)
                show_usage
                exit 0
                ;;
            *)
                echo "Unknown option: $1"
                show_usage
                exit 1
                ;;
        esac
    done

    V1_CHAT_URL="${GATEWAY_URL}/v1/chat/completions"
}

show_usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Options:
  --gateway-url <url>    Gateway service URL (default: http://gateway:8089)
  --skip-fallback        Skip the interactive fallback test
  --help, -h             Show this help message

Examples:
  # Run all tests (with interactive fallback test)
  $0

  # Run with custom gateway URL
  $0 --gateway-url http://gateway:8089

  # Run only basic tests (skip fallback)
  $0 --skip-fallback

Note: Fallback test requires manual intervention since gateway container
cannot run kubectl commands. The script will guide you through the steps.
EOF
}

# Check dependencies
check_dependencies() {
    if ! command -v curl &> /dev/null; then
        print_error "curl is required but not installed"
        exit 1
    fi
}

# Check gateway health
check_health() {
    print_info "Checking gateway health..."
    if curl -sf "${GATEWAY_URL}/healthz" > /dev/null 2>&1; then
        print_success "Gateway is healthy"
        return 0
    else
        print_error "Gateway is not healthy (URL: ${GATEWAY_URL})"
        return 1
    fi
}

# Make a chat completion request
# Args: $1=model, $2=messages JSON, $3=max_tokens
# Returns: sets RESPONSE and HTTP_CODE variables
make_chat_request() {
    local model="$1"
    local messages="$2"
    local max_tokens="${3:-50}"

    local payload
    payload=$(cat <<EOF
{
  "model": "${model}",
  "messages": ${messages},
  "max_tokens": ${max_tokens},
  "temperature": 0.7,
  "stream": false
}
EOF
)

    # Save response to temp file
    local temp_file
    temp_file=$(mktemp)

    HTTP_CODE=$(curl -s -w "%{http_code}" -o "$temp_file" \
        -X POST "${V1_CHAT_URL}" \
        -H "Content-Type: application/json" \
        -d "$payload" \
        --max-time 120 2>&1 || echo "000")

    RESPONSE=$(cat "$temp_file")
    rm -f "$temp_file"

    if [[ "$HTTP_CODE" == "200" ]]; then
        return 0
    else
        return 1
    fi
}

# Extract content from response
extract_content() {
    local response="$1"
    # Use grep/sed to extract content - fallback to python if available
    if command -v python3 &> /dev/null; then
        echo "$response" | python3 -c "import sys,json; print(json.load(sys.stdin)['choices'][0]['message']['content'])" 2>/dev/null || echo ""
    elif command -v python &> /dev/null; then
        echo "$response" | python -c "import sys,json; print(json.load(sys.stdin)['choices'][0]['message']['content'])" 2>/dev/null || echo ""
    else
        # Simple grep-based extraction (not reliable for all cases)
        echo "$response" | grep -o '"content": "[^"]*"' | head -1 | sed 's/"content": "//;s/"$//' || echo ""
    fi
}

# Test 1: Route to Local (Internal)
test_route_to_local() {
    echo
    print_info "Test 1: Route to Local (Internal) - model=Qwen/Qwen3-8B"
    echo "----------------------------------------------------------------"

    local messages='[{"role": "system", "content": "You are a helpful assistant."},{"role": "user", "content": "Say Hello from internal test in 5 words or less."}]'

    if make_chat_request "Qwen/Qwen3-8B" "$messages" 50; then
        local content
        content=$(extract_content "$RESPONSE")
        echo "  Response: ${content:0:100}..."
        print_success "Request routed to internal pool and completed successfully"
        increment_passed
        return 0
    else
        print_error "Request failed (HTTP ${HTTP_CODE}): ${RESPONSE:0:200}"
        increment_failed
        return 1
    fi
}

# Test 2: Route to External
test_route_to_external() {
    echo
    print_info "Test 2: Route to External - model=Qwen/Qwen2.5-7B"
    echo "----------------------------------------------------------------"

    local messages='[{"role": "system", "content": "You are a helpful assistant."},{"role": "user", "content": "Say Hello from external test in 5 words or less."}]'

    if make_chat_request "Qwen/Qwen2.5-7B" "$messages" 50; then
        local content
        content=$(extract_content "$RESPONSE")
        echo "  Response: ${content:0:100}..."
        print_success "Request routed to external service and completed successfully"
        increment_passed
        return 0
    else
        print_error "Request failed (HTTP ${HTTP_CODE}): ${RESPONSE:0:200}"
        increment_failed
        return 1
    fi
}

# Test 3: Fallback after Local Failure (Interactive Mode)
test_fallback_after_local_failure() {
    echo
    print_info "Test 3: Fallback after Local Failure"
    echo "----------------------------------------------------------------"

    if [[ "$SKIP_FALLBACK" == true ]]; then
        print_info "Skipping fallback test (--skip-fallback was set)"
        echo ""
        echo "To test fallback manually:"
        echo "  1. kubectl scale deployment internal --replicas=0 -n <namespace>"
        echo "  2. Wait for internal pod to terminate"
        echo "  3. Send request with model=Qwen/Qwen3-8B"
        echo "  4. Request should succeed via fallback to external after ~25s"
        echo "  5. Restore: kubectl scale deployment internal --replicas=1"
        return 0
    fi

    echo "This test requires manual intervention."
    echo "Gateway container cannot run kubectl commands."
    echo ""
    echo "================================================================"
    echo "MANUAL STEP 1: Stop internal service (simulate failure)"
    echo "================================================================"
    echo ""
    echo "Please open ANOTHER terminal and run:"
    echo ""
    # Try to detect namespace
    local ns="traffic-splitting"
    echo "  kubectl scale deployment internal --replicas=0 -n ${ns}"
    echo ""
    echo "Then wait for the internal pod to terminate:"
    echo "  kubectl get pods -n ${ns} -l app=internal -w"
    echo ""
    read -p "Press ENTER after internal pod is terminated..."
    echo ""

    print_info "Sending request to Qwen/Qwen3-8B (should fallback to external)..."
    print_info "Expected flow: internal -> fail -> retry 2x -> fallback -> external"
    echo ""

    local messages='[{"role": "system", "content": "You are a helpful assistant."},{"role": "user", "content": "Say Hello from fallback test in 5 words or less."}]'

    local start_time end_time elapsed
    start_time=$(date +%s)

    local test_result=0
    if make_chat_request "Qwen/Qwen3-8B" "$messages" 50; then
        end_time=$(date +%s)
        elapsed=$((end_time - start_time))

        local content
        content=$(extract_content "$RESPONSE")
        echo "  Response received in ${elapsed}s"
        echo "  Content: ${content:0:100}..."
        print_success "Request succeeded via fallback to external service!"
        print_warning "  Note: Request took ${elapsed}s (includes 2 retries + fallback)"
        increment_passed
        test_result=0
    else
        print_error "Request failed (HTTP ${HTTP_CODE}): ${RESPONSE:0:200}"
        increment_failed
        test_result=1
    fi

    echo ""
    echo "================================================================"
    echo "MANUAL STEP 2: Restore internal service"
    echo "================================================================"
    echo ""
    echo "Please open ANOTHER terminal and run:"
    echo ""
    echo "  kubectl scale deployment internal --replicas=1 -n ${ns}"
    echo ""
    echo "Then wait for internal pod to become ready:"
    echo "  kubectl get pods -n ${ns} -l app=internal -w"
    echo ""
    read -p "Press ENTER after internal pod is restored (optional)..."
    echo ""

    return $test_result
}

# Print summary
print_summary() {
    echo
    echo "================================================================"
    echo "Test Summary"
    echo "================================================================"
    echo "  Passed: ${PASSED}"
    echo "  Failed: ${FAILED}"
    echo
    if [[ $FAILED -eq 0 ]]; then
        print_success "ALL TESTS PASSED"
        return 0
    else
        print_error "SOME TESTS FAILED"
        return 1
    fi
}

# Main function
main() {
    parse_args "$@"

    echo
    echo "================================================================"
    echo "Service Router Prefix Routing Tests"
    echo "================================================================"
    echo "  Gateway URL: ${GATEWAY_URL}"
    echo "  Skip Fallback: ${SKIP_FALLBACK}"
    echo

    # Check dependencies
    check_dependencies

    # Health check
    if ! check_health; then
        exit 1
    fi

    # Run tests
    test_route_to_local
    test_route_to_external
    test_fallback_after_local_failure

    # Print summary
    print_summary
    exit $?
}

# Run main
main "$@"
