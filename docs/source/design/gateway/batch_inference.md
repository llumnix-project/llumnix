# Batch API

Llumnix LLM Gateway supports OpenAI's [Batch API](https://platform.openai.com/docs/guides/batch) (i.e., batch inference), following the OpenAI interface specification. Batch inference processes large volumes of inference requests asynchronously, making it ideal for running during idle periods to fully utilize machine resources.

The workflow is as follows:

1. Upload a file. Upload a JSONL file containing multiple inference requests.
2. Create a batch task. Create a batch task using the uploaded input file. LLM Gateway automatically shards the input file and processes multiple shards in parallel, sending inference requests to the inference service and recording the results.
3. Wait for the batch task to complete. You can check the task status through the query API.
4. Download the results. Once the task is completed, an output file and an error file (if any) are generated. You can retrieve the results through the file API.

## Architecture

```{mermaid}
graph LR
    Client[Client]

    subgraph Gateway["LLM Gateway"]
        FileAPI[File API]
        BatchAPI[Batch API]
        Reactor[Shard & Parallel Dispatch]
        InferenceAPI[Inference API]
    end

    OSS[(OSS)]
    Redis[(Redis)]
    Scheduler[Scheduler]
    Engine1[Inference Engine]
    Engine2[Inference Engine]

    Client -->|"1. Upload file"| FileAPI
    FileAPI -->|store| OSS
    Client -->|"2. Create batch"| BatchAPI
    BatchAPI -->|read/write metadata| Redis
    BatchAPI --> Reactor
    Reactor -->|"3. HTTP loopback<br/>(127.0.0.1)"| InferenceAPI
    InferenceAPI --> Scheduler
    Scheduler --> Engine1
    Scheduler --> Engine2
    Reactor -->|"4. Write results"| OSS
    Client -->|"5. Download results"| FileAPI
    FileAPI -->|read| OSS
```

## Service Deployment Example

To enable Batch API support in LLM Gateway, you need:
- An **OSS bucket** for storing batch files (input, output, and temporary files), with valid access credentials.
- A **Redis instance** for storing batch metadata.
- A **scheduler** and **inference engine** (e.g., vLLM) deployed alongside the gateway.

A complete deployment example is provided under `deploy/batch-api/`. It consists of the following components:

| Component | Description |
| --- | --- |
| `redis.yaml` | Redis instance for service discovery and batch metadata storage. |
| `scheduler.yaml` | Scheduler for load-balanced request routing. |
| `neutral.yaml` | vLLM inference engine (the actual LLM serving backend). |
| `gateway.yaml` | LLM Gateway with Batch API enabled. |

### Gateway Configuration (gateway.yaml)

The gateway is the core component for Batch API. Below are the key configuration parameters. Fields marked `# required` must be filled in by the user.

```yaml
args:
  - "--port"
  - "8089"
  - "--scheduler-endpoints"
  - "scheduler:8088"
  - "--llm-backend-discovery"
  - "redis"
  - "--scheduler-discovery"
  - "endpoints"
  - "--discovery-redis-host"
  - "redis"
  - "--discovery-redis-port"
  - "6379"
  - "--scheduling-policy"
  - "load-balance"
  - "--tokenizer-path"
  - "/tokenizers/Qwen/Qwen3-30B-A3B-FP8"
  - "--max-model-len"
  - "40960"
  - "--enable-full-mode-scheduling=false"
  # Batch API specific configuration
  - "--batch-oss-path"
  - ""              # required: OSS bucket path, e.g. "my-bucket/batch-prefix"
  - "--batch-oss-endpoint"
  - ""              # required: OSS endpoint, e.g. "oss-cn-beijing.aliyuncs.com"
  - "--batch-redis-addrs"
  - "redis:6379"    # required: Redis address for batch metadata
  - "--batch-redis-username"
  - ""              # required: Redis username (empty string if no auth)
  - "--batch-redis-password"
  - ""              # required: Redis password (empty string if no auth)
env:
  - name: "OSS_ACCESS_KEY_ID"
    value: ""       # required: your OSS access key ID
  - name: "OSS_ACCESS_KEY_SECRET"
    value: ""       # required: your OSS access key secret
```

