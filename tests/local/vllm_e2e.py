import copy
import json
import subprocess
import time
import re
from typing import Generator, List, Dict, Any
import concurrent.futures

import requests
import pytest

from .utils import (wait_for_service, GATEWAY_URL, start_process, cleanup_processes,
                    get_redis_command, get_gateway_command, get_scheduler_command, 
                    get_vllm_command, get_runtime_command, VLLM_BASE_PORT, LOG_DIR)


@pytest.fixture
def test_config(request):
    return getattr(request, 'param', {
        'enable_pd': False,
        'schedule_policy': 'round-robin',
        'enable_full_mode_scheduling': False,
        "enable_migration": False
    })


@pytest.fixture
def redis_server() -> Generator[subprocess.Popen, None, None]:
    """Start Redis server"""
    proc = start_process("Redis", get_redis_command(), "redis.log")
    yield proc
    cleanup_processes([proc])


@pytest.fixture
def gateway_server(test_config: Dict[str, Any]) -> Generator[subprocess.Popen, None, None]:
    """Start Gateway server"""
    schedule_policy = test_config.get('schedule_policy', 'round-robin')
    enable_pd =  test_config.get('enable_pd', False)
    enable_full_mode_scheduling = test_config.get('enable_full_mode_scheduling', False)
    separate_pd_schedule = test_config.get('separate_pd_schedule', False)

    command = get_gateway_command(
        schedule_policy=schedule_policy,
        enable_pd=enable_pd,
        enable_full_mode_scheduling=enable_full_mode_scheduling,
        separate_pd_schedule=separate_pd_schedule
    )

    proc = start_process("Gateway", command, "gateway.log")
    yield proc
    cleanup_processes([proc])


@pytest.fixture
def scheduler_server(test_config: Dict[str, Any]) -> Generator[subprocess.Popen | None, None, None]:
    """Start Scheduler server"""
    proc = None
    schedule_policy = test_config.get('schedule_policy', 'round-robin')
    enable_full_mode_scheduling = test_config.get('enable_full_mode_scheduling', False)
    enable_migration = test_config.get('enable_migration', False)
    enable_pd = test_config.get('enable_pd', False)

    if schedule_policy != "round-robin":
        command = get_scheduler_command(
            schedule_policy=schedule_policy,
            enable_full_mode_scheduling=enable_full_mode_scheduling,
            enable_migration=enable_migration,
            enable_pd=enable_pd
        )
        proc = start_process("Scheduler", command, "scheduler.log")
    yield proc
    if proc:
        cleanup_processes([proc])


@pytest.fixture
def vllm_servers(test_config: Dict[str, Any]) -> Generator[List[subprocess.Popen], None, None]:
    """Start vLLM instances and runtime discovery"""
    processes = []
    schedule_policy = test_config.get('schedule_policy', 'round-robin')
    enable_full_mode_scheduling = test_config.get('enable_full_mode_scheduling', False)

    def launch_vllm_process(role: str, port: int, cuda: int) -> subprocess.Popen:
        vllm_proc = start_process(
            f"vLLM-{cuda}",
            get_vllm_command(role, VLLM_BASE_PORT + cuda, cuda,
                             schedule_policy, enable_full_mode_scheduling),
            f"vllm_{cuda}.log")
        runtime_proc = start_process(
            f"Runtime-{cuda}",
            get_runtime_command(role, VLLM_BASE_PORT + cuda),
            f"runtime_{cuda}.log")
        processes.extend([vllm_proc, runtime_proc])

    all_available_ports = []
    if test_config['enable_pd']:
        for cuda in range(2):
            launch_vllm_process("prefill", VLLM_BASE_PORT + cuda, cuda)
            all_available_ports.append(VLLM_BASE_PORT + cuda)
        for cuda in range(2, 4):
            launch_vllm_process("decode", VLLM_BASE_PORT + cuda, cuda)
            all_available_ports.append(VLLM_BASE_PORT + cuda)
    else:
        for cuda in range(2):
            launch_vllm_process("normal", VLLM_BASE_PORT + cuda, cuda)
            all_available_ports.append(VLLM_BASE_PORT + cuda)

    for port in all_available_ports:
        print("Waiting for vllm services...")
        vllm_health_url = f"http://localhost:{port}/health"
        if not wait_for_service(vllm_health_url, timeout=60):
            cleanup_processes(processes)
            raise TimeoutError("Service startup timeout")

    yield processes
    cleanup_processes(processes)


@pytest.fixture
def setup_services(redis_server, scheduler_server, gateway_server, vllm_servers, test_config):
    """Setup all services - this combines all previous fixtures"""
    print("test_config:", test_config)
    time.sleep(20)
    yield
    # Cleanup handled by individual fixtures


def send_request(
    payload: Dict[str, Any],
    endpoint_type: str = 'completions',
    ignore_output: bool = False
):
    """
    Unified function for both completions and chat completions
    endpoint_type: 'completions' or 'chat'
    """
    if endpoint_type == 'chat':
        url = GATEWAY_URL.replace('/v1/completions', '/v1/chat/completions')
    else:
        url = GATEWAY_URL
    
    if payload.get('stream', False):
        response = requests.post(url, json=payload, timeout=30, stream=True)
        response.raise_for_status()
        
        chunks = []
        full_text = ""
        
        for line in response.iter_lines():
            if line:
                decoded_line = line.decode('utf-8')
                if decoded_line.startswith('data: '):
                    chunk_data = decoded_line[6:]
                    if chunk_data.strip() != '[DONE]':
                        try:
                            chunk_json = json.loads(chunk_data)
                            chunks.append(chunk_json)
                            
                            if "choices" in chunk_json and len(chunk_json["choices"]) > 0:
                                choice = chunk_json["choices"][0]
                                if endpoint_type == 'chat':
                                    full_text += choice.get("delta", {}).get("content", "")
                                else:
                                    full_text += choice.get("text", "")
                        except json.JSONDecodeError:
                            print(f"Failed to parse chunk: {chunk_data}")
        
        assert len(chunks) > 0, "No streaming chunks received"
    else:
        response = requests.post(url, json=payload, timeout=30)
        response.raise_for_status()
        result = response.json()
        
        assert "choices" in result, "Response missing 'choices' field"
        assert len(result["choices"]) > 0, "No choices in response"
        
        if endpoint_type == 'chat':
            full_text = result["choices"][0].get("message", {}).get("content", "")
        else:
            full_text = result["choices"][0].get("text", "")

    if not ignore_output:
        max_tokens = getattr(payload, 'max_tokens', "None")
        stream = getattr(payload, 'stream', False)
        print(f"Response received ({endpoint_type}, stream={stream}, max_tokens={max_tokens}): {full_text}")

    return full_text


