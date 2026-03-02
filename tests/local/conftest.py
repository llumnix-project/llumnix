import pytest
import subprocess
import os
import shutil
import time
from typing import Generator, List, Dict, Any

from modelscope import snapshot_download

from utils import (wait_for_service, start_process, cleanup_processes,
                    get_redis_command, get_gateway_command, get_scheduler_command, 
                    get_vllm_command, get_discovery_command, VLLM_BASE_PORT, LOG_DIR,
                    NAMING_DIR, MODEL_PATH)


def pytest_sessionstart(session):
    if os.path.exists(MODEL_PATH):
        return
    model_dir = snapshot_download('Qwen/Qwen2.5-7B', cache_dir='/models')
    print(f"Downloaded model to {model_dir}")


@pytest.fixture
def test_config(request):
    return getattr(request, 'param')


@pytest.fixture
def redis_server() -> Generator[subprocess.Popen, None, None]:
    """Start Redis server"""
    proc = start_process("Redis", get_redis_command(), "redis.log")
    yield proc
    cleanup_processes([proc])


@pytest.fixture
def gateway_server(test_config: Dict[str, Any]) -> Generator[subprocess.Popen, None, None]:
    """Start Gateway server"""
    policy = test_config.get('policy', 'round-robin')
    enable_pd =  test_config.get('enable_pd', False)
    enable_full_mode_scheduling = test_config.get('enable_full_mode_scheduling', False)
    separate_pd_scheduling = test_config.get('separate_pd_scheduling', False)
    connector_type = test_config.get('connector_type', 'HybridConnector')

    command = get_gateway_command(
        policy=policy,
        enable_pd=enable_pd,
        enable_full_mode_scheduling=enable_full_mode_scheduling,
        separate_pd_scheduling=separate_pd_scheduling,
        connector_type=connector_type
    )

    proc = start_process("Gateway", command, "gateway.log")
    yield proc
    cleanup_processes([proc])


@pytest.fixture
def scheduler_server(test_config: Dict[str, Any]) -> Generator[subprocess.Popen | None, None, None]:
    """Start Scheduler server"""
    proc = None
    policy = test_config.get('policy', 'round-robin')
    enable_full_mode_scheduling = test_config.get('enable_full_mode_scheduling', False)
    enable_migration = test_config.get('enable_migration', False)
    enable_pd = test_config.get('enable_pd', False)

    if policy != "round-robin":
        command = get_scheduler_command(
            policy=policy,
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
    policy = test_config.get('policy', 'round-robin')
    enable_full_mode_scheduling = test_config.get('enable_full_mode_scheduling', False)

    def launch_vllm_process(instance_type: str, port: int, cuda: int, tag: str, connector_type: str) -> subprocess.Popen:
        vllm_proc = start_process(
            f"vLLM-{cuda}",
            get_vllm_command(instance_type, VLLM_BASE_PORT + cuda, cuda, enable_full_mode_scheduling, tag, connector_type),
            f"vllm_{cuda}.log")
        runtime_proc = start_process(
            f"Runtime-{cuda}",
            get_discovery_command(instance_type, VLLM_BASE_PORT + cuda),
            f"runtime_{cuda}.log")
        processes.extend([vllm_proc, runtime_proc])

    all_available_ports = []
    if test_config['enable_pd']:
        for cuda in range(2):
            launch_vllm_process("prefill", VLLM_BASE_PORT + cuda, cuda, f"prefill_{cuda}", test_config.get('connector_type', 'HybridConnector'))
            all_available_ports.append(VLLM_BASE_PORT + cuda)
        for cuda in range(2, 4):
            launch_vllm_process("decode", VLLM_BASE_PORT + cuda, cuda, f"decode_{cuda}", test_config.get('connector_type', 'HybridConnector'))
            all_available_ports.append(VLLM_BASE_PORT + cuda)
    else:
        for cuda in range(2):
            launch_vllm_process("neutral", VLLM_BASE_PORT + cuda, cuda, f"neutral_{cuda}", test_config.get('connector_type', 'HybridConnector'))
            all_available_ports.append(VLLM_BASE_PORT + cuda)

    print("test_config:", test_config)
    for port in all_available_ports:
        print("Waiting for vllm services...")
        vllm_health_url = f"http://localhost:{port}/health"
        if not wait_for_service(vllm_health_url, timeout=180):
            cleanup_processes(processes)
            raise TimeoutError("Service startup timeout")

    yield processes
    cleanup_processes(processes)

@pytest.fixture
def setup_environment():
    """Prepare test environment by cleaning up old files and directories."""
    shutil.rmtree(NAMING_DIR, ignore_errors=True)
    if LOG_DIR.exists():
        for log_file in LOG_DIR.glob('vllm_*.log'):
            try:
                log_file.unlink()
                print(f"Deleted log file: {log_file}")
            except OSError as e:
                print(f"Error deleting file {log_file}: {e}")
    os.makedirs(NAMING_DIR, exist_ok=True)
    yield

@pytest.fixture
def setup_services(setup_environment, redis_server, scheduler_server, gateway_server, vllm_servers):
    time.sleep(20) # wait for redis discovery work
    yield
