from enum import Enum
from typing import Callable, Dict, List

from vllm.v1.core.sched.output import SchedulerOutput
from vllm.v1.request import Request
from vllm.config import VllmConfig

from llumnix.instance_info import ConnectorType, InstanceStatus
from llumnix.logging.logger import init_logger
from llumnix.metrics import generate_instance_status_mask, parse_used_metrics_from_env
from llumnix.status_collector.vllm_v1.base_connector_status_collector import BaseConnectorStatusCollector
from llumnix.compat.vllm_compat import cdiv

logger = init_logger(__name__)

class StepPhase(str, Enum):
    STEP_BEGIN = "STEP_BEGIN"
    AFTER_SCHEDULE = "AFTER_SCHEDULE"
    AFTER_UPDATE = "AFTER_UPDATE"
    BEFORE_RETURN = "BEFORE_RETURN"
    BYPASS_STEP_BEGIN = "BYPASS_STEP_BEGIN"
    BYPASS_STEP_END = "BYPASS_STEP_END"

_REGISTERED_STATUS_COLLECTORS: Dict[str, Callable] = {}

def register(status_name: str):
    def decorator(func):
        _REGISTERED_STATUS_COLLECTORS[status_name] = func
        return func
    return decorator

class InstanceStatusCollector:
    def __init__(self, scheduler: "Scheduler", vllm_config: VllmConfig, connector_type: ConnectorType) -> None:
        self.scheduler = scheduler
        self.async_scheduling = vllm_config.scheduler_config.async_scheduling
        self.last_num_scheduled_tokens = None
        self.connector_update_collector = self.create_connector_status_collector(connector_type, scheduler)
        self.instance_status_mask = generate_instance_status_mask(parse_used_metrics_from_env())
        logger.debug("instance_status_mask: {}".format(self.instance_status_mask))

    def create_connector_status_collector(
        self,
        connector_type: ConnectorType,
        scheduler: "Scheduler"
    ) -> BaseConnectorStatusCollector:
        if not connector_type:
            return None
        if connector_type == ConnectorType.MOONCAKE:
            # pylint: disable=import-outside-toplevel
            from llumnix.status_collector.vllm_v1.mooncake_connector_status_collector import MooncakeConnectorStatusCollector
            return MooncakeConnectorStatusCollector(scheduler)
        if connector_type == ConnectorType.HYBRID:
            # pylint: disable=import-outside-toplevel
            from llumnix.status_collector.vllm_v1.hybrid_connector_status_collector import HybridConnectorStatusCollector
            return HybridConnectorStatusCollector(scheduler)
        raise ValueError("Unsupported connector type: {}".format(connector_type))

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
                    if status_name in ["num_uncomputed_tokens_scheduler_running_prefills"]:
                        self.collect_by_name(status_name, instance_status, step_phase, scheduler_output)
                    else:
                        self.collect_by_name(status_name, instance_status)
                else:
                    logger.warning("Status {} collect function is not found.".format(status_name))

    def get_all_waiting_reqs(self) -> List[Request]:
        all_waitings = list(self.scheduler.waiting)
        if self.connector_update_collector:
            all_waitings.extend(self.connector_update_collector.get_connector_waiting_reqs())
        return all_waitings

    @register("hybrid_scheduler_waiting_to_decode_requests_num")
    def _collect_hybrid_scheduler_waiting_to_decode_requests_num(self, instance_status: InstanceStatus) -> None:
        if not self.connector_update_collector:
            return
        instance_status.hybrid_scheduler_waiting_to_decode_requests_num = self.connector_update_collector.get_connector_waiting_to_decode_reqs_num()

    @register("num_uncomputed_tokens_hybrid_scheduler_waiting_prefills")
    def _collect_num_uncomputed_tokens_hybrid_scheduler_waiting_prefills(self, instance_status: InstanceStatus) -> int:
        if not self.connector_update_collector:
            return 0
        num_uncomputed_tokens_hybrid_scheduler_waiting_prefills = \
            self.connector_update_collector.get_connector_num_uncomputed_tokens_waiting_prefills()
        instance_status.num_uncomputed_tokens_hybrid_scheduler_waiting_prefills = num_uncomputed_tokens_hybrid_scheduler_waiting_prefills

        return num_uncomputed_tokens_hybrid_scheduler_waiting_prefills

    @register("num_unallocated_tokens_hybrid_scheduler_waiting_decodes")
    def _collect_num_unallocated_tokens_hybrid_scheduler_waiting_decodes(self, instance_status: InstanceStatus) -> None:
        if not self.connector_update_collector:
            return
        instance_status.num_unallocated_tokens_hybrid_scheduler_waiting_decodes = \
            self.connector_update_collector.get_connector_num_unallocated_tokens_waiting_decodes()

    @register("hybrid_scheduler_waiting_to_decode_tokens_num")
    def _collect_hybrid_scheduler_waiting_to_decode_tokens_num(self, instance_status: InstanceStatus) -> None:
        if not self.connector_update_collector:
            return
        instance_status.hybrid_scheduler_waiting_to_decode_tokens_num = self.connector_update_collector.get_connector_waiting_to_decode_tokens_num()

    @register("num_uncomputed_tokens_scheduler_waiting_prefills")
    def _collect_num_uncomputed_tokens_scheduler_waiting_prefills(self, instance_status: InstanceStatus) -> int:
        num_uncomputed_tokens_scheduler_waiting_prefills = 0
        for req in self.scheduler.waiting:
            if req.num_tokens - req.num_computed_tokens > 1:
                _, num_new_local_computed_tokens = \
                    self.scheduler.kv_cache_manager.get_computed_blocks(req)
                num_uncomputed_tokens_scheduler_waiting_prefills += (req.num_tokens - num_new_local_computed_tokens)
        instance_status.num_uncomputed_tokens_scheduler_waiting_prefills = num_uncomputed_tokens_scheduler_waiting_prefills

        return num_uncomputed_tokens_scheduler_waiting_prefills

    @register("scheduler_waiting_to_decode_requests_num")
    def _collect_scheduler_waiting_to_decode_requests_num(self, instance_status: InstanceStatus) -> None:
        scheduler_waiting_to_decode_requests_num = 0
        for req in self.scheduler.waiting:
            if (req.sampling_params is not None and req.sampling_params.max_tokens > 1) \
                or req.num_tokens - req.num_computed_tokens == 1:
                scheduler_waiting_to_decode_requests_num += 1
        instance_status.scheduler_waiting_to_decode_requests_num = scheduler_waiting_to_decode_requests_num

    @register("scheduler_waiting_to_decode_tokens_num")
    def _collect_scheduler_waiting_to_decode_tokens_num(self, instance_status: InstanceStatus) -> None:
        scheduler_waiting_to_decode_tokens_num = 0
        for req in self.scheduler.waiting:
            if (req.sampling_params is not None and req.sampling_params.max_tokens > 1) \
                or req.num_tokens - req.num_computed_tokens == 1:
                scheduler_waiting_to_decode_tokens_num += req.num_tokens
        instance_status.scheduler_waiting_to_decode_tokens_num = scheduler_waiting_to_decode_tokens_num

    @register("num_uncomputed_tokens_all_waiting_prefills")
    def _collect_num_uncomputed_tokens_all_waiting_prefills(self, instance_status: InstanceStatus) -> None:
        num_uncomputed_tokens_hybrid_scheduler_waiting_prefills = \
            self._collect_num_uncomputed_tokens_hybrid_scheduler_waiting_prefills(instance_status)
        num_uncomputed_tokens_scheduler_waiting_prefills = \
            self._collect_num_uncomputed_tokens_scheduler_waiting_prefills(instance_status)
        num_uncomputed_tokens_all_waiting_prefills = \
            num_uncomputed_tokens_hybrid_scheduler_waiting_prefills + num_uncomputed_tokens_scheduler_waiting_prefills
        instance_status.num_uncomputed_tokens_all_waiting_prefills = num_uncomputed_tokens_all_waiting_prefills

    @register("num_waiting_requests")
    def _collect_num_waiting_requests(self, instance_status: InstanceStatus, all_waitings: List[Request]) -> None:
        instance_status.num_waiting_requests = len(all_waitings)

    @register("num_uncomputed_tokens_scheduler_running_prefills")
    def _collect_num_uncomputed_tokens_scheduler_running_prefills(
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
                    # The number of computed tokens is pre-advanced in schedule before it has been computed.
                    if step_phase == StepPhase.AFTER_SCHEDULE and scheduler_output is not None:
                        if req.request_id in scheduler_output.num_scheduled_tokens:
                            # exclude request send from p to d
                            if scheduler_output.num_scheduled_tokens[req.request_id] > 1:
                                num_uncomputed_tokens += scheduler_output.num_scheduled_tokens[req.request_id]
                    # In async scheduling mode, the number of scheduled tokens in last schedule should be
                    # added back to the number of uncomputed tokens,
                    # because the tokens scheduled in last schedule has just begun computing,
                    # while the number of computed tokens is pre-advanced in schedule before it has been computed.
                    if self.async_scheduling and self.last_num_scheduled_tokens is not None:
                        # exclude request send from p to d
                        if req.request_id in self.last_num_scheduled_tokens and self.last_num_scheduled_tokens[req.request_id] > 1:
                            num_uncomputed_tokens += self.last_num_scheduled_tokens[req.request_id]
                    num_uncomputed_tokens_scheduler_running_prefills += num_uncomputed_tokens
        if self.async_scheduling:
            if step_phase == StepPhase.AFTER_SCHEDULE and scheduler_output is not None:
                # no need to deep copy, scheduler_output is generated per schedule
                self.last_num_scheduled_tokens = scheduler_output.num_scheduled_tokens
        instance_status.num_uncomputed_tokens_scheduler_running_prefills = num_uncomputed_tokens_scheduler_running_prefills

    @register("num_unallocated_tokens_scheduler_running_prefills")
    def _collect_num_unallocated_tokens_scheduler_running_prefills(self, instance_status: InstanceStatus) -> None:
        num_unallocated_tokens_scheduler_running_prefills = 0
        if self.scheduler.running:
            for req in self.scheduler.running:
                if req.num_computed_tokens < req.num_prompt_tokens:
                    num_unallocated_tokens = req.num_tokens - req.num_computed_tokens
                    num_unallocated_tokens_scheduler_running_prefills += num_unallocated_tokens
        instance_status.num_unallocated_tokens_scheduler_running_prefills = num_unallocated_tokens_scheduler_running_prefills

    @register("scheduler_running_to_decode_requests_num")
    def _collect_scheduler_running_to_decode_requests_num(self, instance_status: InstanceStatus) -> None:
        scheduler_running_to_decode_requests_num = 0
        # pylint: disable=too-many-nested-blocks
        if self.scheduler.running:
            for req in self.scheduler.running:
                if req.sampling_params is not None and req.sampling_params.max_tokens > 1:
                    if self.connector_update_collector and self.connector_update_collector.is_migrating(req):
                        continue
                    scheduler_running_to_decode_requests_num += 1
        instance_status.scheduler_running_to_decode_requests_num = scheduler_running_to_decode_requests_num

    @register("scheduler_running_to_decode_tokens_num")
    def _collect_scheduler_running_to_decode_tokens_num(self, instance_status: InstanceStatus) -> None:
        scheduler_running_to_decode_tokens_num = 0
        # pylint: disable=too-many-nested-blocks
        if self.scheduler.running:
            for req in self.scheduler.running:
                if req.sampling_params is not None and req.sampling_params.max_tokens > 1:
                    if self.connector_update_collector and self.connector_update_collector.is_migrating(req):
                        continue
                    scheduler_running_to_decode_tokens_num += req.num_tokens
        instance_status.scheduler_running_to_decode_tokens_num = scheduler_running_to_decode_tokens_num

    @register("num_loading_requests")
    def _collect_num_loading_requests(self, instance_status: InstanceStatus) -> None:
        if self.connector_update_collector is None:
            return
        instance_status.num_loading_requests = self.connector_update_collector.get_connector_loading_requests_num()

    @register("num_tokens_loading_requests")
    def _collect_num_tokens_loading_requests(self, instance_status: InstanceStatus) -> None:
        if self.connector_update_collector is None:
            return
        instance_status.num_tokens_loading_requests = self.connector_update_collector.get_connector_num_tokens_loading_requests()

    @register("num_running_requests")
    def _collect_num_running_requests(self, instance_status: InstanceStatus) -> None:
        instance_status.num_running_requests = len(self.scheduler.running)
