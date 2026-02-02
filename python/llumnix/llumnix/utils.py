import asyncio
from dataclasses import dataclass
import os
import random
import socket
import time
import threading
from enum import Enum
from typing import Optional, Union

import netifaces
import uvloop

from llumnix import envs
from llumnix.logging.logger import init_logger

logger = init_logger(__name__)

RequestIDType = Union[str, int]
_MAX_PORT = 65536


class MigrationType(str, Enum):
    NUM_REQ = "NUM_REQ"
    TOKEN = "TOKEN"
    RATIO = "RATIO"


class RequestMigrationPolicy(str, Enum):
    LCR = "LCR"  # last running
    FCR = "FCR"  # first running
    LR = "LR"   # longest running
    SR = "SR"   # shortest running
    FCW = "FCW" # first waiting
    FCWSR = "FCWSR" # first waiting and shortest running


class MigrationTriggerPolicy(str, Enum):
    NEUTRAL_LOAD = "NEUTRAL_LOAD"
    DECODE_LOAD = "DECODE_LOAD"
    PREFILL_FAILOVER = "PREFILL_FAILOVER"
    DECODE_FAILOVER = "DECODE_FAILOVER"
    NEUTRAL_FAILOVER = "NEUTRAL_FAILOVER"
    CLEANUP_DECODE_REQUESTS_ON_PREFILL = "CLEAN_UP_DECODE_REQUESTS_ON_PREFILL"
    AGGREGATE_DECODE_REQUESTS_ON_PREFILL = "AGGREGATE_DECODE_REQUESTS_ON_PREFILL"
    EASE_BUSY_DECODE_WITH_FREE_PREFILL = "EASE_BUSY_DECODE_WITH_FREE_PREFILL"


class ForwardOutputType(str, Enum):
    """Output types for ThreadOutputForwarder to forward.
    """
    REQUEST_OUTPUTS = "REQUEST_OUTPUTS"
    RPC_RESULTS = "RPC_RESULTS"
    STOP = "STOP"


class UpdateInstanceStatusMode(str, Enum):
    """Update instance status mode."""
    PUSH = "push"
    PULL = "pull"


@dataclass
class MigrationParams:
    """Parameters for select migrate out reqs."""
    migration_type: MigrationType = MigrationType.NUM_REQ
    mig_req_policy:RequestMigrationPolicy = RequestMigrationPolicy.SR
    num_reqs: int = 1
    num_tokens: int = 0
    block_ratio: float = 0
    trigger_policy: str = ""


@dataclass
class MigrationLimits:
    """Migration limits"""
    max_req_mig_in:int = 1
    max_req_mig_out:int = 1
    max_token_mig_in:int = 10000
    max_token_mig_out:int = 10000
    max_block_ratio_mig_in:float = 0.3
    max_block_ratio_mig_out:float = 0.3

def get_migration_limits(detailed: bool) -> MigrationLimits:
    mig_limits = MigrationLimits()
    mig_limits.max_req_mig_in = envs.LLUMNIX_MAX_REQ_MIG_IN
    mig_limits.max_req_mig_out = envs.LLUMNIX_MAX_REQ_MIG_OUT
    if detailed:
        mig_limits.max_token_mig_in = envs.LLUMNIX_MAX_TOKEN_MIG_IN
        mig_limits.max_token_mig_out = envs.LLUMNIX_MAX_TOKEN_MIG_OUT
        mig_limits.max_block_ratio_mig_in = envs.LLUMNIX_MAX_BLOCK_RATIO_MIG_IN
        mig_limits.max_block_ratio_mig_out = envs.LLUMNIX_MAX_BLOCK_RATIO_MIG_OUT
    return mig_limits

def get_rpc_port() -> int:
    port = int(os.getenv("LLUMNIX_RPC_PORT", "-1"))
    if port == -1:
        port = get_free_port()
    try:
        if not isinstance(port, int):
            port = int(port)
    except TypeError:
        logger.warning("Can not convert port {} to int, get free port.".format(port))
        port = get_free_port()

    if not 1024 <= port <= _MAX_PORT:
        logger.warning("Port must be between 1024 and 65535, get {}".format(port))
    else:
        if check_free_port(port=port):
            return port
        logger.warning("Port {} has been used, find other available port.".format(port))
    port = get_free_port()
    logger.info("Set available port {} for llumlet.".format(port))
    return port

