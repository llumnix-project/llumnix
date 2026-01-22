import asyncio
from collections.abc import Awaitable
from concurrent.futures import Future
import hashlib
import os
from typing import Any, Callable, List, Optional, Union
import weakref

from overrides import override

from vllm.config import VllmConfig
from vllm.v1.engine import (EngineCoreOutputs, UtilityOutput)
from vllm.v1.engine.core_client import AsyncMPClient
from vllm.version import __version__ as VLLM_VERSION
from llumnix.compat.vllm_compat import get_ip, get_open_zmq_ipc_path

from llumnix.engine_client.base_engine_client import BaseEngineClient
from llumnix.llumlet.instance_info import InstanceType, InstanceStatus
from llumnix.logging.logger import init_logger
from llumnix.utils import MigrationParams, RequestIDType, get_ip_address
from llumnix import envs as llumnix_envs


AnyFuture = Union[asyncio.Future[Any], Future[Any]]
logger = init_logger(__name__)


class VLLMEngineClient(BaseEngineClient, AsyncMPClient):
    """vLLM enginecore client."""

    def __init__(
        self,
        vllm_config: VllmConfig,
        log_stats: bool = True,
        executor_class: Any = None,
        client_addresses: Optional[dict[str, str]] = None,
        client_index: int = 1,
    ):
        assert client_addresses is not None
        # data_parallel_size_local should always less than
        # len(engine_ranks). Set its value to 1, otherwise
        # an AssertError will occur.
        data_parallel_size_local = vllm_config.parallel_config.data_parallel_size_local
        vllm_config.parallel_config.data_parallel_size_local = 1
        AsyncMPClient.__init__(
            self,
            vllm_config=vllm_config,
            executor_class=executor_class,
            log_stats=log_stats,
            client_addresses=client_addresses,
        )
        self.client_index = client_index
        # Reset data_parallel_rank_local
        vllm_config.parallel_config.data_parallel_size_local = data_parallel_size_local

    @override
    def _ensure_output_queue_task(self):
        """
        override to replace utility output processor. Llumnix does not use the default
        utility output processor in AsyncMPClient.
        """
        resources = self.resources
        if resources.output_queue_task is not None:
            return

        decoder = self.decoder
        utility_results = self.utility_results
        outputs_queue = self.outputs_queue
        output_handler: Optional[Callable[[AsyncMPClient, EngineCoreOutputs],
                                        Awaitable[None]]] = getattr(
                                            self.__class__,
                                            "process_engine_outputs", None)
        _self_ref = weakref.ref(self) if output_handler else None   # pylint: disable=invalid-name
        output_socket = resources.output_socket
        assert output_socket is not None

        async def process_outputs_socket():
            try:
                while True:
                    frames = await output_socket.recv_multipart(copy=False)
                    resources.validate_alive(frames)
                    outputs: EngineCoreOutputs = decoder.decode(frames)
                    # Replace utility output processor
                    if outputs.utility_output:
                        _llumnix_process_utility_output(
                            outputs.utility_output,
                            utility_results
                        )
                        continue

                    if output_handler is not None:
                        assert _self_ref is not None
                        self_instance = _self_ref() # pylint: disable=not-callable
                        if not self_instance:
                            return
                        if callable(output_handler):
                            await output_handler(self_instance, outputs)  # pylint: disable=not-callable
                        else:
                            logger.warning("output_handler is NOT callable. Type: %s. Skipping call.",type(output_handler))

                    if outputs.outputs or outputs.scheduler_stats:
                        outputs_queue.put_nowait(outputs)
            except Exception as e:   # pylint: disable=broad-except
                outputs_queue.put_nowait(e)

        resources.output_queue_task = asyncio.create_task(
            process_outputs_socket(), name="EngineCoreOutputQueueTask_VLLM")

    async def get_instance_status(self) -> dict[str, InstanceStatus]:
        """Get instance information from the engine core."""
        res = await self.call_utility_async("get_instance_status")
        if llumnix_envs.LLUMNIX_VLLM_BRANCH == "kvs-dev" or VLLM_VERSION.startswith("0.9"):
            instance_status = res
        else:
            instance_status = res.result
        return instance_status

    async def migrate_out(self, dst_engine_host: str, dst_engine_port: int, migration_params: MigrationParams) -> bool:
        """Send migration request to engine core"""
        res = await self.call_utility_async("migrate_out", dst_engine_host, dst_engine_port, migration_params)
        if llumnix_envs.LLUMNIX_VLLM_BRANCH == "kvs-dev" or VLLM_VERSION.startswith("0.9"):
            return res
        return res.result

    async def abort(self, request_ids: List[RequestIDType]) -> None:
        await self.abort_requests_async(request_ids)

    async def migrate_in(self, serialized_data: bytes, _):
        res = await self.call_utility_async("migrate_in", serialized_data)
        if llumnix_envs.LLUMNIX_VLLM_BRANCH == "kvs-dev" or VLLM_VERSION.startswith("0.9"):
            return res
        return res.result


