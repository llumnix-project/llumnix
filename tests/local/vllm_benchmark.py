import argparse
import copy
import os
import time

import pytest
from modelscope import snapshot_download

from utils import GATEWAY_PORT, MODEL_PATH
from benchmarks.benchmark_serving import run_benchmark, set_global_args

MILLISECONDS_TO_SECONDS_CONVERSION = 1000
DATASET_NAME = "gliang1001/ShareGPT_V3_unfiltered_cleaned_split"
DATASET_PATH = f"/datasets/{DATASET_NAME}/ShareGPT_V3_unfiltered_cleaned_split.json"

global args


@pytest.fixture(scope="session")
def prepare_dataset():
    base_dir = os.path.dirname(DATASET_PATH)
    os.makedirs(base_dir, exist_ok=True)

    if not os.path.exists(DATASET_PATH):
        print(f"\nDataset not found at {DATASET_PATH}. Downloading...")
        dataset_dir = snapshot_download(
            DATASET_NAME, cache_dir="/datasets", repo_type="dataset"
        )
        print(f"Downloaded dataset to {dataset_dir}")
    else:
        print(f"\nUsing cached dataset at {DATASET_PATH}")

    yield DATASET_PATH


def generate_benchmark_config():
    base_configs = [
        {
            "policy": "load-balance",
            "enable_full_mode_scheduling": True,
            "connector_type": "HybridConnector",
        },
        {
            "policy": "load-balance",
            "enable_full_mode_scheduling": False,
            "connector_type": "HybridConnector",
        },
    ]

    update_configs = []
    for enable_pd in [False, True]:
        for config in base_configs:
            tmp_config = copy.deepcopy(config)
            tmp_config["enable_pd"] = enable_pd
            update_configs.append(tmp_config)
    base_configs = update_configs
    return base_configs


# pylint: disable=unused-argument
@pytest.mark.parametrize("test_config", generate_benchmark_config())
def test_benchmark(setup_services, test_config, prepare_dataset):
    base_params = {
        "tokenizer": f"{MODEL_PATH}",
        "model": f"{MODEL_PATH}",
        "base_url": f"http://localhost:{GATEWAY_PORT}",
        "dataset_path": prepare_dataset,
        "backend": "vllm",
        "dataset_name": "sharegpt",
    }

    benchmark_run_params = {
        "request_rate": 3.5,
        "max_concurrency": 22,
        "num_prompts": 1200,
        "warmup_requests": 16,
        "disable_tqdm": False,
        "port": None,
    }

    generation_params = {
        "trim_ratio": 1.2,
        "sharegpt_output_len": 1300,
        "disable_stream": False,
        "seed": 1,
        "disable_ignore_eos": False,
        "extra_request_body": None,
        "apply_chat_template": False,
        "lora_name": None,
        "prompt_suffix": "",
        "tokenize_prompt": False,
        "sharegpt_context_len": None,
    }

    unused_params = {
        "profile": False,
        "random_input_len": 1024,
        "random_output_len": 1024,
        "random_range_ratio": 0.0,
        "gsp_num_groups": 64,
        "gsp_prompts_per_group": 16,
        "gsp_system_prompt_len": 2048,
        "gsp_question_len": 128,
        "gsp_output_len": 256,
        "output_file": None,
        "return_logprob": False,
        "pd_separated": False,
        "flush_cache": False,
    }

    all_args_dict = {
        **base_params,
        **benchmark_run_params,
        **generation_params,
        **test_config,
        **unused_params,
    }

    args = argparse.Namespace(**all_args_dict)
    set_global_args(args)
    time.sleep(1)

    print("--- Start benchmark ---")
    result = run_benchmark(args)
    print("--- Benchmark end ---")

    assert result is not None, "Benchmark has no result"
    assert (
        result["completed"] > 0
    ), "Benchmark run successfully but completed 0 requests"
    print(f"Test finished with {result['completed']} requests completed.")
