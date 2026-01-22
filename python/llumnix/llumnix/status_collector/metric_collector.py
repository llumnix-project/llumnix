from enum import Enum
from typing import Callable, Dict, List

from vllm.v1.core.sched.output import SchedulerOutput
from vllm.v1.request import Request
from vllm.config import VllmConfig

from llumnix.llumlet.instance_info import BackendType, InstanceStatus
from llumnix.logging.logger import init_logger
from llumnix.metrics import generate_instance_status_mask, parse_used_metrics_from_env
from llumnix.status_collector.base_connector_metric_collector import BaseConnectorMetricsCollector
from llumnix.compat.vllm_compat import cdiv

logger = init_logger(__name__)

class StepPhase(str, Enum):
    STEP_BEGIN = "STEP_BEGIN"
    AFTER_SCHEDULE = "AFTER_SCHEDULE"
    AFTER_UPDATE = "AFTER_UPDATE"
    BEFORE_RETURN = "BEFORE_RETURN"

_REGISTERED_STATUS_COLLECTORS: Dict[str, Callable] = {}

def register(status_name: str):
    def decorator(func):
        _REGISTERED_STATUS_COLLECTORS[status_name] = func
        return func
    return decorator

class InstanceStatusCollector:
    def __init__(self, scheduler: "Scheduler", vllm_config: VllmConfig, engine_type: str) -> None:
        self.scheduler = scheduler
        self.async_scheduling = vllm_config.scheduler_config.async_scheduling
        self.last_num_scheduled_tokens = None
        self.connector_metric_collector = self.create_connector_metrics_collector(engine_type, scheduler)
        self.instance_status_mask = generate_instance_status_mask(parse_used_metrics_from_env())
        logger.debug("instance_status_mask: {}".format(self.instance_status_mask))

    def create_connector_metrics_collector(
        self,
        backend_type: BackendType,
        scheduler: "Scheduler"
    ) -> BaseConnectorMetricsCollector:
        if backend_type == BackendType.VLLM_V1_CE:
            # pylint: disable=import-outside-toplevel
            from llumnix.status_collector.mooncake_connector_metric_collector import MooncakeConnectorMetricsCollector
            return MooncakeConnectorMetricsCollector(scheduler)
        if backend_type == BackendType.VLLM_V1:
            # pylint: disable=import-outside-toplevel
            from llumnix.status_collector.hybrid_connector_metric_collector import HybridConnectorMetricsCollector
            return HybridConnectorMetricsCollector(scheduler)
        raise ValueError("Unsupported backend type: {}".format(backend_type))

    def collect_by_name(self, status_name: str, instance_status: InstanceStatus, *args, **kwargs):
        if status_name not in _REGISTERED_STATUS_COLLECTORS:
            raise KeyError(f"No collector for {status_name}")
        method = _REGISTERED_STATUS_COLLECTORS[status_name]
        return method(self, instance_status, *args, **kwargs)

    def get_waiting_status(self, waiting_status:List[str], instance_status: InstanceStatus, all_waitings: List[Request]) -> None:
        for status_name in waiting_status:
            if self.instance_status_mask.get(status_name, False):
                if status_name in _REGISTERED_STATUS_COLLECTORS:
                    if status_name == "num_waiting_requests":
                        all_waitings = self.get_all_waiting_reqs()
                        self.collect_by_name(status_name, instance_status, all_waitings)
                    else:
                        self.collect_by_name(status_name, instance_status)
                else:
                    logger.warning("Status {} collect function is not found.".format(status_name))

    def get_running_status(
        self,
        running_status:List[str],
        instance_status: InstanceStatus,
        step_phase: StepPhase,
        scheduler_output: "SchedulerOutput"=None
    ) -> None:
        for status_name in running_status:
            if self.instance_status_mask.get(status_name, False):
                if status_name in _REGISTERED_STATUS_COLLECTORS:
                    if status_name in ["num_uncomputed_blocks_scheduler_running_prefills"]:
                        self.collect_by_name(status_name, instance_status, step_phase, scheduler_output)
                    elif status_name in ["num_blocks_loading_requests", "num_loading_requests"]:
                        self.collect_by_name(status_name, instance_status, scheduler_output)
                    else:
                        self.collect_by_name(status_name, instance_status)
                else:
                    logger.warning("Status {} collect function is not found.".format(status_name))

    def get_all_waiting_reqs(self) -> List[Request]:
        all_waitings = list(self.scheduler.waiting)
        all_waitings.extend(self.connector_metric_collector.get_connector_waiting_reqs())
        return all_waitings

    @register("hybrid_scheduler_waiting_to_decode_requests_num")
    def _collect_hybrid_scheduler_waiting_to_decode_requests_num(self, instance_status: InstanceStatus) -> None:
        instance_status.hybrid_scheduler_waiting_to_decode_requests_num = self.connector_metric_collector.get_connector_waiting_to_decode_reqs_num()

    @register("num_uncomputed_blocks_hybrid_scheduler_waiting_prefills")
    def _collect_num_uncomputed_blocks_hybrid_scheduler_waiting_prefills(self, instance_status: InstanceStatus) -> int:
        num_uncomputed_tokens_hybrid_scheduler_waiting_prefills = \
            self.connector_metric_collector.get_connector_num_uncomputed_blocks_waiting_prefills()
        num_uncomputed_blocks_hybrid_scheduler_waiting_prefills = \
            cdiv(num_uncomputed_tokens_hybrid_scheduler_waiting_prefills, self.scheduler.cache_config.block_size)
        instance_status.num_uncomputed_blocks_hybrid_scheduler_waiting_prefills = num_uncomputed_blocks_hybrid_scheduler_waiting_prefills

        return num_uncomputed_tokens_hybrid_scheduler_waiting_prefills

    @register("num_unallocated_blocks_hybrid_scheduler_waiting_decodes")
    def _collect_num_unallocated_blocks_hybrid_scheduler_waiting_decodes(self, instance_status: InstanceStatus) -> None:
        instance_status.num_unallocated_blocks_hybrid_scheduler_waiting_decodes = \
            self.connector_metric_collector.get_connector_num_unallocated_blocks_waiting_decodes()

    @register("hybrid_scheduler_waiting_to_decode_blocks_num")
    def _collect_hybrid_scheduler_waiting_to_decode_blocks_num(self, instance_status: InstanceStatus) -> None:
        instance_status.hybrid_scheduler_waiting_to_decode_blocks_num = self.connector_metric_collector.get_connector_waiting_to_decode_blocks_num()

    @register("num_uncomputed_blocks_scheduler_waiting_prefills")
    def _collect_num_uncomputed_blocks_scheduler_waiting_prefills(self, instance_status: InstanceStatus) -> int:
        num_uncomputed_tokens_scheduler_waiting_prefills = 0
        for req in self.scheduler.waiting:
            if req.num_tokens - req.num_computed_tokens > 1:
                _, num_new_local_computed_tokens = \
                    self.scheduler.kv_cache_manager.get_computed_blocks(req)
                num_uncomputed_tokens_scheduler_waiting_prefills += (req.num_tokens - num_new_local_computed_tokens)
        num_uncomputed_blocks_scheduler_waiting_prefills = \
            cdiv(num_uncomputed_tokens_scheduler_waiting_prefills, self.scheduler.cache_config.block_size)
        instance_status.num_uncomputed_blocks_scheduler_waiting_prefills = num_uncomputed_blocks_scheduler_waiting_prefills

        return num_uncomputed_tokens_scheduler_waiting_prefills

    @register("scheduler_waiting_to_decode_requests_num")
    def _collect_scheduler_waiting_to_decode_requests_num(self, instance_status: InstanceStatus) -> None:
        scheduler_waiting_to_decode_requests_num = 0
        for req in self.scheduler.waiting:
            if (req.sampling_params is not None and req.sampling_params.max_tokens > 1) \
                or req.num_tokens - req.num_computed_tokens == 1:
                scheduler_waiting_to_decode_requests_num += 1
        instance_status.scheduler_waiting_to_decode_requests_num = scheduler_waiting_to_decode_requests_num

    @register("scheduler_waiting_to_decode_blocks_num")
    def _collect_scheduler_waiting_to_decode_blocks_num(self, instance_status: InstanceStatus) -> None:
        scheduler_waiting_to_decode_tokens_num = 0
        for req in self.scheduler.waiting:
            if (req.sampling_params is not None and req.sampling_params.max_tokens > 1) \
                or req.num_tokens - req.num_computed_tokens == 1:
                scheduler_waiting_to_decode_tokens_num += req.num_tokens
        scheduler_waiting_to_decode_blocks_num = cdiv(scheduler_waiting_to_decode_tokens_num, self.scheduler.cache_config.block_size)
        instance_status.scheduler_waiting_to_decode_blocks_num = scheduler_waiting_to_decode_blocks_num

    @register("num_uncomputed_blocks_all_waiting_prefills")
    def _collect_num_uncomputed_blocks_all_waiting_prefills(self, instance_status: InstanceStatus) -> None:
        num_uncomputed_tokens_hybrid_scheduler_waiting_prefills = \
            self._collect_num_uncomputed_blocks_hybrid_scheduler_waiting_prefills(instance_status)
        num_uncomputed_tokens_scheduler_waiting_prefills = \
            self._collect_num_uncomputed_blocks_scheduler_waiting_prefills(instance_status)
        num_uncomputed_tokens_all_waiting_prefills = \
            num_uncomputed_tokens_hybrid_scheduler_waiting_prefills + num_uncomputed_tokens_scheduler_waiting_prefills
        num_uncomputed_blocks_all_waiting_prefills = \
            cdiv(num_uncomputed_tokens_all_waiting_prefills, self.scheduler.cache_config.block_size)
        instance_status.num_uncomputed_blocks_all_waiting_prefills = num_uncomputed_blocks_all_waiting_prefills

    @register("num_waiting_requests")
    def _collect_num_waiting_requests(self, instance_status: InstanceStatus, all_waitings: List[Request]) -> None:
        instance_status.num_waiting_requests = len(all_waitings)

    @register("num_uncomputed_blocks_scheduler_running_prefills")
    def _collect_num_uncomputed_blocks_scheduler_running_prefills(
        self,
        instance_status: InstanceStatus,
        step_phase: StepPhase,
        scheduler_output: SchedulerOutput = None,
    ) -> None:
        num_uncomputed_tokens_scheduler_running_prefills = 0
        # pylint: disable=too-many-nested-blocks
        # NOTE(sunbiao.sun):
        # In async scheduling and speculative decoding case,
        # num_tokens might be not completely correct due to pre-advanced output token ids, but the error is small thus acceptable.
        if self.scheduler.running:
            for req in self.scheduler.running:
                if req.num_computed_tokens < req.num_prompt_tokens:
                    num_unallocated_tokens = req.num_tokens - req.num_computed_tokens
                    num_uncomputed_tokens = num_unallocated_tokens
                    # The number of computed blocks is pre-advanced in schedule before it has been computed.
                    if step_phase == StepPhase.AFTER_SCHEDULE and scheduler_output is not None:
                        if req.request_id in scheduler_output.num_scheduled_tokens:
                            # exclude request send from p to d
                            if scheduler_output.num_scheduled_tokens[req.request_id] > 1:
                                num_uncomputed_tokens += scheduler_output.num_scheduled_tokens[req.request_id]
                    # In async scheduling mode, the number of scheduled tokens in last schedule should be
                    # added back to the number of uncomputed tokens,
                    # because the tokens scheduled in last schedule has just begun computing,
                    # while the number of computed blocks is pre-advanced in schedule before it has been computed.
                    if self.async_scheduling and self.last_num_scheduled_tokens is not None:
                        # exclude request send from p to d
                        if req.request_id in self.last_num_scheduled_tokens and self.last_num_scheduled_tokens[req.request_id] > 1:
                            num_uncomputed_tokens += self.last_num_scheduled_tokens[req.request_id]
                    num_uncomputed_tokens_scheduler_running_prefills += num_uncomputed_tokens
        if self.async_scheduling:
            if step_phase == StepPhase.AFTER_SCHEDULE and scheduler_output is not None:
                # no need to deep copy, scheduler_output is generated per schedule
                self.last_num_scheduled_tokens = scheduler_output.num_scheduled_tokens
        num_uncomputed_blocks_scheduler_running_prefills = \
            cdiv(num_uncomputed_tokens_scheduler_running_prefills, self.scheduler.cache_config.block_size)
        instance_status.num_uncomputed_blocks_scheduler_running_prefills = num_uncomputed_blocks_scheduler_running_prefills

    @register("num_unallocated_blocks_scheduler_running_prefills")
    def _collect_num_unallocated_blocks_scheduler_running_prefills(self, instance_status: InstanceStatus) -> None:
        num_unallocated_tokens_scheduler_running_prefills = 0
        if self.scheduler.running:
            for req in self.scheduler.running:
                if req.num_computed_tokens < req.num_prompt_tokens:
                    num_unallocated_tokens = req.num_tokens - req.num_computed_tokens
                    num_unallocated_tokens_scheduler_running_prefills += num_unallocated_tokens
        num_unallocated_blocks_scheduler_running_prefills = \
            cdiv(num_unallocated_tokens_scheduler_running_prefills, self.scheduler.cache_config.block_size)
        instance_status.num_unallocated_blocks_scheduler_running_prefills = num_unallocated_blocks_scheduler_running_prefills

    @register("scheduler_running_to_decode_requests_num")
    def _collect_scheduler_running_to_decode_requests_num(self, instance_status: InstanceStatus) -> None:
        scheduler_running_to_decode_requests_num = 0
        # pylint: disable=too-many-nested-blocks
        if self.scheduler.running:
            for req in self.scheduler.running:
                if req.sampling_params is not None and req.sampling_params.max_tokens > 1 \
                    and not self.connector_metric_collector.is_migrating(req):
                    scheduler_running_to_decode_requests_num += 1
        instance_status.scheduler_running_to_decode_requests_num = scheduler_running_to_decode_requests_num

    @register("scheduler_running_to_decode_blocks_num")
    def _collect_scheduler_running_to_decode_blocks_num(self, instance_status: InstanceStatus) -> None:
        scheduler_running_to_decode_tokens_num = 0
        # pylint: disable=too-many-nested-blocks
        if self.scheduler.running:
            for req in self.scheduler.running:
                if req.sampling_params is not None and req.sampling_params.max_tokens > 1 \
                    and not self.connector_metric_collector.is_migrating(req):
                    scheduler_running_to_decode_tokens_num += req.num_tokens
        scheduler_running_to_decode_blocks_num = cdiv(scheduler_running_to_decode_tokens_num, self.scheduler.cache_config.block_size)
        instance_status.scheduler_running_to_decode_blocks_num = scheduler_running_to_decode_blocks_num

    @register("num_loading_requests")
    def _collect_num_loading_requests(self, instance_status: InstanceStatus, scheduler_output: SchedulerOutput) -> None:
        instance_status.num_loading_requests = self.connector_metric_collector.get_connector_loading_requests_num(scheduler_output)

    @register("num_blocks_loading_requests")
    def _collect_num_blocks_loading_requests(self, instance_status: InstanceStatus, scheduler_output: SchedulerOutput) -> None:
        instance_status.num_blocks_loading_requests = self.connector_metric_collector.get_connector_num_blocks_loading_requests(scheduler_output)

    @register("num_running_requests")
    def _collect_num_running_requests(self, instance_status: InstanceStatus) -> None:
        instance_status.num_running_requests = len(self.scheduler.running)
