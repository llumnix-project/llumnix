from typing import List

from vllm.v1.request import Request

from llumnix.status_collector.vllm_v1.base_connector_status_collector import (
    BaseConnectorStatusCollector,
)


class MooncakeConnectorStatusCollector(BaseConnectorStatusCollector):

    def get_connector_waiting_reqs(self) -> List[Request]:
        return []

    def get_connector_waiting_to_decode_reqs_num(self) -> int:
        return 0

    def get_connector_num_unallocated_tokens_waiting_decodes(self) -> int:
        return 0

    def get_connector_waiting_to_decode_tokens_num(self) -> int:
        return 0

    def get_connector_num_uncomputed_tokens_waiting_prefills(self) -> int:
        return 0

    def is_migrating(self, req: Request) -> bool:
        if not req.kv_transfer_params:
            return False
        return req.kv_transfer_params.get("is_migrating", False)

    def get_connector_loading_requests_num(self) -> int:
        return 0

    def get_connector_num_tokens_loading_requests(self) -> int:
        return 0
