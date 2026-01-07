import threading
import time
import queue
from typing import List, Tuple
import uuid
from enum import Enum

import cloudpickle
import grpc

from vllm.version import __version__ as VLLM_VERSION
from vllm.config import VllmConfig
from vllm.v1.core.sched.output import SchedulerOutput
from vllm.v1.engine import EngineCoreRequestType
from vllm.v1.request import Request, RequestStatus
try:
    from vllm.utils.math_utils import cdiv
    from vllm.utils.network_utils import get_ip
except ImportError:
    from vllm.utils import cdiv, get_ip

from llumnix import envs
from llumnix.llumlet.proto import llumlet_server_pb2, llumlet_server_pb2_grpc
from llumnix.llumlet.instance_info import InstanceStatus
from llumnix.llumlet.llumlet import Llumlet
from llumnix.logging.logger import init_logger
from llumnix.outputs.forwarder.thread_output_forwarder import ThreadOutputForwarder
from llumnix.utils import (
    MigrationParams,
    MigrationType,
    RequestIDType,
    RequestMigrationPolicy,
    UpdateInstanceStatusMode,
)

if envs.LLUMNIX_ENABLE_MIGRATION:
    try:
        from mooncake.mooncake_connector_v1 import (
            g_migrate_out_reqs,
            g_migrate_in_reqs,
            g_migrate_out_tokens,
            g_migrate_in_tokens,
            g_migrate_in_req_info_lock,
            g_migrate_out_req_info_lock,
        )
    except ImportError:
        raise ImportError("LLUMNIX_ENABLE_MIGRATION is set to True, but failed to import from 'mooncake'. "
                        "Please ensure 'mooncake_connector_v1' is correctly installed with the llumnix patch. "
                        "Or you can set LLUMNIX_ENABLE_MIGRATION to False to disable PD diaggregation and migration feature.") \
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
    def __init__(self, vllm_config: VllmConfig, scheduler: 'Scheduler', engine_index: int = 0, async_scheduling: bool = False): # type: ignore
        self.enable_migration = envs.LLUMNIX_ENABLE_MIGRATION

        if self.enable_migration:
            self.migration_frontend = MigrationFrontend(vllm_config, dp_rank=engine_index, scheduler=scheduler)
        else:
            self.migration_frontend = None
        self.llumlet = Llumlet(engine_type="vLLM v1 ce",
                               engine_config=vllm_config)
        self.llumlet.start()

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
        if VLLM_VERSION.startswith("0.9"):
            self.async_scheduling = async_scheduling
        else:
            self.async_scheduling = vllm_config.scheduler_config.async_scheduling
        self.last_num_scheduled_tokens = None
        self.update_instance_status_mode = envs.LLUMNIX_UPDATE_INSTANCE_STATUS_MODE

        self.running_ref = None
        self.waiting_ref = None

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
        # All the newly added requests will exist in waiting queue of scheduler
        # at the begin of the step.
        all_waitings = list(self.scheduler.waiting)

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
        # Waiting requests in scheduler or have not been allocated slots,
        # and therefore the number of blocks of these requests should be calculated manually rather than
        # simply read the number of blocks recorded in kv cache manager.
        all_waitings = list(self.scheduler.waiting)

        block_size = self.scheduler.cache_config.block_size

        # calculate uncomputed tokens for prefills, calculate allocated blocks for decodes
        num_uncomputed_tokens_scheduler_waiting_prefills = 0
        num_uncomputed_tokens_all_waiting_prefills = 0

        scheduler_waiting_to_decode_requests_num = 0
        scheduler_waiting_to_decode_tokens_num = 0

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


        num_uncomputed_tokens_all_waiting_prefills = num_uncomputed_tokens_scheduler_waiting_prefills

        num_uncomputed_blocks_scheduler_waiting_prefills = \
            cdiv(num_uncomputed_tokens_scheduler_waiting_prefills, block_size)
        num_uncomputed_blocks_all_waiting_prefills = \
            cdiv(num_uncomputed_tokens_all_waiting_prefills, block_size)

        scheduler_waiting_to_decode_blocks_num = cdiv(scheduler_waiting_to_decode_tokens_num, block_size)

        instance_status.waiting_requests = [req.request_id for req in all_waitings]

        instance_status.num_waiting_requests = len(all_waitings)
        instance_status.num_uncomputed_blocks_scheduler_waiting_prefills = num_uncomputed_blocks_scheduler_waiting_prefills
        instance_status.num_uncomputed_blocks_all_waiting_prefills = num_uncomputed_blocks_all_waiting_prefills

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
                if not req.num_output_tokens > 0:
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

                if req.sampling_params is not None and req.sampling_params.max_tokens > 1:
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
        if envs.LLUMNIX_ENABLE_MIGRATION:
            instance_status.num_migrate_in_reqs = len(g_migrate_in_reqs)
            instance_status.num_migrate_out_reqs = len(g_migrate_out_reqs)
            if self.get_detailed_migration_status:
                with g_migrate_in_req_info_lock:
                    instance_status.num_migrate_in_tokens = sum(g_migrate_in_tokens.values())
                with g_migrate_out_req_info_lock:
                    instance_status.num_migrate_out_tokens = sum(g_migrate_out_tokens.values())
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
        dst_engine_port: int,
    ) -> bool:
        if self.migration_frontend:
            return self.migration_frontend.migrate_out(
                migration_params,
                dst_engine_host,
                dst_engine_port,
            )
        logger.warning("MigrationFrontend was not initialized.")
        return False

    def call_remote_migrate_in(self, migrated_out_requests: List[Request], dst_engine_address: str):
        if self.migration_frontend:
            return self.migration_frontend.call_remote_migrate_in(
                migrated_out_requests,
                dst_engine_address
            )
        logger.warning("MigrationFrontend was not initialized.")
        return False

    def migrate_in(self, serialized_data: bytes) -> bool:
        if self.migration_frontend:
            return self.migration_frontend.migrate_in(serialized_data)
        logger.warning("MigrationFrontend was not initialized.")
        return False

    def abort_migrate_in(self, request_ids: List[str]) -> bool:
        if self.migration_frontend:
            return self.migration_frontend.abort_migrate_in_requets(request_ids)
        logger.warning("MigrationFrontend was not initialized.")
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

        self._is_shutdown = False
        self.scheduler = scheduler
        self.get_detailed_migration_status = envs.LLUMNIX_DETAILED_MIG_STATUS

        self.enable_detailed_mig = envs.LLUMNIX_DETAILED_MIG_STATUS
        max_migrate_in_tokens = envs.LLUMNIX_MAX_TOKEN_MIG_IN
        max_migrate_in_block_ratio = envs.LLUMNIX_MAX_BLOCK_RATIO_MIG_IN
        total_tokens = self._cfg.cache_config.num_gpu_blocks * self._cfg.cache_config.block_size
        self.max_migrate_in_tokens = min(
            max_migrate_in_tokens,
            int(total_tokens * max_migrate_in_block_ratio))

        self.remote_call_queue = queue.Queue()
        self.migrate_thread = threading.Thread(
            target=self._call_remote_migrate_in_worker,
            name="MigrationWorker"
        )
        self.migrate_thread.daemon = True
        self.migrate_thread.start()

    def _call_remote_migrate_in_worker(self):
        while not self._is_shutdown:
            try:
                task = self.remote_call_queue.get(timeout=1)
                if task is None:
                    break
                migrated_out_requests, dst_engine_address = task
                self._execute_remote_migrate_in_task(migrated_out_requests, dst_engine_address)
            except queue.Empty:
                continue
            # pylint: disable=broad-except
            except Exception as e:
                logger.exception("An unexpected error occurred in the migration worker: %s", e)
        logger.info("Migration worker loop finished.")

    def shutdown(self):
        if not self._is_shutdown:
            self._is_shutdown = True
            self.remote_call_queue.put(None)
            self.migrate_thread.join()
        logger.info("MigrationFrontend has been shutdown.")

    def get_sorted_running_indices(self, mig_policy: RequestMigrationPolicy, reqs: List[Request]) -> list[int]:
        indices = range(len(reqs))
        if mig_policy == RequestMigrationPolicy.LCR.value:
            return reversed(indices)
        if mig_policy in (RequestMigrationPolicy.SR.value, RequestMigrationPolicy.FCWSR.value):
            return sorted(indices, key=lambda i: (reqs[i].num_tokens+reqs[i].num_output_tokens))
        if mig_policy == RequestMigrationPolicy.LR.value:
            return sorted(indices, key=lambda i: (reqs[i].num_tokens+reqs[i].num_output_tokens), reverse=True)
        return indices

    def iter_scheduler_requests(self) -> List[Tuple[Request, int]]:
        migrated_out_requests: List[Request]= []
        try:
            for req in self.scheduler.waiting:
                if self._is_migrating(req):
                    continue
                if self._is_not_ready_waiting(req):
                    continue
                migrated_out_requests.append(req)
                self._set_migrating_status(req)

            for req in self.scheduler.running:
                if self._is_migrating(req):
                    continue
                migrated_out_requests.append(req)
                self._set_migrating_status(req)
        # pylint: disable=broad-except
        except Exception as e:
            logger.error("Get migration request failed, %s", e)
            self._cleanup_failed_migration(migrated_out_requests)
            migrated_out_requests = []
        return migrated_out_requests

    def _is_migrating(self, req: Request) -> bool:
        if not req.kv_transfer_params:
            return False
        return req.kv_transfer_params.get("is_migrating", False)

    def _set_migrating_status(self, req: Request) -> None:
        if not req.kv_transfer_params:
            req.kv_transfer_params = {}
        req.kv_transfer_params["is_migrating"] = True
        req.kv_transfer_params["remote_host"] = self.scheduler.connector.connector_scheduler.side_channel_host
        req.kv_transfer_params["remote_port"] = self.scheduler.connector.connector_scheduler.side_channel_port

    def _reset_migrating_status(self, req: Request) -> None:
        if req.kv_transfer_params is None:
            return
        req.kv_transfer_params["is_migrating"] = False

    def _is_not_ready_waiting(self, req: Request) -> bool:
        return req.status == RequestStatus.WAITING_FOR_REMOTE_KVS

    def get_migrated_requests(
        self,
        migration_params: MigrationParams,
    ) -> List[Request]:

        def get_migration_reqs_by_policy(
            reqs: list[Request],
            indices: list[int],
            budget: int,
            migrated_out_requests: list[Request],
            cost_func: callable,
            is_in_waiting: bool = False,
        ) -> int:
            accumulated_cost = 0
            for i in indices:
                if accumulated_cost >= budget:
                    break
                if self._is_migrating(reqs[i]):
                    continue
                if is_in_waiting and self._is_not_ready_waiting(reqs[i]):
                    continue
                req_cost = cost_func(reqs[i])
                migrated_out_requests.append(reqs[i])
                self._set_migrating_status(reqs[i])
                accumulated_cost += req_cost
            return max(0, budget - accumulated_cost)
        migrated_out_requests = []

        if migration_params.migration_type == MigrationType.NUM_REQ and migration_params.num_reqs == -1:
            # migrate all requests
            return self.iter_scheduler_requests()

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

            # Get migration out reqs by mig_req_policy
            if migration_params.mig_req_policy in (RequestMigrationPolicy.FCW, RequestMigrationPolicy.FCWSR):
                running_budget = get_migration_reqs_by_policy(self.scheduler.waiting, range(len(self.scheduler.waiting)),
                                                                budget, migrated_out_requests, cost_func, True)
                if migration_params.mig_req_policy == RequestMigrationPolicy.FCWSR and running_budget > 0:
                    running_indices =  self.get_sorted_running_indices(migration_params.mig_req_policy, self.scheduler.running)
                    get_migration_reqs_by_policy(self.scheduler.running, running_indices, running_budget, migrated_out_requests, cost_func)
            else:
                running_indices =  self.get_sorted_running_indices(migration_params.mig_req_policy, self.scheduler.running)
                get_migration_reqs_by_policy(self.scheduler.running, running_indices, budget, migrated_out_requests,cost_func)
        # pylint: disable=broad-except
        except Exception as e:
            logger.error("Get migration request failed, %s", e, stack_info=True)
            migrated_out_requests = []
        return migrated_out_requests

    def migrate_out(
        self,
        migration_params: dict,
        dst_engine_host: str,
        dst_engine_port: int,
    ) -> bool:
        if isinstance(migration_params, dict):
            migration_params = MigrationParams(**migration_params)

        migrated_out_requests = self.get_migrated_requests(migration_params)
        if not migrated_out_requests:
            logger.warning("No requests to migrate, migration type: %s", migration_params.migration_type)
            return False
        dst_engine_address = "{}:{}".format(dst_engine_host, dst_engine_port)
        self.scheduler.connector.migrate_out_request(migrated_out_requests, dst_engine_address)

        # Update migrate out info
        for req in migrated_out_requests:
            g_migrate_out_reqs.add(req.request_id)
            if self.enable_detailed_mig:
                with g_migrate_out_req_info_lock:
                    g_migrate_out_tokens[req.request_id] = req.num_tokens
        return True

    def call_remote_migrate_in(self, migrated_out_requests: List[Request], dst_engine_address: str):
        task = (migrated_out_requests, dst_engine_address)
        self.remote_call_queue.put(task)

    def _cleanup_failed_migration(self, migrate_out_requests: List[Request]):
        for req in migrate_out_requests:
            self._reset_migrating_status(req)
            g_migrate_out_reqs.discard(req.request_id)
            if self.enable_detailed_mig:
                with g_migrate_out_req_info_lock:
                    g_migrate_out_tokens.pop(req.request_id, None)

    def _has_migrate_in_slots(self, reqs: List[Request]):
        if len(g_migrate_in_reqs) + len(reqs) > envs.LLUMNIX_MAX_REQ_MIG_IN:
            return False
        if envs.LLUMNIX_DETAILED_MIG_STATUS:
            with g_migrate_in_req_info_lock:
                total_tokens = sum(g_migrate_in_tokens.values())
            for req in reqs:
                total_tokens += req.num_tokens
                if total_tokens > self.max_migrate_in_tokens:
                    return False
        return True

    def migrate_in(
        self,
        serialized_data: bytes
    ) -> bool:
        migrate_in_requests: List[Request] = cloudpickle.loads(serialized_data)
        if not self._has_migrate_in_slots(migrate_in_requests):
            logger.error("Failed to migrate in. No enough slots for migrate in %s reqs", len(migrate_in_requests))
            return False
        logger.info("Migrating in requests: %s", [req.request_id for req in migrate_in_requests])
        self.scheduler.connector.handle_migrate_in_request(migrate_in_requests)
        for req in migrate_in_requests:
            g_migrate_in_reqs.add(req.request_id)
            if self.enable_detailed_mig:
                with g_migrate_in_req_info_lock:
                    g_migrate_in_tokens[req.request_id] = req.num_tokens
        return True

    def abort_migrate_in_requets(
        self,
        request_ids: List[str],
    ) -> bool:
        try:
            for req_id in request_ids:
                logger.info("Aborting migrate in request %s", req_id)
                self.scheduler.connector.abort_migrate_in_request(req_id)
                self.migrate_in_reqs.discard(req_id)
        except Exception as e: # pylint: disable=broad-except
            logger.exception("Failed to abort migrate in requests: %s", e)
            return False
        return True

    def _execute_remote_migrate_in_task(self, migrated_out_requests: List[Request], dst_engine_address: str):
        try:
            migrate_out_requests_bytes = cloudpickle.dumps(migrated_out_requests)
            self._call_remote_migrate_in(dst_engine_address, migrate_out_requests_bytes)
        except Exception as e:
            logger.exception("Failed to execute remote migrate_in task: %s", e)
            self._cleanup_failed_migration(migrated_out_requests)

    def _call_remote_migrate_in(
        self,
        dst_engine_address: str,
        serialized_migrate_req: bytes
    ):
        with grpc.insecure_channel(dst_engine_address) as channel:
            stub = llumlet_server_pb2_grpc.LlumletStub(channel)
            migrate_in_req = llumlet_server_pb2.MigrateInRequest(
                serialized_migrate_req=serialized_migrate_req,
            )
            try:
                stub.MigrateIn(migrate_in_req)
            except grpc.RpcError as e:
                raise RuntimeError(f"An RPC error occurred when sending migrate in req: {e.code()} - {e.details()}") from e
