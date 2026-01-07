import asyncio
import copy
import itertools
import struct
import threading
import time
import queue
from typing import List, Optional, Union
import uuid
from enum import Enum

import msgspec

from vllm.config import VllmConfig
from vllm.utils import cdiv
from vllm.v1.request import Request
from vllm.v1.core.sched.output import SchedulerOutput
from vllm.v1.engine import EngineCoreRequestType
from vllm.v1.hybrid_connector.engine_proxy import get_param

from llumnix import envs
from llumnix.constants import MIGRATION_FRONTEND_TIMEOUT, VLLM_MIGRATION_RETRIES
from llumnix.llumlet.instance_info import InstanceStatus
from llumnix.llumlet.llumlet import Llumlet
from llumnix.logging.logger import init_logger
from llumnix.outputs.forwarder.thread_output_forwarder import ThreadOutputForwarder
from llumnix.utils import (
    MigrationParams,
    RequestIDType,
    MigrationType,
    RequestMigrationPolicy,
    UpdateInstanceStatusMode,
)
# pylint: disable=ungrouped-imports
if envs.LLUMNIX_ENABLE_MIGRATION:
    from vllm.v1.hybrid_connector.engine_proxy import (
        MsgpackEncoder,
        PlaceholderModule,
        core_update_params,
        req2corereq,
    )
    from vllm.v1.hybrid_connector.kvtbackend import _get_inst_id, rpc_port, CODE_OK
    from vllm.v1.hybrid_connector.migration import (
        MIGRATE_TO_REQ,
        MIGRATE_TO_RESP,
        SRC_INFO, OUTPUT_TOKENS_N,
        _g_migrate_in_req_ids,
        _g_migrate_out_req_ids,
        _g_migrate_in_req_info,
        _g_migrate_out_req_info,
        _g_migrate_in_req_info_lock,
        _g_migrate_out_req_info_lock,
        MigrateResp,
    )
    from vllm.v1.hybrid_connector.utils import PeerManager, ConnManager

    try:
        import blade_kvt
        from blade_kvt.kv_transfer import connect_naming
    except ImportError:
        blade_kvt = PlaceholderModule("blade_kvt")
        connect_naming = blade_kvt.placeholder_attr("connect_naming")

VLLM_BRANCH = envs.LLUMNIX_VLLM_BRANCH
if VLLM_BRANCH == "kvs-dev":
    try:
        # pylint: disable=ungrouped-imports
        from vllm.utils import get_ip
    except ImportError:
        raise ImportError(f"Cannot import from vllm.utils (branch: {VLLM_BRANCH})") \
            from __import__('sys').exc_info()[1]
else:
    try:
        # pylint: disable=ungrouped-imports
        from vllm.utils.network_utils import get_ip
    except ImportError:
        raise ImportError(f"Cannot import from vllm.utils.network_utils (branch: {VLLM_BRANCH})") \
            from __import__('sys').exc_info()[1]

logger = init_logger(__name__)
step_id = 0
update_id = 0

def random_uuid() -> str:
    return str(uuid.uuid4().hex)


def init_instance_status_with_scheduler(scheduler):
    num_total_gpu_blocks = scheduler.cache_config.num_gpu_blocks
    return InstanceStatus(
        num_total_gpu_blocks=num_total_gpu_blocks,
        timestamp_ms=int(time.time() * 1000),
        step_id=0
    )


class StepPhase(str, Enum):
    STEP_BEGIN = "STEP_BEGIN"
    AFTER_SCHEDULE = "AFTER_SCHEDULE"
    AFTER_UPDATE = "AFTER_UPDATE"
    BEFORE_RETURN = "BEFORE_RETURN"


