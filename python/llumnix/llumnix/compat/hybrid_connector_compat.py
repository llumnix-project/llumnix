# pylint: disable=unused-import
# pylint: disable=ungrouped-imports

try:
    from vllm.v1.hybrid_connector.engine_proxy import (
        get_param,
        sched_rpc_server_port,
        core_update_params,
        MsgpackEncoder,
        PlaceholderModule,
        req2corereq,
    )
except ImportError:
    try:
        from blade_kvt.hybrid_connector.engine_proxy import (
            get_param,
            sched_rpc_server_port,
            core_update_params,
            MsgpackEncoder,
            PlaceholderModule,
            req2corereq,
        )
    except ImportError as e:
        raise ImportError(
            "FATAL: Failed to import from 'vllm.v1.hybrid_connector.engine_proxy' or 'blade_kvt.engine_proxy'."
        ) from e


try:
    from vllm.v1.hybrid_connector.kvtbackend import (
        _get_inst_id,
        CODE_OK,
        rpc_port,
    )
except ImportError:
    try:
        from blade_kvt.hybrid_connector.kvtbackend import (
            _get_inst_id,
            CODE_OK,
            rpc_port,
        )
    except ImportError as e:
        raise ImportError(
            "FATAL: Failed to import from 'vllm.v1.hybrid_connector.kvtbackend' or 'blade_kvt.kvtbackend'."
        ) from e


try:
    from vllm.v1.hybrid_connector.migration import (
        _g_migrate_in_req_ids,
        _g_migrate_in_req_info,
        _g_migrate_in_req_info_lock,
        _g_migrate_out_req_ids,
        _g_migrate_out_req_info,
        _g_migrate_out_req_info_lock,
        MIGRATE_TO_REQ,
        MIGRATE_TO_RESP,
        MigrateResp,
        OUTPUT_TOKENS_N,
        SRC_INFO,
        MIGRATION_TRIGGER_POLICY,
    )
except ImportError:
    try:
        from blade_kvt.hybrid_connector.migration import (
            _g_migrate_in_req_ids,
            _g_migrate_in_req_info,
            _g_migrate_in_req_info_lock,
            _g_migrate_out_req_ids,
            _g_migrate_out_req_info,
            _g_migrate_out_req_info_lock,
            MIGRATE_TO_REQ,
            MIGRATE_TO_RESP,
            MigrateResp,
            OUTPUT_TOKENS_N,
            SRC_INFO,
            MIGRATION_TRIGGER_POLICY,
        )
    except ImportError as e:
        raise ImportError(
            "FATAL: Failed to import from 'vllm.v1.hybrid_connector.migration' or 'blade_kvt.migration'."
        ) from e


try:
    from vllm.v1.hybrid_connector.utils import (
        ConnManager,
        PeerManager,
    )
except ImportError:
    try:
        from blade_kvt.hybrid_connector.utils import (
            ConnManager,
            PeerManager,
        )
    except ImportError as e:
        raise ImportError(
            "FATAL: Failed to import from 'vllm.v1.hybrid_connector.utils' or 'blade_kvt.utils'."
        ) from e
