import os
from typing import TYPE_CHECKING, Any, Callable, Dict, Optional

if TYPE_CHECKING:
    LLUMNIX_CONFIGURE_LOGGING: int = 1
    LLUMNIX_LOGGING_CONFIG_PATH: Optional[str] = None
    LLUMNIX_LOGGING_LEVEL: str = "INFO"
    LLUMNIX_LOGGING_PREFIX: str = "Llumnix"
    LLUMNIX_LOG_STREAM: int = 1
    LLUMNIX_LOG_NODE_PATH: str = ""
    LLUMNIX_METRIC_PUSH_INTERVAL: float = 0.04
    LLUMNIX_ENGINE_GET_STATUS_TIMEOUT: float = 30.0
    LLUMNIX_ENGINE_MIGRATE_TIMEOUT: float = 5.0
    LLUMNIX_ENGINE_ABORT_TIMEOUT: float = 5.0
    LLUMNIX_INSTANCE_UPDATE_STEPS: int = 1
    LLUMNIX_ENGINE_MIGRATE_IN_TIMEOUT: float = 5.0
    LLUMNIX_REPORT_INSTANCE_STATUS_INTERVAL_S: float = 10.0
    LLUMNIX_RECENT_WAITINGS_STALENESS_SECONDS: float = 10.0
    LLUMNIX_ENABLE_MIGRATION: int = 1
    LLUMNIX_MAX_REQ_MIG_IN: int = -1
    LLUMNIX_MAX_REQ_MIG_OUT: int = -1
    LLUMNIX_DETAILED_MIG_STATUS: bool = False
    LLUMNIX_MAX_TOKEN_MIG_IN: int = -1
    LLUMNIX_MAX_TOKEN_MIG_OUT: int = -1
    LLUMNIX_MAX_KV_CACHE_USAGE_RATIO_MIG_IN: float = -1
    LLUMNIX_MAX_KV_CACHE_USAGE_RATIO_MIG_OUT: float = -1
    LLUMNIX_UPDATE_INSTANCE_STATUS_MODE: str = "push"
    LLUMNIX_ENABLE_PROFILING: int = 0
    LLUMNIX_PROFILING_STEPS: int = 50
    LLUMNIX_USED_METRICS: str = "all"
    LLUMNIX_INSTANCE_TYPE: str = ""


