import asyncio
import itertools
import queue
import threading
from typing import Any, List, Optional, Tuple
import struct
import msgspec

from vllm.config import VllmConfig
from vllm.v1.request import Request

from llumnix import envs
from llumnix.constants import MIGRATION_FRONTEND_TIMEOUT, PRESTOP_TIMEOUT, VLLM_MIGRATION_RETRIES
from llumnix.instance_info import InstanceStatus
from llumnix.logging.logger import init_logger
from llumnix.migration_frontend.vllm_v1.base_migration_frontend import BaseMigrationFrontend
from llumnix.outputs.queue.zmq_server import MigrationZmqServer
from llumnix.utils import MigrationParams, MigrationType
from llumnix.compat.vllm_compat import get_ip
from llumnix.compat.hybrid_connector_compat import (
	MsgpackEncoder,
	PlaceholderModule,
	core_update_params,
	req2corereq,
	_get_inst_id,
	rpc_port,
	CODE_OK,
    MIGRATE_TO_REQ,
	MIGRATE_TO_RESP,
	SRC_INFO, OUTPUT_TOKENS_N,
	_g_migrate_out_req_ids,
    _g_migrate_in_req_ids,
	_g_migrate_out_req_info,
    _g_migrate_in_req_info,
	_g_migrate_out_req_info_lock,
    _g_migrate_in_req_info_lock,
	MigrateResp,
    PeerManager,
    ConnManager
)
try:
    import blade_kvt
    from blade_kvt.kv_transfer import connect_naming
except ImportError:
    blade_kvt = PlaceholderModule("blade_kvt")
    connect_naming = blade_kvt.placeholder_attr("connect_naming")


logger = init_logger(__name__)

