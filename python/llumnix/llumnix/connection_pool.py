import asyncio
from collections import OrderedDict
from enum import Enum
from typing import Union

import grpc

import zmq


def get_open_zmq_ipc_path(ip, port) -> str:
    return "tcp://{}:{}".format(ip, port)


def get_grpc_channel_path(ip, port) -> str:
    return "{}:{}".format(ip, port)


class ConnectionType(str, Enum):
    ZMQ_SOCKET = "ZMQ_SOCKET"
    GRPC_CHANNEL = "GRPC_CHANNEL"


class SocketConnection:
    def __init__(self, socket: zmq.asyncio.Socket):
        self.socket: zmq.asyncio.Socket = socket
        # Ensures exclusive access to the socket across coroutines.
        self.lock = asyncio.Lock()

    async def __aenter__(self):
        await self.lock.acquire()
        return self.socket

    async def __aexit__(self, exc_type, exc_value, traceback):
        self.lock.release()

    def close(self):
        self.socket.close(linger=0)


class GrpcConnection:
    def __init__(self, channel: grpc.aio.Channel):
        self.channel: grpc.aio.Channel = channel
        self.lock = asyncio.Lock()

    async def __aenter__(self):
        await self.lock.acquire()
        return self.channel

    async def __aexit__(self, exc_type, exc_value, traceback):
        self.lock.release()

    async def close(self):
        await self.channel.close(linger=0)


Connection = Union[SocketConnection, GrpcConnection]


class LruConnectionPool:
    """A thread-safe LRU connection pool for ZMQ sockets and gRPC channels."""

    def __init__(
        self,
        connection_type: ConnectionType,
        context: zmq.asyncio.Context = None,
        max_connections: int = -1,
    ):
        self.max_connections = max_connections
        self.connection_type = connection_type
        self.connection_pool: OrderedDict[str, Connection] = OrderedDict()
        if connection_type == ConnectionType.ZMQ_SOCKET:
            self.context = context

    def get_connection_through_address(self, dst_address: str) -> Connection:
        if dst_address in self.connection_pool:
            self.connection_pool.move_to_end(dst_address, last=True)
            return self.connection_pool[dst_address]

        if (
            self.max_connections > 0
            and len(self.connection_pool) >= self.max_connections
        ):
            self.connection_pool.popitem(last=False)

        if self.connection_type == ConnectionType.ZMQ_SOCKET:
            socket = self.context.socket(zmq.DEALER)
            socket.connect(dst_address)
            socket_connection = SocketConnection(socket)
            self.connection_pool[dst_address] = socket_connection
        else:
            channel = grpc.aio.insecure_channel(dst_address)
            channel_connection = GrpcConnection(channel)
            self.connection_pool[dst_address] = channel_connection

        return self.connection_pool[dst_address]

    def get_connection_address(self, ip, port) -> str:
        if self.connection_type == ConnectionType.ZMQ_SOCKET:
            return get_open_zmq_ipc_path(ip, port)
        return get_grpc_channel_path(ip, port)

    def get_connection(self, ip, port) -> SocketConnection:
        dst_address = self.get_connection_address(ip, port)
        return self.get_connection_through_address(dst_address)

    async def close_connection(self, ip, port):
        dst_address = self.get_connection_address(ip, port)

        if dst_address in self.connection_pool:
            connection = self.connection_pool[dst_address]
            close_method = connection.close
            if asyncio.iscoroutinefunction(close_method):
                await close_method()
            else:
                close_method()
            del self.connection_pool[dst_address]

    async def close_all(self):
        close_tasks = []
        for connection in self.connection_pool.values():
            close_method = connection.close
            if asyncio.iscoroutinefunction(close_method):
                close_tasks.append(close_method())
            else:
                close_method()

        if close_tasks:
            await asyncio.gather(*close_tasks, return_exceptions=True)

        self.connection_pool.clear()
