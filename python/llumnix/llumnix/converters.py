from llumnix.cms.proto.cms_pb2 import InstanceMetadata as CMSInstanceMetaData
from llumnix.cms.proto.cms_pb2 import InstanceStatus as CMSInstanceStatus
from llumnix.instance_info import InstanceMetaData as LlumletInstanceMetaData
from llumnix.instance_info import InstanceStatus as LlumletInstanceStatus
from llumnix.logging.logger import init_logger

logger = init_logger(__name__)


def to_cms_metadata(llumlet_metadata: LlumletInstanceMetaData) -> CMSInstanceMetaData:  # type: ignore
    """
    Convert LlumletInstanceMetaData to CMSInstanceMetaData for communication
    """
    cms_metadata = CMSInstanceMetaData()

    # engine meta
    if llumlet_metadata.instance_id:
        cms_metadata.instance_id = llumlet_metadata.instance_id
    if llumlet_metadata.instance_type is not None:
        cms_metadata.instance_type = str(llumlet_metadata.instance_type)
    if llumlet_metadata.backend_type:
        cms_metadata.backend_type = str(llumlet_metadata.backend_type)

    # hareware meta
    if llumlet_metadata.ip:
        cms_metadata.ip = llumlet_metadata.ip
    if llumlet_metadata.ip_kvs:
        cms_metadata.ip_kvs = llumlet_metadata.ip_kvs
    if llumlet_metadata.api_server_port != -1:
        cms_metadata.api_server_port = llumlet_metadata.api_server_port
    if llumlet_metadata.llumlet_port != -1:
        cms_metadata.llumlet_port = llumlet_metadata.llumlet_port
    if llumlet_metadata.kvt_port != -1:
        cms_metadata.kvt_port = llumlet_metadata.kvt_port
    if llumlet_metadata.node_id:
        cms_metadata.node_id = llumlet_metadata.node_id

    # parallel meta
    if llumlet_metadata.data_parallel_size != -1:
        cms_metadata.data_parallel_size = llumlet_metadata.data_parallel_size
    if llumlet_metadata.tensor_parallel_size != -1:
        cms_metadata.tensor_parallel_size = llumlet_metadata.tensor_parallel_size
    if llumlet_metadata.unit_id:
        cms_metadata.unit_id = llumlet_metadata.unit_id
    if llumlet_metadata.dp_rank != -1:
        cms_metadata.dp_rank = llumlet_metadata.dp_rank
    if llumlet_metadata.ep_rank != -1:
        cms_metadata.ep_rank = llumlet_metadata.ep_rank
    if llumlet_metadata.dp_local_rank != -1:
        cms_metadata.dp_local_rank = llumlet_metadata.dp_local_rank

    if llumlet_metadata.utc_create != -1:
        cms_metadata.utc_create = llumlet_metadata.utc_create
    if llumlet_metadata.utc_update != -1:
        cms_metadata.utc_update = llumlet_metadata.utc_update

    if llumlet_metadata.ip_kvt:
        cms_metadata.ip_kvt = llumlet_metadata.ip_kvt

    if llumlet_metadata.block_size != -1:
        cms_metadata.block_size = llumlet_metadata.block_size

    if llumlet_metadata.max_num_batched_tokens != -1:
        cms_metadata.max_num_batched_tokens = llumlet_metadata.max_num_batched_tokens

    return cms_metadata


