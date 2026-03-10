# llumnix/outputs/queue/zmq_client.py, llumnix/outputs/queue/zmq_server.py
ZMQ_RPC_TIMEOUT_SECOND: int = 1
ZMQ_IO_THREADS: int = 8

# llumnix/outputs/queue/zmq_server.py
RPC_SOCKET_LIMIT_CUTOFF: int = 2000
RPC_ZMQ_HWM: int = 0
RETRY_BIND_ADDRESS_INTERVAL: float = 10.0
MAX_BIND_ADDRESS_RETRIES: int = 10

# llumnix/entrypoints/client.py
INIT_CACHED_INSTANCES_INFOS_INTERVAL: float = 10.0
UPDATE_CACHED_INSTANCES_INFOS_INTERVAL: float = 60.0

# llumnix/llumlet/rpc_server.py
RPC_TIMEOUT: float = 5.0  # s

# llumnix/llumlet/llumlet.py
MAX_ENGINE_CLIENT_RETRIES: int = 3
ENGINE_CLIENT_RETRY_INTERVAL: float = 2.0  # seconds
PRESTOP_MIGRATION_SEND_TIMES: int = 2

# llumnix/vllm_utils.py
VLLM_MIGRATION_RETRIES: int = 2
MIGRATION_FRONTEND_TIMEOUT: float = 10.0  # seconds

# llumnix/outputs/forwarder/thread_output_forwarder.py
INIT_LLUMLET_STUB_TIMEOUT: float = 10.0  # seconds
OUTPUT_QUEUE_CLIENT_CLOSE_TIMEOUT: float = 10.0
OUTPUT_FORWARDER_CLOSE_TIMEOUT: float = 10.0

# llumnix/outputs/forwarder/base_output_forwarder.py
GRPC_TIMEOUT: float = 5.0
MIGRATION_FRONTEND_INIT_TIMEOUT: float = 30  # seconds
PRESTOP_TIMEOUT: float = 5  # seconds