### Inference Engine Configuration (neutral.yaml)

The inference engine runs the actual model. Make sure the model name matches the gateway's `--tokenizer-path`:

```yaml
CUDA_VISIBLE_DEVICES=$i vllm serve \
  Qwen/Qwen3-30B-A3B-FP8 \              # model name
  --port $PORT \
  --trust-remote-code \
  --gpu-memory-utilization 0.7 \
  ...
```

### Deploying

Deploy all components using kustomize:

```bash
cd deploy/

bash group_deploy.sh batch-api-test batch-api
```

## Deployment Parameters

### Batch API Parameters (Gateway)

| Parameter | Required | Default | Description |
| --- | --- | --- | --- |
| `--batch-oss-path` | Yes | - | OSS bucket path for storing batch files (input, output, and temporary files). |
| `--batch-oss-endpoint` | Yes | - | OSS endpoint URL. |
| `--batch-redis-addrs` | Yes | `redis.roles:10000` | Redis address for batch metadata storage. |
| `--batch-redis-username` | Yes | `default` | Redis username. |
| `--batch-redis-password` | Yes | `default` | Redis password. |
| `--batch-parallel` | No | 8 | Concurrency for processing batch shards. |
| `--batch-lines-per-shard` | No | 1000 | Maximum number of lines per shard. |
| `--batch-request-timeout` | No | 3m | Timeout for a single request (Go duration format). |
| `--batch-request-retry-times` | No | 3 | Retry count for a single request. |
| `--max-model-len` | No | 0 | Override the model max length from `tokenizer_config.json`. Set this to match the inference engine's actual `max_model_len` (which may be lower due to GPU memory constraints). 0 means use the value from `tokenizer_config.json`. |

### Environment Variables (Gateway)

| Variable | Required | Description |
| --- | --- | --- |
| `OSS_ACCESS_KEY_ID` | Yes | Access key ID for OSS authentication. |
| `OSS_ACCESS_KEY_SECRET` | Yes | Access key secret for OSS authentication. |


## Usage Guide
Before using the Batch API, you need to have the service endpoint URL, access credentials, and the input file for the batch task ready.

Refer to the OpenAI batch usage workflow:

### 1. Upload the input file via the File API
```bash
curl -s "<YOUR_GATEWAY_URL>/v1/files" \
  -H "Authorization: Bearer <YOUR_TOKEN>" \
  -F purpose="batch" \
  -F file="@input.jsonl"
```

Example input.jsonl:

```json
{"custom_id": "request-1", "method": "POST", "url": "/v1/chat/completions", "body": {"model": "Qwen3-VL-2B-Instruct", "messages": [{"role": "system", "content": "You are a helpful assistant."},{"role": "user", "content": "Hello world!"}],"max_tokens": 1000}}
...
```

**Response Example**

```json
{
  "bytes": 564813,
  "created_at": 1765868482,
  "filename": "input.jsonl",
  "id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
  "object": "file",
  "purpose": "batch"
}
```

The file will be stored at `oss://your-bucket/path/to/prefix/batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b`

### 2. Create a Batch using the file ID
```bash
curl -s "<YOUR_GATEWAY_URL>/v1/batches" \
  -H "Authorization: Bearer <YOUR_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "input_file_id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
    "endpoint": "/v1/chat/completions",
    "completion_window": "24h"
  }'
```

**Response Example**

```json
{
  "id": "batch_5f968571-b0b6-413f-a2a8-69bf750112af",
  "object": "batch",
  "endpoint": "/v1/chat/completions",
  "model": "",
  "errors": null,
  "input_file_id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
  "completion_window": "24h",
  "status": "pending",
  "output_file_id": null,
  "error_file_id": null,
  "created_at": 1765868672,
  "in_progress_at": null,
  "expires_at": 1765955072,
  "finalizing_at": null,
  "completed_at": null,
  "failed_at": null,
  "expired_at": null,
  "cancelling_at": null,
  "cancelled_at": null,
  "request_counts": {
    "total": 0,
    "completed": 0,
    "failed": 0
  },
  "usage": null,
  "metadata": null
}
```

