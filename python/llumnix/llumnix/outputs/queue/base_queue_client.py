from abc import ABC, abstractmethod
from typing import Any


class BaseQueueClient(ABC):
    @abstractmethod
    async def put_nowait(self, item: Any):
        raise NotImplementedError
