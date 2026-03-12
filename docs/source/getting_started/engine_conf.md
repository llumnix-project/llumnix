# Llumlet Configuration Guide

To enable the Llumlet features within the vLLM engine, you must set the following environment variable.

``` yaml
env:
- name: VLLM_ENABLE_LLUMNIX
  value: "1"
```

It will trigger the internal initialization sequence within the vLLM engine:

1.  `llumlet_proxy` Initialization: vLLM will initialize the `llumlet_proxy`, which acts as the critical bridge between the inference engine and the Llumix management layer.
    
2.  `llumlet` Child Process: vLLM will spawn the `llumlet` child process. This dedicated process handles all heavy-lifting tasks, such as request migration and heartbeat reporting.
    
This is the default configuration for full-mode Scheduling.

## CMS Client Setup

Since instance metrics and status are reported to the CMS, the engine-side (Llumlet) must be configured with the CMS service address and authentication details after CMS initialization.

These settings are typically passed to the container or process via environment variables. Please ensure the following variables are set on the engine side:

*  `LLUMNIX_CMS_REDIS_ADDRESS`: The host address of the CMS Redis instance.
    
*  `LLUMNIX_CMS_REDIS_PORT`: The port of the CMS Redis instance.
    
*   `LLUMNIX_CMS_REDIS_USERNAME`: Username for Redis authentication.
    
*   `LLUMNIX_CMS_REDIS_PASSWORD`: Password for Redis authentication.

In an end-to-end deployment, these environment variables do not need to be modified by default. The Redis service is deployed via the provided YAML configuration (deploy/base/redis.yaml).

Note that the discovery sidecar container within the same Pod of engine also connects to Redis and must remain consistent. Its connection parameters are passed as command-line arguments rather than environment variables:

``` yaml
- name: discovery
  args:
    - |-
      exec python3 -m discovery.discovery \
      ...
      --redis_address redis \
      --redis_port 6379
```
If you modify the Redis Service configuration, ensure both the vllm container's environment variables and the discovery container's arguments are updated consistently.

## Update frequency Setup

The following environment variables control how frequently the engine-side Llumlet pushes metrics to the CMS. They are configured in the env section of the vllm container in the engine's LeaderWorkerSet YAML:

```yaml
env:
- name: LLUMNIX_STATUS_PUSH_INTERVAL
  value: "1"
- name: LLUMNIX_METADATA_PUSH_INTERVAL
  value: "20"
- name: LLUMNIX_CMS_EXPIRED_TIME
  value: "60"
- name: LLUMNIX_INSTANCE_UPDATE_STEPS
  value: "1"
- name: LLUMNIX_UPDATE_INSTANCE_STATUS_MODE
  value: "PUSH"
```
*   `LLUMNIX_STATUS_PUSH_INTERVAL`
    
    *   Description: Determines how often (in seconds) Llumlet pushes real-time instance status to the CMS.
        
    *   Impact: A smaller value allows the global scheduler to make highly accurate load-balancing decisions, but increases network I/O.
        
*    `LLUMNIX_METADATA_PUSH_INTERVAL`
    
    *   Description: Determines how often (in seconds) Llumlet pushes static instance metadata to the CMS. This primarily acts as a periodic keep-alive heartbeat, defaults to`20`.
        
    *   Impact: Since metadata rarely changes during runtime, this value is typically set much higher than the status push interval. However, it must be strictly less than `LLUMNIX_CMS_EXPIRED_TIME` to prevent the instance from being falsely marked as dead.
        
*   `LLUMNIX_CMS_EXPIRED_TIME`
    
    *   Description: The Time-To-Live (TTL) for the instance's metadata and status in the CMS. If the CMS does not receive an update within this timeframe, it will consider the Llumlet instance as "dead" or  disconnected and automatically remove it from the scheduling pool.
        
    *   Impact: Acts as a heartbeat timeout threshold for fault tolerance. 
        
    *   Note: The CMS expired time must be strictly greater than both the status push interval and the metadata push interval.
        
