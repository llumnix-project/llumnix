from abc import ABC, abstractmethod


class BaseQueueServer(ABC):
    @abstractmethod
    async def get(self, timeout):
        raise NotImplementedError

    @abstractmethod
    async def run_server_loop(self):
        raise NotImplementedError

    @abstractmethod
    def stop(self):
        raise NotImplementedError
