import hashlib
import uuid
import asyncio
import tempfile
import pickle
from collections import deque
from typing import Generic, Optional, Deque, TypeVar

import grpc
import zmq
import zmq.asyncio

from sglang.srt.utils import get_zmq_socket
from sglang.utils import TypeBasedDispatcher
from sglang.srt.managers.io_struct import (
    LlumletInstanceStatusReq,
    LlumletInstanceStatusReqOutput,
    MigrateOutReq,
    MigrateOutReqOutput,
    MigrateInReqOutput,
    AbortMigrateInReq,
    AbortMigrateInReqOutput,
)

from llumnix.engine_client.base_engine_client import BaseEngineClient
from llumnix.utils import MigrationParams, RequestIDType, get_ip_address
from llumnix.instance_info import InstanceType
from llumnix.logging.logger import init_logger
from llumnix.sglang_llumlet_proxy import SGLangConfig
from llumnix.server.proto import llumlet_server_pb2_grpc, llumlet_server_pb2

logger = init_logger(__name__)


class SGLangEngineClient(BaseEngineClient):
    def __init__(
        self,
        client_addresses: Optional[dict[str, str]] = None,
    ):
        context = zmq.asyncio.Context(2)

        self.send_to_scheduler = get_zmq_socket(
            context, zmq.PUSH, client_addresses["llumlet_to_scheduler_ipc_name"], False
        )
        self.recv_from_scheduler = get_zmq_socket(
            context, zmq.PULL, client_addresses["scheduler_to_llumlet_ipc_name"], False
        )

        self.get_internal_status_communicator = _RequestReplyCommunicator(
            self.send_to_scheduler
        )

        self.migrate_out_communicator = _RequestReplyCommunicator(
            self.send_to_scheduler
        )

        self.migrate_in_communicator = _RequestReplyCommunicator(self.send_to_scheduler)

        self.abort_migrate_in_communicator = _RequestReplyCommunicator(
            self.send_to_scheduler
        )

        self._result_dispatcher = TypeBasedDispatcher(
            [
                (
                    LlumletInstanceStatusReqOutput,
                    self.get_internal_status_communicator.handle_reply,
                ),
                (MigrateOutReqOutput, self.migrate_out_communicator.handle_reply),
                (MigrateInReqOutput, self.migrate_in_communicator.handle_reply),
                (AbortMigrateInReq, self._call_remote_abort_migate_in_req),
                (
                    AbortMigrateInReqOutput,
                    self.abort_migrate_in_communicator.handle_reply,
                ),
            ]
        )
        self._handle_loop_task: Optional[asyncio.Task] = None
        self.host_ip = get_ip_address()

    async def start(self):
        """Starts the background message handling loop."""
        if self._handle_loop_task is not None:
            return  # Already running
        self._handle_loop_task = asyncio.create_task(self._handle_loop())
        logger.info("SGLangSchedulerClient background handler started.")

    async def stop(self):
        """Stops the background loop gracefully."""
        if self._handle_loop_task is None:
            return

        self._handle_loop_task.cancel()
        try:
            await self._handle_loop_task
        except asyncio.CancelledError:
            pass
        finally:
            self._handle_loop_task = None
            logger.info("SGLangSchedulerClient background handler stopped.")

    # Handles results/messages returned from the scheduler.
    async def _handle_loop(self):
        logger.info("SGLangSchedulerClient handle_loop running...")
        try:
            while True:
                recv_obj = await self.recv_from_scheduler.recv_pyobj()
                if isinstance(recv_obj, AbortMigrateInReq):
                    await self._result_dispatcher(recv_obj)
                else:
                    self._result_dispatcher(recv_obj)
        except asyncio.CancelledError:
            logger.info("SGLangSchedulerClient handle_loop cancelled.")
            raise
        except Exception:  # pylint: disable=broad-except
            logger.exception("Error in SGLangSchedulerClient handle_loop")

    # Called by LlumletProc to send a LlumletInstanceStatusReq request to the scheduler.
    async def get_instance_status(self) -> dict:
        req = LlumletInstanceStatusReq()
        response: LlumletInstanceStatusReqOutput = (
            await self.get_internal_status_communicator(req)
        )
        return response.instance_status

    async def migrate_out(
        self,
        dst_engine_host: str,
        dst_engine_port: str,
        migration_params: MigrationParams,
    ) -> bool:
        logger.info(
            "Received migrate_out call. Migrating a request to %s:%s.",
            dst_engine_host,
            dst_engine_port,
        )
        try:
            migrate_out_req = MigrateOutReq(
                "1", dst_engine_host, dst_engine_port, self.host_ip, migration_params
            )
            logger.info(
                "Sending MigrateOutReq to the scheduler to select a request for migration."
            )
            migrate_out_req_output: MigrateOutReqOutput = (
                await self.migrate_out_communicator(migrate_out_req)
            )
            if not migrate_out_req_output.reqs:
                logger.warning("No requests available for migration")
                return True

            logger.info(
                "Received MigrateInReq from the scheduler, now send it to the destination's llumlet."
            )
            # 2. 序列化请求数据
            serialized_data, serialization_format = self._serialize_migrate_request(
                migrate_out_req_output
            )

            # 3. 发起RPC调用
            success = await self._call_remote_migrate_in(
                dst_engine_host, dst_engine_port, serialized_data, serialization_format
            )

            return success

        except grpc.RpcError as e:
            logger.error("gRPC error in migrate_out: %s", e, exc_info=True)
        except Exception as e:  # pylint: disable=broad-except
            logger.error("migrate_out failed: %s", e, exc_info=True)
        return False

    async def abort(self, request_ids: RequestIDType) -> None:
        # TODO Implementation would go here
        pass

    async def migrate_in(
        self, serialized_migrate_req: bytes, serialization_format: str
    ) -> None:
        try:
            # 根据序列化格式选择合适的反序列化方法
            if serialization_format == "pickle":
                migrate_out_req_output: MigrateOutReqOutput = pickle.loads(
                    serialized_migrate_req
                )
            else:
                raise ValueError(
                    "Unsupported serialization format: %s" % serialization_format
                )

            logger.info("Received MigrateInReq, now send it to the scheduler.")
            # 处理反序列化后的MigrateInReq对象
            response: MigrateInReqOutput = await self.migrate_in_communicator(
                migrate_out_req_output
            )
            if response.success:
                request_ids = [
                    migrate_req.req.rid for migrate_req in migrate_out_req_output.reqs
                ]
                logger.info(
                    "MigrateIn request for %s sent to scheduler successfully",
                    request_ids,
                )

        except Exception as e:
            logger.error("Failed to process migrate_in request: %s", e, exc_info=True)
            raise RuntimeError("Failed to process migrate_in request") from e

    async def abort_migrate_in(
        self, request_id: str, migrate_in_ip_address: str, migrate_in_port: str
    ) -> None:
        """Process abort migrate in request by sending to scheduler"""
        try:
            # 构造AbortMigrateInReq对象
            abort_migrate_in_req = AbortMigrateInReq(
                rid=request_id,
                migrate_in_ip_address=migrate_in_ip_address,
                migrate_in_port=migrate_in_port,
            )

            # 通过communicator发送给scheduler
            response: AbortMigrateInReqOutput = (
                await self.abort_migrate_in_communicator(abort_migrate_in_req)
            )
            if response.success:
                logger.info(
                    "AbortMigrateIn request for %s sent to scheduler successfully",
                    request_id,
                )

        except Exception as e:
            logger.error(
                "Failed to process abort_migrate_in request: %s", e, exc_info=True
            )
            raise RuntimeError("Failed to process abort_migrate_in request") from e

    def _serialize_migrate_request(self, migrate_in_req) -> tuple[bytes, str]:
        """Serialize MigrateInReq object"""
        try:
            serialized_data = pickle.dumps(migrate_in_req)
            serialization_format = "pickle"

            logger.debug(
                "Serialized migrate request, size: %s bytes", len(serialized_data)
            )
            return serialized_data, serialization_format

        except Exception as e:
            logger.error("Failed to serialize migrate request: %s", e)
            raise RuntimeError("Serialization failed: %s" % e) from e

    async def _call_remote_migrate_in(
        self,
        dst_engine_host: str,
        dst_engine_port: str,
        serialized_data: bytes,
        serialization_format: str,
    ) -> bool:
        """Call remote llumlet's MigrateIn RPC method"""

        # 构建目标地址
        target_address = "%s:%s" % (dst_engine_host, dst_engine_port)
        logger.info("Connecting to remote llumlet at %s", target_address)

        channel = None
        try:
            # 建立gRPC连接
            channel = grpc.aio.insecure_channel(target_address)
            stub = llumlet_server_pb2_grpc.LlumletStub(channel)

            # 构造请求消息
            request = llumlet_server_pb2.MigrateInRequest(
                serialized_migrate_req=serialized_data,
                serialization_format=serialization_format,
            )

            # 发起异步RPC调用
            response = await stub.MigrateIn(request)

            # 检查响应结果
            if response.success:
                logger.info("Migration request sent successfully: %s", response.message)
                return True

            logger.error("Migration request failed: %s", response.message)
            return False

        except grpc.aio.AioRpcError as e:
            logger.error("gRPC call failed: %s - %s", e.code(), e.details())
            return False
        except Exception as e:  # pylint: disable=broad-except
            logger.error("Unexpected error during RPC call: %s", e, exc_info=True)
            return False
        finally:
            # 确保连接被正确关闭
            if channel:
                await channel.close()

    async def _call_remote_abort_migate_in_req(self, abort_migrate_in_req):
        """Call remote llumlet's AbortMigrateIn RPC method"""

        # 构建目标地址
        target_address = "%s:%s" % (
            abort_migrate_in_req.migrate_in_ip_address,
            abort_migrate_in_req.migrate_in_port,
        )
        logger.info("Connecting to remote llumlet at %s", target_address)

        channel = None
        try:
            # 建立gRPC连接
            channel = grpc.aio.insecure_channel(target_address)
            stub = llumlet_server_pb2_grpc.LlumletStub(channel)

            # 构造请求消息
            request = llumlet_server_pb2.AbortMigrateInRequest(
                request_id=abort_migrate_in_req.rid,
                migrate_in_ip_address=abort_migrate_in_req.migrate_in_ip_address,
                migrate_in_port=abort_migrate_in_req.migrate_in_port,
            )

            # 发起异步RPC调用
            response = await stub.AbortMigrateIn(request)

            # 检查响应结果
            if response.success:
                logger.info(
                    "AbortMigrateIn request sent successfully: %s", response.message
                )
                return True

            logger.error("AbortMigrateIn request failed: %s", response.message)
            return False

        except grpc.aio.AioRpcError as e:
            logger.error("gRPC call failed: %s - %s", e.code(), e.details())
            return False
        except Exception as e:  # pylint: disable=broad-except
            logger.error("Unexpected error during RPC call: %s", e, exc_info=True)
            return False
        finally:
            # 确保连接被正确关闭
            if channel:
                await channel.close()


