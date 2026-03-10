import asyncio
from abc import ABC, abstractmethod
from functools import partial
from typing import Dict, List

import grpc

from llumnix.outputs.queue.base_queue_client import BaseQueueClient
from llumnix.outputs.request_output import LlumnixRequestOutputsType
from llumnix.utils import RequestIDType, UpdateInstanceStatusMode
from llumnix.logging.logger import init_logger
from llumnix.server.proto import llumlet_server_pb2_grpc
from llumnix.converters import to_cms_status
from llumnix.instance_info import InstanceStatus
from llumnix import envs
from llumnix.server.proto.llumlet_server_pb2 import AbortRequests
from llumnix.constants import GRPC_TIMEOUT

logger = init_logger(__name__)


class BaseOutputForwarder(ABC):
    def __init__(self, llumlet_grpc_address: str):
        self.llumlet_grpc_address = llumlet_grpc_address
        self.update_instance_status_mode = envs.LLUMNIX_UPDATE_INSTANCE_STATUS_MODE
        self._llumlet_abort_channel: grpc.aio.Channel = None
        self._llumlet_abort_stub: llumlet_server_pb2_grpc.LlumletStub = None
        if self.update_instance_status_mode == UpdateInstanceStatusMode.PUSH:
            self._llumlet_status_channel: grpc.aio.Channel = None
            self._llumlet_status_stub: llumlet_server_pb2_grpc.LlumletStub = None
        else:
            self._llumlet_status_channel = None
            self._llumlet_status_stub = None

    @abstractmethod
    def stop(self):
        raise NotImplementedError

    async def connect_llumlet(self):
        if self.update_instance_status_mode == UpdateInstanceStatusMode.PUSH:
            self._llumlet_status_channel = grpc.aio.insecure_channel(
                self.llumlet_grpc_address
            )
            self._llumlet_status_stub = llumlet_server_pb2_grpc.LlumletStub(
                self._llumlet_status_channel
            )
        self._llumlet_abort_channel = grpc.aio.insecure_channel(
            self.llumlet_grpc_address
        )
        self._llumlet_abort_stub = llumlet_server_pb2_grpc.LlumletStub(
            self._llumlet_abort_channel
        )

    async def put_nowait_to_servers_func(
        self,
        output_queue_client: BaseQueueClient,
        request_outputs: Dict[str, LlumnixRequestOutputsType],
    ) -> List[RequestIDType]:
        tasks = []
        aborted_request_ids = []

        def output_done_callback(
            server_addr: str, req_outputs: LlumnixRequestOutputsType, fut
        ):
            ret = fut.result()[0]
            if isinstance(ret, Exception):
                logger.exception(
                    "Server (queue_server_address: %s) is dead.",
                    server_addr,
                    exc_info=ret,
                )
                for req_output in req_outputs:
                    aborted_request_ids.append(req_output.request_id)

        for server_addr, req_outputs in request_outputs.items():
            task = asyncio.gather(
                output_queue_client.put_nowait(server_addr, req_outputs),
                return_exceptions=True,
            )
            task.add_done_callback(
                partial(output_done_callback, server_addr, req_outputs)
            )
            tasks.append(task)
        await asyncio.gather(*tasks)

        return aborted_request_ids

    async def send_to_llumlet_func(
        self,
        instance_status: InstanceStatus,
    ) -> bool:
        try:
            cms_instance_status = to_cms_status(instance_status)
            await asyncio.wait_for(
                self._llumlet_status_stub.PushInstanceStatus(cms_instance_status),
                timeout=GRPC_TIMEOUT,
            )
            return True
        except asyncio.TimeoutError:
            logger.error("Push instance status to llumlet timeout")
            return False
        except grpc.aio.AioRpcError as e:
            logger.error(
                "Push instance status to llumlet, gRPC call failed: %s - %s",
                e.code(),
                e.details(),
            )
            return False
        except Exception as e:  # pylint: disable=broad-except
            logger.exception(
                "Push instance status to llumlet failed, unexpected exception: %s", e
            )
            return False

    async def abort_to_llumlet_func(self, request_ids: List[RequestIDType]) -> bool:
        try:
            await asyncio.wait_for(
                self._llumlet_abort_stub.Abort(AbortRequests(request_ids=request_ids)),
                timeout=GRPC_TIMEOUT,
            )
            return True
        except asyncio.TimeoutError:
            logger.error("Abort to llumlet timeout, request_ids: %s", request_ids)
            return False
        except grpc.aio.AioRpcError as e:
            logger.error(
                "Abort to llumlet, gRPC call failed: %s - %s, request_ids: %s",
                e.code(),
                e.details(),
                request_ids,
            )
            return False
        except Exception as e:  # pylint: disable=broad-except
            logger.exception("Abort to llumlet failed, unexpected exception: %s", e)
            return False

    async def close(self):
        if self.update_instance_status_mode == UpdateInstanceStatusMode.PUSH:
            try:
                await self._llumlet_status_channel.close()
            # pylint: disable=broad-except
            except Exception as e:
                logger.exception("Error closing llumlet status channel: %s", e)
            finally:
                self._llumlet_status_channel = None
                self._llumlet_status_stub = None
        try:
            await self._llumlet_abort_channel.close()
        except Exception as e:  # pylint: disable=broad-except
            logger.exception("Error closing llumlet abort channel: %s", e)
        finally:
            self._llumlet_abort_channel = None
            self._llumlet_abort_stub = None
