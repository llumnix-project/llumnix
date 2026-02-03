import asyncio
from typing import Any, Optional

import grpc

from llumnix.constants import RPC_TIMEOUT
from llumnix.server.proto import llumlet_server_pb2, llumlet_server_pb2_grpc
from llumnix.logging.logger import init_logger
from llumnix.utils import MigrationParams, MigrationType, NotEnoughSlotsError
from llumnix.cms.proto.cms_pb2 import InstanceStatus as CMSInstanceStatus

logger = init_logger(__name__)


class AsyncLlumletRPCServer:
    def __init__(self, port: Optional[int]):
        self.port = str(port)

    async def start(self, handler: Any) -> None:
        self.server = grpc.aio.server()
        llumlet_server_pb2_grpc.add_LlumletServicer_to_server(self.AsyncServicer(handler), self.server)
        bound_port = self.server.add_insecure_port(f"[::]:{self.port}")
        logger.info("Server port configured. Attempting to bind to port %s...", bound_port)
        await self.server.start()
        logger.info("Server started successfully and is listening on port %s.", bound_port)
        return bound_port

    async def stop(self):
        if self.server:
            logger.info("Stopping RPC server...")
            await self.server.stop(grace=1.0)
            logger.info("Server stopped.")

    async def wait_for_termination(self):
        if self.server:
            await self.server.wait_for_termination()

    class AsyncServicer(llumlet_server_pb2_grpc.LlumletServicer):
        def __init__(self, handler):
            self.handler = handler
            self.timeout = RPC_TIMEOUT

        # pylint: disable=invalid-overridden-method
        async def Migrate(
            self, request: llumlet_server_pb2.MigrateRequest, context: grpc.aio.ServicerContext
        ) -> llumlet_server_pb2.MigrateResponse:
            dst_engine_ip = request.dst_engine_ip
            try:
                dst_engine_port = int(request.dst_engine_port)
            except TypeError as e:
                logger.error("Can not convert port to int type: %s", e, exc_info=True)
                context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                context.set_details("Migration failed due to invalid argument: {}".format(e))
                return llumlet_server_pb2.MigrateResponse(
                    message="Migration failed due to invalid argument {}.".format(str(e)),
                    success=False
                )
            try:
                validate_migration_type(request.migration_type)
                mig_params = generate_migration_params(request)
                res = await asyncio.wait_for(
                    self.handler.migrate(dst_engine_ip, dst_engine_port, mig_params), timeout=self.timeout
                )
                if res:
                    return llumlet_server_pb2.MigrateResponse(
                        message="Migrate requests from {} to {}.".format(request.src_engine_id, request.dst_engine_id),
                        success=True
                    )
                return llumlet_server_pb2.MigrateResponse(
                    message="Migration failed due to enginecore can not find expected requests.",
                    success=False
                )
            except asyncio.TimeoutError as e:
                logger.warning("Migration request timed out after %s seconds.", self.timeout)
                context.set_code(grpc.StatusCode.DEADLINE_EXCEEDED)
                context.set_details(
                    "Migration failed to complete within the server's timeout of {} seconds.".format(self.timeout)
                )
                return llumlet_server_pb2.MigrateResponse(message="Migration failed due to timeout {}.".format(str(e)),
                                                          success=False)
            except ValueError as e:
                logger.error("Invalid argument: %s", e, exc_info=True)
                context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                context.set_details("Migration failed due to invalid argument: {}".format(e))
                return llumlet_server_pb2.MigrateResponse(
                    message="Migration failed due to invalid argument {}.".format(str(e)),
                    success=False
                )
            except NotEnoughSlotsError as e:
                logger.error(e)
                context.set_code(grpc.StatusCode.RESOURCE_EXHAUSTED)
                context.set_details("No enough migrate out slots for requests")
                return llumlet_server_pb2.MigrateResponse(message=f"No enough migrate out slots. {str(e)}.",
                                                          success=False)
            except Exception as e: # pylint: disable=broad-except
                logger.error("Migration failed: %s", e, exc_info=True)
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details("Migration failed due to an unexpected server error: {}".format(e))
                return llumlet_server_pb2.MigrateResponse(
                    message="Migration failed due to unexpected exception {}.".format(str(e)),
                    success=False
                )

        # pylint: disable=arguments-renamed,invalid-overridden-method
        async def Abort(
            self, requests: llumlet_server_pb2.AbortRequests, context: grpc.aio.ServicerContext
        ) -> llumlet_server_pb2.AbortResponse:
            request_ids = list(requests.request_ids)
            logger.info("Llumlet try to abort requests %s", request_ids)
            try:
                await asyncio.wait_for(self.handler.abort(request_ids), timeout=self.timeout)
                return llumlet_server_pb2.AbortResponse(
                    success=True,
                    message="Requests {} aborted.".format(request_ids),
                )
            except asyncio.TimeoutError as e:
                logger.warning("Aborting timed out after %s seconds.", self.timeout)
                context.set_code(grpc.StatusCode.DEADLINE_EXCEEDED)
                context.set_details(
                    "Failed to abort requests {} within the server's timeout"
                    " of {} seconds.".format(request_ids, self.timeout)
                )
                return llumlet_server_pb2.AbortResponse(
                    success=False,
                    message="Failed to abort due to timeout {}.".format(str(e)),
                )
            except Exception as e:  # pylint: disable=broad-except
                logger.exception("Failed to abort")
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details("Failed to abort due to an unexpected server error: {}".format(e))
                return llumlet_server_pb2.AbortResponse(
                    success=False,
                    message="Failed to abort due to unexpected exception {}.".format(str(e))
                )

        async def MigrateIn(
            self, request: llumlet_server_pb2.MigrateInRequest, context: grpc.aio.ServicerContext
        ) -> llumlet_server_pb2.MigrateInResponse:
            serialization_format = request.serialization_format
            if serialization_format:
                logger.info("Received MigrateIn request with format: %s", serialization_format)
            try:
                res = await asyncio.wait_for(
                    self.handler.migrate_in(request.serialized_migrate_req, serialization_format),
                    timeout=self.timeout
                )
                return llumlet_server_pb2.MigrateInResponse(
                    success=res,
                    message="MigrateIn request processed successfully."
                )
            except asyncio.TimeoutError as e:
                logger.warning("MigrateIn request timed out after %s seconds.", self.timeout)
                context.set_code(grpc.StatusCode.DEADLINE_EXCEEDED)
                context.set_details("Failed to process MigrateIn within the server's timeout of {} seconds.".format(self.timeout))
                return llumlet_server_pb2.MigrateInResponse(
                    success=False,
                    message="MigrateIn failed due to timeout {}.".format(str(e))
                )
            except Exception as e:  # pylint: disable=broad-except
                logger.error("Failed to process MigrateIn: %s", e, exc_info=True)
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details("MigrateIn failed due to an unexpected server error: {}".format(e))
                return llumlet_server_pb2.MigrateInResponse(
                    success=False,
                    message="MigrateIn failed due to unexpected exception {}.".format(str(e))
                )

        async def AbortMigrateIn(
            self, request: llumlet_server_pb2.AbortMigrateInRequest, context: grpc.aio.ServicerContext
        ) -> llumlet_server_pb2.AbortResponse:
            request_ids = request.request_ids
            if request.has_field('migrate_in_ip_address') and request.has_field('migrate_in_port'):
                migrate_in_ip_address = request.migrate_in_ip_address
                migrate_in_port = request.migrate_in_port
                logger.info("Received AbortMigrateIn request for request_ids: %s", request_ids)
            else:
                migrate_in_ip_address = None
                migrate_in_port = None
                logger.info("Received AbortMigrateIn request for request_ids: %s", request_ids)
            try:
                await asyncio.wait_for(
                    self.handler.abort_migrate_in(request_ids, migrate_in_ip_address, migrate_in_port),
                    timeout=self.timeout
                )
                return llumlet_server_pb2.AbortResponse(
                    success=True,
                    message="AbortMigrateIn request processed successfully. requests ids: {}".format(request_ids)
                )
            except asyncio.TimeoutError as e:
                logger.warning("AbortMigrateIn request timed out after %s seconds.", self.timeout)
                context.set_code(grpc.StatusCode.DEADLINE_EXCEEDED)
                context.set_details("Failed to process AbortMigrateIn within the server's timeout of {} seconds.".format(self.timeout))
                return llumlet_server_pb2.AbortResponse(
                    success=False,
                    message="AbortMigrateIn failed due to timeout {}.".format(str(e))
                )
            except Exception as e:  # pylint: disable=broad-except
                logger.error("Failed to process AbortMigrateIn: %s", e, exc_info=True)
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details("AbortMigrateIn failed due to an unexpected server error: {}".format(e))
                return llumlet_server_pb2.AbortResponse(
                    success=False,
                    message="AbortMigrateIn failed due to unexpected exception {}.".format(str(e))
                )

        async def PushInstanceStatus(
            self, instance_status: CMSInstanceStatus, context: grpc.aio.ServicerContext
        ) -> llumlet_server_pb2.PushInstanceStatusResponse:
            try:
                await self.handler.push_instance_status(instance_status)
                return llumlet_server_pb2.PushInstanceStatusResponse(
                    success=True,
                    message="Push instance status successfully."
                )
            # pylint: disable=broad-except
            except Exception as e:
                logger.error("Failed to process PushInstanceStatus: %s", e, exc_info=True)
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details("PushInstanceStatus failed due to an unexpected server error: {}".format(e))
                return llumlet_server_pb2.PushInstanceStatusResponse(
                    success=False,
                    message="PushInstanceStatus failed due to unexpected exception {}.".format(str(e))
                )