### 3. Query Batch Status
```bash
curl -s "<YOUR_GATEWAY_URL>/v1/batches/batch_98d4d6e3-c7ec-4aa9-969e-fb8531059523" \
  -H "Authorization: Bearer <YOUR_TOKEN>" \
  -H "Content-Type: application/json"
```

**Response Example**

```json
{
  "id": "batch_5f968571-b0b6-413f-a2a8-69bf750112af",
  "object": "batch",
  "endpoint": "/v1/chat/completions",
  "model": "",
  "errors": null,
  "input_file_id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
  "completion_window": "24h",
  "status": "completed",
  "output_file_id": "batch_output_20bafd19-6d73-4b61-a770-ebcb377d286d",
  "error_file_id": null,
  "created_at": 1765868672,
  "in_progress_at": 1765868674,
  "expires_at": 1765955072,
  "finalizing_at": 1765868740,
  "completed_at": 1765868741,
  "failed_at": null,
  "expired_at": null,
  "cancelling_at": null,
  "cancelled_at": null,
  "request_counts": {
    "total": 2160,
    "completed": 2160,
    "failed": 0
  },
  "usage": null,
  "metadata": {}
}
```

### 4. Retrieve Batch Results
When the batch status is `completed`, you can retrieve the results via the File API. The file ID is the batch's `output_file_id`, and the actual location is `oss://your-bucket/path/to/prefix/batch_output_20bafd19-6d73-4b61-a770-ebcb377d286d`

```bash
curl -s "<YOUR_GATEWAY_URL>/v1/files/batch_output_20bafd19-6d73-4b61-a770-ebcb377d286d/content" \
  -H "Authorization: Bearer <YOUR_TOKEN>" > output.jsonl
```

Example output.jsonl content:

```json
{"id":"batch_5f968571-b0b6-413f-a2a8-69bf750112af","custom_id":"request-1","response":{"status_code":200,"request_id":"282f82b5-577a-44f3-9bf7-a17522ac7d1c","body":{"id":"chatcmpl-282f82b5-577a-44f3-9bf7-a17522ac7d1c","object":"chat.completion","created":1765868675,"model":"Qwen3-VL-2B-Instruct","choices":[{"index":0,"message":{"role":"assistant","content":"Hello! How can I assist you today?","refusal":null,"annotations":null,"audio":null,"function_call":null,"tool_calls":[],"reasoning_content":null},"logprobs":null,"finish_reason":"stop","stop_reason":null,"token_ids":null}],"service_tier":null,"system_fingerprint":null,"usage":{"prompt_tokens":22,"total_tokens":32,"completion_tokens":10,"prompt_tokens_details":null},"prompt_logprobs":null,"prompt_token_ids":null,"kv_transfer_params":null}}}
...
```

## Batch Task Statuses
### Status List
| **Status** | **Description** | **Phase** | **Actions** |
| --- | --- | --- | --- |
| pending | Pending: Waiting to be processed. | Preparation | Can be cancelled. |
| validating | Validating: Checking the input file format and validating content parameters. | Preparation | Can be cancelled (if validation fails, the task transitions to `failed`). |
| failed | Failed: Input file or task parameter validation failed; the task cannot start processing. | Terminal (Failure) | No actions available. |
| in_progress | In Progress: The task has been picked up and is executing batch requests. | Processing | Can be cancelled. |
| finalizing | Finalizing: All shards have been processed; waiting for aggregation. | Terminal (Processing) | No actions available. |
| finalize | Finalize: Aggregating results and generating output files. | Terminal (Completed) | No actions available. |
| completed | Completed: All requests have been processed; the output file is available for download. | Terminal (Success) | No actions available. |
| cancelling | Cancelling: The user has requested cancellation; the system is stopping in-progress requests. | Terminal (Cancelling) | No actions available. |
| cancelled | Cancelled: The task was successfully cancelled before completion. | Terminal (Cancelled) | No actions available. |
| expired | Expired: The task did not complete within the specified time window; the system has automatically terminated it. | Terminal (Failure) | No actions available. |