def _llumnix_process_utility_output(output: UtilityOutput,
                            utility_results: dict[int, AnyFuture]):
    """
    Set the result from a utility method in the waiting future, Check if the future
    has been cancelled first. Ignore cancelled futures.
    Do not raise exceptions from this function, otherwise the output queue task will exit.
    """
    future = utility_results.pop(output.call_id)
    if future.cancelled():
        logger.warning("llumnix utility future (call_id %s) is cancelled.", output.call_id)
        return
    if output.failure_message is not None:
        try:
            future.set_exception(Exception(output.failure_message))
        except asyncio.InvalidStateError:
            logger.error("Failed to set exception on future (call_id: %s).", output.call_id)
    else:
        try:
            future.set_result(output.result)
        except asyncio.InvalidStateError:
            logger.error("Failed to set result on future (call_id: %s).", output.call_id)


def get_host_ip(ifname: str = None) -> str:
    return get_ip_address(ifname=ifname)

def get_kvt_port(cfg: VllmConfig) -> int:
    if cfg.kv_transfer_config:
         # pylint: disable=import-outside-toplevel
        from vllm.v1.hybrid_connector.engine_proxy import sched_rpc_server_port
        return sched_rpc_server_port(cfg)
    return -1

def gen_unit_id(vllm_config: VllmConfig) -> str:
    parallel_config = vllm_config.parallel_config
    enable_dp = parallel_config.data_parallel_size
    if not enable_dp:
        unit_id = vllm_config.instance_id
    else:
        dp_master_ip_port = \
            f"{parallel_config.data_parallel_master_ip}:{parallel_config.data_parallel_master_port}"
        unit_id = hashlib.md5(dp_master_ip_port.encode()).hexdigest()

    return unit_id


def vllm_get_instance_meta_data(cfg: VllmConfig) -> dict:
    instance_id = cfg.instance_id
    unit_id = gen_unit_id(cfg)
    if not cfg.kv_transfer_config:
        instance_type = InstanceType.NEUTRAL.value
    else:
        if cfg.kv_transfer_config.kv_role == "kv_producer":
            instance_type = InstanceType.PREFILL.value
        elif cfg.kv_transfer_config.kv_role == "kv_consumer":
            instance_type = InstanceType.DECODE.value
        elif cfg.kv_transfer_config.kv_role == "kv_both":
            instance_type = InstanceType.NEUTRAL.value

    try:
        api_server_port = int(os.getenv("LLUMNIX_VLLM_API_SERVER_PORT"))
    except Exception as e:
        logger.exception("Can not parse api_server from envs var, Can not be scheduled")
        raise SystemExit() from e
    metadata = {
        "instance_id": instance_id,
        "ip": get_ip_address("eth0"), # Use the IP of eth0 to ensure that llm-gateway can reach the API server.
        "ip_kvs": get_host_ip(),
        "instance_type": instance_type,
        "unit_id": unit_id,
        "api_server_port": api_server_port,
        "max_num_batched_tokens": cfg.scheduler_config.max_num_batched_tokens,
        "block_size": cfg.cache_config.block_size,
    }
    if cfg.kv_transfer_config:
        if cfg.kv_transfer_config.kv_connector=='HybridConnector':
            metadata["kvt_port"] = get_kvt_port(cfg)
            ip_kvt = get_ip()
            if isinstance(ip_kvt, tuple):
                ip_kvt = ip_kvt[0]
            metadata['ip_kvt'] = ip_kvt
        else:
            metadata["kvt_port"] = -1
            metadata['ip_kvt'] = ''
    if cfg.parallel_config.data_parallel_rank:
        metadata["dp_rank"] = cfg.parallel_config.data_parallel_rank
    if cfg.parallel_config.data_parallel_rank_local:
        metadata["dp_local_rank"] = cfg.parallel_config.data_parallel_rank_local
    if cfg.parallel_config.data_parallel_size:
        metadata["data_parallel_size"] = cfg.parallel_config.data_parallel_size
        if cfg.parallel_config.data_parallel_size > 1:
            instance_id = instance_id+'_dp'+str(cfg.parallel_config.data_parallel_rank)
    metadata["instance_id"] = instance_id
    metadata["tensor_parallel_size"] = cfg.parallel_config.tensor_parallel_size

    logger.info("Llumnix Instance metadata: %s", metadata)

    return metadata


def vllm_get_addresses() -> dict[str, str]:
    input_addresses = get_open_zmq_ipc_path()
    output_addresses = get_open_zmq_ipc_path()
    client_addresses = {
        "input_address": input_addresses,
        "output_address": output_addresses
    }
    return client_addresses