*   `LLUMNIX_INSTANCE_UPDATE_STEPS`
    
    *   Description: Determines the frequency of status updates based on the number of inference engine scheduling steps, defaults to `1`.
        
    *   Impact: If set to `1`, the engine updates its status after every single inference step. Tying status updates to inference steps can introduce CPU overhead, potentially degrading ITL (Inter-Token Latency). 
        
*   `LLUMNIX_UPDATE_INSTANCE_STATUS_MODE`
    
    *   Description: Defines the architecture for how Llumlet retrieves status metrics from the underlying inference engine (e.g., vLLM). Must be set to either `PULL` or `PUSH`, defaults to `PUSH`.
        
    *   Impact:
        
        *   `PULL`: Llumlet actively queries the engine at fixed intervals. It is simpler but may block if the engine is busy.
            
        *   `PUSH`: The engine core actively pushes its state into an asynchronous queue whenever a state change occurs. This mode is more efficient, decoupled, and prevents Llumlet from aggressively polling the engine.

## Metrics scope Setup

The following environment variables control what metrics Llumlet collects and reports to the CMS. They are configured in the env section of the vllm container in the engine's LeaderWorkerSet YAML:

```yaml
env:
- name: LLUMNIX_DETAILED_MIG_STATUS
  value: "True"
- name: LLUMNIX_USED_METRICS
  value: "num_requests,kv_cache_usage_ratio_projected"
```
 
*   `LLUMNIX_DETAILED_MIG_STATUS`:
    
    *   Description: A boolean flag (`True` / `False`) that dictates whether Llumlet should calculate and report fine-grained migration metrics (token counts and KV cache usage ratios) to CMS, rather than just basic request counts.
        
    *   Impact & Behavior: This variable does not trigger migrations; instead, it acts as a detailed visibility and local safeguard mechanism.
        
        *   Enhanced Status Reporting: When `True`, the `InstanceStatus` pushed to the CMS is enriched with detailed capacity fields (e.g., `num_available_migrate_in_tokens`, `available_kv_cache_usage_ratio_migrate_in`). 
            
        *   Local Execution Safeguard: When receiving a migration command from the scheduler, Llumlet performs a strict local pre-check. If detailed status is enabled, it will actively reject the outgoing migration (raising a `NotEnoughSlotsError`) if the parameters exceed the local token or KV cache migration-out limits.
            
        *   When set to `False`: Llumlet minimizes computational overhead by only reporting and validating coarse-grained _Request Slots_ (`num_reqs`).
                    
*   `LLUMNIX_USED_METRICS`
    
    *   Description: Specifies the exact list of high-level metrics that Llumlet should collect and report. The value should be a string of metric names separated by commas (`,`) or semicolons (`;`).
        
    *   Impact & Behavior: This configuration acts as a performance optimization filter (Status Mask). Llumlet intelligently maps your requested metrics to their underlying low-level engine states, generating a boolean mask. It only serializes and transmits the required fields, significantly reducing the network payload.
        
    *   Supported Metrics Reference：Below is the definitive list of valid metric keys you can use in the `LLUMNIX_USED_METRICS` environment variable. You can combine them based on your scheduling algorithms
        

	| Metric Key | Description |
	| --- | --- |
	| `all` | Bypasses the mask and collects every available status field. |
	| `kv_cache_usage_ratio_projected` | Calculates the KV cache usage by analyzing currently used tokens. |
	| `decode_batch_size` | Tracks the total number of requests currently in the decode phase. |
	| `adaptive_decode_batch_size` | Similar to `decode_batch_size`, but typically utilized by adaptive prefill-decode schedule mode. |
	| `num_waiting_requests` | A simple counter for requests that are queued and waiting to be processed. |
	| `num_requests` | The total aggregate of all requests in the instance. |
	| `all_prefills_tokens_num` | The total number of tokens currently in the prefill phase. |
	| `cache_aware_all_prefills_tokens_num` | Tracks prefill tokens while accounting for potential Prefix/KV cache hits. |
	| `all_decodes_tokens_num` | The total number of tokens currently being generated in the decode phase.|
	| `kv_cache_hit_len` | Tracks the length of prefix cache hits to measure KV cache reuse efficiency. |

## Migration Setup

### vLLM startup parameters