environment_variables: Dict[str, Callable[[], Any]] = {
    # ================== Llumnix environment variables ==================
    # Logging configuration
    # If set to 0, llumnix will not configure logging
    # If set to 1, llumnix will configure logging using the default configuration
    # or the configuration file specified by LLUMNIX_LOGGING_CONFIG_PATH
    "LLUMNIX_CONFIGURE_LOGGING": lambda: int(os.getenv("LLUMNIX_CONFIGURE_LOGGING", "1")),
    "LLUMNIX_LOGGING_CONFIG_PATH": lambda: os.getenv("LLUMNIX_LOGGING_CONFIG_PATH"),
    # this is used for configuring the default logging level
    "LLUMNIX_LOGGING_LEVEL": lambda: os.getenv("LLUMNIX_LOGGING_LEVEL", "INFO"),
    # if set, LLUMNIX_LOGGING_PREFIX will be prepended to all log messages
    "LLUMNIX_LOGGING_PREFIX": lambda: os.getenv("LLUMNIX_LOGGING_PREFIX", ""),
    # if set, llumnix will routing all logs to stream
    "LLUMNIX_LOG_STREAM": lambda: os.getenv("LLUMNIX_LOG_STREAM", "1"),
    # if set, llumnix will routing all node logs to this path
    "LLUMNIX_LOG_NODE_PATH": lambda: os.getenv("LLUMNIX_LOG_NODE_PATH", ""),
    # if set, llumnix will push instance status with specific interval
    "LLUMNIX_METRIC_PUSH_INTERVAL": lambda: float(os.getenv("LLUMNIX_METRIC_PUSH_INTERVAL", "0.04")),
    # this is used for configuring the timeout for get_instance_status engine call
    "LLUMNIX_ENGINE_GET_STATUS_TIMEOUT": lambda: float(os.getenv("LLUMNIX_ENGINE_GET_STATUS_TIMEOUT", "30.0")),
    # this is used for configuring the timeout for migrate engine call
    "LLUMNIX_ENGINE_MIGRATE_TIMEOUT": lambda: float(os.getenv("LLUMNIX_ENGINE_MIGRATE_TIMEOUT", "5.0")),
    # this is used for configuring the timeout for abort engine call
    "LLUMNIX_ENGINE_ABORT_TIMEOUT": lambda: float(os.getenv("LLUMNIX_ENGINE_ABORT_TIMEOUT", "5.0")),
    # this is used for setting instance status frequency
    "LLUMNIX_INSTANCE_UPDATE_STEPS": lambda: int(os.getenv("LLUMNIX_INSTANCE_UPDATE_STEPS", "1")),
    # if set, llumnix will limit the number of migrate in reqs size
    "LLUMNIX_MAX_REQ_MIG_IN": lambda: int(os.getenv("LLUMNIX_MAX_REQ_MIG_IN", "1")),
    # if set, llumnix will limit the number of migrate out reqs size
    "LLUMNIX_MAX_REQ_MIG_OUT": lambda: int(os.getenv("LLUMNIX_MAX_REQ_MIG_OUT", "1")),
    # if set llumnix will report detailed migration instance status
    "LLUMNIX_DETAILED_MIG_STATUS": lambda: bool(int(os.getenv("LLUMNIX_DETAILED_MIG_STATUS", "0"))),

    # if set migration concurrency is limited, only works when LLUMNIX_DETAILED_MIG_STATUS == True
    # if set, llumnix will limit the number of migrate in tokens
    "LLUMNIX_MAX_TOKEN_MIG_IN": lambda: int(os.getenv("LLUMNIX_MAX_TOKEN_MIG_IN", "100000")),
    # if set, llumnix will limit the number of migrate out tokens
    "LLUMNIX_MAX_TOKEN_MIG_OUT": lambda: int(os.getenv("LLUMNIX_MAX_TOKEN_MIG_OUT", "100000")),
    # if set, llumnix will limit the kv cache usage ratio of migrate in tokens
    "LLUMNIX_MAX_KV_CACHE_USAGE_RATIO_MIG_IN": lambda: float(os.getenv("LLUMNIX_MAX_KV_CACHE_USAGE_RATIO_MIG_IN", "0.3")),
    # if set, llumnix will limit the kv cache usage atio of migrate out tokens
    "LLUMNIX_MAX_KV_CACHE_USAGE_RATIO_MIG_OUT": lambda: float(os.getenv("LLUMNIX_MAX_KV_CACHE_USAGE_RATIO_MIG_OUT", "0.3")),

    # this is used for configuring the timeout for migrate_in engine call
    "LLUMNIX_ENGINE_MIGRATE_IN_TIMEOUT": lambda: float(os.getenv("LLUMNIX_ENGINE_MIGRATE_IN_TIMEOUT", "5.0")),

    # this is used for setting the interval of reporting instance status
    "LLUMNIX_REPORT_INSTANCE_STATUS_INTERVAL_S": lambda: float(os.getenv("LLUMNIX_REPORT_INSTANCE_STATUS_INTERVAL_S", "10.0")),

    # this is used for setting the stale interval of recent waiting requests
    "LLUMNIX_RECENT_WAITINGS_STALENESS_SECONDS": lambda: float(os.getenv("LLUMNIX_RECENT_WAITINGS_STALENESS_SECONDS", "10.0")),

    # if set, llumnix will enable migration
    "LLUMNIX_ENABLE_MIGRATION": lambda: int(os.getenv("LLUMNIX_ENABLE_MIGRATION", "1")),

    # there are two modes "push" and "pull" of updating instance status
    "LLUMNIX_UPDATE_INSTANCE_STATUS_MODE": lambda: os.getenv("LLUMNIX_UPDATE_INSTANCE_STATUS_MODE", "push"),

    # If set, llumnix will enable profiling ttft/tpot
    "LLUMNIX_ENABLE_PROFILING": lambda: int(os.getenv("LLUMNIX_ENABLE_PROFILING", "0")),

    # If set, llumnix will profile ttft in the initial steps
    "LLUMNIX_PROFILING_STEPS": lambda: int(os.getenv("LLUMNIX_PROFILING_STEPS", "50")),

    # If not all, llumnix will only collect statuses that are used by metrics.
    "LLUMNIX_USED_METRICS": lambda: os.getenv("LLUMNIX_USED_METRICS", "all"),

    # If set, override the instance type. Valid values: "prefill", "decode", "neutral".
    # Default is empty, which means the instance type is determined by engine config.
    "LLUMNIX_INSTANCE_TYPE": lambda: os.getenv("LLUMNIX_INSTANCE_TYPE", ""),
}


# pylint: disable=invalid-name
def __getattr__(name: str):
    # lazy evaluation of environment variables
    if name in environment_variables:
        return environment_variables[name]()
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")


# pylint: disable=invalid-name
def __dir__():
    return list(environment_variables.keys())
