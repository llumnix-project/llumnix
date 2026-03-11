import asyncio
from collections.abc import Awaitable
import multiprocessing
import os
import signal
import sys
import time
from typing import Any, Callable, List, Optional

import psutil

from llumnix import envs
from llumnix.cms.cms_client_async import CMSWriteClient
from llumnix.constants import (
    ENGINE_CLIENT_RETRY_INTERVAL,
    MAX_ENGINE_CLIENT_RETRIES,
    PRESTOP_MIGRATION_SEND_TIMES,
)
from llumnix.engine_client.base_engine_client import BaseEngineClient
from llumnix.engine_client.utils import (
    create_engine_client,
    get_engine_client_addresses,
    get_engine_mp_context,
    get_specific_instance_meta_data,
    add_llumlet_addresses,
)
from llumnix.converters import to_cms_metadata, to_cms_status
from llumnix.instance_info import (
    BackendType,
    ConnectorType,
    InstanceMetaData,
    InstanceStatus,
)
from llumnix.rpc_server import AsyncLlumletRPCServer
from llumnix.logging.logger import init_logger
from llumnix.outputs.queue.zmq_client import MigrationZmqClient
from llumnix.utils import (
    MigrationLimits,
    MigrationParams,
    MigrationType,
    NotEnoughSlotsError,
    RequestIDType,
    get_metric_push_interval,
    get_migration_limits,
    get_rpc_port,
    UpdateInstanceStatusMode,
)
from llumnix.cms.proto.cms_pb2 import (
    InstanceStatus as CMSInstanceStatus,
    InstanceMetadata as CMSInstanceMetadata,
)

logger = init_logger(__name__)


class Llumlet:
    """Class to start and terminate LlumletProc"""

    def __init__(
        self,
        engine_type: str,
        engine_config: Any,
        mig_address: Optional[str] = None,
        connector_type: Optional[str] = None,
    ):
        self.engine_type = engine_type
        self.client_addresses = get_engine_client_addresses(engine_type=engine_type)
        context = get_engine_mp_context(engine_type)
        self.rpc_port = get_rpc_port()
        self.proc: multiprocessing.Process = context.Process(
            target=LlumletProc.run_llumlet,
            name="Llumlet",
            kwargs={
                "engine_type": engine_type,
                "engine_config": engine_config,
                "client_addresses": self.client_addresses,
                "parent_pid": os.getpid(),
                "rpc_port": self.rpc_port,
                "mig_address": mig_address,
                "connector_type": connector_type,
            },
            daemon=False,
        )

    def add_llumlet_addresses(self, addresses: Optional[dict[str, str]]) -> list[str]:
        """Add llumlet rpc address to the front of addresses list"""
        addresses = add_llumlet_addresses(
            self.engine_type, addresses, self.client_addresses
        )
        return addresses

    def start(self):
        self.proc.start()
        logger.info("Llumlet process starting with PID: %s", self.proc.pid)

    def terminate(self):
        if self.proc.is_alive():
            logger.info(
                "Signaling Llumlet process (PID: %s) to shutdown...", self.proc.pid
            )
            self.proc.terminate()

    def is_alive(self):
        return self.proc.is_alive()