class VLLMLlumletProxy:
    def __init__(self, vllm_config: VllmConfig, scheduler: 'Scheduler', engine_index: int = 0): # type: ignore
        self.llumlet = Llumlet(engine_type="vLLM v1",
                               engine_config=vllm_config)
        self.llumlet.start()
        self.enable_migration = envs.LLUMNIX_ENABLE_MIGRATION
        # vllm does not support enable kvt and kvs simultaneously
        if vllm_config.kv_transfer_config and \
            vllm_config.kv_transfer_config.kv_connector_extra_config.get("backend") == "kvt+migration" and \
            self.enable_migration:
            self.migration_frontend = MigrationFrontend(
                vllm_config=vllm_config, dp_rank=vllm_config.parallel_config.data_parallel_rank, scheduler=scheduler)
        else:
            self.migration_frontend = None
        self.rpc_port = self.llumlet.rpc_port
        self.llumlet_grpc_address = "{}:{}".format(get_ip(), self.rpc_port)
        self.output_forwarder = ThreadOutputForwarder(
            vllm_config.instance_id,
            self.llumlet_grpc_address,
            engine_index
        )
        self.update_freq = envs.LLUMNIX_INSTANCE_UPDATE_STEPS
        self.scheduler = scheduler
        self.get_detailed_migration_status = envs.LLUMNIX_DETAILED_MIG_STATUS

        self.recent_waitings_set = set()
        self.recent_waitings_staleness_seconds = envs.LLUMNIX_RECENT_WAITINGS_STALENESS_SECONDS

        self.async_scheduling = vllm_config.scheduler_config.async_scheduling
        self.last_num_scheduled_tokens = None
        self.update_instance_status_mode = envs.LLUMNIX_UPDATE_INSTANCE_STATUS_MODE

    def add_llumlet_address(self, addresses: dict[str, str]) -> dict[str, str]:
        return self.llumlet.add_llumlet_addresses(addresses)

    def update_instance_status(
        self,
        instance_status: InstanceStatus,
        step_phase: StepPhase,
        scheduler_output: SchedulerOutput = None,
    ):
        try:
            global step_id
            global update_id
            if step_id % self.update_freq != 0:
                return
            if step_phase == StepPhase.AFTER_UPDATE:
                step_id += 1
            update_id += 1

            if self.update_instance_status_mode == UpdateInstanceStatusMode.PUSH:
                # pylint: disable=consider-using-with
                self.output_forwarder.forward_status_lock.acquire()
            if step_phase == StepPhase.STEP_BEGIN:
                # NOTE(sunbiao.sun):
                # Only update recent waitings status at the beginning of step,
                # which can ensure all the newly added requests can be recorded
                # rather than be finished in one step.
                self._update_recent_waitings_status(instance_status)
            else:
                self._update_waiting_status(instance_status)
                self._update_running_status(instance_status, step_phase, scheduler_output)
                self._update_instance_status(instance_status)
                # There is no need to push instance status in STEP_BEGIN step phase,
                # because the instance status will be pushed in AFTER_SCHEDULE step phase,
                # which is right after the STEP_BEGIN step phase.
                if self.update_instance_status_mode == UpdateInstanceStatusMode.PUSH:
                    self.forward_status(instance_status)
            if self.update_instance_status_mode == UpdateInstanceStatusMode.PUSH:
                self.output_forwarder.forward_status_lock.release()
        # pylint: disable=broad-except
        except Exception:
            logger.exception("Failed to update instance status.")

        return

    def _update_recent_waitings_status(
        self,
        instance_status: InstanceStatus,
    ) -> None:
        # NOTE(sunbiao.sun):
        # All the newly added requests will exist in waiting queue of scheduler or hybrid scheduler
        # at the begin of the step.
        all_waitings = list(self.scheduler.waiting)
        # _waiting: deque[tuple[Request, bool, bool]]
        if self.scheduler.connector is not None:
            # pylint: disable=protected-access
            all_waitings.extend([
                item[0] for item in self.scheduler.connector._sched._waiting
            ])

        for req in all_waitings:
            if req not in self.recent_waitings_set:
                self.recent_waitings_set.add(req)

        # Newly added requests will not be stale in the first step no matter how long the step takes,
        # because we only update recent waitings status at the beginning of step.
        for req in list(self.recent_waitings_set):
            if time.time() - req.arrival_time > self.recent_waitings_staleness_seconds:
                self.recent_waitings_set.remove(req)
        instance_status.recent_waiting_requests = [req.request_id for req in self.recent_waitings_set]

    def _update_waiting_status(
        self,
        instance_status: InstanceStatus,
    ) -> None:
        # NOTE(sunbiao.sun):
        # Waiting requests in scheduler or hybrid scheduler have not been allocated slots,
        # and therefore the number of blocks of these requests should be calculated manually rather than
        # simply read the number of blocks recorded in kv cache manager.
        all_waitings = list(self.scheduler.waiting)
        # _waiting: deque[tuple[Request, bool, bool]]
        if self.scheduler.connector is not None:
            # pylint: disable=protected-access
            all_waitings.extend([
                item[0] for item in self.scheduler.connector._sched._waiting
            ])

        block_size = self.scheduler.cache_config.block_size

        # calculate uncomputed tokens for prefills, calculate allocated blocks for decodes
        hybrid_scheduler_waiting_to_decode_requests_num = 0
        num_uncomputed_tokens_hybrid_scheduler_waiting_prefills = 0
        num_unallocated_blocks_hybrid_scheduler_waiting_decodes = 0
        num_uncomputed_tokens_scheduler_waiting_prefills = 0
        num_uncomputed_tokens_all_waiting_prefills = 0

        hybrid_scheduler_waiting_to_decode_tokens_num = 0
        scheduler_waiting_to_decode_requests_num = 0
        scheduler_waiting_to_decode_tokens_num = 0

        if self.scheduler.connector is not None:
            # pylint: disable=protected-access
            for req, load, save in self.scheduler.connector._sched._waiting:
                _, num_new_local_computed_tokens = \
                    self.scheduler.kv_cache_manager.get_computed_blocks(req)
                if save:
                    num_uncomputed_tokens_hybrid_scheduler_waiting_prefills += (req.num_tokens - num_new_local_computed_tokens)
                if load:
                    hybrid_scheduler_waiting_to_decode_requests_num += 1
                    num_unallocated_blocks_hybrid_scheduler_waiting_decodes += \
                        cdiv(req.num_tokens - num_new_local_computed_tokens, block_size)
                    hybrid_scheduler_waiting_to_decode_tokens_num += req.num_tokens

        # waiting requests is not scheduled requests, so there is no need to correct computed tokens.

        for req in self.scheduler.waiting:
            if req.num_tokens - req.num_computed_tokens > 1:
                _, num_new_local_computed_tokens = \
                    self.scheduler.kv_cache_manager.get_computed_blocks(req)
                num_uncomputed_tokens_scheduler_waiting_prefills += (req.num_tokens - num_new_local_computed_tokens)

            if (req.sampling_params is not None and req.sampling_params.max_tokens > 1) \
                or req.num_tokens - req.num_computed_tokens == 1:
                scheduler_waiting_to_decode_requests_num += 1
                scheduler_waiting_to_decode_tokens_num += req.num_tokens

        num_uncomputed_tokens_all_waiting_prefills = \
            num_uncomputed_tokens_hybrid_scheduler_waiting_prefills + num_uncomputed_tokens_scheduler_waiting_prefills

        num_uncomputed_blocks_hybrid_scheduler_waiting_prefills = \
            cdiv(num_uncomputed_tokens_hybrid_scheduler_waiting_prefills, block_size)
        num_uncomputed_blocks_scheduler_waiting_prefills = \
            cdiv(num_uncomputed_tokens_scheduler_waiting_prefills, block_size)
        num_uncomputed_blocks_all_waiting_prefills = \
            cdiv(num_uncomputed_tokens_all_waiting_prefills, block_size)

        hybrid_scheduler_waiting_to_decode_blocks_num = cdiv(hybrid_scheduler_waiting_to_decode_tokens_num, block_size)
        scheduler_waiting_to_decode_blocks_num = cdiv(scheduler_waiting_to_decode_tokens_num, block_size)

        instance_status.waiting_requests = [req.request_id for req in all_waitings]

        instance_status.num_waiting_requests = len(all_waitings)
        instance_status.hybrid_scheduler_waiting_to_decode_requests_num = hybrid_scheduler_waiting_to_decode_requests_num
        instance_status.num_uncomputed_blocks_hybrid_scheduler_waiting_prefills = num_uncomputed_blocks_hybrid_scheduler_waiting_prefills
        instance_status.num_unallocated_blocks_hybrid_scheduler_waiting_decodes = num_unallocated_blocks_hybrid_scheduler_waiting_decodes
        instance_status.num_uncomputed_blocks_scheduler_waiting_prefills = num_uncomputed_blocks_scheduler_waiting_prefills
        instance_status.num_uncomputed_blocks_all_waiting_prefills = num_uncomputed_blocks_all_waiting_prefills

        instance_status.hybrid_scheduler_waiting_to_decode_blocks_num = hybrid_scheduler_waiting_to_decode_blocks_num
        instance_status.scheduler_waiting_to_decode_requests_num = scheduler_waiting_to_decode_requests_num
        instance_status.scheduler_waiting_to_decode_blocks_num = scheduler_waiting_to_decode_blocks_num

    def _update_running_status(
        self,
        instance_status: InstanceStatus,
        step_phase: StepPhase,
        scheduler_output: SchedulerOutput = None,
    ) -> None:
        block_size = self.scheduler.cache_config.block_size

        num_uncomputed_tokens_scheduler_running_prefills = 0
        num_unallocated_tokens_scheduler_running_prefills = 0
        scheduler_running_to_decode_requests_num = 0
        scheduler_running_to_decode_tokens_num = 0

        # NOTE(sunbiao.sun):
        # In async scheduling and speculative decoding case,
        # num_tokens might be not completely correct due to pre-advanced output token ids, but the error is small thus acceptable.

        if self.scheduler.running:
            curr_num_scheduled_tokens = None
            if self.async_scheduling and self.last_num_scheduled_tokens is not None:
                curr_num_scheduled_tokens = self.last_num_scheduled_tokens
            elif step_phase == StepPhase.AFTER_SCHEDULE and scheduler_output is not None:
                curr_num_scheduled_tokens = scheduler_output.num_scheduled_tokens
            for req in self.scheduler.running:
                if not req.prefill_done:
                    num_unallocated_tokens = req.num_tokens - req.num_computed_tokens
                    num_uncomputed_tokens = num_unallocated_tokens
                    # The number of computed blocks is pre-advanced in schedule before it has been computed.
                    if step_phase == StepPhase.AFTER_SCHEDULE and scheduler_output is not None:
                        if req.request_id in scheduler_output.num_scheduled_tokens:
                            num_uncomputed_tokens += scheduler_output.num_scheduled_tokens[req.request_id]
                    # In async scheduling mode, the number of scheduled tokens in last schedule should be
                    # added back to the number of uncomputed tokens,
                    # because the tokens scheduled in last schedule has just begun computing,
                    # while the number of computed blocks is pre-advanced in schedule before it has been computed.
                    if self.async_scheduling and self.last_num_scheduled_tokens is not None:
                        if req.request_id in self.last_num_scheduled_tokens:
                            num_uncomputed_tokens += self.last_num_scheduled_tokens[req.request_id]
                    num_uncomputed_tokens_scheduler_running_prefills += num_uncomputed_tokens
                    num_unallocated_tokens_scheduler_running_prefills += num_unallocated_tokens
                else:
                    if curr_num_scheduled_tokens is not None and req.request_id in curr_num_scheduled_tokens and \
                        curr_num_scheduled_tokens[req.request_id] > 1:
                        num_uncomputed_tokens_scheduler_running_prefills += curr_num_scheduled_tokens[req.request_id]

                if req.sampling_params is not None and req.sampling_params.max_tokens > 1 \
                    and not get_param(req, "is_migrating"):
                    scheduler_running_to_decode_requests_num += 1
                    scheduler_running_to_decode_tokens_num += req.num_tokens

        num_uncomputed_blocks_scheduler_running_prefills = \
            cdiv(num_uncomputed_tokens_scheduler_running_prefills, block_size)
        num_unallocated_blocks_scheduler_running_prefills = \
            cdiv(num_unallocated_tokens_scheduler_running_prefills, block_size)

        scheduler_running_to_decode_blocks_num = cdiv(scheduler_running_to_decode_tokens_num, block_size)

        # NOTE(sunbiao.sun):
        # Loading requests will not exist in the running/waiting queue of scheduler,
        # while they are also an important indicator for the instance load, so we recorded it separately.
        # Meanwhile, saving requests will exist in the running queue of scheduler
        num_loading_requests = 0
        num_tokens_loading_requests = 0
        if self.scheduler.connector is not None:
            # pylint: disable=protected-access
            num_loading_requests = len(self.scheduler.connector._sched._loading)
            for loading_info in self.scheduler.connector._sched._loading.values():
                num_tokens_loading_requests += loading_info._req.num_tokens
        num_blocks_loading_requests = cdiv(num_tokens_loading_requests, block_size)

        instance_status.num_running_requests = len(self.scheduler.running)
        instance_status.num_uncomputed_blocks_scheduler_running_prefills = num_uncomputed_blocks_scheduler_running_prefills
        instance_status.num_unallocated_blocks_scheduler_running_prefills = num_unallocated_blocks_scheduler_running_prefills
        instance_status.num_loading_requests = num_loading_requests
        instance_status.num_blocks_loading_requests = num_blocks_loading_requests

        instance_status.scheduler_running_to_decode_requests_num = scheduler_running_to_decode_requests_num
        instance_status.scheduler_running_to_decode_blocks_num = scheduler_running_to_decode_blocks_num

        if self.async_scheduling:
            if step_phase == StepPhase.AFTER_SCHEDULE and scheduler_output is not None:
                # no need to deep copy, scheduler_output is generated per schedule
                self.last_num_scheduled_tokens = scheduler_output.num_scheduled_tokens

    def _update_instance_status(
        self,
        instance_status: InstanceStatus,
    ) -> None:
        num_used_gpu_blocks = instance_status.num_total_gpu_blocks - self.scheduler.kv_cache_manager.block_pool.get_num_free_blocks()

        instance_status.step_id = step_id
        instance_status.update_id = update_id
        instance_status.num_used_gpu_blocks = num_used_gpu_blocks

        # Update migration status
        if envs.LLUMNIX_ENABLE_MIGRATION:
            instance_status.num_migrate_in_reqs = len(_g_migrate_in_req_ids)
            instance_status.num_migrate_out_reqs = len(_g_migrate_out_req_ids)
            if self.get_detailed_migration_status:
                with _g_migrate_in_req_info_lock:
                    instance_status.num_migrate_in_tokens = sum(_g_migrate_in_req_info.values())
                with _g_migrate_out_req_info_lock:
                    instance_status.num_migrate_out_tokens = sum(_g_migrate_out_req_info.values())
                instance_status.block_ratio_migrate_in = round(cdiv(instance_status.num_migrate_in_tokens, self.scheduler.cache_config.block_size)
                                                            / instance_status.num_total_gpu_blocks, 4)
                instance_status.block_ratio_migrate_out = round(cdiv(instance_status.num_migrate_out_tokens, self.scheduler.cache_config.block_size)
                                                            / instance_status.num_total_gpu_blocks, 4)

    def forward_outputs(self, outputs) -> List[RequestIDType]:
        return self.output_forwarder.forward_outputs(outputs)

    def forward_status(self, instance_status: InstanceStatus):
        self.output_forwarder.forward_status(instance_status)

    def add_abort_requests(self, request_queue: queue.Queue, request_ids: List[RequestIDType]) -> None:
        request_queue.put_nowait((EngineCoreRequestType.ABORT, request_ids))

    def migrate_out(
        self,
        migration_params: MigrationParams,
        dst_engine_host: str,
        dst_engine_port: str,
    ) -> bool:
        if self.migration_frontend:
            return self.migration_frontend.migrate_out(
                migration_params,
                dst_engine_host,
                dst_engine_port,
            )
        logger.info("MigrationFrontend was not initialized.")
        return False


    def shutdown(self):
        """
        Shutdown output forwarder and migration frontend.
        Terminate llumlet Process.
        """
        self.output_forwarder.stop()
        if self.migration_frontend:
            self.migration_frontend.shutdown()
        self.llumlet.terminate()


