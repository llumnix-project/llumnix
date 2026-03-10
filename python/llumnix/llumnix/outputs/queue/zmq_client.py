import time
from collections.abc import Iterable
from typing import Any

import zmq
import zmq.asyncio
import cloudpickle

from llumnix.outputs.queue.base_queue_client import BaseQueueClient
from llumnix.outputs.queue.zmq_utils import (
    MIGRATION_FAILURE_STR,
    MIGRATION_SUCCESS_STR,
    RPC_SUCCESS_STR,
    LlumletMigrateRequest,
    LlumletRequestType,
    RPCRequestType,
    RPCPutNoWaitQueueRequest,
    RPCPutNoWaitBatchQueueRequest,
)
from llumnix.connection_pool import ConnectionType, LruConnectionPool
from llumnix.constants import ZMQ_RPC_TIMEOUT_SECOND, ZMQ_IO_THREADS
from llumnix.logging.logger import init_logger
from llumnix.utils import MigrationParams

logger = init_logger(__name__)


class ZmqClient(BaseQueueClient):
    def __init__(self):
        super().__init__()
        self.context = zmq.asyncio.Context(ZMQ_IO_THREADS)
        self.socket_conn_pool = LruConnectionPool(
            connection_type=ConnectionType.ZMQ_SOCKET, context=self.context
        )

    async def close(self):
        await self.socket_conn_pool.close_all()
        self.context.destroy()

    # pylint: disable=arguments-differ
    async def put_nowait(self, server_addr: str, item: Any):
        queue_request = RPCPutNoWaitQueueRequest(
            item=item, send_time=time.perf_counter()
        )
        await self._send_one_way_rpc_request(
            request_type=RPCRequestType.PUT_NOWAIT,
            request=queue_request,
            ip=server_addr.split(":")[0],
            port=int(server_addr.split(":")[1]),
            error_message="Unable to put item into queue.",
        )

    # not used
    async def put_nowait_batch(self, server_addr: str, items: Iterable):
        batch_queue_request = RPCPutNoWaitBatchQueueRequest(
            items=items, send_time=time.perf_counter()
        )
        await self._send_one_way_rpc_request(
            request_type=RPCRequestType.PUT_NOWAIT_BATCH,
            request=batch_queue_request,
            ip=server_addr.split(":")[0],
            port=int(server_addr.split(":")[1]),
            error_message="Unable to put items into queue.",
        )

    async def _send_one_way_rpc_request(
        self,
        request_type: RPCRequestType,
        request: Any,
        ip: str,
        port: int,
        error_message: str,
    ):
        async def do_rpc_call(
            socket: zmq.asyncio.Socket, request_type: RPCRequestType, request: Any
        ):
            await socket.send_multipart(
                [request_type.value, cloudpickle.dumps(request)]
            )
            if await socket.poll(timeout=ZMQ_RPC_TIMEOUT_SECOND * 1000) == 0:
                raise TimeoutError(
                    f"Server didn't reply within {ZMQ_RPC_TIMEOUT_SECOND * 1000} ms"
                )

            return cloudpickle.loads(await socket.recv())

        try:
            socket_connection = self.socket_conn_pool.get_connection(ip, port)
            async with socket_connection as socket:
                response = await do_rpc_call(socket, request_type, request)
        # pylint: disable=broad-except
        except Exception as e:
            logger.exception("Error in send one way rpc request")
            response = e
        if not isinstance(response, str) or response != RPC_SUCCESS_STR:
            # close the socket if something wrong
            await self.socket_conn_pool.close_connection(ip, port)
            if isinstance(response, Exception):
                logger.error(error_message)
                raise response
            raise ValueError(error_message)


class MigrationZmqClient:
    """Migration zmq client, used to trigger migration"""

    def __init__(self, server_address: str):
        super().__init__()
        self.server_address = server_address
        self.context: zmq.asyncio.Context = zmq.asyncio.Context.instance()
        self.socket: zmq.asyncio.Socket = self.context.socket(zmq.DEALER)
        self.socket.connect(self.server_address)
        logger.info("MigrationZmqClient connected to %s", self.server_address)

    def close(self):
        if not self.socket.closed:
            self.socket.close()
            logger.info("MigrationZmqClient disconnected from %s", self.server_address)

    async def migrate(
        self, dst_host: str, dst_port: int, migration_params: MigrationParams
    ):
        queue_request = LlumletMigrateRequest(
            dst_host=dst_host, dst_port=dst_port, migration_params=migration_params
        )
        res = await self._send_request(
            request_type=LlumletRequestType.MIGRATE,
            request=queue_request,
            error_message="Failed to migrate.",
        )
        return res

    async def _send_request(
        self,
        request_type: LlumletRequestType,
        request: Any,
        error_message: str,
    ):
        try:
            request_payload = cloudpickle.dumps(request)
            await self.socket.send_multipart([request_type.value, request_payload])

            timeout_ms = ZMQ_RPC_TIMEOUT_SECOND * 1000
            if await self.socket.poll(timeout=timeout_ms) == 0:
                raise TimeoutError(
                    f"MigServer at {self.server_address} didn't reply within {timeout_ms} ms."
                )

            response_payload = await self.socket.recv()
            response = cloudpickle.loads(response_payload)

        except Exception as e:
            logger.exception(
                "Error during call to MigrationFrontend %s", self.server_address
            )
            raise RuntimeError(f"{error_message} Reason: {e}") from e
        if not isinstance(response, str) or response not in (
            MIGRATION_SUCCESS_STR,
            MIGRATION_FAILURE_STR,
        ):
            if isinstance(response, Exception):
                logger.error(
                    "%s. Server returned an exception: %s", error_message, response
                )
                raise response
        if response == MIGRATION_SUCCESS_STR:
            return True
        return False
