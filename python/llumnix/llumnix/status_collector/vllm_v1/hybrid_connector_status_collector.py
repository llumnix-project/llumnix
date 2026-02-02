from typing import List

from vllm.v1.request import Request

from llumnix.compat.vllm_compat import cdiv
from llumnix.compat.hybrid_connector_compat import get_param
from llumnix.status_collector.vllm_v1.base_connector_status_collector import BaseConnectorStatusCollector

class HybridConnectorStatusCollector(BaseConnectorStatusCollector):

    def get_connector_waiting_reqs(self) -> List[Request]:
        # _waiting: deque[tuple[Request, bool, bool]]
        if self.scheduler.connector is not None:
            # pylint: disable=protected-access
            return [item[0] for item in self.scheduler.connector._sched._waiting]
        return []

    def get_connector_waiting_to_decode_reqs_num(self) -> int:
        count = 0
        if self.scheduler.connector is not None:
            # pylint: disable=protected-access
            for _, load, _ in self.scheduler.connector._sched._waiting:
                if load:
                    count += 1
        return count

    def get_connector_num_unallocated_blocks_waiting_decodes(self) -> int:
        num_unallocated_blocks_waiting_decodes = 0
        if self.scheduler.connector is not None:
            # pylint: disable=protected-access
            for req, load, _ in self.scheduler.connector._sched._waiting:
                _, num_new_local_computed_tokens = \
                    self.scheduler.kv_cache_manager.get_computed_blocks(req)
                if load:
                    num_unallocated_blocks_waiting_decodes += \
                        cdiv(req.num_tokens - num_new_local_computed_tokens, self.scheduler.cache_config.block_size)
        return num_unallocated_blocks_waiting_decodes

    def get_connector_waiting_to_decode_blocks_num(self) -> int:
        num_waiting_to_decode_tokens = 0
        if self.scheduler.connector is not None:
            # pylint: disable=protected-access
            for req, load, _ in self.scheduler.connector._sched._waiting:
                if load:
                    num_waiting_to_decode_tokens += req.num_tokens
        num_blocks_waiting_to_decode_tokens = cdiv(num_waiting_to_decode_tokens, self.scheduler.cache_config.block_size)
        return num_blocks_waiting_to_decode_tokens

    def get_connector_num_uncomputed_blocks_waiting_prefills(self) -> int:
        num_uncomputed_tokens_waiting_prefills = 0
        if self.scheduler.connector is not None:
            # pylint: disable=protected-access
            for req, _, save in self.scheduler.connector._sched._waiting:
                _, num_new_local_computed_tokens = \
                    self.scheduler.kv_cache_manager.get_computed_blocks(req)
                if save:
                    num_uncomputed_tokens_waiting_prefills += (req.num_tokens - num_new_local_computed_tokens)
        return num_uncomputed_tokens_waiting_prefills

    def is_migrating(self, req: Request) -> bool:
        return get_param(req, "is_migrating")

    def get_connector_loading_requests_num(self) -> int:
        num_loading_requests = 0
        if self.scheduler.connector is not None:
            # pylint: disable=protected-access
            num_loading_requests = len(self.scheduler.connector._sched._loading)
        return num_loading_requests

    def get_connector_num_blocks_loading_requests(self) -> int:
        num_tokens_loading_requests = 0
        if self.scheduler.connector is not None:
            # pylint: disable=protected-access
            for loading_info in self.scheduler.connector._sched._loading.values():
                num_tokens_loading_requests += loading_info._req.num_tokens
        num_blocks_loading_requests = cdiv(num_tokens_loading_requests, self.scheduler.cache_config.block_size)
        return num_blocks_loading_requests