class LlumletProc:
    """
    Llumlet Core Processing Class

    This class is responsible for the core logic of a Llumlet instance.

    Key Responsibilities:
    - Reports the real-time status of the inference engine.
    - Processes migration requests from the scheduler.
    - Handles engine call failures with a retry mechanism (3 attempts).
    If failures persist, it marks the instance as unschedulable and reports
    the final status to the Cluster Management Service (CMS).
    - Engine call timeout is configurable via environment variables.
    """

    def __init__(
        self,
        engine_type: str,
        engine_config: Any,
        rpc_port: int,
        client_addresses: Optional[dict[str, str]] = None,
        parent_pid: Optional[int] = None,
        mig_address: Optional[str] = None,
        connector_type: Optional[ConnectorType] = None,
    ) -> None:
        logger.info("Initializing LlumletProc...")
        for key in dir(envs):
            if key.startswith("LLUMNIX_"):
                logger.info("%s=%s", key, getattr(envs, key))
        self.parent_pid = parent_pid
        self.engine_config = engine_config
        self.engine_type = engine_type
        self.engine_client: BaseEngineClient = create_engine_client(
            engine_type=engine_type,
            engine_config=engine_config,
            client_addresses=client_addresses,
        )

        self.status_push_interval, self.metadata_push_interval = (
            get_metric_push_interval()
        )
        self.push_task: Optional[asyncio.Task] = None
        self.cms_client: CMSWriteClient = CMSWriteClient()
        self.loop = None
        self.rpc_server: Optional[AsyncLlumletRPCServer] = None
        self.rpc_port = rpc_port

        self.connector_type = connector_type
        self.instance_metadata: InstanceMetaData = self.get_instance_meta_data(
            engine_type, connector_type, self.engine_config
        )
        self.cms_instance_metadata: CMSInstanceMetadata = to_cms_metadata(
            self.instance_metadata
        )
        self.instance_id = self.instance_metadata.instance_id
        self.instance_status: InstanceStatus = InstanceStatus()
        self.last_report_instance_status_timestamp = None
        self.expired_time = envs.LLUMNIX_CMS_EXPIRED_TIME
        self.last_metadata_report_time = None
        assert self.expired_time > self.status_push_interval
        assert self.expired_time > self.metadata_push_interval

        self.enable_mig = envs.LLUMNIX_ENABLE_MIGRATION
        self.detailed_mig = envs.LLUMNIX_DETAILED_MIG_STATUS
        if self.enable_mig:
            if (
                self.engine_type == BackendType.VLLM_V1
                and self.connector_type == ConnectorType.HYBRID
            ):
                self.mig_client = MigrationZmqClient(mig_address)
            self.mig_limits: MigrationLimits = get_migration_limits(self.detailed_mig)

        self.update_instance_status_mode = envs.LLUMNIX_UPDATE_INSTANCE_STATUS_MODE
        assert self.update_instance_status_mode in [
            UpdateInstanceStatusMode.PUSH,
            UpdateInstanceStatusMode.PULL,
        ], "Only support push or pull mode for updating instance status"
        if self.update_instance_status_mode == UpdateInstanceStatusMode.PUSH:
            self.cms_instance_status_queue = asyncio.Queue()

    class RpcHandler:
        def __init__(self, llumlet_proc_instance: "LlumletProc"):
            self.llumlet = llumlet_proc_instance

        async def migrate(
            self,
            dst_engine_ip: str,
            dst_engine_port: int,
            migration_params: MigrationParams,
        ) -> bool:
            return await self.llumlet.migrate(
                dst_engine_ip, dst_engine_port, migration_params
            )

        async def abort(self, request_ids: List[RequestIDType]) -> None:
            await self.llumlet.abort(request_ids)

        async def migrate_in(
            self, serialized_migrate_req: bytes, serialization_format: str
        ) -> None:
            await self.llumlet.migrate_in(serialized_migrate_req, serialization_format)

        async def abort_migrate_in(
            self,
            request_id: str,
            migrate_in_ip_address: str | None,
            migrate_in_port: str | None,
        ) -> None:
            await self.llumlet.abort_migrate_in(
                request_id, migrate_in_ip_address, migrate_in_port
            )

        async def push_instance_status(
            self, instance_status: CMSInstanceStatus
        ) -> None:
            await self.llumlet.push_instance_status(instance_status)

    @staticmethod
    def run_llumlet(
        engine_type: str,
        engine_config: Any,
        rpc_port: int,
        client_addresses: Optional[dict[str, str]] = None,
        parent_pid: Optional[int] = None,
        mig_address: Optional[str] = None,
        connector_type: Optional[ConnectorType] = None,
    ):
        # Signal handler used for graceful termination.
        # SystemExit exception is only raised once to allow this and worker
        # processes to terminate without error
        shutdown_requested = False

        # pylint: disable=unused-argument
        def signal_handler(signum, frame):
            nonlocal shutdown_requested
            if not shutdown_requested:
                shutdown_requested = True
                raise SystemExit()

        # Either SIGTERM or SIGINT will terminate the worker
        signal.signal(signal.SIGTERM, signal_handler)
        signal.signal(signal.SIGINT, signal_handler)

        try:
            engine_type = BackendType(engine_type)
        except ValueError as e:
            raise ValueError(
                "Invalid engine_type: {}. Valid options: {}".format(
                    engine_type, [e.value for e in BackendType]
                )
            ) from e
        try:
            llumlet = LlumletProc(
                engine_type=engine_type,
                engine_config=engine_config,
                client_addresses=client_addresses,
                parent_pid=parent_pid,
                rpc_port=rpc_port,
                mig_address=mig_address,
                connector_type=connector_type,
            )
        # pylint: disable=broad-except
        except Exception:
            logger.exception("An expection occurred when creating LlumletProc.")
            return
        logger.info("LlumletProc instance created, process PID:%s", os.getpid())

        try:
            asyncio.run(llumlet.start_services())
        # pylint: disable=broad-except
        except Exception:
            logger.exception("llumlet start_services failed.")
        finally:
            llumlet.shutdown()

    async def start_services(self):
        """Start Llumlet RPC server and periodic metric push."""
        if self.engine_type == BackendType.SGLANG:
            await self.engine_client.start()
        self.rpc_server = AsyncLlumletRPCServer(port=self.rpc_port)
        rpc_handler = self.RpcHandler(self)
        await self.rpc_server.start(rpc_handler)
        logger.info("Llumlet rpc server started on port:%s", self.rpc_port)

        self.push_task = asyncio.create_task(self.run_busy_loop())
        try:
            await asyncio.gather(self.rpc_server.wait_for_termination(), self.push_task)
        except SystemExit:
            logger.info("Receive exit signal.")
            raise
        except Exception as e:  # pylint: disable=broad-except
            logger.error("A background task failed: %s. Shutting down the service.", e)
        finally:
            logger.info("Llumlet Service shutdown...")
            if self.engine_type == BackendType.SGLANG:
                await self.engine_client.stop()
            await self.remove_from_cms()
            if self.push_task and not self.push_task.done():
                self.push_task.cancel()
            await self.rpc_server.stop()

    def get_instance_meta_data(
        self,
        engine_type: str,
        connector_type: Optional[ConnectorType],
        engine_config: Any,
    ):
        utc_now = time.time()
        llumlet_specific_meta = {
            "utc_create": utc_now,
            "utc_update": utc_now,
            "node_id": 0,  # TODO(jiangjiemin): get from eas env variables
            "llumlet_port": self.rpc_port,
            "backend_type": engine_type,
        }
        engine_specific_meta = get_specific_instance_meta_data(
            engine_type, engine_config
        )
        if engine_type == BackendType.SGLANG or (
            engine_type == BackendType.VLLM_V1
            and connector_type == ConnectorType.MOONCAKE
        ):
            # set ip/port for migration
            engine_specific_meta["ip_kvs"] = engine_specific_meta["ip"]
            engine_specific_meta["kvt_port"] = self.rpc_port
        all_metadata_dict = {**engine_specific_meta, **llumlet_specific_meta}
        logger.debug(all_metadata_dict)
        return InstanceMetaData(**all_metadata_dict)

    async def run_busy_loop(self):
        """
        Main loop to monitor parent process and handle shutdown.
        If parent process exists, llumlet will push engine status to cms periodically.
        """
        if not await self.register_with_cms():
            return
        self.last_metadata_report_time = time.time()
        logger.info(
            "Starting periodic status reporting every %s seconds.",
            self.status_push_interval,
        )
        while True:
            start_time = time.time()
            if (
                start_time - self.last_metadata_report_time
            ) >= self.metadata_push_interval:
                # Metadata needs to be resent periodically to act as a heartbeat and
                # to refresh its TTL (Time-To-Live) in the CMS. This ensures that
                # if the Llumlet process dies unexpectedly, its registration will
                # automatically expire and be removed from the CMS.
                if await self.report_metadata_to_cms():
                    self.last_metadata_report_time = start_time
                else:
                    logger.warning(
                        "Failed to resend metadata. Will retry in the next cycle."
                    )
            if self.parent_pid and not psutil.pid_exists(self.parent_pid):
                self.shutdown()
                return
            try:
                if self.update_instance_status_mode == UpdateInstanceStatusMode.PULL:
                    self.instance_status = await self.get_instance_status_from_engine()
                else:
                    self.instance_status = await self.get_instance_status_from_queue()
                await self.report_status_to_cms(self.instance_status)
            # pylint: disable=broad-except
            except Exception:
                logger.exception("An error occurred in get instance status loop")
            elapsed = time.time() - start_time
            if elapsed < self.status_push_interval:
                await asyncio.sleep(self.status_push_interval - elapsed)

    async def _execute_with_timeout(
        self,
        coro_factory: Callable[[], Awaitable[Any]],
        operation_name: str,
        timeout: float,
        max_retries: int = MAX_ENGINE_CLIENT_RETRIES,
        delay: int = ENGINE_CLIENT_RETRY_INTERVAL,
    ) -> tuple[bool, Optional[Any]]:
        last_exception = None
        for attempt in range(max_retries):
            coro = coro_factory()
            try:
                result = await asyncio.wait_for(coro, timeout=timeout)
                return True, result
            except asyncio.TimeoutError as e:
                logger.warning(
                    "Operation '{}' timeout (attempt {}/{}), retry in {} seconds.".format(
                        operation_name, attempt + 1, max_retries, delay
                    )
                )
                if attempt < max_retries - 1:
                    await asyncio.sleep(delay)
                last_exception = e
            except Exception as e:  # pylint: disable=broad-except
                logger.exception(
                    "Error during %s on attempt %s/%s: %s",
                    operation_name,
                    attempt + 1,
                    max_retries,
                    e,
                )
                if attempt < max_retries - 1:
                    await asyncio.sleep(delay)
                last_exception = e
        logger.error(
            "Operation '{}' failed after {} attempts, last error: {}".format(
                operation_name, max_retries, last_exception
            )
        )
        await self.send_unschedulable_status_to_cms()

        return False, None

    async def send_unschedulable_status_to_cms(self):
        logger.info("Reporting Unshcedulable status to CMS...")
        self.instance_status.timestamp_ms = int(time.time() * 1000)
        self.instance_status.schedulable = False
        await self.report_status_to_cms(self.instance_status)

    def shutdown(self):
        """shutdown current process."""
        logger.info("Exiting Llumlet process now.")
        sys.exit(0)

    async def report_status_to_cms(self, cms_status: CMSInstanceStatus):
        """Report cms_instance_status to cms."""
        try:
            await self.cms_client.update_instance_status(
                self.instance_id, cms_status, expired=self.expired_time
            )
        # pylint: disable=broad-except
        except Exception:
            logger.exception("Failed to report status to CMS.")

    async def report_metadata_to_cms(self) -> bool:
        try:
            await self.cms_client.update_instance_metadata(
                self.instance_id, self.cms_instance_metadata, expired=self.expired_time
            )
            return True
        # pylint: disable=broad-except
        except Exception:
            logger.exception("Failed to report metadata to CMS.")
            return False

    async def register_with_cms(self, max_retries=3, delay=5) -> bool:
        """Add instance metadate to cms."""
        logger.info("Registering instance %s with CMS...", self.instance_id)
        last_exception = None
        for attempt in range(max_retries):
            try:
                await self.cms_client.add_instance(
                    self.instance_id, self.cms_instance_metadata, self.expired_time
                )
                logger.info(
                    "Instance %s registered successfully on attempt %s.",
                    self.instance_id,
                    attempt + 1,
                )
                return True
            except Exception as e:  # pylint: disable=broad-except
                last_exception = e
                logger.warning(
                    "Attempt %s/%s to register with CMS failed. Retrying in %s seconds... Error: %s",
                    attempt + 1,
                    max_retries,
                    delay,
                    e,
                )
                if attempt < max_retries - 1:
                    await asyncio.sleep(delay)
        logger.error(
            "Could not register instance with CMS after %s attempts.", max_retries
        )
        raise RuntimeError(
            "Failed to register with CMS after multiple retries"
        ) from last_exception

    async def remove_from_cms(self):
        """Remove instance status with instance_id from cms"""
        try:
            await self.cms_client.remove_instance(self.instance_id)
        # pylint: disable=broad-except
        except Exception:
            logger.exception("Failed to remove instance status from CMS.")

    async def get_instance_status_from_engine(self) -> InstanceStatus:
        """Get instance status from engine core"""
        success, instance_status_dict = await self._execute_with_timeout(
            self.engine_client.get_instance_status,
            "get_instance_status",
            envs.LLUMNIX_ENGINE_GET_STATUS_TIMEOUT,
        )
        if success:
            instance_status = InstanceStatus(**instance_status_dict)
            instance_status = to_cms_status(llumlet_status=instance_status)
            self.complete_instance_status(instance_status)
            return instance_status

        raise RuntimeError("Engine client failed to get_instance_status_from_engine")

    async def get_instance_status_from_queue(self) -> InstanceStatus:
        """Get instance status pushed by engine core from queue"""
        try:
            instance_status = await asyncio.wait_for(
                self._get_instance_status_from_queue(),
                envs.LLUMNIX_ENGINE_GET_STATUS_TIMEOUT,
            )
            self.complete_instance_status(instance_status)
        except (TimeoutError, asyncio.TimeoutError):
            logger.debug(
                "_get_instance_status_from_queue timeout, engine might be idle, fallback to get_instance_status_from_engine"
            )
            instance_status = await self.get_instance_status_from_engine()
        return instance_status

    async def push_instance_status(self, instance_status: CMSInstanceStatus) -> None:
        await self.cms_instance_status_queue.put(instance_status)

    async def _get_instance_status_from_queue(self) -> InstanceStatus:
        instance_status = await self.cms_instance_status_queue.get()
        while not self.cms_instance_status_queue.empty():
            instance_status = self.cms_instance_status_queue.get_nowait()
        return instance_status

    def complete_instance_status(self, instance_status: CMSInstanceStatus) -> None:
        instance_status.timestamp_ms = int(time.time() * 1000)
        instance_status.instance_id = self.instance_id
        # Update migration slots
        if self.enable_mig:
            instance_status.num_available_migrate_in_slots = (
                self.mig_limits.max_req_mig_in - instance_status.num_migrate_in_reqs
            )
            instance_status.num_available_migrate_out_slots = (
                self.mig_limits.max_req_mig_out - instance_status.num_migrate_out_reqs
            )
            if self.detailed_mig:
                instance_status.num_available_migrate_in_tokens = (
                    self.mig_limits.max_token_mig_in
                    - instance_status.num_migrate_in_tokens
                )
                instance_status.num_available_migrate_out_tokens = (
                    self.mig_limits.max_token_mig_out
                    - instance_status.num_migrate_out_tokens
                )
                instance_status.available_kv_cache_usage_ratio_migrate_in = (
                    self.mig_limits.max_kv_cache_usage_ratio_mig_in
                    - instance_status.kv_cache_usage_ratio_migrate_in
                )
                instance_status.available_kv_cache_usage_ratio_migrate_out = (
                    self.mig_limits.max_kv_cache_usage_ratio_mig_out
                    - instance_status.kv_cache_usage_ratio_migrate_out
                )
        if (
            self.last_report_instance_status_timestamp is None
            or time.time() - self.last_report_instance_status_timestamp
            >= envs.LLUMNIX_REPORT_INSTANCE_STATUS_INTERVAL_S
        ):
            self.last_report_instance_status_timestamp = time.time()
            logger.debug("instance_status: {}".format(instance_status))

    async def migrate(
        self,
        dst_engine_host: str,
        dst_engine_port: int,
        migration_params: MigrationParams,
    ) -> bool:
        """Send migration request to engine core"""
        if (
            migration_params.num_reqs
            > self.instance_status.num_available_migrate_out_slots
        ):
            raise NotEnoughSlotsError(
                f"Number of migration requests exceeds engine migration limits. Max_migrate_out_reqs: "
                f"{self.instance_status.num_available_migrate_out_slots} , got {migration_params.num_reqs}"
            )
        if (
            self.detailed_mig
            and migration_params.num_tokens
            > self.instance_status.num_available_migrate_out_tokens
        ):
            raise NotEnoughSlotsError(
                f"Number of migration tokens exceeds engine migration limits. Max_migrate_out_tokens: "
                f"{self.instance_status.num_available_migrate_out_tokens} , got {migration_params.num_tokens}"
            )
        if (
            self.detailed_mig
            and migration_params.kv_cache_usage_ratio
            > self.instance_status.available_kv_cache_usage_ratio_migrate_out
        ):
            raise NotEnoughSlotsError(
                f"KV cache usage ratio of migration request exceeds engine migration limits. Max_migrate_out_kv_cache_usage_ratio: "
                f"{self.instance_status.available_kv_cache_usage_ratio_migrate_out} , got {migration_params.kv_cache_usage_ratio}"
            )
        if (
            self.engine_type != BackendType.VLLM_V1
            or self.connector_type != ConnectorType.HYBRID
            or (
                migration_params.migration_type == MigrationType.NUM_REQ
                and migration_params.num_reqs == -1
            )
        ):
            # Pre-stop migration, attempting twice to drain most of requests.
            # This is necessary because some requests maybe not in running or waiting lists.
            any_req_migrated = False
            any_success = False
            send_time = 1
            if (
                migration_params.migration_type == MigrationType.NUM_REQ
                and migration_params.num_reqs == -1
            ):
                logger.info(
                    "Starting pre-stop migration to %s:%s for all requests.",
                    dst_engine_host,
                    dst_engine_port,
                )
                send_time = PRESTOP_MIGRATION_SEND_TIMES
            for _ in range(send_time):
                success, res = await self._execute_with_timeout(
                    lambda: self.engine_client.migrate_out(
                        dst_engine_host=dst_engine_host,
                        dst_engine_port=dst_engine_port,
                        migration_params=migration_params,
                    ),
                    "migrate",
                    envs.LLUMNIX_ENGINE_MIGRATE_TIMEOUT,
                )
                if success:
                    any_req_migrated = any_req_migrated or res
                    any_success = True
            if any_success:
                return any_req_migrated
        else:
            success, res = await self._execute_with_timeout(
                lambda: self.mig_client.migrate(
                    dst_host=dst_engine_host,
                    dst_port=dst_engine_port,
                    migration_params=migration_params,
                ),
                "migrate",
                envs.LLUMNIX_ENGINE_MIGRATE_TIMEOUT,
            )
            if success:
                return res
        raise RuntimeError("Llumlet failed to finish sending Migrate request(s).")

    async def abort(self, request_ids: List[RequestIDType]) -> None:
        success, _ = await self._execute_with_timeout(
            lambda: self.engine_client.abort(request_ids),
            "abort",
            envs.LLUMNIX_ENGINE_ABORT_TIMEOUT,
        )
        if not success:
            raise RuntimeError(
                "Engine client failed to abort requests {}".format(request_ids)
            )

    async def migrate_in(
        self, serialized_migrate_req: bytes, serialization_format: str
    ) -> bool:
        """Process migration in request with serialized data"""
        success, res = await self._execute_with_timeout(
            lambda: self.engine_client.migrate_in(
                serialized_migrate_req, serialization_format
            ),
            "migrate_in",
            envs.LLUMNIX_ENGINE_MIGRATE_IN_TIMEOUT,
        )
        if not success:
            raise RuntimeError("Engine client failed to process MigrateIn request")
        return res

    async def abort_migrate_in(
        self,
        request_ids: list[str],
        migrate_in_ip_address: str | None,
        migrate_in_port: str | None,
    ) -> None:
        """Process abort migration in request"""
        if self.engine_type == BackendType.SGLANG:
            for req_id in request_ids:
                success, _ = await self._execute_with_timeout(
                    lambda req_id=req_id: self.engine_client.abort_migrate_in(
                        req_id, migrate_in_ip_address, migrate_in_port
                    ),
                    "abort_migrate_in",
                    envs.LLUMNIX_ENGINE_ABORT_TIMEOUT,
                )
                if not success:
                    raise RuntimeError(
                        "Engine client failed to process AbortMigrateIn request for {}".format(
                            req_id
                        )
                    )
        else:
            success, _ = await self._execute_with_timeout(
                # pylint: disable=no-value-for-parameter
                lambda: self.engine_client.abort_migrate_in(request_ids),
                "abort_migrate_in",
                envs.LLUMNIX_ENGINE_ABORT_TIMEOUT,
            )
            if not success:
                raise RuntimeError(
                    "Engine client failed to process AbortMigrateIn request for {}".format(
                        request_ids
                    )
                )
