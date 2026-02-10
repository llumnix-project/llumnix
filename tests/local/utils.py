import subprocess
import time
import os
from typing import List, Dict, Any, Optional
import uuid

import json
import requests
import psutil
import subprocess
from pathlib import Path

from vllm.utils.network_utils import get_ip

LOG_DIR = Path("logs")
NAMING_DIR = Path(f"{LOG_DIR}/naming.vllm")
VLLM_BASE_PORT: int = 8000
KVT_PORT_OFFSET: int = 20000
GATEWAY_PORT: int = 18089
SCHEDULER_PORT: int = 18088
GATEWAY_URL = f"http://localhost:{GATEWAY_PORT}/v1/completions"
MODEL_NAME: str = "Qwen/Qwen2.5-7B"
MODEL_PATH: str = os.environ.get("MODEL_PATH", f"/models/{MODEL_NAME}")


def get_redis_command() -> str:
    return "redis-server --port 6379"


def get_gateway_command(
    policy: str = "round-robin",
    tokenizer_path: str = MODEL_PATH,
    enable_pd: bool = False,
    enable_full_mode_scheduling: bool = False,
    separate_pd_schedule: bool = False,
    connector_type: str = "HybridConnector",
) -> str:
    command = (
        f"./bin/gateway "
        f"--port={GATEWAY_PORT} "
        f"--enable-log-input "
        f"--llm-backend-discovery=redis "
        f"--scheduler-discovery=endpoints "
        f"--scheduler-endpoints=localhost:{SCHEDULER_PORT} "
        f"--discovery-redis-status-ttl-ms=20000 "
        f"--discovery-redis-host=127.0.0.1 "
        f"--discovery-redis-port=6379 "
        f"--schedule-policy={policy} "
        f"--tokenizer-path={tokenizer_path} "
        f"-v=4 "
    )

    if enable_full_mode_scheduling:
        command += "--enable-full-mode-scheduling "
    else:
        command += "--enable-full-mode-scheduling=false "

    if enable_pd:
        if connector_type == "MooncakeConnector":
            command += "--pd-disagg-protocol=vllm-mooncake "
        else:
            command += "--pd-disagg-protocol=vllm-kvt "

    if separate_pd_schedule:
        command += "--separate-pd-schedule "
    else:
        command += "--separate-pd-schedule=false "

    print(f"gateway command: {command}")

    return command


def get_scheduler_command(
    policy: str = "load-balance",
    enable_full_mode_scheduling: bool = False,
    enable_migration: bool = False,
    enable_pd: bool = False,
) -> str:
    command = (
        f"./bin/scheduler "
        f"--port={SCHEDULER_PORT} "
        f"--host=0.0.0.0 "
        f"--enable-log-input "
        f"--llm-backend-discovery=redis "
        f"--discovery-redis-host=127.0.0.1 "
        f"--discovery-redis-port=6379 "
        f"--discovery-redis-status-ttl-ms=20000 "
        f"--schedule-policy={policy} "
        f"--cms-redis-host=127.0.0.1 "
        f"--cms-redis-port=6379 "
        f"--cms-pull-status-interval-ms=100 "
        f"--cms-pull-metadata-interval-ms=100 "
        f"-v 4 "
    )

    if enable_full_mode_scheduling:
        command += "--enable-full-mode-scheduling "
    else:
        command += "--enable-full-mode-scheduling=false "

    if enable_migration:
        reschedule_policy = "decode_load" if enable_pd else "neutral_load"
        command += "--colocated-reschedule-mode "
        command += "--enable-rescheduling "
        command += f"--reschedule-policies={reschedule_policy} "
        command += "--reschedule-load-balance-threshold=0 "
        command += "--reschedule-neutral-load-threshold=0 "
        command += "--reschedule-decode-load-threshold=0 "
        command += "--reschedule-req-select-rule=NUM_REQ "
    else:
        command += "--colocated-reschedule-mode=false "
        command += "--enable-rescheduling=false "

    print(f"scheduler command: {command}")

    return command