class MigrationFrontend:
    """A frontend to handle migration requests asynchronously."""

    def __init__(self, vllm_config: "VllmConfig", dp_rank: int, scheduler: "Scheduler"): # type: ignore
        self._cfg = vllm_config
        self._dp_rank = dp_rank

        self._loop: Optional[asyncio.AbstractEventLoop] = None
        self._pmgr = None
        self._naming_cli = None
        self._enc = MsgpackEncoder()
        self._migration_out_tasks = set()

        self._worker_thread: Optional[threading.Thread] = None
        self._loop_started = threading.Event()
        self._is_shutdown = False
        self.scheduler = scheduler
        self.get_detailed_migration_status = envs.LLUMNIX_DETAILED_MIG_STATUS
        self._migrespdec = msgspec.msgpack.Decoder(MigrateResp)

        self._start_worker_and_wait()

    def _start_worker_and_wait(self):
        if self._worker_thread is not None:
            return
        self._worker_thread = threading.Thread(
            target=self._run_loop,
            daemon=True,
            name="MigrationFrontendWorker"
        )
        self._worker_thread.start()

        if not self._loop_started.wait(timeout=MIGRATION_FRONTEND_TIMEOUT):
            raise RuntimeError("MigrationFrontend worker thread failed to start in time.")

        init_future = asyncio.run_coroutine_threadsafe(self._async_init(), self._loop)
        try:
            init_future.result(timeout=MIGRATION_FRONTEND_TIMEOUT)
            logger.info("MigrationFrontend initialized successfully.")
        except Exception as e:
            logger.exception("Failed to initialize MigrationFrontend.")
            self.shutdown()
            raise e

    def _run_loop(self):
        asyncio.set_event_loop(asyncio.new_event_loop())
        self._loop = asyncio.get_event_loop()
        self._loop_started.set()

        try:
            self._loop.run_forever()
        finally:
            self._loop.run_until_complete(self._loop.shutdown_asyncgens())
            self._loop.close()
            logger.info("MigrationFrontend event loop has been shutdown.")

    async def _async_init(self):
        # When migration is disabled, these modules are no need to import.

        assert self._cfg.kv_transfer_config is not None
        self.peer_addr_port = (get_ip(), rpc_port(self._cfg))

        self._inst_id = _get_inst_id(self._cfg)
        self._naming_url = self._cfg.kv_transfer_config.get_from_extra_config("naming_url", "badbad")
        self._naming_cli = connect_naming(self._inst_id, self._naming_url)
        self._conn_mgr = ConnManager()
        self._pmgr = PeerManager(self._naming_cli, None, self._conn_mgr)
        assert self._loop is not None
        self._pmgr.start(self._loop)

    async def _run_and_manage_task(self, coro, done_callback=None):
        task = self._loop.create_task(coro)
        self._migration_out_tasks.add(task)

        def final_callback(fut):
            if done_callback:
                done_callback(fut)
            self._migration_out_tasks.discard(task)

        task.add_done_callback(final_callback)

    def submit_managed_coro(self, coro, done_callback=None):
        if not self._loop or self._is_shutdown:
            raise RuntimeError("MigrationFrontend is not running or has been shutdown.")
        asyncio.run_coroutine_threadsafe(
            self._run_and_manage_task(coro, done_callback),
            self._loop
        )

    async def _do_migrate(self, msgbuf, retry: int, dst_host: str, dst_port: str) -> "MigrateResp":
        if not self._pmgr:
            raise RuntimeError("PeerManager not initialized")
        peer_addr_port = (dst_host, dst_port)
        res = await self._rpc(peer_addr_port, msgbuf, MIGRATE_TO_RESP, retry)
        return res

    async def migrate(self, req: "EngineCoreRequest", dst_host: str, dst_port: str) -> "MigrateResp":
        num_output_tokens = req.num_output_tokens
        req = req2corereq(req)
        core_update_params(req, {SRC_INFO: self.peer_addr_port})
        core_update_params(req, {OUTPUT_TOKENS_N: num_output_tokens})
        msgbuf = bytearray.fromhex("00 00 00 00 00 00 00 00")
        struct.pack_into("=II", msgbuf, 0, MIGRATE_TO_REQ, 0)
        reqbufs = self._enc.encode_into(req, msgbuf, 8)
        assert len(reqbufs) == 1
        struct.pack_into("=I", msgbuf, 4, len(msgbuf) - 8)
        reqid = req.request_id
        for idx in range(VLLM_MIGRATION_RETRIES):
            try:
                res = await self._do_migrate(msgbuf, idx, dst_host, dst_port)
                return res
            # pylint: disable=broad-except
            except Exception:
                logger.exception("migration failed: reqid=%s try=%s", reqid, idx)
        raise RuntimeError(f"Migration for reqid={reqid} failed after retries.")

    async def _rpc(self, peer_addr_port: tuple[str, int], msgbuf, exp_resp: int,
                   retry: int) -> "MigrateResp":

        pconn = await self._conn_mgr.acquire_conn(peer_addr_port, retry > 0)
        try:
            pconn[1].write(msgbuf)
            await pconn[1].drain()

            resp: Optional[MigrateResp] = None
            respbuf = await pconn[0].readexactly(4 + 4)
            head, bodylen = struct.unpack("=II", respbuf)
            if head != exp_resp:
                raise RuntimeError(f"invalid resp {head=}, expected {exp_resp=}")
            respbuf = await pconn[0].readexactly(bodylen)
            resp = self._migrespdec.decode(respbuf)
            if resp is None:
                raise RuntimeError("invalid empty resp")
            return resp
        finally:
            self._conn_mgr.release_conn(peer_addr_port, pconn)

    def shutdown(self):
        if self._is_shutdown:
            return
        self._is_shutdown = True
        if self._loop and self._loop.is_running():
            self._loop.call_soon_threadsafe(self._loop.stop)
        if self._worker_thread:
            self._worker_thread.join()
        logger.info("MigrationFrontend has been shutdown.")

    def get_sorted_running_indices(self, mig_policy: RequestMigrationPolicy, reqs: list[Request]) -> list[int]:
        indices = range(len(reqs))
        if mig_policy == RequestMigrationPolicy.LCR.value:
            return reversed(indices)
        if mig_policy in (RequestMigrationPolicy.SR.value, RequestMigrationPolicy.FCWSR.value):
            return sorted(indices, key=lambda i: (reqs[i].num_tokens+reqs[i].num_output_tokens))
        if mig_policy == RequestMigrationPolicy.LR.value:
            return sorted(indices, key=lambda i: (reqs[i].num_tokens+reqs[i].num_output_tokens), reverse=True)
        return indices

    def get_migrated_requests(
        self,
        migration_params: MigrationParams,
    ) -> List[Request]:

        def is_migrating(req: Request) -> bool:
            if req.sampling_params.extra_args is None:
                return False
            if req.sampling_params.extra_args.get("kv_transfer_params", None) is None:
                return False
            return req.sampling_params.extra_args["kv_transfer_params"].get("is_migrating", False)

        def set_migrating_status(req: Request):
            if req.sampling_params.extra_args is None:
                req.sampling_params.extra_args = {}
            if req.sampling_params.extra_args.get("kv_transfer_params", None) is None:
                req.sampling_params.extra_args["kv_transfer_params"] = {}
            req.sampling_params.extra_args["kv_transfer_params"]["is_migrating"] = True

        def get_migration_reqs_by_policy(
            reqs: list[Request],
            indices: list[int],
            budget: int,
            migrated_out_requests: list[Request],
            cost_func: callable,
        ) -> int:
            accumulated_cost = 0
            for i in indices:
                if accumulated_cost >= budget:
                    break
                if is_migrating(reqs[i]):
                    continue
                req_cost = cost_func(reqs[i])
                set_migrating_status(reqs[i])
                migrated_out_requests.append(copy.deepcopy(reqs[i]))
                accumulated_cost += req_cost
            return max(0, budget - accumulated_cost)

        migrated_out_requests = []
        try:
            # Get budget and cost func by migration_type
            if migration_params.migration_type == MigrationType.TOKEN:
                budget = migration_params.num_tokens
                cost_func = lambda r: r.num_tokens
            elif migration_params.migration_type == MigrationType.RATIO:
                total_tokens = self.scheduler.cache_config.num_gpu_blocks * self.scheduler.cache_config.block_size
                budget = int(total_tokens * migration_params.block_ratio)
                cost_func = lambda r: r.num_tokens
            elif migration_params.migration_type == MigrationType.NUM_REQ:
                budget = migration_params.num_reqs
                cost_func = lambda r: 1
            else:
                raise NotImplementedError("Not Implemented MigrationType")
            # Migrate out all requests if migration_params.num_reqs == -1
            if migration_params.migration_type == MigrationType.NUM_REQ and budget == -1:
                for req in itertools.chain(self.scheduler.waiting, self.scheduler.running):
                    if not is_migrating(req):
                        migrated_out_requests.append(copy.deepcopy(req))
                        set_migrating_status(req)
                return migrated_out_requests
            # Get migration out reqs by mig_req_policy
            if migration_params.mig_req_policy in (RequestMigrationPolicy.FCW, RequestMigrationPolicy.FCWSR):
                running_budget = get_migration_reqs_by_policy(self.scheduler.waiting, range(len(self.scheduler.waiting)),
                                                                budget, migrated_out_requests, cost_func)
                if migration_params.mig_req_policy == RequestMigrationPolicy.FCWSR and running_budget > 0:
                    running_indices =  self.get_sorted_running_indices(migration_params.mig_req_policy, self.scheduler.running)
                    get_migration_reqs_by_policy(self.scheduler.running, running_indices, running_budget, migrated_out_requests, cost_func)
            else:
                running_indices =  self.get_sorted_running_indices(migration_params.mig_req_policy, self.scheduler.running)
                get_migration_reqs_by_policy(self.scheduler.running, running_indices, budget, migrated_out_requests,cost_func)
        # pylint: disable=broad-except
        except Exception as e:
            logger.error("Get migration request failed, %s", e)
            migrated_out_requests = []
        finally:
            for req in migrated_out_requests:
                _g_migrate_out_req_ids.add(req.request_id)
                if self.get_detailed_migration_status:
                    with _g_migrate_out_req_info_lock:
                        _g_migrate_out_req_info[req.request_id] = req.num_computed_tokens
        return migrated_out_requests

    def migrate_out(
        self,
        migration_params: dict,
        dst_engine_host: str,
        dst_engine_port: str,
    ) -> bool:

        if isinstance(migration_params, dict):
            migration_params = MigrationParams(**migration_params)

        migrated_out_requests = self.get_migrated_requests(migration_params)
        if not migrated_out_requests:
            logger.warning("No requests to migrate, migration type: %s", migration_params.migration_type)
            return False
        coro = self._dispatch_and_collect_tasks(migrated_out_requests, dst_engine_host, dst_engine_port)
        future = asyncio.run_coroutine_threadsafe(coro, self._loop)

        try:
            res = future.result(timeout=MIGRATION_FRONTEND_TIMEOUT)
        except Exception as e: # pylint: disable=broad-except
            logger.exception("Failed to get migration dispatch results: %s", e)
            for req in migrated_out_requests:
                self._cleanup_failed_migration(req.request_id, f'dispatch exception: {e}')
            return False

        return res

    async def _dispatch_and_collect_tasks(
        self,
        requests_to_migrate: list[Request],
        dst_engine_host: str,
        dst_engine_port: str,
    ) -> bool:
        tasks = [
            self.migrate(
                req=req,
                dst_host=dst_engine_host,
                dst_port=dst_engine_port
            ) for req in requests_to_migrate
        ]
        results = await asyncio.gather(*tasks, return_exceptions=True)

        accepted_reqs = []
        rejected_reqs = []
        failed_reqs = []
        for req, result in zip(requests_to_migrate, results):
            if isinstance(result, Exception):
                failed_reqs.append(req.request_id)
                self._cleanup_failed_migration(req.request_id, f"Unexpected exception: {result}")
            elif result.code == CODE_OK:
                accepted_reqs.append(req.request_id)
                logger.info("Migration for request %s was accepted.", req.request_id)
            else:
                rejected_reqs.append(req.request_id)
                self._cleanup_failed_migration(
                    req.request_id,
                    f"Rejected with code {result.code}"
                )
        logger.info(
            "Migration dispatch summary: %d accepted, %d rejected, %d failed.",
            len(accepted_reqs), len(rejected_reqs), len(failed_reqs)
        )
        if accepted_reqs:
            return True
        return False

    def _cleanup_failed_migration(self, request_id: str, reason: str):
        logger.warning("Cleaning up migration state for request %s. reason: %s", request_id, reason)
        self.reset_request_migrating_state(request_id)
        _g_migrate_out_req_ids.discard(request_id)
        if self.get_detailed_migration_status:
            with _g_migrate_out_req_info_lock:
                _g_migrate_out_req_info.pop(request_id, None)

    def reset_request_migrating_state(self, request_id: Union[str, int]):  # type: ignore
        for req in itertools.chain(self.scheduler.waiting, self.scheduler.running):
            if req.request_id == request_id:
                if (
                    req.sampling_params.extra_args
                    and req.sampling_params.extra_args.get("kv_transfer_params", False)
                    and req.sampling_params.extra_args["kv_transfer_params"].get("is_migrating", False)
                ):
                    req.sampling_params.extra_args["kv_transfer_params"]["is_migrating"] = False
                else:
                    return
