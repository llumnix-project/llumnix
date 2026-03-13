# Traffic Mirror Integration Tests

Test deployment to validate the traffic mirroring feature: requests to the gateway are asynchronously copied to a mirror target, with hot-reload support for enabling/disabling mirroring at runtime.

## Deployment Architecture

```
                     ┌─────────────────┐
   Request ──────────│     Gateway     │──────────── Response
                     │   (port 8089)   │
                     └────────┬────────┘
                              │
                    ┌─────────┴─────────┐
                    │ (async)           │ (primary)
                    ▼                   ▼
          ┌──────────────────┐ ┌──────────────────┐
          │  mirror-target   │ │    Internal       │
          │  (python echo)   │ │   (llumnix)       │
          │  port 8000       │ │   Qwen3-8B        │
          │  no GPU          │ │   1 GPU            │
          └──────────────────┘ └──────────────────┘
```

- **Gateway**: Serves requests normally via the internal pool; asynchronously mirrors a copy to `mirror-target` based on `/mnt/mirror.json` configuration.
- **Internal**: Standard llumnix-managed vLLM serving Qwen3-8B (handles the actual request).
- **Mirror Target**: Lightweight Python echo server (no GPU, uses the existing `python3:3.12-slim-bookworm` image from the registry). Returns 200 for any request. Check its stdout logs to verify mirror traffic arrived.

## Mirror Configuration

The mirror configuration is stored in a ConfigMap (`mirror-config`) mounted at `/mnt/mirror.json` inside the gateway pod.

### ConfigMap Content

```json
{
  "Enable": true,
  "Target": "http://mirror-target:8000",
  "Ratio": 100,
  "Timeout": 5000,
  "Authorization": "",
  "EnableLog": true
}
```

| Field           | Type    | Description                                      |
|-----------------|---------|--------------------------------------------------|
| `Enable`        | bool    | Master switch for mirroring                       |
| `Target`        | string  | Base URL of the mirror target                     |
| `Ratio`         | float64 | Percentage of requests to mirror (0-100)          |
| `Timeout`       | float64 | Mirror request timeout in ms (0 = use request ctx)|
| `Authorization` | string  | Override Authorization header for mirror requests |
| `EnableLog`     | bool    | Enable mirror-related logging in gateway          |

### Hot-Reload

The gateway watches `/mnt/mirror.json` every 10 seconds and auto-reloads on change. No restart required.

**Fast hot-reload** (exec into pod and write directly):

```bash
# Disable mirroring
kubectl exec -it deployment/gateway -n <ns> -c gateway -- \
  sh -c 'echo '\''{"Enable":false,"Target":"http://mirror-target:8000","Ratio":100,"Timeout":5000,"Authorization":"","EnableLog":true}'\'' > /mnt/mirror.json'

# Re-enable mirroring
kubectl exec -it deployment/gateway -n <ns> -c gateway -- \
  sh -c 'echo '\''{"Enable":true,"Target":"http://mirror-target:8000","Ratio":100,"Timeout":5000,"Authorization":"","EnableLog":true}'\'' > /mnt/mirror.json'
```

## Test Scenarios

### Test 1: Mirror Enabled

Send requests with mirroring enabled (Ratio=100). All requests should be mirrored to mirror-target.

**Verification**: mirror-target access logs show entries for each request.

### Test 2: Hot-Reload Disable

Write a new config with `Enable=false`, wait for reload, then send requests. No mirror traffic should appear.

**Verification**: mirror-target access logs do NOT grow.

### Test 3: Hot-Reload Re-Enable

Write a new config with `Enable=true`, wait for reload, then send requests. Mirror traffic resumes.

**Verification**: mirror-target access logs show new entries.

## Usage

### 1. Deploy

```bash
cd deploy/

./group_deploy.sh <namespace> traffic-mirror
```

### 2. Wait for Services

```bash
kubectl get pods -n <namespace> -w
```

Wait until:
- `gateway` pod is `Running` and ready
- `internal` pod is `Running` and ready (this takes time due to model download)
- `mirror-target` pod is `Running` and ready (fast, no GPU)
- `scheduler` pod is `Running` and ready

### 3. Run Test Script

```bash
# Find gateway pod
kubectl get pod -n <namespace> -l app=gateway

# Copy test script
kubectl cp traffic-mirror/test_traffic_mirror.sh <namespace>/<gateway-pod>:/tmp/

# Execute
kubectl exec -it <namespace>/<gateway-pod> -c gateway -- bash
chmod +x /tmp/test_traffic_mirror.sh
/tmp/test_traffic_mirror.sh
```

### 4. Verify Mirror Traffic

In a separate terminal, watch mirror-target logs:

```bash
kubectl logs -f deployment/mirror-target -n <namespace>
```

Expected log entries (one per mirrored request):

```
Mirror target echo server listening on :8000
[2026-03-13 12:00:00] POST /v1/chat/completions (88 bytes)
[2026-03-13 12:00:01] POST /v1/chat/completions (88 bytes)
```

### 5. Verify Gateway Logs

```bash
kubectl logs deployment/gateway -n <namespace> -c gateway | grep -i mirror
```

Expected log entries:

```
I0313 xx:xx:xx mirror.go:59] [req-id] Mirror request enabled, target: http://mirror-target:8000, ratio: 100.00, timeout: 5000.00
I0313 xx:xx:xx mirror.go:115] [req-id] Mirror request sent to http://mirror-target:8000/v1/chat/completions, status: 200
```

## Expected Test Output

```
================================================================
  Traffic Mirror Integration Tests
================================================================
  Gateway URL:       http://gateway:8089
  Mirror Target URL: http://mirror-target:8000
  Requests per step: 5
  Config path:       /mnt/mirror.json

ℹ Checking gateway health...
✓ Gateway is healthy
ℹ Checking mirror-target health...
✓ Mirror target is reachable

================================================================
ℹ TEST 1: Mirror Enabled - requests should be mirrored
================================================================

ℹ Wrote mirror config: Enable=true, Target=http://mirror-target:8000, Ratio=100
ℹ Waiting 15s for gateway to pick up new config...

  Request 1: HTTP 200 ✓
  Request 2: HTTP 200 ✓
  ...
✓ Completed: 5 success, 0 failed

>>> Check mirror-target logs for 5 access entries

================================================================
ℹ TEST 2: Hot-Reload - disable mirroring
================================================================

ℹ Wrote mirror config: Enable=false, Target=http://mirror-target:8000, Ratio=100
ℹ Waiting 15s for gateway to pick up new config...

  Request 1: HTTP 200 ✓
  ...
✓ Completed: 5 success, 0 failed

>>> Verify mirror-target logs did NOT grow

================================================================
ℹ TEST 3: Hot-Reload - re-enable mirroring
================================================================

ℹ Wrote mirror config: Enable=true, Target=http://mirror-target:8000, Ratio=100
ℹ Waiting 15s for gateway to pick up new config...

  Request 1: HTTP 200 ✓
  ...
✓ Completed: 5 success, 0 failed

>>> Check mirror-target logs for new access entries

================================================================
  Test Summary
================================================================
✓ All requests sent successfully.
✓ Traffic mirror test completed!
```

## Cleanup

```bash
./deploy/group_delete.sh <namespace> traffic-mirror
```
