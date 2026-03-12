# Service Router Prefix Routing Tests

Test script to validate the prefix-based routing and fallback behavior of the `service-router/prefix` deployment.

## Test File

- `test_service_router.sh` - Bash test script (uses only `curl`, no dependencies)

## Test Scenarios

### 1. Route to Local (Internal)

**Model:** `Qwen/Qwen3-8B`

**Routing Rule:**
```json
{"prefix":"Qwen/Qwen3-*","base_url":"local","fallback":false}
```

**Expected:** Request is routed to the internal llumnix-managed pool via scheduler.

### 2. Route to External

**Model:** `Qwen/Qwen2.5-7B`

**Routing Rule:**
```json
{"prefix":"Qwen/Qwen2.5-*","base_url":"http://vllm-external:8000","fallback":true}
```

**Expected:** Request is directly proxied to the external service (`vllm-external:8000`).

### 3. Fallback after Local Failure

**Model:** `Qwen/Qwen3-8B`

**How it works:**
1. Internal service is scaled down (simulates failure)
2. Request sent with `model=Qwen/Qwen3-8B` (matches internal prefix)
3. Gateway tries internal → fails (no instances available)
4. Gateway retries 2 times (configured by `--retry-max-count 2`)
5. After retries exhausted, gateway triggers fallback
6. Fallback endpoint (`vllm-external:8000`) serves the request
7. External has `--served-model-name "Qwen/Qwen3-8B"` alias, so it accepts the request

**Note:** This test is interactive. The script will prompt you to run kubectl commands in another terminal since the gateway container cannot execute kubectl.

## Usage

### Quick Start (Test 1 & 2 Only)

If you only want to verify basic routing (without fallback test):

```bash
# Copy test script to gateway container
kubectl cp test_service_router.sh <namespace>/gateway-xxx:/tmp/

# Execute in gateway container
kubectl exec -it <namespace>/gateway-xxx -- bash
cd /tmp
chmod +x test_service_router.sh

# Run with --skip-fallback to skip the interactive fallback test
./test_service_router.sh --skip-fallback
```

This runs:
- ✅ Test 1: Route Qwen3-8B to internal
- ✅ Test 2: Route Qwen2.5-7B to external
- ⏭️  Skips Test 3 (fallback)

### Full Test (Including Fallback - Requires Manual Steps)

To run all three tests including the fallback scenario:

```bash
# Terminal 1: Gateway container
kubectl exec -it <namespace>/gateway-xxx -- bash
cd /tmp
./test_service_router.sh

# Script will pause during Test 3 and prompt you to run kubectl commands
# in another terminal. Follow the on-screen instructions.
```

**Test 3 requires**: Another terminal with kubectl access to scale deployments.

