import time
import asyncio
import logging


class MockVllmConfig:
    def __init__(self):
        self.parallel_config = None


class MockInstanceMetaData:
    def __init__(self, instance_id, utc_create, utc_update):
        self.instance_id = instance_id
        self.utc_create = utc_create
        self.utc_update = utc_update


class MockEngineClient:
    def __init__(self, engine_config, client_addresses):
        logging.info(
            f"MockLlumletCoreClient initialized with addresses: {client_addresses}"
        )
        self.get_core_instance_status_func = None

    async def get_core_instance_status(self):
        if self.get_core_instance_status_func:
            return await self.get_core_instance_status_func()
        await asyncio.sleep(0.01)
        return {"schedulable": True}


class MockAsyncLLumletRPCServer:
    def __init__(self, port):
        self.port = port
        self._stop_event = asyncio.Event()

    async def start(self, handler):
        logging.info(f"Mock RPC server starting on port {self.port}")

    async def wait_for_termination(self):
        await self._stop_event.wait()

    async def stop(self):
        self._stop_event.set()


class MockCMSWriteClient:
    """A mock for the CMSWriteClient to use in tests."""

    def __init__(self, *args, **kwargs):
        logging.info("MockCMSWriteClient initialized.")
        self.add_instance_called = False
        self.update_status_call_count = 0

    async def add_instance(self, instance_id, instance_metadata):
        """Mock method for adding an instance."""
        logging.info(f"MockCMS: add_instance called for {instance_id}")
        self.add_instance_called = True
        await asyncio.sleep(0.01)

    async def update_instance_status(self, instance_id, instance_status):
        """Mock method for updating instance status."""
        logging.info(f"MockCMS: update_instance_status called for {instance_id}")
        self.update_status_call_count += 1
        await asyncio.sleep(0.01)