## API Reference
The LLM Gateway Batch APIs are largely consistent with the OpenAI specification. The following APIs are supported:

### Batch
#### Batch Object
| **Parameter** | **Type** | **Description** |
| --- | --- | --- |
| id | string | Unique identifier for the batch task. |
| object | string | The type of the object, always "batch". |
| endpoint | string | The API endpoint to be called by the batch task (e.g., /v1/chat/completions). Currently supported endpoints:<br/>+ /v1/responses<br/>+ /v1/chat/completions<br/>+ /v1/embeddings<br/>+ /v1/completions |
| model | string | The model used in batch requests. Currently empty. |
| errors | object | Error details when the task fails (only present when status is `failed`). |
| errors.data | array | Error information generated during validation. |
| errors.data[].code | string | Error code. |
| errors.data[].line | int | Not yet supported; always 0. |
| errors.data[].message | string | Error message. |
| errors.data[].param | string | Not yet supported; always empty. |
| input_file_id | string | The ID of the file containing input requests (JSONL format). |
| completion_window | string | The time window for task completion (e.g., 24h). |
| status | string | The current status of the batch task (e.g., validating, in_progress, completed). |
| output_file_id | string | The ID of the file containing processed responses (only present when the task completes successfully). |
| error_file_id | string | The ID of the file containing failed requests. |
| created_at | integer | The time the task was created (Unix timestamp in seconds). |
| in_progress_at | integer | The time the task started processing (Unix timestamp in seconds). |
| expires_at | integer | The configured task expiration time; tasks not completed by this time will expire (Unix timestamp in seconds). |
| finalizing_at | integer | The time the task entered the finalizing phase (Unix timestamp in seconds). |
| completed_at | integer | The time the task completed successfully (Unix timestamp in seconds). |
| failed_at | integer | The time the task failed (Unix timestamp in seconds). |
| expired_at | integer | The time the task expired (Unix timestamp in seconds). |
| cancelling_at | integer | The time the task entered the cancelling phase (Unix timestamp in seconds). |
| cancelled_at | integer | The time the task was actually cancelled (Unix timestamp in seconds). |
| request_counts | object | Statistics on the number of requests in the task (total, completed, failed, etc.). |
| request_counts.total | integer | Total number of requests. |
| request_counts.completed | integer | Number of successful requests. |
| request_counts.failed | integer | Number of failed requests. |
| usage | object | Not yet supported. Usage information consumed by the task (e.g., token counts). |
| metadata | map | Optional user-provided key-value metadata. |


#### Create Batch: POST /v1/batches
**Request Example**

```bash
curl -s "<YOUR_GATEWAY_URL>/v1/batches" \
  -H "Authorization: Bearer <YOUR_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "input_file_id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
    "endpoint": "/v1/chat/completions",
    "completion_window": "24h"
  }'
```

**Response Example**

```json
{
  "id": "batch_5f968571-b0b6-413f-a2a8-69bf750112af",
  "object": "batch",
  "endpoint": "/v1/chat/completions",
  "model": "",
  "errors": null,
  "input_file_id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
  "completion_window": "24h",
  "status": "completed",
  "output_file_id": "batch_output_20bafd19-6d73-4b61-a770-ebcb377d286d",
  "error_file_id": null,
  "created_at": 1765868672,
  "in_progress_at": 1765868674,
  "expires_at": 1765955072,
  "finalizing_at": 1765868740,
  "completed_at": 1765868741,
  "failed_at": null,
  "expired_at": null,
  "cancelling_at": null,
  "cancelled_at": null,
  "request_counts": {
    "total": 2160,
    "completed": 2160,
    "failed": 0
  },
  "usage": null,
  "metadata": {}
}
```

