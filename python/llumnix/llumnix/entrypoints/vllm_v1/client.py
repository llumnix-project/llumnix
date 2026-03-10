import asyncio
from collections import defaultdict
from typing import Dict, List, Tuple, Optional

import msgspec
from vllm.config import VllmConfig
from vllm.v1.engine import (
    EngineCoreRequestType,
    EngineCoreRequest,
    EngineCoreOutput,
    EngineCoreOutputs,
    FinishReason,
)
from vllm.v1.engine.core_client import AsyncMPClient, DPAsyncMPClient, DPLBAsyncMPClient
from vllm.v1.engine.output_processor import OutputProcessor
from vllm.v1.executor.abstract import Executor

from llumnix.outputs.request_output import LlumnixRequestOutputs
from llumnix.entrypoints.base_client import BaseLlumnixClient
from llumnix.utils import RequestIDType
from llumnix.logging.logger import init_logger

logger = init_logger(__name__)


def get_num_output_tokens(core_output: EngineCoreOutput) -> Optional[int]:
    num_output_tokens = None
    if isinstance(core_output.kv_transfer_params, dict):
        num_output_tokens = core_output.kv_transfer_params.get(
            "num_output_tokens", None
        )
    return num_output_tokens


class LlumnixClient(BaseLlumnixClient):
    # pylint: disable=unused-argument
    def __init__(
        self,
        loop: asyncio.AbstractEventLoop,
        output_processor: OutputProcessor,
        *args,
        **kwargs
    ):
        BaseLlumnixClient.__init__(self, loop)
        self.output_processor = output_processor
        self.core_output_stash: Dict[str, Tuple[List[EngineCoreOutput]]] = defaultdict(
            list
        )

    async def abort_requests_async(self, request_ids: list[str]) -> None:
        for request_id in request_ids:
            await self._abort(request_id)

    # ================== overriding from BaseLlumnixClient ==================
    async def get_request_outputs_loop(self):
        """Process output order and put EngineCoreOutputs to local queue"""
        while True:
            request_outputs: LlumnixRequestOutputs = (
                await self.output_queue_server.get()
            )
            llumlet_addr = request_outputs.llumlet_grpc_address
            requests_to_abort: List[RequestIDType] = []

            processed_outputs: List[EngineCoreOutput] = []
            for core_output in request_outputs.engine_outputs.outputs:
                request_id = core_output.request_id

                # In the case request was stopped due to 'stop string', output_processor will
                # remove request from output_processor.request_state first, and then abort request
                # in enginecore.
                if request_id in self.aborted_requests:
                    # The request is aborted, notify EngineCore later
                    requests_to_abort.append(request_id)
                    continue

                if request_id not in self.output_processor.request_states:
                    # Ignore output for already-aborted request
                    continue

                # update the latest instance_id for adapting migration scene
                if self.request_instances[request_id]:
                    self.request_instances[request_id][-1] = request_outputs.instance_id
                else:
                    self.request_instances[request_id].append(
                        request_outputs.instance_id
                    )

                processed_output = self._process_output_order(request_id, core_output)
                if not processed_output:
                    continue
                processed_outputs.extend(processed_output)
                last_output = processed_output[-1]
                self.request_num_output_tokens[request_id] = get_num_output_tokens(
                    last_output
                )
                if last_output.finished:
                    logger.info("Client finished request {}.".format(request_id))
                    self._clear_client_request_states(request_id)

            request_outputs.engine_outputs.outputs = processed_outputs
            if processed_outputs or request_outputs.engine_outputs.scheduler_stats:
                self.outputs_queue.put_nowait(request_outputs.engine_outputs)

            if requests_to_abort:
                # TODO(jjm): Design a non-blocking approach for the abort operation that ensures
                # output token processing is not blocked, and requests in 'requests_to_abort'
                # are not aborted multiple times.
                await self._call_llumlet_abort_requests(llumlet_addr, requests_to_abort)

    # pylint: disable=arguments-renamed
    def _process_output_order(
        self,
        request_id: str,
        core_output: EngineCoreOutput,
    ) -> List[EngineCoreOutput]:
        current_len = get_num_output_tokens(core_output)

        if not current_len:
            # No num_output_tokens info, return the core_output directly.
            return [core_output]

        num_new_tokens = len(core_output.new_token_ids)
        prev_len = self.request_num_output_tokens.get(request_id, 0)
        if current_len > prev_len + num_new_tokens:
            logger.info(
                "request-{} outputs are out of order. Previous output was at "
                "position {}, and now receiving {} new tokens, but correspond "
                "to position {}.".format(
                    request_id, prev_len, num_new_tokens, current_len
                )
            )
            self.core_output_stash[request_id].append(core_output)
            return []

        return [core_output]

    def cancel_dead_instance_requests(self, dead_instance_ids: List[str]) -> None:
        for dead_instance_id in dead_instance_ids:
            request_ids = self.instance_requests.get(dead_instance_id, [])
            llumnix_request_outputs = LlumnixRequestOutputs(
                instance_id=dead_instance_id,
                llumlet_grpc_address="",
                engine_outputs=EngineCoreOutputs(),
            )
            for request_id in request_ids:
                logger.info(
                    "Request {} is cancelled because instance {} is dead".format(
                        request_id, dead_instance_id
                    )
                )
                llumnix_request_outputs.engine_outputs.outputs.append(
                    EngineCoreOutput(
                        request_id=request_id,
                        new_token_ids=[],
                        finish_reason=FinishReason.ABORT,
                        stop_reason="Server internal error, please retry.",
                    )
                )
            if len(llumnix_request_outputs.engine_outputs.outputs) > 0:
                self.output_queue_server.put_nowait(llumnix_request_outputs)
            self.instance_requests.pop(dead_instance_id, None)

    def _clear_client_request_states(self, request_id: str):
        super()._clear_client_request_states(request_id)
        self.core_output_stash.pop(request_id, None)


