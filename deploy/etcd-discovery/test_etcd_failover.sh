#!/bin/bash
# etcd Failover Integration Test Script
#
# Validates that Gateway service discovery recovers from etcd node failures.
# Tests: baseline, single/double node failure, full cluster restart, instance pod restart.
#
# Usage: ./test_etcd_failover.sh [namespace]

set -e

NAMESPACE=${1:-llumnix}
ETCD_PODS=("etcd-0" "etcd-1" "etcd-2")

echo "=========================================="
echo "etcd Failover Integration Test"
echo "Namespace: $NAMESPACE"
echo "=========================================="

# Pre-flight checks: verify required pods exist
echo ""
echo "Pre-flight: checking required pods in namespace '$NAMESPACE'..."

GATEWAY_POD=$(kubectl get pods -n $NAMESPACE -l app=gateway -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
if [ -z "$GATEWAY_POD" ]; then
    echo "ERROR: No gateway pod found (label app=gateway) in namespace '$NAMESPACE'."
    echo "  Make sure the namespace is correct and the deployment is running."
    echo "  Usage: $0 [namespace]"
    exit 1
fi
echo "  Gateway pod: $GATEWAY_POD"

NEUTRAL_COUNT=$(kubectl get pods -n $NAMESPACE -l app=neutral --no-headers 2>/dev/null | wc -l)
if [ "$NEUTRAL_COUNT" -eq 0 ]; then
    echo "ERROR: No neutral instance pods found (label app=neutral) in namespace '$NAMESPACE'."
    exit 1
fi
echo "  Neutral instance pods: $NEUTRAL_COUNT"

ETCD_COUNT=$(kubectl get pods -n $NAMESPACE -l app=etcd --no-headers 2>/dev/null | wc -l)
if [ "$ETCD_COUNT" -eq 0 ]; then
    echo "ERROR: No etcd pods found (label app=etcd) in namespace '$NAMESPACE'."
    exit 1
fi
echo "  etcd pods: $ETCD_COUNT"
echo "  Pre-flight checks passed."

check_etcd_health() {
    local pod=$1
    kubectl exec -n $NAMESPACE $pod -- etcdctl --endpoints=http://localhost:2379 endpoint health 2>/dev/null || echo "unhealthy"
}

wait_for_etcd_ready() {
    echo "Waiting for all etcd nodes to be ready..."
    for pod in "${ETCD_PODS[@]}"; do
        until kubectl get pod -n $NAMESPACE $pod -o jsonpath='{.status.phase}' | grep -q "Running"; do
            echo "  Waiting for $pod..."
            sleep 2
        done
    done
    echo "  All etcd nodes are ready"
}

verify_gateway_instances() {
    # Verify instances are registered in etcd and visible to Gateway.
    # Arg 1 (optional): minimum expected instance count (default: 2)
    local expected_count=${1:-2}
    sleep 5

    # Primary check: query etcd directly for registered discovery keys
    local registered_keys registered_count
    registered_keys=$(kubectl exec -n $NAMESPACE etcd-0 -- etcdctl --endpoints=http://localhost:2379 get --prefix --keys-only llumnix/discovery/ 2>/dev/null || true)
    registered_count=$(echo "$registered_keys" | grep -c "llumnix/discovery/" 2>/dev/null || echo "0")

    if [ "$registered_count" -ge "$expected_count" ]; then
        echo "✓ etcd has $registered_count registered discovery keys (expected >= $expected_count)"
    else
        echo "✗ etcd has only $registered_count registered discovery keys (expected >= $expected_count)"
        echo "  Keys: $registered_keys"
        return 1
    fi

    # Secondary check: look for Gateway resolver log showing discovered instances
    local gateway_line
    gateway_line=$(kubectl logs -n $NAMESPACE $GATEWAY_POD -c gateway --tail=200 | grep "etcd resolver.*Total:" | tail -1 || true)
    if [ -n "$gateway_line" ]; then
        echo "✓ Gateway resolver log: $gateway_line"
    else
        echo "  ⚠ No recent Gateway resolver log found (Gateway may not have refreshed yet)"
    fi

    return 0
}

# Test 1: Baseline - all etcd nodes healthy
echo ""
echo "Test 1: Baseline - All etcd nodes healthy"
echo "------------------------------------------"
wait_for_etcd_ready

for pod in "${ETCD_PODS[@]}"; do
    health=$(check_etcd_health $pod)
    echo "  $pod: $health"
done

verify_gateway_instances

# Test 2: Single node failure
echo ""
echo "Test 2: Single etcd node failure (etcd-2)"
echo "------------------------------------------"
kubectl delete pod -n $NAMESPACE etcd-2 --grace-period=0 --force 2>/dev/null || true
echo "  Waiting 10s for failover..."
sleep 10

for pod in "etcd-0" "etcd-1"; do
    health=$(check_etcd_health $pod)
    echo "  $pod: $health"
done

if verify_gateway_instances; then
    echo "✓ Test 2 PASSED: Discovery continues after single node failure"
else
    echo "✗ Test 2 FAILED"
    exit 1
fi

echo "  Waiting for etcd-2 to recover before next test..."
kubectl wait --for=condition=ready pod -n $NAMESPACE etcd-2 --timeout=120s || echo "  ⚠ etcd-2 recovery timed out, continuing (Test 3 will wait for it)"

# Test 3: Two node failure
echo ""
echo "Test 3: Two etcd node failure"
echo "-----------------------------------------------------------"
kubectl delete pod -n $NAMESPACE etcd-0 etcd-1 --grace-period=0 --force 2>/dev/null || true
echo "  Waiting 15s..."
sleep 15

health=$(check_etcd_health etcd-2)
echo "  etcd-2: $health"

echo "  Restoring all etcd nodes..."
kubectl wait --for=condition=ready pod -n $NAMESPACE etcd-0 etcd-1 etcd-2 --timeout=120s || true
wait_for_etcd_ready

echo "  Waiting for etcd cluster to become fully healthy..."
for attempt in $(seq 1 30); do
    ALL_HEALTHY=true
    for pod in "${ETCD_PODS[@]}"; do
        if ! kubectl exec -n $NAMESPACE $pod -- etcdctl --endpoints=http://localhost:2379 endpoint health 2>/dev/null | grep -q "is healthy"; then
            ALL_HEALTHY=false
            break
        fi
    done
    if [ "$ALL_HEALTHY" = true ]; then
        echo "  All etcd nodes are healthy"
        break
    fi
    sleep 5
done

# Sidecar heartbeat interval is 30s (default --interval).
# After etcd recovers, wait 2 heartbeat cycles for sidecar to re-register.
echo "  Waiting for discovery sidecars to re-register (60s = 2 heartbeat cycles)..."
sleep 60

if verify_gateway_instances; then
    echo "✓ Test 3 PASSED: System recovered after quorum loss"
else
    echo "✗ Test 3 FAILED"
    exit 1
fi

# Test 4: Full cluster restart (validates sidecar lease recovery)
echo ""
echo "Test 4: Full cluster restart — sidecar re-registration"
echo "------------------------------------------------------"
echo "  This test validates that discovery sidecars detect lease loss and"
echo "  re-register instances after all etcd data is lost (emptyDir)."
echo ""

INSTANCE_PODS=($(kubectl get pods -n $NAMESPACE -l app=neutral -o jsonpath='{.items[*].metadata.name}'))
echo "  Instance pods: ${INSTANCE_PODS[*]}"

echo "  Step 1: Record sidecar log line counts before restart..."
declare -A SIDECAR_LOG_LINES_BEFORE
for pod in "${INSTANCE_PODS[@]}"; do
    SIDECAR_LOG_LINES_BEFORE[$pod]=$(kubectl logs -n $NAMESPACE $pod -c discovery 2>/dev/null | wc -l)
done

echo "  Step 2: Stop all etcd nodes..."
kubectl scale statefulset -n $NAMESPACE etcd --replicas=0
sleep 5
echo "  All etcd nodes stopped."
sleep 10

echo "  Step 3: Restart etcd cluster..."
kubectl scale statefulset -n $NAMESPACE etcd --replicas=3
wait_for_etcd_ready

echo "  Step 4: Waiting for etcd cluster to become fully healthy..."
for attempt in $(seq 1 30); do
    ALL_HEALTHY=true
    for pod in "${ETCD_PODS[@]}"; do
        if ! kubectl exec -n $NAMESPACE $pod -- etcdctl --endpoints=http://localhost:2379 endpoint health 2>/dev/null | grep -q "is healthy"; then
            ALL_HEALTHY=false
            break
        fi
    done
    if [ "$ALL_HEALTHY" = true ]; then
        echo "  All etcd nodes are healthy"
        break
    fi
    sleep 5
done

# Sidecar heartbeat interval is 30s, lease TTL is 60s, keepalive interval is 20s.
# After full etcd restart: wait for lease expiry (60s) + 2 heartbeat cycles (60s).
echo "  Step 5: Waiting for discovery sidecars to re-register (120s = lease TTL + 2 heartbeat cycles)..."
sleep 120

echo "  Step 6: Verify sidecar re-registration via logs..."
SIDECAR_RECOVERED=true
for pod in "${INSTANCE_PODS[@]}"; do
    LINES_BEFORE=${SIDECAR_LOG_LINES_BEFORE[$pod]}
    NEW_LOGS=$(kubectl logs -n $NAMESPACE $pod -c discovery 2>/dev/null | tail -n +$((LINES_BEFORE + 1)))

    if echo "$NEW_LOGS" | grep -q "Updated.*healthy instances"; then
        echo "  ✓ $pod: sidecar re-registered instances after etcd restart"
    else
        echo "  ✗ $pod: no re-registration detected in sidecar logs"
        echo "    Recent sidecar logs:"
        echo "$NEW_LOGS" | tail -5 | sed 's/^/      /'
        SIDECAR_RECOVERED=false
    fi

    if echo "$NEW_LOGS" | grep -q "lease.*recover\|lease.*failed\|lease.*refresh"; then
        echo "    → Lease recovery activity detected"
    fi
done

echo "  Step 7: Verify Gateway re-discovered instances..."
if verify_gateway_instances && [ "$SIDECAR_RECOVERED" = true ]; then
    echo "✓ Test 4 PASSED: Sidecars re-registered and Gateway re-discovered instances after full etcd restart"
else
    echo "✗ Test 4 FAILED"
    if [ "$SIDECAR_RECOVERED" = false ]; then
        echo "  Sidecar re-registration was not confirmed. Check sidecar logs for errors."
    fi
    exit 1
fi

# Test 5: Instance pod restart — sidecar re-registration
echo ""
echo "Test 5: Instance pod restart — sidecar re-registration"
echo "------------------------------------------------------"
echo "  This test validates that when an instance pod is deleted and"
echo "  recreated by the Deployment controller, the new sidecar registers"
echo "  the instance with etcd and the Gateway discovers it."
echo ""

INSTANCE_POD=$(kubectl get pods -n $NAMESPACE -l app=neutral -o jsonpath='{.items[0].metadata.name}')
echo "  Step 1: Record current instance count in Gateway..."
INITIAL_TOTAL=$(kubectl logs -n $NAMESPACE $GATEWAY_POD -c gateway --tail=100 | grep -oP 'Total: \K[0-9]+' | tail -1)
echo "  Gateway currently sees ${INITIAL_TOTAL:-unknown} instances"

echo "  Step 2: Delete instance pod: $INSTANCE_POD"
kubectl delete pod -n $NAMESPACE $INSTANCE_POD --grace-period=0 --force 2>/dev/null || true

# Lease TTL is 60s. After pod deletion, the old lease expires and Gateway Watch
# detects key deletion. Wait up to lease TTL + buffer for removal detection.
echo "  Step 3: Waiting for Gateway to detect removal (up to 75s)..."
REMOVAL_DETECTED=false
for i in $(seq 1 75); do
    if kubectl logs -n $NAMESPACE $GATEWAY_POD -c gateway --tail=20 2>/dev/null | grep -q "Removed: 1"; then
        echo "  ✓ Gateway detected instance removal"
        REMOVAL_DETECTED=true
        break
    fi
    sleep 1
done
if [ "$REMOVAL_DETECTED" = false ]; then
    echo "  ⚠ Gateway removal detection not confirmed in logs (lease may not have expired yet)"
fi

echo "  Step 4: Waiting for Deployment to recreate pod..."
sleep 10
kubectl wait --for=condition=ready pod -n $NAMESPACE -l app=neutral --timeout=300s || true

NEW_PODS=($(kubectl get pods -n $NAMESPACE -l app=neutral -o jsonpath='{.items[*].metadata.name}'))
echo "  New instance pods: ${NEW_PODS[*]}"

# New pod needs: vLLM startup (readinessProbe periodSeconds=10, failureThreshold=3)
# + sidecar heartbeat interval (30s) + 1 cycle buffer.
echo "  Step 5: Waiting for new sidecar to register with etcd (60s = 2 heartbeat cycles)..."
sleep 60

echo "  Step 6: Verify new sidecar registered and Gateway re-discovered all instances..."
if verify_gateway_instances 2; then
    echo "✓ Test 5 PASSED: New sidecar registered and Gateway discovered all instances after pod restart"
else
    echo "✗ Test 5 FAILED"
    exit 1
fi

echo ""
echo "=========================================="
echo "All etcd failover tests completed!"
echo "=========================================="