See [Test 3 (Fallback) - Manual Steps](#test-3-fallback---manual-steps) below for detailed instructions.

## Deployment Architecture

```
                    ┌─────────────────┐
                    │     Gateway     │
                    │   (port 8089)   │
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
      Route: Qwen/Qwen3-*   │      Route: Qwen/Qwen2.5-*
      (Prefix Match)        │      (Prefix Match)
              │              │              │
              ▼              │              ▼
    ┌─────────────────┐     │     ┌─────────────────┐
    │    Internal     │     │     │    External     │
    │   (llumnix)     │     │     │  (standalone)   │
    │   Qwen3-8B      │     │     │  Qwen2.5-7B     │
    │   Redis +       │     │     │  + Qwen3-8B     │
    │   Discovery     │     │     │  (alias)        │
    └─────────────────┘     │     └─────────────────┘
              │              │              ▲
              │              │              │
              └──────────────┴──────────────┘
                   Fallback (when internal fails)
```

## Configuration Details

### Gateway Configuration

Key flags in `gateway.yaml`:

```yaml
- "--retry-max-count"
- "2"                                    # Retry internal 2x before fallback
- "--route-policy"
- "prefix"                               # Prefix-based routing
- "--route-config"
- '[{"prefix":"Qwen/Qwen3-*","base_url":"local","fallback":false},
    {"prefix":"Qwen/Qwen2.5-*","base_url":"http://vllm-external:8000","fallback":true}]'
```

### External Service Configuration

Key configuration in `external.yaml`:

```yaml
# Exposes both models (primary + alias for fallback)
--served-model-name "Qwen/Qwen2.5-7B" "Qwen/Qwen3-8B"
```

## Test 3 (Fallback) - Manual Steps

Since the test script runs inside the gateway container (no kubectl access), follow these manual steps:

### Terminal 1: Run Test Script
```bash
kubectl exec -it <namespace>/gateway-xxx -- bash
./test_service_router.sh
# When prompted for Step 1, switch to Terminal 2
```

### Terminal 2: Stop Internal Service
```bash
# Scale down internal (simulate failure)
kubectl scale deployment internal --replicas=0 -n <namespace>

# Wait for pod to terminate
kubectl get pods -n <namespace> -l app=internal -w
# (Press Ctrl+C when pod is gone, then return to Terminal 1)
```

### Terminal 1: Continue Test
```bash
# Press ENTER to continue - test will send request
# Should see response after ~25s (retries + fallback)
```

### Terminal 2: Restore Internal Service
```bash
# Restore internal
kubectl scale deployment internal --replicas=1 -n <namespace>

# Wait for pod to be ready
kubectl get pods -n <namespace> -l app=internal -w
```

## Verifying Logs (How to Confirm Correct Routing)

The test script can only verify that requests succeed. **To confirm which service actually handled the request, you need to check the gateway logs:**

```bash
# In another terminal, view gateway logs in real-time
kubectl logs -f deployment/gateway -n <namespace>
```

### Correct Log Examples

#### Test 1: Qwen3-8B → Internal
```
I0312 07:27:43.717473 gateway_service.go:560] Request [xxx] routed to <nil> (type: RouteInternal)
...(followed by vllm response logs)...
```
**Verification**: `type: RouteInternal` indicates routing to internal service

#### Test 2: Qwen2.5-7B → External
```
I0312 07:29:48.987637 gateway_service.go:560] Request [xxx] routed to http://vllm-external:8000 (type: RouteExternal)
2026-03-12 07:29:49.248 Request completed [xxx] status:200...RT:261ms...
```
**Verification**:
- `type: RouteExternal` indicates routing to external service
- `RT:261ms` fast response (~260ms) because it's direct proxy without retries

#### Test 3: Qwen3-8B → Fallback to External
```
I0312 07:33:38.527952 gateway_service.go:560] Request [xxx] routed to <nil> (type: RouteInternal)
I0312 07:33:38.530886 scheduler_client.go:164] [xxx] all service endpoints are busy, try next after 1000ms
I0312 07:33:39.532439 scheduler_client.go:164] [xxx] all service endpoints are busy, try next after 1000ms
...(retry 5 times)...
I0312 07:33:43.539730 gateway_service.go:566] request [xxx] scheduling failed: no available endpoint
I0312 07:33:43.539755 service_router.go:230] get fallback endpoint with external endpoint: http://vllm-external:8000
I0312 07:33:43.539766 gateway_service.go:490] request [xxx] will fallback to: http://vllm-external:8000
2026-03-12 07:33:43.803 Request completed [xxx] status:200...RT:5276ms...
```
**Verification**:
1. First tries `RouteInternal`, but scheduling fails
2. Retries multiple times (`all service endpoints are busy`)
3. Triggers fallback: `will fallback to: http://vllm-external:8000`
4. Eventually succeeds but takes longer (`RT:5276ms`, ~5 seconds)

### Key Log Patterns Summary

| Scenario | Key Log |
|---------|---------|
| Normal → Internal | `type: RouteInternal` |
| Normal → External | `type: RouteExternal` |
| Fallback → External | `type: RouteInternal` → `will fallback to` → `type: RouteExternal` |

**Note**: The fallback scenario takes significantly longer because gateway first retries internal (with backoff), then falls back to external.

## Expected Output

```
================================================================
Service Router Prefix Routing Tests
================================================================
  Gateway URL: http://gateway:8089
  Skip Fallback: false

ℹ Checking gateway health...
✓ Gateway is healthy

ℹ Test 1: Route to Local (Internal) - model=Qwen/Qwen3-8B
----------------------------------------------------------------
  Response: Hello! Internal routing test successful...
✓ Request routed to internal pool and completed successfully

ℹ Test 2: Route to External - model=Qwen/Qwen2.5-7B
----------------------------------------------------------------
  Response: Hello! External routing test successful...
✓ Request routed to external service and completed successfully

ℹ Test 3: Fallback after Local Failure
----------------------------------------------------------------
This test requires manual intervention.

================================================================
MANUAL STEP 1: Stop internal service (simulate failure)
================================================================

Please open ANOTHER terminal and run:

  kubectl scale deployment internal --replicas=0 -n service-router

Press ENTER after internal pod is terminated...

ℹ Sending request to Qwen/Qwen3-8B (should fallback to external)...
  Response received in 25s
  Content: Hello! Fallback test successful...
✓ Request succeeded via fallback to external service!

================================================================
MANUAL STEP 2: Restore internal service
================================================================

Please open ANOTHER terminal and run:

  kubectl scale deployment internal --replicas=1 -n service-router

Press ENTER after internal pod is restored (optional)...

================================================================
Test Summary
================================================================
  Passed: 3
  Failed: 0

✓ ALL TESTS PASSED
```
