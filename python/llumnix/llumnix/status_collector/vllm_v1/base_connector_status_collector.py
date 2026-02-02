from abc import ABC, abstractmethod
from typing import Any, List

from vllm.v1.request import Request

class BaseConnectorStatusCollector(ABC):

    def __init__(self, scheduler: "Scheduler"):
        self.scheduler = scheduler
        self.cache_config = scheduler.cache_config

    @abstractmethod
    def get_connector_waiting_reqs(self) -> List[Request]:
        raise NotImplementedError

    @abstractmethod
    def get_connector_waiting_to_decode_reqs_num(self) -> int:
        raise NotImplementedError

    @abstractmethod
    def get_connector_num_unallocated_blocks_waiting_decodes(self) -> int:
        raise NotImplementedError

    @abstractmethod
    def get_connector_waiting_to_decode_blocks_num(self) -> int:
        raise NotImplementedError

    @abstractmethod
    def get_connector_num_uncomputed_blocks_waiting_prefills(self) -> int:
        raise NotImplementedError

    @abstractmethod
    def is_migrating(self, req: Any) -> bool:
        raise NotImplementedError

    @abstractmethod
    def get_connector_loading_requests_num(self) -> int:
        raise NotImplementedError

    @abstractmethod
    def get_connector_num_blocks_loading_requests(self) -> int:
        raise NotImplementedError