To successfully enable and manage KV Cache migration, precise parameter configuration is crucial. The combination of these settings dictates how the KV Cache transfer backend operates, particularly concerning its support for migration functionality.

#### Environment Variable ：

Set the following in the env section:

```yaml
env:
- name: VLLM_ENABLE_LLUMNIX
  value: "1"          # Enable Llumnix integration
- name: LLUMNIX_ENABLE_MIGRATION
  value: "1"          # Enable KV Cache migration (set to "0" to disable)
```

#### KV-transfer-config

When the connector\_type is set to `HybridConnector`, the --kv-transfer-config template is:

```bash
--kv-transfer-congig {
"kv_connector": "HybridConnector",
"kv_role": "{kv_role}",
"kv_connector_extra_config": {
	"backend": "{backend}",
	"naming_url": "file:{naming_dir}",
	"kvt_inst_id": "{tag}",
	"rpc_port": "{kvt_port}",
	}
}
```

Key Parameter Descriptions:

* `kv_connector`: Fixed to `"HybridConnector"`.

* `kv_role`: Represent pd disaggragation phase
	* If the instance's `role` is `"prefill"`: `backend` is set to `"kvt"`. In this mode, the connector primarily performs only pd disaggragation operations.

	* If the instance's `role` is `"decode"`: `backend` can be set to `"kvt+migration"`. This specific setting explicitly enables KV Cache migration.

*  `kv_connector_extra_config.naming_url`: Specifies a public file or service that enables the connector to discover the URLs of other instances in the system. This allows for inter-instance communication and coordination.

*  `kv_connector_extra_config.kvt_inst_id`: Sets a unique identifier for the KV Transfer instance.

*  `kv_connector_extra_config.rpc_port`: Defines the RPC communication port for KV Transfer operations. This port is typically derived by adding an offset (e.g., `KVT_PORT_OFFSET`) to the server's base port.

When the connector\_type is set to `HybridConnector` , the `--kv-transfer-config` template is:

```bash
--kv-transfer-congig {
"kv_connector": "MooncakeConnector",
"kv_role": "{kv_role}",
"kv_connector_module_path": "mooncake.mooncake_connector_v1"
}
```

Key Parameter Descriptions:

* `kv_connector`: Fixed to `"MooncakeConnector"`. This explicitly designates the use of the Mooncake Connector for KV Cache operations.

* `kv_role`: Same as above.

* `kv_connector_module_path`: Fixed to `"mooncake.mooncake_connector_v1"`. This parameter defines the import path for the Mooncake connector's implementation module.

### Trigger methods

In the Llumix system, migration is managed by the Llumlet component through gRPC interfaces. You can trigger a migration either by using the provided automated test script or by manually calling the gRPC API.

Python Client Example

```python
import asyncio
import grpc
from llumnix.llumlet.proto import llumlet_server_pb2_grpc, llumlet_server_pb2
from llumnix.utils import MigrationType, RequestMigrationPolicy

async def trigger_manual_migration():
    # 1. Connect to the Source Llumlet
    address = f"{llumlet_host}:{llumlet_port}"
    channel = grpc.aio.insecure_channel(address)
    stub = llumlet_server_pb2_grpc.LlumletStub(channel)

    # 2. Construct the Migration Request
    request = llumlet_server_pb2.MigrateRequest(
        src_engine_id="engine_01",
        dst_engine_id="engine_02",
        dst_engine_ip="10.0.0.2",   # Destination IP
        dst_engine_port=29876,      # Destination KV Transfer Port
        migration_req_policy=RequestMigrationPolicy.SR,
        migration_type=MigrationType.NUM_REQ,
        num_reqs=1,                 # Migrate 1 active request
        trigger_policy="NEUTRAL_LOAD"
    )

    # 3. Execute
    try:
        response = await stub.Migrate(request)
        print(f"Success: {response.success}, Message: {response.message}")
    except grpc.aio.AioRpcError as e:
        print(f"RPC Failed: {e.code()} - {e.details()}")

asyncio.run(trigger_manual_migration())

```

  
Where `dst_engine_port` represents the target engine's KV Transfer (KVT) port (metadata field `kvt_port`), which can be obtained from the destination engine's metadata.