**Request Parameters**

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| input_file_id | string | Yes | The ID of an uploaded file. The file must be a valid JSONL format, containing at most 50,000 requests and with a maximum file size of 200 MB. |
| endpoint | string | Yes | The API endpoint for the batch requests. |
| completion_window | string | No | The time window for batch task completion. Currently only `24h` is supported. |
| metadata | map | No | Metadata for the batch task. Supports up to 16 key-value pairs, where keys can be up to 16 characters and values up to 512 characters. |
| output_expires_after | object | No | Not yet supported; all files will not be automatically expired or cleaned up. Expiration policy for the batch output or error files. |


**Response Parameters**

Returns the created Batch object.

#### Get Batch: GET /v1/batches/{batch_id}
**Request Example**

```bash
curl -s "<YOUR_GATEWAY_URL>/v1/batches/batch_98d4d6e3-c7ec-4aa9-969e-fb8531059523" \
  -H "Authorization: Bearer <YOUR_TOKEN>" \
  -H "Content-Type: application/json"
```

**Response Example**

```json
{
  "id": "batch_5f968571-b0b6-413f-a2a8-69bf750112af",
  "object": "batch",
  "endpoint": "/v1/chat/completions",
  "model": "",
  "errors": null,
  "input_file_id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
  "completion_window": "24h",
  "status": "completed",
  "output_file_id": "batch_output_20bafd19-6d73-4b61-a770-ebcb377d286d",
  "error_file_id": null,
  "created_at": 1765868672,
  "in_progress_at": 1765868674,
  "expires_at": 1765955072,
  "finalizing_at": 1765868740,
  "completed_at": 1765868741,
  "failed_at": null,
  "expired_at": null,
  "cancelling_at": null,
  "cancelled_at": null,
  "request_counts": {
    "total": 2160,
    "completed": 2160,
    "failed": 0
  },
  "usage": null,
  "metadata": {}
}
```

**Request Parameters**

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| batch_id | string | Yes | The ID of the batch task to query. |


**Response Parameters**

Returns the queried Batch object.

#### Cancel Batch: POST /v1/batches/{batch_id}/cancel
Only tasks in `validating` or `in_progress` status can be cancelled.

**Request Example**

```bash
curl -s "<YOUR_GATEWAY_URL>/v1/batches/batch_98d4d6e3-c7ec-4aa9-969e-fb8531059523/cancel" \
  -H "Authorization: Bearer <YOUR_TOKEN>"
  -X POST
```

**Response Example**

```json
{
  "id": "batch_93559b00-67bf-4615-895e-16fd30196ecb",
  "object": "batch",
  "endpoint": "/v1/chat/completions",
  "model": "",
  "errors": null,
  "input_file_id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
  "completion_window": "24h",
  "status": "cancelling",
  "output_file_id": null,
  "error_file_id": null,
  "created_at": 1765870619,
  "in_progress_at": 1765870620,
  "expires_at": 1765957019,
  "finalizing_at": null,
  "completed_at": null,
  "failed_at": null,
  "expired_at": null,
  "cancelling_at": 1765870629,
  "cancelled_at": null,
  "request_counts": {
    "total": 2160,
    "completed": 0,
    "failed": 0
  },
  "usage": null,
  "metadata": {}
}
```

**Request Parameters**

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| batch_id | string | Yes | The ID of the batch task to cancel. |


**Response Parameters**

Returns the cancelled Batch object.

#### List Batches: GET /v1/batches
**Request Example**

```bash
curl -s "<YOUR_GATEWAY_URL>/v1/batches" \
  -H "Authorization: Bearer <YOUR_TOKEN>"
```

**Response Example**

