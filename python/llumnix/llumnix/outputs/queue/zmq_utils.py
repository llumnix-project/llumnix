from dataclasses import dataclass, field
from enum import Enum
from typing import List, Any

from llumnix.utils import MigrationParams

RPC_SUCCESS_STR = "SUCCESS"
MIGRATION_SUCCESS_STR = "SUCCESS"
MIGRATION_FAILURE_STR = "FAILURE"


@dataclass
class RPCPutNoWaitQueueRequest:
    item: Any = field(default=None)
    send_time: float = field(default=None)


@dataclass
class RPCPutNoWaitBatchQueueRequest:
    items: List[Any] = field(default=None)
    send_time: float = field(default=None)


class RPCRequestType(Enum):
    """
    RPC Request types defined as hex byte strings, so it can be sent over sockets
    without separate encoding step.
    """

    HANDSHAKE = b"\x00"
    PUT_NOWAIT = b"\x01"
    PUT_NOWAIT_BATCH = b"\x02"


@dataclass
class LlumletMigrateRequest:
    dst_host: str = field(default=None)
    dst_port: int = field(default=None)
    migration_params: MigrationParams = field(default=None)


class LlumletRequestType(Enum):
    MIGRATE = b"\x03"


# ================== RPC exceptions ==================
class RPCClientClosedError(Exception):
    """
    Exception class raised when the client is used post-close.

    The client can be closed, which closes the ZMQ context. This normally
    happens on server shutdown. In some cases, methods like abort and
    do_log_stats will still be called and then try to open a socket, which
    causes a ZMQError and creates a huge stack trace.
    So, we throw this error such that we can suppress it.
    """


class RPCQueueEmptyError(Exception):
    """
    Exception class raised when the queue of RPC server is empty.
    """


class RPCQueueFullError(Exception):
    """
    Exception class raised when the queue of RPC server is full.
    """
