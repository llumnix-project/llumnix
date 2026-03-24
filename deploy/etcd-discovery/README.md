# etcd Discovery Integration Tests

Test deployment to validate etcd-based service discovery and etcd failover recovery.

## Deployment Architecture

This deployment uses full-mode scheduling with CMS. The Gateway discovers backend instances via etcd Watch; the Scheduler reads instance metadata from CMS.

- **Gateway**: Discovers backend instances via etcd Watch (`--llm-backend-discovery=etcd`).
- **Scheduler**: Reads instance metadata from CMS (Redis) for scheduling decisions.
- **etcd Cluster**: 3-node StatefulSet storing instance discovery data. Tolerates 1 node failure.
- **neutral-\***: 2 vLLM inference instances (single-GPU, Qwen/Qwen3-8B), each with a discovery sidecar that registers the instance with etcd.

## Test Scenarios

The integration test script (`test_etcd_failover.sh`) validates 5 scenarios:

### Test 1: Baseline

All 3 etcd nodes healthy. Gateway and Scheduler discover both instances.

### Test 2: Single node failure

Delete `etcd-2`. The remaining 2 nodes maintain quorum. Discovery continues without interruption.

### Test 3: Two node failure

Delete `etcd-0` and `etcd-1`. Quorum is lost; the Gateway retains its cached instance list. After restoring all nodes, the system recovers.

### Test 4: Full cluster restart — sidecar re-registration

Scale the etcd StatefulSet to 0, then back to 3. All etcd data is lost (emptyDir). Discovery sidecars detect the lease loss, grant new leases via their heartbeat loop, and re-register instances. The test verifies both sidecar re-registration logs and Gateway re-discovery.

### Test 5: Instance pod restart — sidecar re-registration

Delete an instance pod and wait for the Deployment controller to recreate it. The new pod's discovery sidecar registers the instance with etcd. The test verifies that etcd contains the expected number of discovery keys and the Gateway re-discovers all instances.

## Usage

### 1. Deploy

```bash
cd deploy/
./group_deploy.sh <namespace> etcd-discovery
```

### 2. Wait for Services

```bash
kubectl get pods -n <namespace> -w
```

Wait until all pods show `Running` and `Ready`:
- `etcd-0`, `etcd-1`, `etcd-2`
- `gateway`, `scheduler`
- `neutral-*` (2 pods, takes time due to model download)

### 3. Verify etcd Cluster Health

```bash
kubectl exec -n <namespace> etcd-0 -- etcdctl \
  --endpoints=http://localhost:2379 endpoint health
```

Expected output:

```
http://localhost:2379 is healthy: successfully committed proposal: took = 1.234ms
```

### 4. Verify Instance Registration

List registered discovery keys:

```bash
kubectl exec -n <namespace> etcd-0 -- etcdctl \
  --endpoints=http://localhost:2379 get --prefix --keys-only llumnix/discovery/
```

Expected output (one key per instance):

```
llumnix/discovery/neutral-<hash1>
llumnix/discovery/neutral-<hash2>
```

### 5. Verify Gateway Discovery

Check Gateway logs for etcd resolver messages:

```bash
kubectl logs -n <namespace> deployment/gateway -c gateway | grep "etcd resolver"
```

Expected log entries:

```
I0320 10:00:01.234 etcd_resolver.go:200] etcd resolver refresh: loaded 2 instances
I0320 10:00:01.235 etcd_resolver.go:170] etcd resolver (inferType=neutral): Added: 2, Removed: 0, Total: 2
```

### 6. Run Failover Tests

```bash
./deploy/etcd-discovery/test_etcd_failover.sh <namespace>
```

The script runs 5 tests sequentially. Each test prints its result inline. In a separate terminal, watch Gateway logs to observe instance add/remove events in real time:

```bash
kubectl logs -f -n <namespace> deployment/gateway -c gateway | grep "etcd resolver"
```

Expected test output:

```
==========================================
etcd Failover Integration Test
Namespace: llumnix
==========================================

Test 1: Baseline - All etcd nodes healthy
------------------------------------------
  etcd-0: healthy
  etcd-1: healthy
  etcd-2: healthy
✓ Gateway has discovered instances via etcd

Test 2: Single etcd node failure (etcd-2)
------------------------------------------
✓ Test 2 PASSED: Discovery continues after single node failure

Test 3: Two etcd node failure
-----------------------------------------------------------
  Restoring all etcd nodes...
✓ Test 3 PASSED: System recovered after quorum loss

Test 4: Full cluster restart — sidecar re-registration
------------------------------------------------------
  Instance pods: neutral-<hash1> neutral-<hash2>
  Step 1: Record sidecar log line counts before restart...
  Step 2: Stop all etcd nodes...
  All etcd nodes stopped.
  Step 3: Restart etcd cluster...
  All etcd nodes are ready
  Step 4: Waiting for discovery sidecars to re-register (90s)...
  Step 5: Verify sidecar re-registration via logs...
  ✓ neutral-<hash1>: sidecar re-registered instances after etcd restart
    → Lease recovery activity detected
  ✓ neutral-<hash2>: sidecar re-registered instances after etcd restart
    → Lease recovery activity detected
  Step 6: Verify Gateway re-discovered instances...
✓ Test 4 PASSED: Sidecars re-registered and Gateway re-discovered instances after full etcd restart

Test 5: Measure failover latency
---------------------------------
✓ Gateway detected instance removal in 47ms

==========================================
All etcd failover tests completed!
==========================================
```

After Test 5, verify the Gateway detected the instance removal:

```bash
kubectl logs -n <namespace> deployment/gateway -c gateway | grep "Removed"
```

Expected log entry:

```
I0320 10:05:12.345 etcd_resolver.go:170] etcd resolver (inferType=neutral): Added: 0, Removed: 1, Total: 1
```

## Cleanup

```bash
./deploy/group_delete.sh <namespace> etcd-discovery
```
