from typing import List

from vllm.v1.request import Request
from vllm.v1.core.sched.output import SchedulerOutput

from llumnix.status_collector.base_connector_metric_collector import BaseConnectorMetricsCollector


class MooncakeConnectorMetricsCollector(BaseConnectorMetricsCollector):

    def get_connector_waiting_reqs(self) -> List[Request]:
        return []

    def get_connector_waiting_to_decode_reqs_num(self) -> int:
        return 0

    def get_connector_num_unallocated_blocks_waiting_decodes(self) -> int:
        return 0

    def get_connector_waiting_to_decode_blocks_num(self) -> int:
        return 0

    def get_connector_num_uncomputed_blocks_waiting_prefills(self) -> int:
        return 0

    def is_migrating(self, req: Request) -> bool:
        if not req.kv_transfer_params:
            return False
        return req.kv_transfer_params.get("is_migrating", False)

    def get_connector_loading_requests_num(self, scheduler_output: SchedulerOutput) -> int:
        return 0

    def get_connector_num_blocks_loading_requests(self, scheduler_output: SchedulerOutput) -> int:
        return 0
