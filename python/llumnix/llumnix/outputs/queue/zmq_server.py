import asyncio
import time
from typing import Coroutine, Any
from typing_extensions import Never

import zmq
import zmq.asyncio
import cloudpickle

from llumnix.outputs.queue.base_queue_server import BaseQueueServer
from llumnix.outputs.queue.zmq_utils import (
    RPC_SUCCESS_STR,
    RPCPutNoWaitQueueRequest,
    RPCPutNoWaitBatchQueueRequest,
    RPCRequestType,
    RPCQueueEmptyError,
    RPCQueueFullError,
)
from llumnix.constants import (
    RPC_SOCKET_LIMIT_CUTOFF,
    RPC_ZMQ_HWM,
    RETRY_BIND_ADDRESS_INTERVAL,
    MAX_BIND_ADDRESS_RETRIES,
    ZMQ_IO_THREADS,
    ZMQ_RPC_TIMEOUT_SECOND,
)
from llumnix.utils import get_ip_address, get_free_port
from llumnix.connection_pool import get_open_zmq_ipc_path
from llumnix.logging.logger import init_logger

logger = init_logger(__name__)


class ZmqServer(BaseQueueServer):
    def __init__(self, ip: str, port: int = None, maxsize: int = 0):
        super().__init__()
        self.ip = ip
        self.port = port or get_free_port()
        rpc_path = get_open_zmq_ipc_path(ip, self.port)

        self.context: zmq.asyncio.Context = zmq.asyncio.Context(ZMQ_IO_THREADS)

        # Maximum number of sockets that can be opened (typically 65536).
        # ZMQ_SOCKET_LIMIT (http://api.zeromq.org/4-2:zmq-ctx-get)
        socket_limit = self.context.get(zmq.constants.SOCKET_LIMIT)
        if socket_limit < RPC_SOCKET_LIMIT_CUTOFF:
            raise ValueError(
                f"Found zmq.constants.SOCKET_LIMIT={socket_limit}, which caps "
                "the number of concurrent requests Llumnix can process."
            )

        # We only have 1 ipc connection that uses unix sockets, so
        # safe to set MAX_SOCKETS to the zmq SOCKET_LIMIT (i.e. will
        # not run into ulimit issues)
        self.context.set(zmq.constants.MAX_SOCKETS, socket_limit)
        self.socket = self.context.socket(zmq.ROUTER)
        self.socket.set_hwm(RPC_ZMQ_HWM)

        for attempt in range(MAX_BIND_ADDRESS_RETRIES):
            try:
                self.socket.bind(rpc_path)
                logger.info("QueueServer's socket bind to: {}".format(rpc_path))
                break
            # pylint: disable=broad-except
            except Exception as e:
                logger.error("Failed to bind QueueServer's socket to {}, exception: {}.".format(rpc_path, e))
                if attempt < MAX_BIND_ADDRESS_RETRIES - 1:
                    logger.warning("The rpc path {} is already in use, sleep {}s, and retry bind to it again."
                                   .format(rpc_path, RETRY_BIND_ADDRESS_INTERVAL))
                    time.sleep(RETRY_BIND_ADDRESS_INTERVAL)
                else:
                    logger.error("The rpc path {} is still in use after {} times retries."
                                 .format(rpc_path, MAX_BIND_ADDRESS_RETRIES))
                    raise

        self.maxsize = maxsize
        self.queue = asyncio.Queue(maxsize)

    async def run_server_loop(self):
        running_tasks = set()
        while True:
            identity, request_type, request = await self.socket.recv_multipart()
            task = asyncio.create_task(
                self._make_handler_coro(identity, request_type, request)
            )
            # We need to keep around a strong reference to the task,
            # to avoid the task disappearing mid-execution as running tasks
            # can be GC'ed. Below is a common "fire-and-forget" tasks
            # https://docs.python.org/3/library/asyncio-task.html#asyncio.create_task
            running_tasks.add(task)
            task.add_done_callback(running_tasks.discard)

    def stop(self):
        self.socket.close()
        self.context.destroy()

    async def put(self, item, timeout=None):
        try:
            await asyncio.wait_for(self.queue.put(item), timeout=timeout)
        except asyncio.TimeoutError as e:
            raise RPCQueueFullError from e

    async def get(self, timeout=None):
        try:
            return await asyncio.wait_for(self.queue.get(), timeout=timeout)
        except asyncio.TimeoutError as e:
            raise RPCQueueEmptyError from e

    def put_nowait(self, item):
        self.put_nowait_batch(list([item]))

    def put_nowait_batch(self, items):
        # If maxsize is 0, queue is unbounded, so no need to check size.
        if self.maxsize > 0 and len(items) + self.qsize > self.maxsize:
            raise RPCQueueFullError("Cannot add {} items to queue of size {} "
                "and maxsize {}.".format(len(items), self.qsize, self.maxsize))
        for item in items:
            self.queue.put_nowait(item)

    def get_nowait(self):
        return self.get_nowait_batch(num_items=1)

    def get_nowait_batch(self, num_items):
        if num_items > self.qsize:
            raise RPCQueueEmptyError(
                f"Cannot get {num_items} items from queue of size " f"{self.qsize}."
            )
        return [self.queue.get_nowait() for _ in range(num_items)]

    def _make_handler_coro(
        self,
        identity,
        request_type,
        request,
    ) -> Coroutine[Any, Any, Never]:
        request = cloudpickle.loads(request)
        if request_type == RPCRequestType.HANDSHAKE.value:
            return self._is_server_ready(identity)
        if request_type == RPCRequestType.PUT_NOWAIT.value:
            return self._put_nowait(identity, request)
        if request_type == RPCRequestType.PUT_NOWAIT_BATCH.value:
            return self._put_nowait_batch(identity, request)

        logger.error("Unknown RPCRequest type: {}".format(request_type))
        return None

    async def _send_response(self, identity, response_error=True):
        try:
            await asyncio.wait_for(
                self.socket.send_multipart([identity, cloudpickle.dumps(RPC_SUCCESS_STR)]),
                timeout=ZMQ_RPC_TIMEOUT_SECOND,
            )
        # pylint: disable=broad-except
        except Exception as e:
            self._log_exception(e)
            if response_error:
                try:
                    await asyncio.wait_for(
                        self.socket.send_multipart([identity, cloudpickle.dumps(e)]),
                        timeout=ZMQ_RPC_TIMEOUT_SECOND,
                    )
                # pylint: disable=broad-except
                except Exception as ex:
                    self._log_exception(ex)

    async def _is_server_ready(self, identity):
        await self._send_response(identity, response_error=False)

    async def _put_nowait(
        self,
        identity,
        put_nowait_queue_request: RPCPutNoWaitQueueRequest,
    ) -> None:
        # Server does not die when encoutering exception during sending message to client.
        # Server handles exception inside, while client raises exception to outside.
        item = put_nowait_queue_request.item
        self.put_nowait(item)
        await self._send_response(identity)

    async def _put_nowait_batch(
        self,
        identity,
        put_nowait_batch_queue_request: RPCPutNoWaitBatchQueueRequest,
    ) -> None:
        items = put_nowait_batch_queue_request.items
        self.put_nowait_batch(items)
        await self._send_response(identity)

    def _log_exception(self, e: Exception):
        if isinstance(e, asyncio.TimeoutError):
            logger.error("Zmq server send response to zmq client timeout (host: {})."
                         .format(get_ip_address()))
        else:
            logger.exception("Error in zmq server send response to zmq client (host: {})"
                             .format(get_ip_address()))

    @property
    def server_address(self):
        return "{}:{}".format(self.ip, self.port)

    @property
    def qsize(self):
        return self.queue.qsize()
