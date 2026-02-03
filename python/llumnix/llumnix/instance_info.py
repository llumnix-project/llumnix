from dataclasses import dataclass, field
from enum import Enum
from typing import List


class InstanceType(str, Enum):
    NEUTRAL = "neutral"
    PREFILL = "prefill"
    DECODE = "decode"


class BackendType(str, Enum):
    VLLM = "vLLM"
    VLLM_V1 = "vLLM v1"
    BLADELLM = "BladeLLM"
    SGLANG = "SGLang"

class ConnectorType(str, Enum):
    MOONCAKE = "mooncake"
    HYBRID = "hybrid"

@dataclass
class InstanceMetaData:
    """
    Generated when instance is created
    """
    # engine meta
    instance_id: str = ""
    instance_type: InstanceType = InstanceType.NEUTRAL  # type: ignore
    backend_type: BackendType = ""

    # hardware meta
    ip: str = ""
    ip_kvs: str = ""
    api_server_port: int = -1
    llumlet_port: int = -1
    kvt_port: int = -1
    node_id: str = ""

    # parallel meta
    data_parallel_size: int = -1
    tensor_parallel_size: int = -1
    unit_id: str = ""
    dp_rank: int = -1
    ep_rank: int = -1
    dp_local_rank: int = -1

    utc_create: float = -1
    utc_update: float = -1

    ip_kvt: str = ""
    block_size: int = -1

    max_num_batched_tokens: int = -1

    def __hash__(self):
        return hash(self.instance_id)

    def __repr__(self):
        return f"InstanceInfo(instance_id={self.instance_id}, instance_type={self.instance_type})"


@dataclass
class InstanceStatus:
    """
    Generated when instance is created
    """
    instance_id: str = ""

    # step loop status
    schedulable: bool = True
    timestamp_ms: int = None
    step_id: int = None
    update_id: int = None

    # status for updating instance status local account
    recent_waiting_requests: List[str] = field(default_factory=list)
    waiting_requests: List[str] = field(default_factory=list)

    # num request status
    hybrid_scheduler_waiting_to_decode_requests_num: int = 0
    scheduler_waiting_to_decode_requests_num: int = 0
    num_waiting_requests: int = 0
    scheduler_running_to_decode_requests_num: int = 0
    num_running_requests: int = 0
    num_loading_requests: int = 0

    # num tokens status from kv manager
    num_total_gpu_tokens: int = 0
    num_used_gpu_tokens: int = 0

    # num tokens status from scheduler and hybrid scheduler
    num_uncomputed_tokens_hybrid_scheduler_waiting_prefills: int = 0
    num_unallocated_tokens_hybrid_scheduler_waiting_decodes: int = 0
    hybrid_scheduler_waiting_to_decode_tokens_num: int = 0
    num_uncomputed_tokens_scheduler_waiting_prefills: int = 0
    scheduler_waiting_to_decode_tokens_num: int = 0
    num_uncomputed_tokens_scheduler_running_prefills: int = 0
    num_unallocated_tokens_scheduler_running_prefills: int = 0
    scheduler_running_to_decode_tokens_num: int = 0
    num_tokens_loading_requests: int = 0
    num_uncomputed_tokens_all_waiting_prefills: int = 0

    # migration status
    num_migrate_in_reqs: int = 0
    num_migrate_out_reqs: int = 0
    num_available_migrate_in_slots: int = 0
    num_available_migrate_out_slots: int = 0
    num_migrate_in_tokens: int = 0
    num_migrate_out_tokens: int = 0
    num_available_migrate_in_tokens: int = 0
    num_available_migrate_out_tokens: int = 0
    kv_cache_usage_ratio_migrate_in: float = 0
    kv_cache_usage_ratio_migrate_out: float = 0
    available_kv_cache_usage_ratio_migrate_in: float = 0
    available_kv_cache_usage_ratio_migrate_out: float = 0

    # profiling status
    profiling_id: int = -1
    step_duration: float = 0.0
    num_scheduled_prefill_tokens: int = 0
