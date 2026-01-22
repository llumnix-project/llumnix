from abc import ABC, abstractmethod
from typing import List

from llumnix.llumlet.instance_info import InstanceStatus
from llumnix.utils import MigrationParams, RequestIDType


class BaseEngineClient(ABC):
    """Abstract Base Class for engine clients."""

    @abstractmethod
    async def get_instance_status(self) -> InstanceStatus:
        """Get instance status from the engine core."""
        raise NotImplementedError

    @abstractmethod
    async def migrate_out(self, dst_engine_host: str, dst_engine_port: int, migration_params: MigrationParams) -> bool:
        """Send migration request to the engine core."""
        raise NotImplementedError

    @abstractmethod
    async def abort(self, request_ids: List[RequestIDType]) -> None:
        """Abort request."""
        raise NotImplementedError