def to_cms_status(llumlet_status: LlumletInstanceStatus) -> CMSInstanceStatus:  # type: ignore
    """
    Convert LlumletInstanceStatus to CMSInstanceStatus for communication
    """
    cms_status = CMSInstanceStatus()

    cms_status.instance_id = llumlet_status.instance_id

    # step loop status
    cms_status.schedulable = llumlet_status.schedulable
    if llumlet_status.timestamp_ms is not None:
        cms_status.timestamp_ms = llumlet_status.timestamp_ms
    if llumlet_status.step_id is not None:
        cms_status.step_id = llumlet_status.step_id
    if llumlet_status.update_id is not None:
        cms_status.update_id = llumlet_status.update_id

    # status for updating instance status local account
    if llumlet_status.recent_waiting_requests is not None:
        cms_status.ClearField("recent_waiting_requests")
        cms_status.recent_waiting_requests.extend(
            llumlet_status.recent_waiting_requests
        )
    if llumlet_status.waiting_requests is not None:
        cms_status.ClearField("waiting_requests")
        cms_status.waiting_requests.extend(llumlet_status.waiting_requests)

    # num request status
    if llumlet_status.hybrid_scheduler_waiting_to_decode_requests_num is not None:
        cms_status.hybrid_scheduler_waiting_to_decode_requests_num = (
            llumlet_status.hybrid_scheduler_waiting_to_decode_requests_num
        )
    if llumlet_status.scheduler_waiting_to_decode_requests_num is not None:
        cms_status.scheduler_waiting_to_decode_requests_num = (
            llumlet_status.scheduler_waiting_to_decode_requests_num
        )
    if llumlet_status.num_waiting_requests is not None:
        cms_status.num_waiting_requests = llumlet_status.num_waiting_requests
    if llumlet_status.scheduler_running_to_decode_requests_num is not None:
        cms_status.scheduler_running_to_decode_requests_num = (
            llumlet_status.scheduler_running_to_decode_requests_num
        )
    if llumlet_status.num_running_requests is not None:
        cms_status.num_running_requests = llumlet_status.num_running_requests
    if llumlet_status.num_loading_requests is not None:
        cms_status.num_loading_requests = llumlet_status.num_loading_requests

    # num tokens status from kv manager
    if llumlet_status.num_total_gpu_tokens is not None:
        cms_status.num_total_gpu_tokens = llumlet_status.num_total_gpu_tokens
    if llumlet_status.num_used_gpu_tokens is not None:
        cms_status.num_used_gpu_tokens = llumlet_status.num_used_gpu_tokens

    # num tokens status from scheduler and hybrid scheduler
    if (
        llumlet_status.num_uncomputed_tokens_hybrid_scheduler_waiting_prefills
        is not None
    ):
        cms_status.num_uncomputed_tokens_hybrid_scheduler_waiting_prefills = (
            llumlet_status.num_uncomputed_tokens_hybrid_scheduler_waiting_prefills
        )
    if (
        llumlet_status.num_unallocated_tokens_hybrid_scheduler_waiting_decodes
        is not None
    ):
        cms_status.num_unallocated_tokens_hybrid_scheduler_waiting_decodes = (
            llumlet_status.num_unallocated_tokens_hybrid_scheduler_waiting_decodes
        )
    if llumlet_status.hybrid_scheduler_waiting_to_decode_tokens_num is not None:
        cms_status.hybrid_scheduler_waiting_to_decode_tokens_num = (
            llumlet_status.hybrid_scheduler_waiting_to_decode_tokens_num
        )
    if llumlet_status.num_uncomputed_tokens_scheduler_waiting_prefills is not None:
        cms_status.num_uncomputed_tokens_scheduler_waiting_prefills = (
            llumlet_status.num_uncomputed_tokens_scheduler_waiting_prefills
        )
    if llumlet_status.scheduler_waiting_to_decode_tokens_num is not None:
        cms_status.scheduler_waiting_to_decode_tokens_num = (
            llumlet_status.scheduler_waiting_to_decode_tokens_num
        )
    if llumlet_status.num_uncomputed_tokens_scheduler_running_prefills is not None:
        cms_status.num_uncomputed_tokens_scheduler_running_prefills = (
            llumlet_status.num_uncomputed_tokens_scheduler_running_prefills
        )
    if llumlet_status.num_unallocated_tokens_scheduler_running_prefills is not None:
        cms_status.num_unallocated_tokens_scheduler_running_prefills = (
            llumlet_status.num_unallocated_tokens_scheduler_running_prefills
        )
    if llumlet_status.scheduler_running_to_decode_tokens_num is not None:
        cms_status.scheduler_running_to_decode_tokens_num = (
            llumlet_status.scheduler_running_to_decode_tokens_num
        )
    if llumlet_status.num_tokens_loading_requests is not None:
        cms_status.num_tokens_loading_requests = (
            llumlet_status.num_tokens_loading_requests
        )
    if llumlet_status.num_uncomputed_tokens_all_waiting_prefills is not None:
        cms_status.num_uncomputed_tokens_all_waiting_prefills = (
            llumlet_status.num_uncomputed_tokens_all_waiting_prefills
        )

    # migration status
    if llumlet_status.num_migrate_in_reqs is not None:
        cms_status.num_migrate_in_reqs = llumlet_status.num_migrate_in_reqs
    if llumlet_status.num_migrate_out_reqs is not None:
        cms_status.num_migrate_out_reqs = llumlet_status.num_migrate_out_reqs
    if llumlet_status.num_available_migrate_in_slots is not None:
        cms_status.num_available_migrate_in_slots = (
            llumlet_status.num_available_migrate_in_slots
        )
    if llumlet_status.num_available_migrate_out_slots is not None:
        cms_status.num_available_migrate_out_slots = (
            llumlet_status.num_available_migrate_out_slots
        )
    if llumlet_status.num_migrate_in_tokens is not None:
        cms_status.num_migrate_in_tokens = llumlet_status.num_migrate_in_tokens
    if llumlet_status.num_migrate_out_tokens is not None:
        cms_status.num_migrate_out_tokens = llumlet_status.num_migrate_out_tokens
    if llumlet_status.num_available_migrate_in_tokens is not None:
        cms_status.num_available_migrate_in_tokens = (
            llumlet_status.num_available_migrate_in_tokens
        )
    if llumlet_status.num_available_migrate_out_tokens is not None:
        cms_status.num_available_migrate_out_tokens = (
            llumlet_status.num_available_migrate_out_tokens
        )
    if llumlet_status.kv_cache_usage_ratio_migrate_in is not None:
        cms_status.kv_cache_usage_ratio_migrate_in = (
            llumlet_status.kv_cache_usage_ratio_migrate_in
        )
    if llumlet_status.kv_cache_usage_ratio_migrate_out is not None:
        cms_status.kv_cache_usage_ratio_migrate_out = (
            llumlet_status.kv_cache_usage_ratio_migrate_out
        )
    if llumlet_status.available_kv_cache_usage_ratio_migrate_in is not None:
        cms_status.available_kv_cache_usage_ratio_migrate_in = (
            llumlet_status.available_kv_cache_usage_ratio_migrate_in
        )
    if llumlet_status.available_kv_cache_usage_ratio_migrate_out is not None:
        cms_status.available_kv_cache_usage_ratio_migrate_out = (
            llumlet_status.available_kv_cache_usage_ratio_migrate_out
        )

    if llumlet_status.profiling_id is not None:
        cms_status.profiling_id = llumlet_status.profiling_id
    if llumlet_status.step_duration is not None:
        cms_status.step_duration = llumlet_status.step_duration
    if llumlet_status.num_scheduled_prefill_tokens is not None:
        cms_status.num_scheduled_prefill_tokens = (
            llumlet_status.num_scheduled_prefill_tokens
        )

    return cms_status