# ================== Address related ==================
def get_ip_address(ifname: str = None):
    # Try to get IP address for the specified interface first
    if ifname:
        try:
            addrs = netifaces.ifaddresses(ifname)
            ip_info = addrs.get(netifaces.AF_INET)
            if ip_info and len(ip_info) > 0:
                return ip_info[0]['addr']
        except (ImportError, ValueError, KeyError, OSError):
            pass

    # try ipv4
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.connect(("8.8.8.8", 80))  # Doesn't need to be reachable
        return s.getsockname()[0]
    # pylint: disable=broad-except
    except Exception:
        pass

    # try ipv6
    try:
        s = socket.socket(socket.AF_INET6, socket.SOCK_DGRAM)
        # Google's public DNS server, see
        # https://developers.google.com/speed/public-dns/docs/using#addresses
        s.connect(("2001:4860:4860::8888", 80))  # Doesn't need to be reachable
        return s.getsockname()[0]
    # pylint: disable=broad-except
    except Exception:
        pass

    try:
        hostname = socket.gethostname()
        ip_address = socket.gethostbyname(hostname)
        return ip_address
    # pylint: disable=broad-except
    except Exception:
        pass

    logger.warning(
        "Failed to get the IP address, using 0.0.0.0 by default."
        "The value can be set by the environment variable"
        " VLLM_HOST_IP or HOST_IP.",
        stacklevel=2)

    return "0.0.0.0"

def _get_port_by_pid(pid: int, start: int, end: int) -> int:
    assert start < end
    assert end <= _MAX_PORT
    return pid % (end - start) + start


def _bind_and_close_port(port: Optional[int] = None, host: str = "0.0.0.0") -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        # the SO_REUSEADDR flag tells the kernel to reuse a local socket in TIME_WAIT state,
        # without waiting for its natural timeout to expire. see https://docs.python.org/3/library/socket.html#example
        # s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s.bind((host, port or 0))
        return s.getsockname()[1]


def check_free_port(host="0.0.0.0", port=8081):
    try:
        _bind_and_close_port(port=port, host=host)
        return True
    except socket.error as e:
        # pylint: disable=no-else-return
        if e.errno == socket.errno.EADDRINUSE:
            return False
        else:
            raise


def get_free_port() -> int:
    # try to find a free port based on pid to avoid port conflict between
    # multiple processes
    base_port = os.getpid()
    for i in range(10000, 60000, 2000):
        # sleep a random time to avoid port conflict
        time.sleep(random.randint(1, 1000) / 1000)
        port = _get_port_by_pid(base_port, i, i + 2000)
        if check_free_port(port=port) and check_free_port(port=port + 1):
            return port
    # fallback to random port if pid based port in all segments are occupied
    return _bind_and_close_port()


# ================== Others ==================
def get_metric_push_interval():
    interval = envs.LLUMNIX_METRIC_PUSH_INTERVAL
    if isinstance(interval, str):
        interval = float(interval)
    if interval < 0:
        logger.warning("LLUMNIX_METRIC_PUSH_INTERVAL must be positive, get {}, set to default value 1.0".format(interval))
        interval = 1.0
    return interval

# pylint: disable=unused-argument
def _loop_on_ex(loop, context):
    logger.exception("loop ex. context=%s", context)
    os.abort()

def _asyncio_loop_main(loop):
    asyncio.set_event_loop(loop)
    loop.run_forever()

def start_asyncio_thread(name):
    loop = uvloop.new_event_loop()
    loop.set_exception_handler(_loop_on_ex)
    threading.Thread(target=_asyncio_loop_main,
                     args=(loop, ),
                     name=name,
                     daemon=True).start()
    return loop


# ================== Exception ==================
class NotEnoughSlotsError(Exception):
    """
    Exception class raised when the number of migration requests is larger than available slots.
    """
    pass # pylint: disable=unnecessary-pass
