import time
from collections.abc import Iterable
from typing import Any

import zmq
import zmq.asyncio
import cloudpickle

from llumnix.outputs.queue.base_queue_client import BaseQueueClient
from llumnix.outputs.queue.zmq_utils import (
    RPC_SUCCESS_STR,
    RPCRequestType,
    RPCPutNoWaitQueueRequest,
    RPCPutNoWaitBatchQueueRequest,
)
from llumnix.connection_pool import ConnectionType, LruConnectionPool
from llumnix.constants import ZMQ_RPC_TIMEOUT_SECOND, ZMQ_IO_THREADS
from llumnix.logging.logger import init_logger

logger = init_logger(__name__)


class ZmqClient(BaseQueueClient):
    def __init__(self):
        super().__init__()
        self.context = zmq.asyncio.Context(ZMQ_IO_THREADS)
        self.socket_conn_pool = LruConnectionPool(
            connection_type=ConnectionType.ZMQ_SOCKET, context=self.context)

    async def close(self):
        await self.socket_conn_pool.close_all()
        self.context.destroy()

    # pylint: disable=arguments-differ
    async def put_nowait(self, server_addr: str, item: Any):
        queue_request = RPCPutNoWaitQueueRequest(item=item, send_time=time.perf_counter())
        await self._send_one_way_rpc_request(
            request_type=RPCRequestType.PUT_NOWAIT,
            request=queue_request,
            ip=server_addr.split(":")[0],
            port=int(server_addr.split(":")[1]),
            error_message="Unable to put item into queue."
        )

    # not used
    async def put_nowait_batch(self, server_addr: str, items: Iterable):
        batch_queue_request = RPCPutNoWaitBatchQueueRequest(items=items, send_time=time.perf_counter())
        await self._send_one_way_rpc_request(
            request_type=RPCRequestType.PUT_NOWAIT_BATCH,
            request=batch_queue_request,
            ip=server_addr.split(":")[0],
            port=int(server_addr.split(":")[1]),
            error_message="Unable to put items into queue."
        )

    async def _send_one_way_rpc_request(
        self,
        request_type: RPCRequestType,
        request: Any,
        ip: str,
        port: int,
        error_message: str,
    ):
        async def do_rpc_call(socket: zmq.asyncio.Socket, request_type: RPCRequestType, request: Any):
            await socket.send_multipart([request_type.value, cloudpickle.dumps(request)])
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
