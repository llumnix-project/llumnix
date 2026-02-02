from abc import ABC, abstractmethod
import asyncio
from collections import defaultdict
from typing import Dict, List, Set, Any

from llumnix.outputs.queue.zmq_server import ZmqServer
from llumnix.logging.logger import init_logger
from llumnix.connection_pool import ConnectionType, LruConnectionPool
from llumnix.utils import (RequestIDType, get_ip_address, get_free_port)

from llumnix.server.proto import (llumlet_server_pb2,
                                  llumlet_server_pb2_grpc)

logger = init_logger(__name__)


class BaseLlumnixClient(ABC):
    def __init__(self, loop: asyncio.AbstractEventLoop):
        self.ip = get_ip_address()
        self.port = get_free_port()

        self.output_queue_server = ZmqServer(self.ip, self.port)

        # instance_id -> requests running on the instance
        self.instance_requests: Dict[str, Set[RequestIDType]] = defaultdict(set)
        # request_id -> instances running the request
        self.request_instances: Dict[RequestIDType, List[str]] = defaultdict(list)
        # request_id -> number of output tokens of the request
        self.request_num_output_tokens: Dict[RequestIDType, int] = {}

        self.grpc_conn_pool = LruConnectionPool(ConnectionType.GRPC_CHANNEL, max_connections=5)
        # Requests had been aborted but not notify EngineCore yet
        self.aborted_requests: Set[RequestIDType] = set()

        loop.create_task(self.get_request_outputs_loop())
        loop.create_task(self.output_queue_server.run_server_loop())

    @abstractmethod
    async def get_request_outputs_loop(self):
        raise NotImplementedError

    @abstractmethod
    def _process_output_order(self, request_id: RequestIDType, request_output: Any):
        raise NotImplementedError

    @abstractmethod
    def cancel_dead_instance_requests(self, dead_instance_ids: List[str]) -> None:
        raise NotImplementedError

    async def _abort(self, request_id: RequestIDType) -> None:
        # clear requests states and record it as aborted to notify EngineCore later
        self._clear_client_request_states(request_id)
        self.aborted_requests.add(request_id)

    async def _call_llumlet_abort_requests(self, llumlet_addr: str, request_ids: List[RequestIDType]) -> None:
        channel_conn = self.grpc_conn_pool.get_connection_through_address(llumlet_addr)
        async with channel_conn as channel:
            stub = llumlet_server_pb2_grpc.LlumletStub(channel)
            requests = llumlet_server_pb2.AbortRequests()
            requests.request_ids.extend(request_ids)
            response = await stub.Abort(requests)
            if not response.success:
                logger.error("Abort requests {} on Llumlet failed: {}".format(request_ids, response.msg))
            else:
                logger.info("Successfully aborted requests {}".format(request_ids))
                self.aborted_requests -= set(request_ids)

    def _clear_client_request_states(self, request_id: RequestIDType):
        self.request_num_output_tokens.pop(request_id, None)
        instance_ids = self.request_instances.pop(request_id, [])
        for instance_id in instance_ids:
            if instance_id in self.instance_requests and request_id in self.instance_requests[instance_id]:
                self.instance_requests[instance_id].remove(request_id)
