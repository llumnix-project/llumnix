import copy
import time
import re
import concurrent.futures

import pytest

from utils import LOG_DIR, send_request


def generate_e2e_config():
    base_configs = [
        {"policy": "round-robin", "enable_full_mode_scheduling": False},
        {"policy": "load-balance", "enable_full_mode_scheduling": True},
        {"policy": "load-balance", "enable_full_mode_scheduling": False},
    ]

    update_configs = []
    for connector_type in ["HybridConnector", "MooncakeConnector"]:
        for config in base_configs:
            tmp_config = copy.deepcopy(config)
            tmp_config["connector_type"] = connector_type
            update_configs.append(tmp_config)
    base_configs = update_configs

    update_configs = []
    for enable_pd in [False, True]:
        for config in base_configs:
            tmp_config = copy.deepcopy(config)
            tmp_config["enable_pd"] = enable_pd
            update_configs.append(tmp_config)
    base_configs = update_configs

    update_configs = []
    for separate_pd_scheduling in [False, True]:
        for config in base_configs:
            if not config["enable_pd"]:
                continue
            tmp_config = copy.deepcopy(config)
            tmp_config["separate_pd_scheduling"] = separate_pd_scheduling
            update_configs.append(tmp_config)
    base_configs = update_configs

    return base_configs


# pylint: disable=unused-argument
@pytest.mark.parametrize("test_config", generate_e2e_config(), indirect=True)
def test_simple_requests(setup_services):
    for stream in [False, True]:
        for max_tokens in [1, 5]:
            completions_payload = {
                "prompt": "Hello, my name is",
                "max_tokens": max_tokens,
                "stream": stream,
                "temperature": 0.0,
            }
            result = send_request(completions_payload, endpoint_type="completions")
            if max_tokens == 5:
                assert result == " Dr. David M."
            else:
                assert result == " Dr"

            # For chat endpoint - convert format
            chat_payload = {
                "messages": [{"role": "user", "content": "Hello, my name is"}],
                "max_tokens": max_tokens,
                "stream": stream,
                "temperature": 0.0,
            }
            result = send_request(chat_payload, endpoint_type="chat")
            if max_tokens == 5:
                assert result in ("How can I assist you", "How can I help you")
            else:
                assert result == "How"


def check_migration_logs(connector_type: str):
    if connector_type == "MooncakeConnector":
        migrate_in_pattern = r"update success migrate in request.*"
        migrate_out_pattern = r"Migration .* suceess"
        traceback_pattern = r"Traceback"
    elif connector_type == "HybridConnector":
        migrate_in_pattern = r"migration end.*"
        migrate_out_pattern = r"suspend end:*"
        traceback_pattern = r"Traceback"
    else:
        raise ValueError(f"Unknown connector type: {connector_type}")

    migrate_in_count = 0
    migrate_out_count = 0
    traceback_count = 0
    traceback_files = []

    log_files = list(LOG_DIR.glob("vllm_*.log"))
    assert len(log_files) > 0

    for log_file in log_files:
        try:
            with open(log_file, "r", encoding="utf-8") as f:
                content = f.read()
                migrate_in_matches = re.findall(migrate_in_pattern, content)
                migrate_in_count += len(migrate_in_matches)

                migrate_out_matches = re.findall(migrate_out_pattern, content)
                migrate_out_count += len(migrate_out_matches)

                traceback_matches = re.findall(traceback_pattern, content)
                if len(traceback_matches) > 0:
                    traceback_count += len(traceback_matches)
                    traceback_files.append(log_file.name)

                print(
                    f"File {log_file.name}: migrate_in={len(migrate_in_matches)}, migrate_out={len(migrate_out_matches)}, traceback={len(traceback_matches)}"
                )
        except Exception as e:
            print(f"Error reading {log_file}: {e}")

    print(f"\nTotal - Migrate in count: {migrate_in_count}")
    print(f"Total - Migrate out count: {migrate_out_count}")
    print(f"Total - Traceback count: {traceback_count}")

    assert (
        migrate_in_count == migrate_out_count
    ), f"Migration count mismatch! Migrate in: {migrate_in_count}, Migrate out: {migrate_out_count}"

    assert (
        traceback_count == 0
    ), f"Found {traceback_count} traceback(s) in log files: {', '.join(traceback_files)}"

    print("✓ Migration log check passed!")
    print("✓ No tracebacks found!")
    return migrate_in_count, migrate_out_count


def generate_migration_config():
    base_configs = [
        {
            "policy": "flood",
            "enable_full_mode_scheduling": True,
            "enable_migration": True,
            "enable_pd": False,
            "separate_pd_scheduling": False,
        },
        {
            "policy": "flood",
            "enable_full_mode_scheduling": True,
            "enable_migration": True,
            "enable_pd": True,
            "separate_pd_scheduling": False,
        },
    ]

    update_configs = []
    for connector_type in ["HybridConnector", "MooncakeConnector"]:
        for config in base_configs:
            tmp_config = copy.deepcopy(config)
            tmp_config["connector_type"] = connector_type
            update_configs.append(tmp_config)
    base_configs = update_configs

    return base_configs


# pylint: disable=unused-argument
@pytest.mark.parametrize("test_config", generate_migration_config())
def test_migration(setup_services, test_config):
    num_requests = 50

    def send_single_request(request_id, connector_type):
        payload = {
            "prompt": f"Request {request_id}: Hello, my name is" * 10,
            "stream": False,
            "max_tokens": 1000,
            "ignore_eos": True,
            "temperature": 0.0,
        }
        if connector_type == "HybridConnector" and not test_config.get(
            "enable_pd", False
        ):
            # HybridConnector may produce longer outputs due to its design
            payload["kv_transfer_params"] = {"ali_llumnix_disagg": False}
        result = send_request(payload, endpoint_type="completions", ignore_output=True)
        return result

    print(f"\n=== Starting {num_requests} concurrent requests ===")
    start_time = time.time()

    with concurrent.futures.ThreadPoolExecutor(max_workers=20) as executor:
        futures = [
            executor.submit(send_single_request, i, test_config.get("connector_type"))
            for i in range(num_requests)
        ]
        results = [
            future.result() for future in concurrent.futures.as_completed(futures)
        ]

    end_time = time.time()

    print(
        f"\n=== Completed {len(results)}/{num_requests} requests in {end_time - start_time:.2f}s ==="
    )

    assert (
        len(results) == num_requests
    ), f"Only {len(results)}/{num_requests} requests completed"

    check_migration_logs(connector_type=test_config.get("connector_type"))
