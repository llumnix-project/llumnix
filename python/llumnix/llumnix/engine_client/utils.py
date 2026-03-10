import multiprocessing
from typing import Any, Dict, Optional

from llumnix.engine_client.base_engine_client import BaseEngineClient
from llumnix.instance_info import BackendType


def create_engine_client(
    engine_type: str,
    engine_config: Any,
    client_addresses: Optional[Dict[str, str]] = None,
    **kwargs
) -> BaseEngineClient:
    """
    Factory function to create an engine client based on the engine type.
    """
    if engine_type in (BackendType.VLLM_V1):
        # pylint: disable=import-outside-toplevel
        from vllm.config import VllmConfig
        from llumnix.engine_client.vllm_v1.engine_client import VLLMEngineClient

        if not isinstance(engine_config, VllmConfig):
            raise TypeError(
                "engine_config must be of type VllmConfig for engine_type 'vllm'"
            )
        return VLLMEngineClient(
            vllm_config=engine_config, client_addresses=client_addresses, **kwargs
        )

    if engine_type == BackendType.SGLANG:
        # pylint: disable=import-outside-toplevel
        from llumnix.engine_client.sglang.engine_client import SGLangEngineClient

        return SGLangEngineClient(client_addresses=client_addresses, **kwargs)

    raise NotImplementedError


def get_engine_mp_context(engine_type: str):
    if engine_type in (BackendType.VLLM_V1):
        return multiprocessing.get_context("fork")
    return multiprocessing.get_context("spawn")


def get_engine_client_addresses(engine_type: str) -> dict[str, str]:
    if engine_type in (BackendType.VLLM_V1):
        # pylint: disable=import-outside-toplevel
        from llumnix.engine_client.vllm_v1.engine_client import vllm_get_addresses

        return vllm_get_addresses()

    if engine_type == BackendType.SGLANG:
        # pylint: disable=import-outside-toplevel
        from llumnix.engine_client.sglang.engine_client import sglang_get_addresses

        return sglang_get_addresses()

    raise NotImplementedError


def get_specific_instance_meta_data(engine_type: str, cfg: Any) -> dict:
    if engine_type == BackendType.VLLM_V1:
        # pylint: disable=import-outside-toplevel
        from llumnix.engine_client.vllm_v1.engine_client import (
            vllm_get_instance_meta_data,
        )

        return vllm_get_instance_meta_data(cfg)

    if engine_type == BackendType.SGLANG:
        # pylint: disable=import-outside-toplevel
        from llumnix.engine_client.sglang.engine_client import (
            sglang_get_instance_meta_data,
        )

        return sglang_get_instance_meta_data(cfg)

    raise NotImplementedError


def add_llumlet_addresses(
    engine_type: str,
    addresses: Optional[dict[str, str]],
    llumlet_addresses: dict[str, str],
) -> dict[str, str]:
    """Add llumlet rpc address to the front of addresses list."""
    if engine_type in (BackendType.VLLM_V1):
        assert addresses is not None
        addresses.inputs.append(llumlet_addresses["input_address"])
        addresses.outputs.append(llumlet_addresses["output_address"])
        return addresses

    if engine_type == BackendType.SGLANG:
        addresses["llumlet_to_scheduler_ipc_name"] = llumlet_addresses[
            "llumlet_to_scheduler_ipc_name"
        ]
        addresses["scheduler_to_llumlet_ipc_name"] = llumlet_addresses[
            "scheduler_to_llumlet_ipc_name"
        ]
        return addresses

    raise NotImplementedError


def get_connector_type(engine_type: str, cfg: Any) -> Optional[str]:
    if engine_type == BackendType.VLLM_V1:
        # pylint: disable=import-outside-toplevel
        from llumnix.engine_client.vllm_v1.engine_client import vllm_get_connector_type

        return vllm_get_connector_type(cfg)
    return None