class KVTMigrationFrontend(BaseMigrationFrontend):
    def __init__(self, vllm_config: VllmConfig, dp_rank: int, scheduler: "Scheduler"):
        super().__init__(vllm_config, dp_rank, scheduler)

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

        self.mig_server:MigrationZmqServer = None
        self.mig_server_ready_event = threading.Event()
        self.scheduler_state_queue = queue.Queue(maxsize=1)
        self.latest_running_snapshot = None
        self.latest_waiting_snapshot = None
        self.migrating_reqs = set()
        self.migration_finish_event : Optional[threading.Event] = None
        self.enginecore_requests: dict[str, "EngineCoreRequest"] = {}

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

    def get_enginecore_request(self, req: "Request"):
        if not req.request_id in self.enginecore_requests:
            enginecore_req = req2corereq(req)
            self.enginecore_requests[req.request_id] = enginecore_req
        return self.enginecore_requests[req.request_id]

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

        # init zmq server for migration
        self.migration_finish_event = threading.Event()
        self.migration_finish_event.set()
        self.mig_server = MigrationZmqServer(self)
        asyncio.create_task(self.mig_server.run_server_loop())
        self.mig_server_ready_event.set()

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

    def shutdown(self):
        if self._is_shutdown:
            return
        self._is_shutdown = True
        if self._loop and self._loop.is_running():
            self._loop.call_soon_threadsafe(self._loop.stop)
        if self._worker_thread:
            self._worker_thread.join()
        logger.info("MigrationFrontend has been shutdown.")

    def _is_migrating(self, req: Request) -> bool:
        if req.sampling_params.extra_args is None:
            return False
        kv_params = req.sampling_params.extra_args.get("kv_transfer_params", None)
        if kv_params is None:
            return False
        return kv_params.get("is_migrating", False)

    def _set_migrating_status(self, req: Any):
        if isinstance(req, Tuple):
            self.migrating_reqs.add(req[0].request_id)
        else:
            self.migrating_reqs.add(req.request_id)

    def _cleanup_failed_migration(self, migrate_out_requests: List[Tuple["EngineCoreRequest", int]]):
        for req, _ in migrate_out_requests:
            logger.warning("Cleaning up migration state for request %s", req.request_id)
            self.migrating_reqs.discard(req.request_id)
            _g_migrate_out_req_ids.discard(req.request_id)
            if self.get_detailed_migration_status:
                with _g_migrate_out_req_info_lock:
                    _g_migrate_out_req_info.pop(req.request_id, None)

    def iter_scheduler_requests(self) -> List[Tuple["EngineCoreRequest", int]]:
        migrated_out_requests: List[Tuple["EngineCoreRequest", int]]= []
        try:
            for req in itertools.chain(self.scheduler.waiting, self.scheduler.running):
                if req.request_id not in self.migrating_reqs:
                    if req.request_id not in self.enginecore_requests:
                        self.enginecore_requests[req.request_id] = self.get_enginecore_request(req)
                    migrated_out_requests.append((self.enginecore_requests[req.request_id], req.num_output_tokens))
                    self.migrating_reqs.add(req.request_id)
                    # Update migrate out concurrency
                    _g_migrate_out_req_ids.add(req.request_id)
                    if self.get_detailed_migration_status:
                        with _g_migrate_out_req_info_lock:
                            _g_migrate_out_req_info[req.request_id] = len(req.prompt_token_ids) + req.num_output_tokens
        # pylint: disable=broad-except
        except Exception:
            logger.exception("Get migration request failed")
            migrated_out_requests = []
        return migrated_out_requests

    def _get_running_requests(self) -> List[Any]:
        return self.latest_running_snapshot

    def _get_waiting_requests(self) -> List[Any]:
        return self.latest_waiting_snapshot

    def _should_skip_migration(self, req: Tuple[Any, int], is_in_waiting: bool):
        _ = is_in_waiting
        return req[0].request_id in self.migrating_reqs

    def _get_sorting_key(self, req: Tuple[Any, int]) -> int:
        return len(req[0].prompt_token_ids) + req[1]

    def _get_request_cost(self, req: Tuple[Any, int], migration_type: MigrationType) -> int:
        if migration_type in (MigrationType.TOKEN, MigrationType.RATIO):
            return len(req[0].prompt_token_ids) + req[1]
        if migration_type == MigrationType.NUM_REQ:
            return 1
        raise NotImplementedError

    async def migrate_out(
        self,
        migration_params: dict,
        dst_engine_host: str,
        dst_engine_port: int,
    ) -> bool:
        if not self.migration_finish_event.is_set():
            logger.warning("Migration is already in progress. Non-pre-stop request is skipped.")
            return False

        self.migration_finish_event.clear()
        if isinstance(migration_params, dict):
            migration_params = MigrationParams(**migration_params)

        # Read latest snapshot
        self.update_snapshots()
        if self.latest_running_snapshot is None or self.latest_waiting_snapshot is None:
            logger.warning("Scheduler state snapshot is not available yet. Skipping.")
            self.migration_finish_event.set()
            return False
        migrated_out_requests = self.get_migrated_requests(migration_params)
        if not migrated_out_requests:
            logger.warning("No requests to migrate, migration type: %s", migration_params.migration_type)
            self.migration_finish_event.set()
            return False
        try:
            res = await self._dispatch_and_collect_tasks(migrated_out_requests, dst_engine_host, dst_engine_port)
        except Exception as e: # pylint: disable=broad-except
            logger.exception("Failed to get migration dispatch results: %s", e)
            self._cleanup_failed_migration(migrated_out_requests)
            self.migration_finish_event.set()
            return False
        self.migration_finish_event.set()
        return res

    async def _dispatch_and_collect_tasks(
        self,
        requests_to_migrate: list[Tuple["EngineCoreRequest", int]],
        dst_engine_host: str,
        dst_engine_port: int,
    ) -> bool:
        tasks = [
            self.migrate(
                req=req,
                dst_host=dst_engine_host,
                dst_port=dst_engine_port,
                num_output_tokens=num_tokens,
            ) for req, num_tokens in requests_to_migrate
        ]
        results = await asyncio.gather(*tasks, return_exceptions=True)

        accepted_reqs = []
        rejected_reqs = []
        failed_reqs = []
        for (req, o),  result in zip(requests_to_migrate, results):
            if isinstance(result, Exception):
                failed_reqs.append((req, o))
            elif result.code == CODE_OK:
                accepted_reqs.append((req, o))
                logger.info("Migration for request %s was accepted.", req.request_id)
            else:
                rejected_reqs.append((req, o))
        to_clean_reqs = failed_reqs + rejected_reqs
        self._cleanup_failed_migration(to_clean_reqs)
        logger.info(
            "Migration dispatch summary: %d accepted, %d rejected, %d failed.",
            len(accepted_reqs), len(rejected_reqs), len(failed_reqs)
        )
        if accepted_reqs:
            return True
        return False

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

    async def _do_migrate(self, msgbuf, retry: int, dst_host: str, dst_port: int) -> "MigrateResp":
        if not self._pmgr:
            raise RuntimeError("PeerManager not initialized")
        peer_addr_port = (dst_host, dst_port)
        res = await self._rpc(peer_addr_port, msgbuf, MIGRATE_TO_RESP, retry)
        return res

    async def migrate(self, req: "EngineCoreRequest", dst_host: str, dst_port: int, num_output_tokens: int) -> "MigrateResp":
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

    def update_migration_status(self, instance_status: InstanceStatus):
        instance_status.num_migrate_in_reqs = len(_g_migrate_in_req_ids)
        instance_status.num_migrate_out_reqs = len(_g_migrate_out_req_ids)
        if self.get_detailed_migration_status:
            with _g_migrate_in_req_info_lock:
                instance_status.num_migrate_in_tokens = sum(_g_migrate_in_req_info.values())
            with _g_migrate_out_req_info_lock:
                instance_status.num_migrate_out_tokens = sum(_g_migrate_out_req_info.values())
            instance_status.kv_cache_usage_ratio_migrate_in = \
                round(instance_status.num_migrate_in_tokens / instance_status.num_total_gpu_tokens, 4)
            instance_status.kv_cache_usage_ratio_migrate_out = \
                round(instance_status.num_migrate_out_tokens / instance_status.num_total_gpu_tokens, 4)

    def update_req_status(self, new_state: Tuple[List[Request], List[Request]]):
        # push the latest request snapshot to migration_frontend
        try:
            self.scheduler_state_queue.get_nowait()
        except queue.Empty:
            pass
        try:
            logger.debug("Update scheduler_state_queue")
            self.scheduler_state_queue.put_nowait(new_state)
        except queue.Full:
            logger.error("Failed to put new state into the queue, something is wrong.")

    def migrate_out_prestop(
        self,
        migration_params: dict,
        dst_engine_host: str,
        dst_engine_port: int,
    ) -> bool:

        if not self.migration_finish_event.is_set():
            logger.info("A pre-stop migration is requested, but another is in progress. Waiting for it to complete...")
            finished_in_time = self.migration_finish_event.wait(timeout=PRESTOP_TIMEOUT)
            if not finished_in_time:
                logger.error("Timed out waiting for the previous migration to finish. Aborting pre-stop migration.")
                return False
        self.migration_finish_event.clear()
        if isinstance(migration_params, dict):
            migration_params = MigrationParams(**migration_params)

        # If prestop, it's save to iterate scheduler
        migrated_out_requests = self.iter_scheduler_requests()
        if not migrated_out_requests:
            logger.warning("No requests to migrate, migration type: %s", migration_params.migration_type)
            self.migration_finish_event.set()
            return False
        try:
            coro = self._dispatch_and_collect_tasks(migrated_out_requests, dst_engine_host, dst_engine_port)
            future = asyncio.run_coroutine_threadsafe(coro, self._loop)
            res = future.result(timeout=MIGRATION_FRONTEND_TIMEOUT)
        except Exception as e: # pylint: disable=broad-except
            logger.exception("Failed to get migration dispatch results: %s", e)
            self._cleanup_failed_migration(migrated_out_requests)
            self.migration_finish_event.set()
            return False
        self.migration_finish_event.set()
        return res

    def update_snapshots(self):
        try:
            latest_running, latest_waiting = self.scheduler_state_queue.get_nowait()

            self.latest_running_snapshot = latest_running
            self.latest_waiting_snapshot = latest_waiting
            logger.info("Fetched new scheduler state and updated local snapshots.")
            if self.latest_running_snapshot is None or self.latest_waiting_snapshot is None:
                return
            exist_reqs = {req.request_id for req, _ in latest_running}
            exist_reqs.update(req.request_id for req, _ in latest_waiting)

            if self.migrating_reqs:
                # cleanup aborted reqs
                original_size = len(self.migrating_reqs)
                self.migrating_reqs.intersection_update(exist_reqs)
                new_size = len(self.migrating_reqs)
                if original_size != new_size:
                    num_cleaned = original_size-new_size
                    logger.info("Cleaned up migrating set with new state. Removed %s finished reqs.", num_cleaned)
        except queue.Empty:
            # No update
            pass

    def clear_finished_reqs(self):
        exist_reqs = {req.request_id for req in self.scheduler.running}
        exist_reqs.update(req.request_id for req in self.scheduler.waiting)
        finished_reqs = [req_id for req_id in self.enginecore_requests if req_id not in exist_reqs]
        for req_id in finished_reqs:
            del self.enginecore_requests[req_id]
