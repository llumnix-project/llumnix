from typing import Dict, Set, List
from dataclasses import fields

from llumnix.llumlet.instance_info import InstanceStatus
from llumnix.logging.logger import init_logger
from llumnix import envs

logger = init_logger(__name__)

METRIC_STATUSES_REQUIREMENTS: Dict[str, Set[str]] = {
    "kv_blocks_ratio_with_all_prefills": {
        "num_used_gpu_blocks",
        "num_total_gpu_blocks",
        "num_uncomputed_blocks_all_waiting_prefills",
        "num_unallocated_blocks_scheduler_running_prefills",
        "num_unallocated_blocks_hybrid_scheduler_waiting_decodes",
    },
    "decode_batch_size": {
        "hybrid_scheduler_waiting_to_decode_requests_num",
        "num_loading_requests",
        "scheduler_waiting_to_decode_requests_num",
        "scheduler_running_to_decode_requests_num",
    },
    "num_waiting_requests": {
        "num_waiting_requests",
    },
    "all_prefills_kv_blocks_num": {
        "num_uncomputed_blocks_all_waiting_prefills",
        "num_uncomputed_blocks_scheduler_running_prefills",
    },
    "kv_cache_hit_len": {},
    "cache_aware_all_prefills_kv_blocks_num": {
        "num_uncomputed_blocks_all_waiting_prefills",
        "num_uncomputed_blocks_scheduler_running_prefills",
    },
    "adaptive_decode_batch_size": {
        "hybrid_scheduler_waiting_to_decode_requests_num",
        "num_loading_requests",
        "scheduler_waiting_to_decode_requests_num",
        "scheduler_running_to_decode_requests_num",
    },
    "num_requests": {
        "num_waiting_requests",
        "num_loading_requests",
        "num_running_requests",
    },
    "all_decodes_seq_len_with_all_prefills": {
        "hybrid_scheduler_waiting_to_decode_blocks_num",
        "scheduler_waiting_to_decode_blocks_num",
        "scheduler_running_to_decode_blocks_num",
        "num_blocks_loading_requests",
    },
}

def parse_used_metrics_from_env() -> List[str]:
    metrics_str = envs.LLUMNIX_USED_METRICS
    if not metrics_str:
        return []

    separators = [',', ';']
    for sep in separators:
        metrics_str = metrics_str.replace(sep, ',')

    return [m.strip() for m in metrics_str.split(',') if m.strip()]

def generate_instance_status_mask(used_metrics: List[str]) -> Dict[str, bool]:
    logger.debug("Used metrics: {}".format(used_metrics))
    if len(used_metrics) == 1 and used_metrics[0] == "all":
        return {
            status_name: True
            for status_name in {f.name for f in fields(InstanceStatus)}
        }

    required_statuses: Set[str] = set()
    collect_all_statuses = False
    for metric in used_metrics:
        statuses = METRIC_STATUSES_REQUIREMENTS.get(metric, None)
        if statuses is None:
            collect_all_statuses = True
            logger.warning("Metric {} is not supported, all statuses will be collected.".format(metric))
            break
        if statuses:
            required_statuses.update(statuses)

    all_statuses = {f.name for f in fields(InstanceStatus)}

    if not collect_all_statuses:
        status_mask: Dict[str, bool] = {
            status_name: (status_name in required_statuses)
            for status_name in all_statuses
        }
    else:
        status_mask = {
            status_name: True
            for status_name in all_statuses
        }

    return status_mask
