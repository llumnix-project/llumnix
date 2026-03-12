# Service Router Weight Routing Tests

Test script to validate weight-based routing between internal and external vLLM services.

## Overview

This deployment tests the `weight` routing policy where requests are **randomly distributed 50/50 between internal and external services**, regardless of the model name.

**Key difference from prefix routing**: Weight routing does not inspect the model name. Any request has a 50% chance of going to internal and 50% chance of going to external.

Both services run the **same model (Qwen3-8B)** to demonstrate pure weight-based load balancing.

## Deployment Architecture

```
                    ┌─────────────────┐
                    │     Gateway     │
                    │   (port 8089)   │
                    └────────┬────────┘
                             │
                    Weight-based routing
                    (50% / 50%)
                    /                \
           Internal (50%)        External (50%)
              ↓                      ↓
    ┌─────────────────┐    ┌─────────────────┐
    │    Internal     │    │    External     │
    │   (llumnix)     │    │  (standalone)   │
    │   Qwen3-8B      │    │  Qwen2.5-7B     │
    └─────────────────┘    └─────────────────┘
```

## Configuration

### Gateway Configuration

Key configuration for weight routing:

```yaml
- "--route-policy"
- "weight"
- "--route-config"
- '[{"base_url":"local","weight":50},{"base_url":"http://vllm-external:8000","weight":50}]'
```

**Routing behavior**:
- No model prefix matching - requests are routed purely by weight
- 50% chance to internal, 50% chance to external for ANY model

## Test Scenarios

### Weight Distribution Test

Send multiple requests and verify both services receive traffic (regardless of model name):

```bash
./test_service_router.sh --count 10
```

**Expected**: Roughly 50% of requests go to internal, 50% to external. Since both services run the same model, each request is randomly routed.

## Usage

### Deploy

```bash
./deploy/group_deploy.sh <namespace> service-router/weight
```

### Copy Test Script to Gateway

```bash
# Find gateway pod
kubectl get pod -n <namespace> -l app=gateway

# Copy test script
kubectl cp test_service_router.sh <namespace>/gateway-xxx:/tmp/

# Execute
kubectl exec -it <namespace>/gateway-xxx -- bash
cd /tmp
chmod +x test_service_router.sh
./test_service_router.sh
```

## Verifying Logs

To confirm the 50/50 weight distribution, watch gateway logs during the test:

```bash
kubectl logs -f deployment/gateway -n <namespace> | grep "routed to"
```

### Expected Log Patterns

#### Route to Internal (50%)
```
I0312 xx:xx:xx gateway_service.go:560] Request [xxx] routed to <nil> (type: RouteInternal)
```

#### Route to External (50%)
```
I0312 xx:xx:xx gateway_service.go:560] Request [xxx] routed to http://vllm-external:8000 (type: RouteExternal)
```

### Sample Output from 10 Requests

```
Request [req-1] routed to <nil> (type: RouteInternal)
Request [req-2] routed to http://vllm-external:8000 (type: RouteExternal)
Request [req-3] routed to <nil> (type: RouteInternal)
Request [req-4] routed to http://vllm-external:8000 (type: RouteExternal)
Request [req-5] routed to http://vllm-external:8000 (type: RouteExternal)
Request [req-6] routed to <nil> (type: RouteInternal)
Request [req-7] routed to <nil> (type: RouteInternal)
Request [req-8] routed to http://vllm-external:8000 (type: RouteExternal)
Request [req-9] routed to http://vllm-external:8000 (type: RouteExternal)
Request [req-10] routed to <nil> (type: RouteInternal)
```

**Distribution**: Internal 5/10, External 5/10 (varies due to randomness)

## Expected Test Output

```
================================================================
Service Router Weight Routing Tests
================================================================
  Gateway URL: http://gateway:8089
  Request Count: 10
  Routing: 50% internal, 50% external (weight-based)

ℹ Checking gateway health...
✓ Gateway is healthy

================================================================
ℹ STEP 1: Send 10 requests (weight-based routing)
================================================================

All requests use the same model (Qwen/Qwen3-8B).
Expected: ~50% routed to internal, ~50% to external (random)

  Request 1: HTTP 200 ✓
  Request 2: HTTP 200 ✓
  Request 3: HTTP 200 ✓
  ...
✓ Completed: 10 success, 0 failed

================================================================
ℹ STEP 2: Verify routing distribution
================================================================

To verify that requests were distributed 50/50 between internal
and external services, check the gateway logs:

  kubectl logs -f deployment/gateway -n <namespace>

Expected log patterns:
  - type: RouteInternal  (roughly 50% of requests)
  - type: RouteExternal  (roughly 50% of requests)

✓ Test completed! Check gateway logs to verify routing.
```
