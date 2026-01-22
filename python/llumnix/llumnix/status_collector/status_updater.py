import time
from typing import List, Tuple

from vllm.config import VllmConfig
from vllm.v1.core.sched.output import SchedulerOutput
from vllm.v1.outputs import ModelRunnerOutput

from llumnix import envs
from llumnix.llumlet.instance_info import BackendType, InstanceStatus
from llumnix.logging.logger import init_logger
from llumnix.migration_frontend.base_migration_frontend import BaseMigrationFrontend
from llumnix.outputs.forwarder.thread_output_forwarder import ThreadOutputForwarder
from llumnix.status_collector.metric_collector import InstanceStatusCollector, StepPhase
from llumnix.utils import UpdateInstanceStatusMode


logger = init_logger(__name__)

class StatusUpdater:
    _WAITING_STATUSES = {
        "hybrid_scheduler_waiting_to_decode_requests_num",
        "num_unallocated_blocks_hybrid_scheduler_waiting_decodes",
        "hybrid_scheduler_waiting_to_decode_blocks_num",
        "scheduler_waiting_to_decode_requests_num",
        "scheduler_waiting_to_decode_blocks_num",
        "num_uncomputed_blocks_all_waiting_prefills",
        "num_waiting_requests"
    }

    _RUNNING_STATUSES = {
        "num_uncomputed_blocks_scheduler_running_prefills",
        "num_unallocated_blocks_scheduler_running_prefills",
        "scheduler_running_to_decode_requests_num",
        "scheduler_running_to_decode_blocks_num",
        "num_loading_requests",
        "num_blocks_loading_requests",
        "num_running_requests",
    }
    def __init__(self, scheduler: "Scheduler",
                 engine_type: BackendType,
                 vllm_config: VllmConfig,
                 mig_async: bool,
                 migration_frontend: BaseMigrationFrontend=None,
                 metric_forwarder: ThreadOutputForwarder=None):
        self.engine_type: BackendType = BackendType.VLLM_V1
        self.update_freq = envs.LLUMNIX_INSTANCE_UPDATE_STEPS
        self.enable_migration = envs.LLUMNIX_ENABLE_MIGRATION
        self.get_detailed_migration_status = envs.LLUMNIX_DETAILED_MIG_STATUS
        self.scheduler = scheduler
        self.recent_waitings_set = set()
        self.recent_waitings_staleness_seconds = envs.LLUMNIX_RECENT_WAITINGS_STALENESS_SECONDS
        self.update_instance_status_mode = envs.LLUMNIX_UPDATE_INSTANCE_STATUS_MODE
        self.step_id = 0
        self.update_id = 0

        self.enable_profiling = envs.LLUMNIX_ENABLE_PROFILING
        if engine_type != BackendType.VLLM_V1:
            logger.warning("Profiling is not supported in this engine type.")
            self.enable_profiling = False
        self.profiling_steps = envs.LLUMNIX_PROFILING_STEPS
        self.profiling_id = 0

        self.mig_async = mig_async
        self.migration_frontend = migration_frontend
        self.running_ref = None
        self.waiting_ref = None
        self.metric_collector = InstanceStatusCollector(scheduler, vllm_config, engine_type)
        self.metric_forwarder = metric_forwarder

    def update_instance_status(
        self,
        instance_status: InstanceStatus,
        step_phase: StepPhase,
        scheduler_output: SchedulerOutput = None,
        model_output: ModelRunnerOutput = None,
    ):
        try:
            if step_phase == StepPhase.AFTER_UPDATE:
                self.step_id += 1
            if self.step_id % self.update_freq != 0:
                return
            self.update_id += 1

            if step_phase == StepPhase.AFTER_UPDATE:
                self._update_profiling_status(instance_status, scheduler_output, model_output)
                if self.enable_migration:
                    self.migration_frontend.clear_finished_reqs()

            # If scheduler has requests, update instance status will be called again immediately, so there is no need to update.
            if step_phase == StepPhase.AFTER_UPDATE and self.scheduler.has_requests():
                return

            if self.update_instance_status_mode == UpdateInstanceStatusMode.PUSH:
                # pylint: disable=consider-using-with
                self.metric_forwarder.forward_status_lock.acquire()
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
                    self.metric_forwarder.forward_status(instance_status)
            if self.update_instance_status_mode == UpdateInstanceStatusMode.PUSH:
                self.metric_forwarder.forward_status_lock.release()
        # pylint: disable=broad-except
        except Exception:
            logger.exception("Failed to update instance status.")
        return


    def _update_profiling_status(
        self,
        instance_status: InstanceStatus,
        scheduler_output: SchedulerOutput,
        model_output: ModelRunnerOutput,
    ) -> None:
        # profiling_id == -1 means that there is no valid profiling data in the instance status.
        if not (self.enable_profiling and self.profiling_id < self.profiling_steps):
            instance_status.profiling_id = -1
            return
        if not (model_output.step_duration and scheduler_output and scheduler_output.total_num_scheduled_tokens > 0):
            instance_status.profiling_id = -1
            return

        num_scheduled_prefill_tokens = 0
        for num_scheduled_tokens in scheduler_output.num_scheduled_tokens.values():
            if num_scheduled_tokens > 1:
                num_scheduled_prefill_tokens += num_scheduled_tokens

        if num_scheduled_prefill_tokens == 0:
            instance_status.profiling_id = -1
            return

        self.profiling_id += 1
        instance_status.profiling_id = self.profiling_id
        instance_status.step_duration = model_output.step_duration if num_scheduled_prefill_tokens > 0 else 0.0
        instance_status.num_scheduled_prefill_tokens = num_scheduled_prefill_tokens


    def _update_recent_waitings_status(self, instance_status: InstanceStatus) -> None:
        all_waitings = self.metric_collector.get_all_waiting_reqs()
        for req in all_waitings:
            if req not in self.recent_waitings_set:
                self.recent_waitings_set.add(req)
            if time.time() - req.arrival_time > self.recent_waitings_staleness_seconds:
                self.recent_waitings_set.remove(req)
        instance_status.recent_waiting_requests = [req.request_id for req in self.recent_waitings_set]

    def _update_waiting_status(
        self,
        instance_status: InstanceStatus,
    ) -> None:
        # waiting_requests statuses are used by instance status local account rather than metrics, so always collected.
        all_waitings = self.metric_collector.get_all_waiting_reqs()

        # waiting requests is not scheduled requests, so there is no need to correct computed tokens.
        if self.enable_migration and self.mig_async:
            waiting_snapshot:list[Tuple["EngineCoreRequest", int]] = []
            for req in self.scheduler.waiting:
                # EngineCoreRequest is static and has the same lifecycle as Request object.
                waiting_snapshot.append((self.migration_frontend.get_enginecore_request(req), 0))
            self.waiting_ref = waiting_snapshot
        instance_status.waiting_requests = [req.request_id for req in all_waitings]

        self.metric_collector.get_waiting_status(self._WAITING_STATUSES, instance_status, all_waitings)

    def _update_running_status(
        self,
        instance_status: InstanceStatus,
        step_phase: StepPhase,
        scheduler_output: SchedulerOutput = None,
    ) -> None:
        self.metric_collector.get_running_status(self._RUNNING_STATUSES, instance_status, step_phase, scheduler_output)
        if self.enable_migration and self.mig_async:
            running_snapshot: List[Tuple["EngineCoreRequest", int]] = []
            if self.scheduler.running:
                for req in self.scheduler.running:
                    if req.prefill_done:
                        try:
                            running_snapshot.append((self.migration_frontend.get_enginecore_request(req), req.num_output_tokens))
                        except KeyError as e:
                            logger.error("Failed to append running_snapshot %s", e)
            self.running_ref = running_snapshot

    def _update_instance_status(
        self,
        instance_status: InstanceStatus,
    ) -> None:
        # basic instance statuses, always collected
        instance_status.step_id = self.step_id
        instance_status.update_id = self.update_id
        instance_status.num_used_gpu_blocks = \
            instance_status.num_total_gpu_blocks - self.scheduler.kv_cache_manager.block_pool.get_num_free_blocks()
        # Update migration status
        if self.enable_migration:
            self.migration_frontend.update_migration_status(instance_status)
            if self.mig_async:
                new_state = (self.running_ref, self.waiting_ref)
                self.migration_frontend.update_req_status(new_state)
