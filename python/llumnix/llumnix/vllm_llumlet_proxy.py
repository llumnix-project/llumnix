import os
import time
import queue
from typing import List
import uuid

from vllm.config import VllmConfig
from vllm.v1.request import Request
from vllm.v1.core.sched.output import SchedulerOutput
from vllm.v1.engine import EngineCoreRequestType
from vllm.v1.outputs import ModelRunnerOutput

from llumnix import envs
from llumnix.constants import MIGRATION_FRONTEND_INIT_TIMEOUT
from llumnix.compat.vllm_compat import get_ip
from llumnix.engine_client.utils import get_connector_type
from llumnix.instance_info import ConnectorType, InstanceStatus
from llumnix.llumlet import Llumlet
from llumnix.logging.logger import init_logger
from llumnix.outputs.forwarder.vllm_v1.thread_output_forwarder import ThreadOutputForwarder
from llumnix.status_collector.vllm_v1.status_updater import StatusUpdater, StepPhase
from llumnix.utils import (
    MigrationParams,
    RequestIDType,
)

logger = init_logger(__name__)

def random_uuid() -> str:
    return str(uuid.uuid4().hex)


def init_instance_status_with_scheduler(scheduler):
    num_total_gpu_blocks = scheduler.cache_config.num_gpu_blocks
    return InstanceStatus(
        num_total_gpu_blocks=num_total_gpu_blocks,
        timestamp_ms=int(time.time() * 1000),
        step_id=0
    )

class VLLMLlumletProxy:
    def __init__(self, vllm_config: VllmConfig, scheduler: "Scheduler", engine_index: int = 0, engine_type: str = "vLLM v1"): # type: ignore

        self.enable_migration = envs.LLUMNIX_ENABLE_MIGRATION
        self.mig_async = False
        self.mig_server_address = None
        # vllm does not support enable kvt and kvs simultaneously
        self.migration_frontend = None
        self.connector_type = get_connector_type(engine_type, vllm_config)
        if self.enable_migration:
            if not self.connector_type:
                logger.warning("Migration enabled but no kv_transfer_config found. Migration will be disabled.")
                self.enable_migration = False
                os.environ["LLUMNIX_ENABLE_MIGRATION"] = "0"
            elif self.connector_type == ConnectorType.HYBRID:
                # pylint: disable=import-outside-toplevel
                from llumnix.migration_frontend.vllm_v1.kvt_migration_frontend import KVTMigrationFrontend
                self.migration_frontend = KVTMigrationFrontend(vllm_config=vllm_config,
                                                               dp_rank=vllm_config.parallel_config.data_parallel_rank,
                                                               scheduler=scheduler)
                self.mig_async = True
                self.migration_frontend.mig_server_ready_event.wait(timeout=MIGRATION_FRONTEND_INIT_TIMEOUT)
                self.mig_server_address = self.migration_frontend.mig_server.address
            elif self.connector_type == ConnectorType.MOONCAKE:
                # pylint: disable=import-outside-toplevel
                from llumnix.migration_frontend.vllm_v1.mooncake_migration_frontend import MooncakeMigrationFrontend
                self.migration_frontend = MooncakeMigrationFrontend(vllm_config=vllm_config,
                                                                    dp_rank=vllm_config.parallel_config.data_parallel_rank,
                                                                    scheduler=scheduler)
            else:
                raise NotImplementedError(f"Failed to initialize migration frontend. kv_transfer_config:{vllm_config.kv_transfer_config}")
        self.llumlet = Llumlet(engine_type=engine_type,
                               engine_config=vllm_config,
                               mig_address=self.mig_server_address,
                               connector_type = self.connector_type)
        self.llumlet.start()

        self.rpc_port = self.llumlet.rpc_port
        self.llumlet_grpc_address = "{}:{}".format(get_ip(), self.rpc_port)
        self.output_forwarder = ThreadOutputForwarder(
            vllm_config.instance_id,
            self.llumlet_grpc_address,
            engine_index
        )
        self.status_updater = StatusUpdater(scheduler, engine_type, vllm_config, self.mig_async, \
            self.connector_type, self.migration_frontend, self.output_forwarder)
        self.scheduler = scheduler

    def add_llumlet_address(self, addresses: dict[str, str]) -> dict[str, str]:
        return self.llumlet.add_llumlet_addresses(addresses)

    def update_instance_status(
        self,
        instance_status: InstanceStatus,
        step_phase: StepPhase,
        scheduler_output: SchedulerOutput = None,
        model_output: ModelRunnerOutput = None,
    ):
        self.status_updater.update_instance_status(
            instance_status,
            step_phase,
            scheduler_output,
            model_output,
        )

    def forward_outputs(self, outputs) -> List[RequestIDType]:
        return self.output_forwarder.forward_outputs(outputs)

    def add_abort_requests(self, request_queue: queue.Queue, request_ids: List[RequestIDType]) -> None:
        request_queue.put_nowait((EngineCoreRequestType.ABORT, request_ids))

    def migrate_out(
        self,
        migration_params: MigrationParams,
        dst_engine_host: str,
        dst_engine_port: int,
    ) -> bool:
        if self.migration_frontend:
            if self.mig_async:
                return self.migration_frontend.migrate_out_prestop(
                    migration_params,
                    dst_engine_host,
                    dst_engine_port,
                )
            return self.migration_frontend.migrate_out(
                    migration_params,
                    dst_engine_host,
                    dst_engine_port,
            )
        logger.info("MigrationFrontend was not initialized.")
        return False

    def migrate_in(self, serialized_data: bytes) -> bool:
        if self.migration_frontend:
            return self.migration_frontend.migrate_in(serialized_data)
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

    def shutdown(self):
        """
        Shutdown output forwarder and migration frontend.
        Terminate llumlet Process.
        """
        self.output_forwarder.stop()
        if self.migration_frontend:
            self.migration_frontend.shutdown()
        self.llumlet.terminate()
