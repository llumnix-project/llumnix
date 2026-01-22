import queue
import threading
from typing import Tuple, List
import cloudpickle
import grpc

from vllm.config import VllmConfig
from vllm.v1.request import Request, RequestStatus

from llumnix import envs
from llumnix.llumlet.instance_info import InstanceStatus
from llumnix.llumlet.proto import llumlet_server_pb2, llumlet_server_pb2_grpc
from llumnix.logging.logger import init_logger
from llumnix.migration_frontend.base_migration_frontend import BaseMigrationFrontend
from llumnix.utils import MigrationParams, MigrationType, RequestMigrationPolicy
from llumnix.compat.vllm_compat import cdiv

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

class MooncakeMigrationFrontend(BaseMigrationFrontend):
    def __init__(self, vllm_config: VllmConfig, dp_rank: int, scheduler: "Scheduler"):
        super().__init__(vllm_config, dp_rank, scheduler)

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

    def shutdown(self):
        if not self._is_shutdown:
            self._is_shutdown = True
            self.remote_call_queue.put(None)
            self.migrate_thread.join()
        logger.info("MooncakeMigrationFrontend has been shutdown.")

    def _is_migrating(self, req: Request) -> bool:
        if not req.kv_transfer_params:
            return False
        return req.kv_transfer_params.get("is_migrating", False)

    def _set_migrating_status(self, req: Request):
        if not req.kv_transfer_params:
            req.kv_transfer_params = {}
        req.kv_transfer_params["is_migrating"] = True
        req.kv_transfer_params["remote_host"] = self.scheduler.connector.connector_scheduler.side_channel_host
        req.kv_transfer_params["remote_port"] = self.scheduler.connector.connector_scheduler.side_channel_port

    def _get_sorted_running_indices(self, mig_policy: RequestMigrationPolicy, reqs: List[Request]) -> list[int]:
        return self._get_sorted_indices_helper(mig_policy, reqs, lambda i: (reqs[i].num_tokens + reqs[i].num_output_tokens))

    def _reset_migrating_status(self, req: Request):
        if req.kv_transfer_params is not None:
            req.kv_transfer_params["is_migrating"] = False

    def update_migration_status(self, instance_status: InstanceStatus):
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

    def iter_scheduler_requests(self) -> List[Tuple[Request, int]]:
        migrated_out_requests: List[Request]= []
        try:
            for req in self.scheduler.waiting:
                if self._should_skip_migration(req, is_in_waiting=True):
                    continue
                migrated_out_requests.append(req)
                self._set_migrating_status(req)
            for req in self.scheduler.running:
                if self._should_skip_migration(req, is_in_waiting=False):
                    continue
                migrated_out_requests.append(req)
                self._set_migrating_status(req)
        # pylint: disable=broad-except
        except Exception as e:
            logger.error("Get migration request failed, %s", e)
            self._cleanup_failed_migration(migrated_out_requests)
            migrated_out_requests = []
        return migrated_out_requests

    def _cleanup_failed_migration(self, migrate_out_requests: List[Request]):
        for req in migrate_out_requests:
            self._reset_migrating_status(req)
            g_migrate_out_reqs.discard(req.request_id)
            if self.enable_detailed_mig:
                with g_migrate_out_req_info_lock:
                    g_migrate_out_tokens.pop(req.request_id, None)

    def _call_remote_migrate_in_worker(self):
        while not self._is_shutdown:
            try:
                task = self.remote_call_queue.get(timeout=1)
                logger.info("Migration worker fetched a task from the queue.")
                if task is None:
                    break
                migrated_out_requests, dst_engine_address = task
                self._execute_remote_migrate_in_task(migrated_out_requests, dst_engine_address)
            except queue.Empty:
                continue
            except Exception as e: #pylint: disable=broad-except
                logger.exception("An unexpected error occurred in the migration worker: %s", e)
        logger.info("Migration worker loop finished.")

    def _is_not_ready_waiting(self, req: Request) -> bool:
        return req.status == RequestStatus.WAITING_FOR_REMOTE_KVS

    def _get_sorting_key(self, req: Request):
        return req.num_tokens + req.num_output_tokens

    def _should_skip_migration(self, req: Request, is_in_waiting: bool) -> bool:
        if self._is_migrating(req):
            return True
        if is_in_waiting and self._is_not_ready_waiting(req):
            return True
        return False

    def _get_running_requests(self) -> List[Request]:
        return self.scheduler.running

    def _get_waiting_requests(self) -> List[Request]:
        return self.scheduler.waiting

    def _get_request_cost(self, req: Request, migration_type: MigrationType) -> int:
        if migration_type in (MigrationType.TOKEN, MigrationType.RATIO):
            return req.num_tokens
        if migration_type == MigrationType.NUM_REQ:
            return 1
        raise NotImplementedError

    def migrate_out(self, migration_params: dict, dst_engine_host: str, dst_engine_port: int) -> bool:

        if isinstance(migration_params, dict):
            migration_params = MigrationParams(**migration_params)

        migrated_out_requests = self.get_migrated_requests(migration_params)
        if not migrated_out_requests:
            logger.warning("No requests to migrate based on policy, migration type: %s", migration_params.migration_type)
            return False
        dst_engine_address = f"{dst_engine_host}:{dst_engine_port}"
        self.scheduler.connector.migrate_out_request(migrated_out_requests, dst_engine_address)
        for req in migrated_out_requests:
            g_migrate_out_reqs.add(req.request_id)
            if self.enable_detailed_mig:
                with g_migrate_out_req_info_lock:
                    g_migrate_out_tokens[req.request_id] = req.num_tokens
        return True

    def call_remote_migrate_in(self, migrated_out_requests: List[Request], dst_engine_address: str):
        """Puts a remote migration task into the queue."""
        task = (migrated_out_requests, dst_engine_address)
        self.remote_call_queue.put(task)

    def _execute_remote_migrate_in_task(self, migrated_out_requests: List[Request], dst_engine_address: str):
        """Executes the gRPC call to trigger migrate_in on the destination."""
        try:
            migrate_out_requests_bytes = cloudpickle.dumps(migrated_out_requests)
            self._call_remote_migrate_in(dst_engine_address, migrate_out_requests_bytes)
        except Exception as e: # pylint: disable=broad-except
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
