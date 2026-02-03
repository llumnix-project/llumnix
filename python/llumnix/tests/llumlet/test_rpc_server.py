# tests/test_async_server.py

import unittest
import asyncio
from unittest.mock import AsyncMock
import grpc
from llumnix.llumlet.rpc_server import AsyncLlumletRPCServer
from llumnix.llumlet.proto import llumlet_server_pb2
from llumnix.llumlet.proto import llumlet_server_pb2_grpc
from llumnix.utils import MigrationParams, MigrationType, NotEnoughSlotsError, RequestMigrationPolicy


class MockLlumletHandler():
    def __init__(self):
        self.migrate = AsyncMock()


class TestAsyncLlumletRPCServer(unittest.IsolatedAsyncioTestCase):

    async def asyncSetUp(self):
        self.mock_handler = MockLlumletHandler()
        self.server = AsyncLlumletRPCServer(port=50051)
        self.bound_port = await self.server.start(self.mock_handler)
        self.server_task = asyncio.create_task(self.server.wait_for_termination())
        await asyncio.sleep(1)
        self.channel = grpc.aio.insecure_channel(f'localhost:{self.bound_port}')
        self.stub = llumlet_server_pb2_grpc.LlumletStub(self.channel)

    async def asyncTearDown(self):
        await self.channel.close()
        await self.server.stop()
        if not self.server_task.done():
            self.server_task.cancel()
            try:
                await self.server_task
            except asyncio.CancelledError:
                pass

    async def test_migrate_success(self):
        """Test migrate success"""

        src_engine_id = "a"
        dst_engine_id = "b"
        migrate_request = llumlet_server_pb2.MigrateRequest(
            src_engine_id=src_engine_id,
            dst_engine_id=dst_engine_id,
            dst_engine_ip="192.168.1.200",
            dst_engine_port=9999,
            migration_req_policy=RequestMigrationPolicy.SR,
            migration_type=MigrationType.NUM_REQ,
            num_reqs=1,
            trigger_policy="NEUTRAL_LOAD",
        )
        response = await self.stub.Migrate(migrate_request)

        self.mock_handler.migrate.assert_awaited_once_with("192.168.1.200", 9999, MigrationParams(migration_type='NUM_REQ', mig_req_policy='SR', num_reqs=1, num_tokens=0, kv_cache_usage_ratio=0, trigger_policy='NEUTRAL_LOAD'))
        expected_message = "Migrate requests from %s to %s." % (src_engine_id, dst_engine_id)
        self.assertEqual(response.message, expected_message)

    async def test_migrate_failure_on_exception(self):
        """Test handler.migrate raise exception"""

        error_message = "Error"
        self.mock_handler.migrate.side_effect = Exception(error_message)

        src_engine_id = "a"
        dst_engine_id = "b"
        migrate_request = llumlet_server_pb2.MigrateRequest(
            src_engine_id=src_engine_id,
            dst_engine_id=dst_engine_id,
            dst_engine_ip="192.168.1.200",
            dst_engine_port=9999,
            migration_req_policy=RequestMigrationPolicy.SR,
            migration_type=MigrationType.NUM_REQ,
            num_reqs=1,
            trigger_policy="NEUTRAL_LOAD",
        )
        try:
            await self.stub.Migrate(migrate_request)
        except grpc.aio.AioRpcError as e:
            self.assertEqual(e.code(), grpc.StatusCode.INTERNAL)
            self.assertIn("Error", e.details())
        self.mock_handler.migrate.assert_awaited_once_with("192.168.1.200", 9999, MigrationParams(migration_type='NUM_REQ', mig_req_policy='SR', num_reqs=1, num_tokens=0, kv_cache_usage_ratio=0, trigger_policy='NEUTRAL_LOAD'))

    async def test_migrate_timeout(self):
        """Test handler.migrate timeout."""
        await self.channel.close()
        await self.server.stop()
        self.server_task.cancel()
        try:
            await self.server_task
        except asyncio.CancelledError:
            pass

        timeout = 0.1
        timeout_server = AsyncLlumletRPCServer(port=50051)
        async def slow_migrate(*args, **kwargs):
            await asyncio.sleep(timeout+0.1)
            return ""

        self.mock_handler.migrate.side_effect = slow_migrate

        bound_port = await timeout_server.start(self.mock_handler)
        channel = grpc.aio.insecure_channel(f'localhost:{bound_port}')
        stub = llumlet_server_pb2_grpc.LlumletStub(channel)

        src_engine_id = "a"
        dst_engine_id = "b"
        migrate_request = llumlet_server_pb2.MigrateRequest(
            src_engine_id=src_engine_id,
            dst_engine_id=dst_engine_id,
            dst_engine_ip="192.168.1.200",
            dst_engine_port=9999,
            migration_req_policy=RequestMigrationPolicy.SR,
            migration_type=MigrationType.NUM_REQ,
            num_reqs=1,
            trigger_policy="NEUTRAL_LOAD",
        )

        try:
            await stub.Migrate(migrate_request)
        except grpc.aio.AioRpcError as e:
            self.assertEqual(e.code(), grpc.StatusCode.DEADLINE_EXCEEDED)
            self.assertIn("timeout", e.details())
            self.mock_handler.migrate.assert_awaited_once()
        finally:
            await channel.close()
            await timeout_server.stop()

    async def test_batched_migrate_success(self):
        """Test migrate success"""

        src_engine_id = "a"
        dst_engine_id = "b"
        num_reqs = 5
        migrate_request = llumlet_server_pb2.MigrateRequest(
            src_engine_id=src_engine_id,
            dst_engine_id=dst_engine_id,
            dst_engine_ip="192.168.1.200",
            dst_engine_port=9999,
            migration_req_policy=RequestMigrationPolicy.SR,
            migration_type=MigrationType.NUM_REQ,
            num_reqs = num_reqs,
            trigger_policy="NEUTRAL_LOAD",
        )
        response = await self.stub.Migrate(migrate_request)

        self.mock_handler.migrate.assert_awaited_once_with("192.168.1.200", 9999, MigrationParams(migration_type='NUM_REQ', mig_req_policy='SR', num_reqs=num_reqs, num_tokens=0, kv_cache_usage_ratio=0, trigger_policy='NEUTRAL_LOAD'))
        expected_message = f"Migrate requests from {src_engine_id} to {dst_engine_id}."
        self.assertEqual(response.message, expected_message)

    async def test_batched_migrate_failed(self):
        """Test migrate failed"""

        src_engine_id = "a"
        dst_engine_id = "b"
        migrate_request = llumlet_server_pb2.MigrateRequest(
            src_engine_id=src_engine_id,
            dst_engine_id=dst_engine_id,
            dst_engine_ip="192.168.1.200",
            dst_engine_port=9999,
            migration_req_policy=RequestMigrationPolicy.SR,
            migration_type=MigrationType.NUM_REQ,
            trigger_policy="NEUTRAL_LOAD",
        )
        try:
            await self.stub.Migrate(migrate_request)
        except grpc.aio.AioRpcError as e:
            self.assertEqual(e.code(), grpc.StatusCode.INVALID_ARGUMENT)
            self.assertIn("Empty num_reqs", e.details())

    async def test_migrate_failed_invalid_migration_type(self):
        """Test migrate failure due to invalid migration_type."""
        migrate_request = llumlet_server_pb2.MigrateRequest(
            src_engine_id="a",
            dst_engine_id="b",
            migration_type="INVALID_TYPE",
        )

        with self.assertRaises(grpc.aio.AioRpcError) as cm:
            await self.stub.Migrate(migrate_request)
        self.assertEqual(cm.exception.code(), grpc.StatusCode.INVALID_ARGUMENT)
        self.assertIn("'INVALID_TYPE' is not a valid MigrationType.", cm.exception.details())
        self.mock_handler.migrate.assert_not_awaited()

    async def test_migrate_failed_not_enough_slots(self):
        """Test migrate failed when handler raises NotEnoughSlotsError."""
        error_message = "Number of migration requests exceeds engine migration limits."
        self.mock_handler.migrate.side_effect = NotEnoughSlotsError(error_message)

        migrate_request = llumlet_server_pb2.MigrateRequest(
            src_engine_id="a",
            dst_engine_id="b",
            migration_type=MigrationType.NUM_REQ,
            num_reqs=5,
        )

        with self.assertRaises(grpc.aio.AioRpcError) as cm:
            await self.stub.Migrate(migrate_request)
        self.assertEqual(cm.exception.code(), grpc.StatusCode.RESOURCE_EXHAUSTED)
        self.assertEqual(cm.exception.details(), "No enough migrate out slots for requests")
        self.mock_handler.migrate.assert_awaited_once()

if __name__ == '__main__':
    unittest.main()
