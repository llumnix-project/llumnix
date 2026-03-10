import asyncio
import copy
from typing import Optional
import threading

from vllm.v1.engine import EngineCoreOutputs

from llumnix.constants import (
    INIT_LLUMLET_STUB_TIMEOUT,
    OUTPUT_QUEUE_CLIENT_CLOSE_TIMEOUT,
    OUTPUT_FORWARDER_CLOSE_TIMEOUT,
)
from llumnix.outputs.forwarder.base_output_forwarder import BaseOutputForwarder
from llumnix.outputs.queue.zmq_client import ZmqClient
from llumnix.outputs.request_output import LlumnixRequestOutputs
from llumnix.utils import start_asyncio_thread
from llumnix.logging.logger import init_logger
from llumnix.instance_info import InstanceStatus

logger = init_logger(__name__)


class ThreadOutputForwarder(BaseOutputForwarder):
    """
    Forwarding output tokens to API Servers and RPC results to Llumlets in a separate thread.
    """

    def __init__(self, instance_id: str, llumlet_grpc_address: str, engine_index: int):
        super().__init__(llumlet_grpc_address)

        self.instance_id = instance_id
        self.llumlet_grpc_address = llumlet_grpc_address
        self.engine_index = engine_index

        self.main_loop = asyncio.get_event_loop()
        self.output_queue_client = ZmqClient()

        self.forwarder_loop = start_asyncio_thread("thread_output_forwarder")
        self.forward_status_lock = threading.Lock()
        future = asyncio.run_coroutine_threadsafe(
            self._connect_llumlet(), self.forwarder_loop
        )
        try:
            future.result(timeout=INIT_LLUMLET_STUB_TIMEOUT)
        except TimeoutError as e:
            self.stop()
            raise RuntimeError(
                "ThreadOutputForwarder background initialization failed."
            ) from e

    async def _connect_llumlet(self):
        await self.connect_llumlet()

    def forward_outputs(
        self,
        outputs: Optional[dict[int, EngineCoreOutputs]],
    ) -> None:
        if not outputs:
            return
        asyncio.run_coroutine_threadsafe(
            self._put_nowait_to_servers(outputs),
            self.forwarder_loop,
        )

    async def _put_nowait_to_servers(
        self,
        outputs: Optional[dict[int, EngineCoreOutputs]],
    ) -> None:
        try:
            request_outputs: dict[str, LlumnixRequestOutputs] = {}
            for engine_core_outputs in outputs.values():
                if len(engine_core_outputs.outputs) == 0:
                    continue
                engine_core_outputs.engine_index = self.engine_index

                multi_clients_for_engine_index = False
                previous_queue_server_addr = None
                for engine_core_output in engine_core_outputs.outputs:
                    if previous_queue_server_addr is None:
                        previous_queue_server_addr = (
                            engine_core_output.queue_server_address
                        )
                    elif (
                        previous_queue_server_addr
                        != engine_core_output.queue_server_address
                    ):
                        multi_clients_for_engine_index = True
                        break

                if multi_clients_for_engine_index:
                    base_engine_core_output = EngineCoreOutputs(
                        finished_requests=engine_core_outputs.finished_requests,
                        scheduler_stats=engine_core_outputs.scheduler_stats,
                    )

                    for engine_core_output in engine_core_outputs.outputs:
                        queue_server_addr = engine_core_output.queue_server_address
                        llumnix_outputs = request_outputs.get(queue_server_addr, None)
                        if llumnix_outputs is None:
                            llumnix_outputs = LlumnixRequestOutputs(
                                self.instance_id,
                                self.llumlet_grpc_address,
                                copy.deepcopy(base_engine_core_output),
                            )
                            request_outputs[queue_server_addr] = llumnix_outputs
                        llumnix_outputs.engine_outputs.outputs.append(
                            engine_core_output
                        )
                else:
                    queue_server_addr = engine_core_outputs.outputs[
                        0
                    ].queue_server_address
                    request_outputs[queue_server_addr] = LlumnixRequestOutputs(
                        self.instance_id, self.llumlet_grpc_address, engine_core_outputs
                    )

            requests_to_abort = await self.put_nowait_to_servers_func(
                self.output_queue_client, request_outputs
            )
            if len(requests_to_abort) > 0:
                await self.abort_to_llumlet_func(requests_to_abort)
        # pylint: disable=broad-except
        except Exception:
            logger.exception(
                "Exception in ThreadOutputForwarder._put_nowait_to_servers"
            )

    def forward_status(
        self,
        instance_status: InstanceStatus,
    ) -> None:
        asyncio.run_coroutine_threadsafe(
            self._send_to_llumlet(instance_status),
            self.forwarder_loop,
        )

    async def _send_to_llumlet(
        self,
        instance_status: InstanceStatus,
    ) -> bool:
        try:
            # Copy status in sub thread to avoid deep copy overhead in main process.
            # Use threading lock to avoid main process updating status when sub thread is copying status.
            with self.forward_status_lock:
                instance_status_copy = copy.deepcopy(instance_status)
            success = await self.send_to_llumlet_func(instance_status_copy)
            return success
        # pylint: disable=broad-except
        except Exception:
            logger.exception("Exception in ThreadOutputForwarder._send_to_llumlet")
            return False

    def stop(self) -> None:
        if self.forwarder_loop.is_running():
            future = asyncio.run_coroutine_threadsafe(
                self.output_queue_client.close(), self.forwarder_loop
            )
            try:
                future.result(timeout=OUTPUT_QUEUE_CLIENT_CLOSE_TIMEOUT)
            # pylint: disable=broad-except
            except Exception as e:
                logger.exception("Failed to close output_queue_client: %s", e)
            future = asyncio.run_coroutine_threadsafe(self.close(), self.forwarder_loop)
            try:
                future.result(timeout=OUTPUT_FORWARDER_CLOSE_TIMEOUT)
            # pylint: disable=broad-except
            except Exception as e:
                logger.exception("Failed to close base_output_forwarder: %s", e)

        self.forwarder_loop.call_soon_threadsafe(self.forwarder_loop.stop)
