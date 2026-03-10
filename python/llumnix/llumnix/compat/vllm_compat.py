try:
    # pylint: disable=unused-import
    from vllm.utils.network_utils import get_ip, get_open_zmq_ipc_path
    from vllm.utils.math_utils import cdiv
except ImportError:
    try:
        # pylint: disable=unused-import
        from vllm.utils import get_ip, get_open_zmq_ipc_path, cdiv
    except ImportError as e:
        # pylint: disable=unused-import
        from vllm.version import __version__ as VLLM_VERSION
        from llumnix.utils import envs

        error_message = (
            "FATAL: Failed to import 'get_ip' from both 'vllm.utils.network_utils' and 'vllm.utils'. "
            "Your vllm installation might be broken, incomplete, or an unsupported version."
            f"(vllm version:{VLLM_VERSION}"
        )
        raise ImportError(error_message) from e