T = TypeVar("T")


class _RequestReplyCommunicator(Generic[T]):
    """
    A specialized communicator for a 1-to-1 request-reply pattern.
    It ensures that only one request is in-flight at any given time.
    """

    def __init__(self, sender):
        self._sender = sender
        self._result_event: Optional[asyncio.Event] = None
        self._result_value: Optional[T] = None
        self._ready_queue: Deque[asyncio.Event] = deque()

    async def __call__(self, obj: T) -> T:
        """Sends an object and waits for a single reply."""
        ready_event = asyncio.Event()
        if self._result_event is not None or self._ready_queue:
            self._ready_queue.append(ready_event)
            await ready_event.wait()

        # The try/finally block ensures state is cleaned up even if the
        # task is cancelled by a timeout.
        try:
            if obj:
                self._sender.send_pyobj(obj)

            self._result_event = asyncio.Event()
            self._result_value = None

            await self._result_event.wait()

            return self._result_value
        finally:
            # This cleanup is critical and runs regardless of how the try block exits.
            self._result_event = None
            self._result_value = None

            if self._ready_queue:
                self._ready_queue.popleft().set()

    def handle_reply(self, reply_obj: T):
        """
        Handles a received reply. This method is called by the receiver loop.
        """
        if self._result_event and not self._result_event.is_set():
            self._result_value = reply_obj
            self._result_event.set()
        else:
            # This is an important edge case to log. It means a reply arrived,
            # but no one was waiting for it (e.g., the request timed out).
            logger.warning(
                "Received a reply but no active request is waiting. "
                "This might be a late reply for a timed-out request. Reply ignored: %s",
                reply_obj,
            )