```json
{
  "data": [
    {
      "id": "batch_5f968571-b0b6-413f-a2a8-69bf750112af",
      "object": "batch",
      "endpoint": "/v1/chat/completions",
      "model": "",
      "errors": null,
      "input_file_id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
      "completion_window": "24h",
      "status": "completed",
      "output_file_id": "batch_output_20bafd19-6d73-4b61-a770-ebcb377d286d",
      "error_file_id": null,
      "created_at": 1765868672,
      "in_progress_at": 1765868674,
      "expires_at": 1765955072,
      "finalizing_at": 1765868740,
      "completed_at": 1765868741,
      "failed_at": null,
      "expired_at": null,
      "cancelling_at": null,
      "cancelled_at": null,
      "request_counts": {
        "total": 2160,
        "completed": 2160,
        "failed": 0
      },
      "usage": null,
      "metadata": {}
    }
  ],
  "first_id": "batch_5f968571-b0b6-413f-a2a8-69bf750112af",
  "has_more": false,
  "last_id": "batch_5f968571-b0b6-413f-a2a8-69bf750112af",
  "object": "list"
}
```

**Request Parameters**

| **Parameter** | **Type** | **Required** | **Description** |
| --- | --- | --- | --- |
| limit | integer | No | Maximum number of results to return. |
| after | string | No | The last batch_id from the previous request. |


**Response Parameters**

| **Parameter** | **Type** | **Description** |
| --- | --- | --- |
| object | string | Always "list". |
| data | array | A list of Batch objects, sorted in reverse chronological order (newest first). |
| first_id | string | The first batch_id in this response. |
| last_id | string | The last batch_id in this response. |
| has_more | bool | Whether there are more results after the last batch_id. |


### File
#### File Object
| **Parameter** | **Type** | **Description** |
| --- | --- | --- |
| id | string | Unique identifier for the file. |
| object | string | The type of the object, always "file". |
| bytes | integer | The size of the file in bytes. |
| created_at | integer | The time the file was created (Unix timestamp in seconds). |
| expires_at | integer | The time the file will expire (Unix timestamp in seconds). |
| filename | string | The filename specified by the user during upload. |
| purpose | string | The purpose of the file, always "batch". |


**Input File Format**

| **Parameter** | **Type** | **Required** | **Description** |
| --- | --- | --- | --- |
| custom_id | string | Yes | A custom request ID; must be unique across all requests. |
| method | string | Yes | The HTTP method for the inference request, typically POST. |
| url | string | Yes | The endpoint for the inference request. Currently must match the endpoint specified when creating the batch task. |
| body | object | Yes | The request body for the inference request; it is forwarded to the inference service without modification. |


**Output File Format**

Note: The order of responses in the output file is not guaranteed to match the order of requests in the input file. Use `custom_id` to match each request with its response.

| Parameter | Type | Description |
| --- | --- | --- |
| id | string | The batch_id. |
| custom_id | string | The custom request ID. |
| response | object | The response for the request. |
| response.status_code | int | The HTTP status code returned by the inference service. |
| response.request_id | string | The inference request ID. |
| response.body | object | The response body returned by the inference service. |
| error | object | Error information; present when an error occurs. Possible causes include inference service unavailability, invalid request format, or LLM Gateway internal errors. |
| error.code | string | The specific error code. |
| error.message | string | The specific error message. |


#### Upload File: POST /v1/files
**Request Example**

```bash
curl -s "<YOUR_GATEWAY_URL>/v1/files" \
  -H "Authorization: Bearer <YOUR_TOKEN>" \
  -F purpose="batch" \
  -F file="@input.jsonl"
```

**Response Example**

```json
{
  "bytes": 564813,
  "created_at": 1765868482,
  "filename": "input.jsonl",
  "id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
  "object": "file",
  "purpose": "batch"
}
```

**Request Parameters**

| **Parameter** | **Type** | **Required** | **Description** |
| --- | --- | --- | --- |
| file | File | Yes | The file to upload (multipart/form-data). |
| purpose | string | Yes | The purpose of the file; must be "batch" (multipart/form-data). |


**Response Parameters**

Returns the created File object.

#### List Files: GET /v1/files
**Request Example**

```bash
curl -s "<YOUR_GATEWAY_URL>/v1/files" \
  -H "Authorization: Bearer <YOUR_TOKEN>" \
  -H "Content-Type: application/json"
```