def generate_e2e_config():
    base_configs = [
        {'schedule_policy': 'round-robin', 'enable_full_mode_scheduling': False},
        {'schedule_policy': 'load-balance', 'enable_full_mode_scheduling': True},
        {'schedule_policy': 'load-balance', 'enable_full_mode_scheduling': False},
    ]

    update_configs = []
    for enable_pd in [False, True]:
        for config in base_configs:
            tmp_config = copy.deepcopy(config)
            tmp_config['enable_pd'] = enable_pd
            update_configs.append(tmp_config)
    base_configs = update_configs

    update_configs = []
    for separate_pd_schedule in [False, True]:
        for config in base_configs:
            if not config['enable_pd']:
                continue
            tmp_config = copy.deepcopy(config)
            tmp_config['separate_pd_schedule'] = separate_pd_schedule
            update_configs.append(tmp_config)
    base_configs = update_configs


    base_configs = [{'schedule_policy': 'load-balance', 'enable_full_mode_scheduling': True, 'enable_pd': True, 'separate_pd_schedule': False}]
    return base_configs

@pytest.mark.parametrize("test_config", generate_e2e_config(), indirect=True)
def test_completion_request(setup_services):
    for stream in [True]:
        for max_tokens in [1, 5]:
            completions_payload = {
                "prompt": "Hello, my name is",
                "max_tokens": max_tokens,
                "stream": stream,
                "temperature": 0.0,
            }
            result = send_request(completions_payload, endpoint_type='completions')
            if max_tokens == 5:
                assert result == ' Dr. David M.'
            else:
                assert result == ' Dr'
            
            # For chat endpoint - convert format
            chat_payload = {
                "messages": [
                    {"role": "user", "content": "Hello, my name is"}
                ],
                "max_tokens": max_tokens,
                "stream": stream,
                "temperature": 0.0,
            }
            result = send_request(chat_payload, endpoint_type='chat')
            if max_tokens == 5:
                assert result == 'How can I assist you' or result == 'How can I help you'
            else:
                assert result == 'How'


def check_migration_logs():
    migrate_in_pattern = r"update success migrate in request.*"
    migrate_out_pattern = r"Migration .* suceess"
    
    migrate_in_count = 0
    migrate_out_count = 0
    
    log_files = list(LOG_DIR.glob('vllm_*.log'))
    assert len(log_files) > 0

    for log_file in log_files:
        try:
            with open(log_file, 'r', encoding='utf-8') as f:
                content = f.read()
                migrate_in_matches = re.findall(migrate_in_pattern, content)
                migrate_in_count += len(migrate_in_matches)

                migrate_out_matches = re.findall(migrate_out_pattern, content)
                migrate_out_count += len(migrate_out_matches)
                
                print(f"File {log_file.name}: migrate_in={len(migrate_in_matches)}, migrate_out={len(migrate_out_matches)}")
        except Exception as e:
            print(f"Error reading {log_file}: {e}")
    
    print(f"\nTotal - Migrate in count: {migrate_in_count}")
    print(f"Total - Migrate out count: {migrate_out_count}")
    
    assert migrate_in_count == migrate_out_count, \
        f"Migration count mismatch! Migrate in: {migrate_in_count}, Migrate out: {migrate_out_count}"
    
    print("✓ Migration log check passed!")
    return migrate_in_count, migrate_out_count


def generate_migration_config():
    base_configs = [
        {'schedule_policy': 'flood', 'enable_full_mode_scheduling': True, 'enable_migration': True, 'enable_pd': False, 'separate_pd_schedule': False},
        {'schedule_policy': 'flood', 'enable_full_mode_scheduling': True, 'enable_migration': True, 'enable_pd': True, 'separate_pd_schedule': False},
    ]

    return base_configs

@pytest.mark.parametrize("test_config", generate_migration_config(), indirect=True)
def test_migration(setup_services):
    num_requests = 50

    def send_single_request(request_id):
        payload = {
            "prompt": f"Request {request_id}: Hello, my name is"*10,
            "stream": False,
            "max_tokens": 1000,
            "ignore_eos": True,
            "temperature": 0.0,
        }
        result = send_request(payload, endpoint_type='completions', ignore_output=True)
        return result
    
    print(f"\n=== Starting {num_requests} concurrent requests ===")
    start_time = time.time()

    with concurrent.futures.ThreadPoolExecutor(max_workers=20) as executor:
        futures = [executor.submit(send_single_request, i) for i in range(num_requests)]
        results = [future.result() for future in concurrent.futures.as_completed(futures)]
    
    end_time = time.time()
    
    print(f"\n=== Completed {len(results)}/{num_requests} requests in {end_time - start_time:.2f}s ===")

    assert len(results) == num_requests, f"Only {len(results)}/{num_requests} requests completed"

    check_migration_logs()
