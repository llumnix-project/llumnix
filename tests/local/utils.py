import subprocess
import time
import os
from typing import List
import uuid

import requests
import psutil
import subprocess
from pathlib import Path

LOG_DIR = Path("logs")
VLLM_BASE_PORT: int = 8000
GATEWAY_PORT: int = 18089
SCHEDULER_PORT: int = 18088
GATEWAY_URL = f"http://localhost:{GATEWAY_PORT}/v1/completions"
MODEL_PATH: str = os.environ.get("MODEL_PATH", "/mnt/eas/models/Qwen2.5-7B")

def get_redis_command() -> str:
    return "redis-server --port 6379"


def get_gateway_command(
    schedule_policy: str = "round-robin",
    tokenizer_path: str = MODEL_PATH,
    enable_pd: bool = False,
    enable_full_mode_scheduling: bool = False,
    separate_pd_schedule: bool = False,
) -> str:
    command = (
        f"./bin/llm-gateway "
        f"--port {GATEWAY_PORT} "
        f"--llm-scheduler llumnix-scheduler "
        f"--enable-log-input "
        f"--use-discovery redis "
        f"--llumnix-cms-redis-host 127.0.0.1 "
        f"--llumnix-cms-redis-port 6379 "
        f"--schedule-policy {schedule_policy} "
        f"--local-test-scheduler-ip localhost:{SCHEDULER_PORT} "
        f"--tokenizer-path {tokenizer_path} "
        f"-v 4 "
    )

    if enable_full_mode_scheduling:
        command += "--enable-full-mode-scheduling "

    if enable_pd:
        command += "--pdsplit-mode vllm-mooncake "

    if separate_pd_schedule:
        command += "--separate-pd-schedule "

    print(f"gateway command: {command}")

    return command


def get_scheduler_command(
    schedule_policy: str = "load-balance",
    enable_full_mode_scheduling: bool = False,
    enable_migration: bool = False,
    enable_pd: bool = False,
) -> str:
    command = (
        f"./bin/llm-gateway "
        f"--schedule-mode "
        f"--port {SCHEDULER_PORT} "
        f"--host 0.0.0.0 "
        f"--use-discovery redis "
        f"--schedule-policy {schedule_policy} "
        f"--llumnix-cms-redis-host 127.0.0.1 "
        f"--llumnix-cms-redis-port 6379 "
        f"--llumnix-cms-pull-status-interval-ms 100 "
        f"--llumnix-cms-pull-metadata-interval-ms 100 "
        f"-v 4 "
    )

    if enable_full_mode_scheduling:
        command += "--enable-full-mode-scheduling "

    if enable_migration:
        reschedule_policy = "decode_load" if enable_pd else "neutral_load"
        command += "--colocated-reschedule-mode "
        command += "--llumnix-enable-rescheduling "
        command += f"--llumnix-reschedule-policies {reschedule_policy} "
        command += "--llumnix-reschedule-load-balance-threshold 0 "
        command += "--llumnix-reschedule-neutral-load-threshold 0 "
        command += "--llumnix-reschedule-decode-load-threshold 0 "
        command += "--llumnix-reschedule-req-select-rule NUM_REQ "

    print(f"scheduler command: {command}")

    return command


def get_vllm_command(
    role: str,
    port: int,
    cuda: int,
    schedule_policy: str,
    enable_full_mode_scheduling: bool
) -> str:
    if role == "prefill":
        kv_role = "kv_producer"
    elif role == "decode":
        kv_role = "kv_consumer"
    else:
        kv_role = "kv_both"
    
    kv_transfer_config = f'{{"kv_connector":"MooncakeConnector","kv_role":"{kv_role}","kv_connector_module_path":"mooncake.mooncake_connector_v1"}}'

    command = (
        f"VLLM_LOGGING_LEVEL=DEBUG "
        f"vllm serve "
        f"--enforce-eager "
        f"--model {MODEL_PATH} "
        f"--port {port} "
        f"--kv-transfer-config '{kv_transfer_config}' "
    )

    command = f"VLLM_MOONCAKE_SIDE_CHANNEL_PORT={16557+8*cuda} " + command
    command = f"VLLM_MOONCAKE_MIGRATION_BASE_PORT={17557+8*cuda} " + command
    command = f"CUDA_VISIBLE_DEVICES={cuda} " + command
    command = "LLUMNIX_CMS_REDIS_ADDRESS=127.0.0.1 " + command
    command = "LLUMNIX_CMS_REDIS_PORT=6379 " + command
    command = "LLUMNIX_ENGINE_GET_STATUS_TIMEOUT=1 " + command
   
    if enable_full_mode_scheduling:
        command = "LLUMNIX_ENABLE_MIGRATION=1 " + command
        command = "VLLM_ENABLE_LLUMNIX=1 " + command

    print(f"vllm command: {command}")

    return command


def get_runtime_command(
    role: str = "normal",
    port: int = 8000,
    dp_size_local: int = 1,
):
    command = (
        f"python3 -m python.runtime.discovery "
        f"--pod_name {uuid.uuid4()} "
        f"--entrypoint_ip 0.0.0.0 "
        f"--entrypoint_port {port} "
        f"--kv_transfer_ip 0.0.0.0 "
        f"--kv_transfer_port {port} "
        f"--role {role} "
        f"--dp_size_local {dp_size_local} "
        f"--redis_address 127.0.0.1 "
        f"--redis_port 6379 "
    )

    print(f"runtime command: {command}")

    return command


def wait_for_service(url: str, timeout: int = 60) -> bool:
    start = time.time()
    while time.time() - start < timeout:
        try:
            requests.get(url, timeout=1)
            return True
        except requests.RequestException:
            time.sleep(5)
        print(f"Waiting for service at {url}...")
    return False


def start_process(name: str, command: str, log_file: str = None) -> subprocess.Popen:    
    if log_file:
        LOG_DIR.mkdir(exist_ok=True)
        log_path = LOG_DIR / log_file
        with open(log_path, "w") as f:
            proc = subprocess.Popen(command, shell=True, stdout=f, stderr=subprocess.STDOUT)
    else:
        proc = subprocess.Popen(command, shell=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    print(f"Starting {name}, pid: {proc.pid} ...")
    return proc

def kill_process_tree(pid: int):
    """Kill a process and all its children using psutil"""
    try:
        parent = psutil.Process(pid)
        children = parent.children(recursive=True)
        
        # First try SIGTERM for graceful shutdown
        for child in children:
            try:
                child.terminate()
            except psutil.NoSuchProcess:
                pass
        
        try:
            parent.terminate()
        except psutil.NoSuchProcess:
            pass
        
        # Wait for processes to terminate
        gone, alive = psutil.wait_procs(children + [parent], timeout=5)
        
        # Force kill remaining processes
        for proc in alive:
            try:
                print(f"  Force killing process {proc.pid}...")
                proc.kill()
            except psutil.NoSuchProcess:
                pass
                
    except psutil.NoSuchProcess:
        pass
    except Exception as e:
        print(f"  Error killing process tree {pid}: {e}")

def cleanup_processes(processes: List[subprocess.Popen]):
    """Cleanup all processes"""
    print("\nCleaning up processes...")
    for proc in processes:
        try:
            if proc.poll() is None:  # Process is still running
                print(f"Cleaning up process {proc.pid}...")
                kill_process_tree(proc.pid)
        except Exception as e:
            print(f"Cleanup failed for pid {proc.pid}: {e}")
    
    # Give processes time to clean up
    time.sleep(1)
    print("Cleanup complete.")