def validate_migration_type(type_string: str) -> None:
    """
    Check whether string is in MigrationType
    """
    try:
        MigrationType(type_string)
    except ValueError as e:
        logger.error("Invalid migration type: %s. Must be one of: %s",
                     type_string, [member.value for member in MigrationType])
        raise ValueError("'{}' is not a valid MigrationType.".format(type_string)) from e

def generate_migration_params(request: llumlet_server_pb2.MigrateRequest):
    migration_params: MigrationParams = MigrationParams(migration_type=request.migration_type,
                                                    mig_req_policy=request.migration_req_policy,
                                                    trigger_policy=request.trigger_policy)
    if request.migration_type == MigrationType.NUM_REQ.value:
        if not request.HasField('num_reqs'):
            raise ValueError("Empty num_reqs. If migration_type is NUM_REQ, num_reqs is necessary.")
        migration_params.num_reqs = request.num_reqs
    if request.migration_type == MigrationType.TOKEN.value:
        if not request.HasField('num_tokens'):
            raise ValueError("Empty num_tokens. If migration_type is TOKEN, num_tokens is necessary.")
        migration_params.num_tokens = request.num_tokens
    if request.migration_type == MigrationType.RATIO.value:
        if not request.HasField('kv_cache_usage_ratio'):
            raise ValueError("Empty kv_cache_usage_ratio. If migration_type is RATIO, kv_cache_usage_ratio is necessary.")
        migration_params.kv_cache_usage_ratio = request.kv_cache_usage_ratio
    logger.info("Received Migration request. migration_params: %s", migration_params)
    return migration_params