def get_vllm_command(
    role: str,
    port: int,
    cuda: int,
    enable_full_mode_scheduling: bool,
    tag: str,
    connector_type: str,
) -> str:
    if role == "prefill":
        kv_role = "kv_producer"
    elif role == "decode":
        kv_role = "kv_consumer"
    else:
        kv_role = "kv_both"
    if connector_type == "HybridConnector":
        if role == "prefill":
            backend = "kvt"
        else:
            backend = "kvt+migration"
        kv_transfer_config = f'{{"kv_connector":"HybridConnector", "kv_role": "{kv_role}","kv_connector_extra_config": {{"backend":"{backend}","naming_url": "file:{NAMING_DIR}","kvt_inst_id": "{tag}","rpc_port":{port+KVT_PORT_OFFSET}}}}}'
    elif connector_type == "MooncakeConnector":
        kv_transfer_config = f'{{"kv_connector":"MooncakeConnector","kv_role":"{kv_role}","kv_connector_module_path":"mooncake.mooncake_connector_v1"}}'

    command = (
        f"VLLM_LOGGING_LEVEL=DEBUG "
        f"vllm serve "
        f"--enforce-eager "
        f"--model {MODEL_PATH} "
        f"--port {port} "
        f"--kv-transfer-config '{kv_transfer_config}' "
    )
    if connector_type == "MooncakeConnector":
        command = f"VLLM_MOONCAKE_SIDE_CHANNEL_PORT={16557+8*cuda} " + command
        command = f"VLLM_MOONCAKE_MIGRATION_BASE_PORT={17557+8*cuda} " + command
    elif connector_type == "HybridConnector":
        command = f"BLLM_KVTRANS_PORT_BASE={33218+8*cuda} " + command
    
    command = f"CUDA_VISIBLE_DEVICES={cuda} " + command
    command = "LLUMNIX_CMS_REDIS_ADDRESS=127.0.0.1 " + command
    command = "LLUMNIX_CMS_REDIS_PORT=6379 " + command
    command = "LLUMNIX_ENGINE_GET_STATUS_TIMEOUT=1 " + command
        
    if enable_full_mode_scheduling:
        command = "LLUMNIX_ENABLE_MIGRATION=1 " + command
        command = "VLLM_ENABLE_LLUMNIX=1 " + command

    command = "VLLM_USE_MODELSCOPE=true " + command

    print(f"vllm command: {command}")

    return command


def get_discovery_command(
    role: str = "normal",
    port: int = 8000,
    dp_size_local: int = 1,
):
    command = (
        f"python3 -m discovery.discovery "
        f"--role {role} "
        f"--pod_name {uuid.uuid4()} "
        f"--entrypoint_ip {get_ip()} " 
        f"--entrypoint_port {port} " 
        f"--kv_transfer_ip {get_ip()} "
        f"--kv_transfer_port {port+KVT_PORT_OFFSET} "
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
        response = requests.post(url, json=payload, timeout=60, stream=True)
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
        response = requests.post(url, json=payload, timeout=60)
        response.raise_for_status()
        result = response.json()
        
        assert "choices" in result, "Response missing 'choices' field"
        assert len(result["choices"]) > 0, "No choices in response"
        
        if endpoint_type == 'chat':
            full_text = result["choices"][0].get("message", {}).get("content", "")
        else:
            full_text = result["choices"][0].get("text", "")

    if not ignore_output:
        max_tokens = payload.get('max_tokens', None)
        stream = payload.get('stream', False)
        print(f"Response received ({endpoint_type}, stream={stream}, max_tokens={max_tokens}): {full_text}")

    return full_text

def download_and_cache_file(url: str, filename: Optional[str] = None) -> str:
    if filename is None:
        filename = os.path.join("/data", url.split("/")[-1])
    if os.path.exists(filename):
        return filename
    print(f"Downloading from {url} to {filename}")
    response = requests.get(url, stream=True)
    response.raise_for_status()
    with open(filename, "wb") as f:
        for chunk in response.iter_content(chunk_size=1024):
            f.write(chunk)
    return filename
