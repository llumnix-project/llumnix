#!/bin/bash
# lint-backend.sh - Check backend files for forbidden handler-specific type access
#
# IMPORTANT: Backend implementations MUST NOT be coupled with handlers.
# Do NOT reference handler-specific types (e.g., LLMRequest, AnthropicRequest)
# from RequestContext within backend code.
#
# Usage: ./scripts/lint-backend.sh
# Exit code: 0 if clean, 1 if violations found

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
BACKEND_DIR="$PROJECT_ROOT/pkg/gateway/service/backend"

# Patterns to detect violations
FORBIDDEN_PATTERNS=(
    '\.LLMRequest\.'
    '\.AnthropicRequest\.'
)

# Files to check (only *_backend.go files)
BACKEND_FILES=$(find "$BACKEND_DIR" -name '*_backend.go' -type f 2>/dev/null)

if [ -z "$BACKEND_FILES" ]; then
    echo "No backend files found in $BACKEND_DIR"
    exit 0
fi

VIOLATIONS_FOUND=0

echo "Checking backend files for forbidden handler-specific type access..."
echo "=================================================================="

for file in $BACKEND_FILES; do
    filename=$(basename "$file")
    
    for pattern in "${FORBIDDEN_PATTERNS[@]}"; do
        # Use grep to find violations, suppress errors
        matches=$(grep -n "$pattern" "$file" 2>/dev/null || true)
        
        if [ -n "$matches" ]; then
            if [ $VIOLATIONS_FOUND -eq 0 ]; then
                echo ""
                echo "ERROR: Backend architecture violation detected!"
                echo ""
            fi
            VIOLATIONS_FOUND=1
            
            echo "File: $filename"
            echo "Pattern: $pattern"
            echo "Violations:"
            echo "$matches" | while read -r line; do
                echo "  $line"
            done
            echo ""
        fi
    done
done

if [ $VIOLATIONS_FOUND -eq 1 ]; then
    echo "=================================================================="
    echo "FAILED: Backend files must not directly access LLMRequest or AnthropicRequest."
    echo "Use RequestContext methods instead (e.g., MarshalRequestWithArgs, GetBackendURLPath)."
    echo ""
    exit 1
else
    echo "PASSED: No backend architecture violations found."
    exit 0
fi