class VLLMV1LlumnixClient(LlumnixClient, AsyncMPClient):
    # pylint: disable=unused-argument
    def __init__(
        self,
        loop: asyncio.AbstractEventLoop,
        output_processor: OutputProcessor,
        vllm_config: VllmConfig,
        executor_class: type[Executor],
        log_stats: bool,
        client_addresses: Optional[Dict[str, str]],
        *args,
        **kwargs
    ):
        LlumnixClient.__init__(self, loop, output_processor)

        client_count, client_index, driver_tensor_queue_union = args
        AsyncMPClient.__init__(
            self,
            vllm_config,
            executor_class,
            log_stats,
            client_addresses,
            client_count,
            client_index,
            driver_tensor_queue_union,
        )

    # ================== overriding from AsyncMPClient ==================
    async def add_request_async(self, request: EngineCoreRequest) -> None:
        request.client_index = self.client_index
        request.queue_server_address = self.output_queue_server.server_address
        await self._send_input(EngineCoreRequestType.ADD, request)
        self._ensure_output_queue_task()


class VLLMV1DPLlumnixClient(VLLMV1LlumnixClient, DPAsyncMPClient):
    def __init__(self, *args, **kwargs):
        self.current_wave = 0

        VLLMV1LlumnixClient.__init__(self, *args, **kwargs)


class VLLMV1CELlumnixClient(LlumnixClient, AsyncMPClient):
    def __init__(
        self,
        loop: asyncio.AbstractEventLoop,
        output_processor: OutputProcessor,
        vllm_config: VllmConfig,
        executor_class: type[Executor],
        log_stats: bool,
        client_addresses: Optional[Dict[str, str]],
        *args,
        **kwargs
    ):
        LlumnixClient.__init__(self, loop, output_processor)
        client_count, client_index = args
        AsyncMPClient.__init__(
            self,
            vllm_config,
            executor_class,
            log_stats,
            client_addresses,
            client_count,
            client_index,
        )

    # ================== overriding from AsyncMPClient ==================
    async def add_request_async(self, request: EngineCoreRequest) -> None:
        request.client_index = self.client_index
        request.queue_server_address = self.output_queue_server.server_address
        await self._send_input(EngineCoreRequestType.ADD, request)
        self._ensure_output_queue_task()


class VLLMV1DPCELlumnixClient(LlumnixClient, DPAsyncMPClient):
    def __init__(
        self,
        loop: asyncio.AbstractEventLoop,
        output_processor: OutputProcessor,
        vllm_config: VllmConfig,
        executor_class: type[Executor],
        log_stats: bool,
        client_addresses: Optional[Dict[str, str]],
        *args,
        **kwargs
    ):
        LlumnixClient.__init__(self, loop, output_processor)
        client_count, client_index = args
        DPAsyncMPClient.__init__(
            self,
            vllm_config,
            executor_class,
            log_stats,
            client_addresses,
            client_count,
            client_index,
        )

    # ================== overriding from DPAsyncMPClient ==================
    async def add_request_async(self, request: EngineCoreRequest) -> None:
        self._ensure_stats_update_task()
        request.current_wave = self.current_wave
        request.client_index = self.client_index
        chosen_engine = self.get_core_engine_for_request(request)
        request.queue_server_address = self.output_queue_server.server_address
        to_await = self._send_input(EngineCoreRequestType.ADD, request, chosen_engine)
        if not self.engines_running:
            # Notify coordinator that we're sending a request
            req_msg = msgspec.msgpack.encode(("FIRST_REQ", chosen_engine))
            await self.first_req_send_socket.send(req_msg)
        await to_await
        self._ensure_output_queue_task()


class VLLMV1CEDPLBLlumnixClient(LlumnixClient, DPLBAsyncMPClient):
    def __init__(
        self,
        loop: asyncio.AbstractEventLoop,
        output_processor: OutputProcessor,
        vllm_config: VllmConfig,
        executor_class: type[Executor],
        log_stats: bool,
        client_addresses: Optional[Dict[str, str]],
        *args,
        **kwargs
    ):
        LlumnixClient.__init__(self, loop, output_processor)
        client_count, client_index = args
        DPLBAsyncMPClient.__init__(
            self,
            vllm_config,
            executor_class,
            log_stats,
            client_addresses,
            client_count,
            client_index,
        )

    # ================== overriding from DPLBAsyncMPClient ==================
    async def add_request_async(self, request: EngineCoreRequest) -> None:
        self._ensure_stats_update_task()

        request.current_wave = self.current_wave
        request.client_index = self.client_index

        chosen_engine = self.get_core_engine_for_request(request)
        request.queue_server_address = self.output_queue_server.server_address
        to_await = self._send_input(EngineCoreRequestType.ADD, request, chosen_engine)
        if not self.engines_running:
            # Notify coordinator that we're sending a request
            req_msg = msgspec.msgpack.encode(("FIRST_REQ", chosen_engine))
            await self.first_req_send_socket.send(req_msg)
        await to_await

        self._ensure_output_queue_task()