**Response Example**

```json
{
  "data": [
    {
      "id": "batch_output_20bafd19-6d73-4b61-a770-ebcb377d286d",
      "object": "file",
      "bytes": 1740241,
      "created_at": 1765868741,
      "expires_at": 0,
      "filename": "output.jsonl",
      "purpose": "batch_output"
    },
    {
      "id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
      "object": "file",
      "bytes": 564813,
      "created_at": 1765868482,
      "expires_at": 0,
      "filename": "1.jsonl",
      "purpose": "batch"
    }
  ],
  "first_id": "batch_output_20bafd19-6d73-4b61-a770-ebcb377d286d",
  "has_more": false,
  "last_id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
  "object": "list"
}
```

**Request Parameters**

None.

**Response Parameters**

| **Parameter** | **Type** | **Description** |
| --- | --- | --- |
| object | string | Always "list". |
| data | array | A list of File objects, sorted in reverse chronological order (newest first). |
| first_id | string | The first file_id in this response. |
| last_id | string | The last file_id in this response. |


#### Get File: GET /v1/files/{file_id}
**Request Example**

```bash
curl -s "<YOUR_GATEWAY_URL>/v1/files/batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b" \
  -H "Authorization: Bearer <YOUR_TOKEN>"
```

**Response Example**

```json
{
  "id": "batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b",
  "object": "file",
  "bytes": 564813,
  "created_at": 1765868482,
  "expires_at": 0,
  "filename": "input.jsonl",
  "purpose": "batch"
}
```

**Request Parameters**

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| file_id | string | Yes | The ID of the file to query. |


**Response Parameters**

Returns the queried File object.

#### Delete File: DELETE /v1/files/{file_id}
Note: This only deletes the file metadata. The actual file on OSS will not be deleted.

**Request Example**

```bash
curl -s "<YOUR_GATEWAY_URL>/v1/files/batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b" \
  -H "Authorization: Bearer <YOUR_TOKEN>"
  -X DELETE
```

**Response Example**

```json
{
  "deleted": true,
  "id": "batch_output_a31a8f26-3abe-4522-9e3f-5c845fa56af7",
  "object": "file"
}
```

**Request Parameters**

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| file_id | string | Yes | The ID of the file to delete. |


**Response Parameters**

| **Parameter** | **Type** | **Description** |
| --- | --- | --- |
| id | string | The file ID. |
| object | string | The file type, always "file". |
| deleted | bool | true |


#### Get File Content: GET /v1/files/{file_id}/content
**Request Example**

```bash
curl -s "<YOUR_GATEWAY_URL>/v1/files/batch_input_11fb297e-653d-47cf-bb6a-a80209dc562b/content" \
  -H "Authorization: Bearer <YOUR_TOKEN>"
```

**Response Example**

```json
{"id":"batch_5f968571-b0b6-413f-a2a8-69bf750112af","custom_id":"request-1","response":{"status_code":200,"request_id":"282f82b5-577a-44f3-9bf7-a17522ac7d1c","body":{"id":"chatcmpl-282f82b5-577a-44f3-9bf7-a17522ac7d1c","object":"chat.completion","created":1765868675,"model":"Qwen3-VL-2B-Instruct","choices":[{"index":0,"message":{"role":"assistant","content":"Hello! How can I assist you today?","refusal":null,"annotations":null,"audio":null,"function_call":null,"tool_calls":[],"reasoning_content":null},"logprobs":null,"finish_reason":"stop","stop_reason":null,"token_ids":null}],"service_tier":null,"system_fingerprint":null,"usage":{"prompt_tokens":22,"total_tokens":32,"completion_tokens":10,"prompt_tokens_details":null},"prompt_logprobs":null,"prompt_token_ids":null,"kv_transfer_params":null}}}
...
```

**Request Parameters**

| Parameter | Type | Required | Description |
| --- | --- | --- | --- |
| file_id | string | Yes | The ID of the file whose content to retrieve. |


**Response Parameters**

Returns the file content.