def sglang_get_instance_meta_data(cfg: SGLangConfig) -> dict:
    if cfg.instance_type == "prefill":
        instance_type = InstanceType.PREFILL.value
    elif cfg.instance_type == "decode":
        instance_type = InstanceType.DECODE.value
    else:
        instance_type = InstanceType.NEUTRAL.value
    if cfg.enable_dp_attention:
        dp_rank = cfg.attn_dp_rank
    else:
        if cfg.dp_rank is not None:
            dp_rank = cfg.dp_rank
        else:
            dp_rank = 0

    if cfg.dist_init_addr is not None:
        namespace_uuid = uuid.UUID(
            hashlib.sha1(instance_type.encode("utf-8")).hexdigest()[:32]
        )
        generated_uuid = uuid.uuid5(namespace_uuid, cfg.dist_init_addr)
    else:
        generated_uuid = uuid.uuid4()

    instance_id = str(generated_uuid) + "_dp" + str(dp_rank)

    metadata = {
        "instance_id": instance_id,
        "ip": get_ip_address(),
        "dp_rank": dp_rank,
        "dp_local_rank": cfg.attn_dp_rank,
        "data_parallel_size": cfg.dp_size,
        "ep_rank": cfg.moe_ep_rank,
        "instance_type": instance_type,
        "api_server_port": cfg.api_server_port,
    }
    return metadata


def sglang_get_addresses() -> dict[str, str]:
    # pylint: disable=consider-using-with
    llumlet_to_scheduler_ipc_name = (
        "ipc://%s" % tempfile.NamedTemporaryFile(delete=False).name
    )
    scheduler_to_llumlet_ipc_name = (
        "ipc://%s" % tempfile.NamedTemporaryFile(delete=False).name
    )

    addresses = {
        "llumlet_to_scheduler_ipc_name": llumlet_to_scheduler_ipc_name,
        "scheduler_to_llumlet_ipc_name": scheduler_to_llumlet_ipc_name,
    }
    return addresses
