import ast
import copy
import json
import time
import re
from typing import List, Any
import concurrent.futures

import numpy as np
import pytest

from utils import download_and_cache_file, send_request, LOG_DIR

INVALID = -9999


def generate_mig_correct_config():
    base_configs = [
        {
            "policy": "load-balance",
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

    update_configs = []
    for dataset in ["cnt", "gsm8k"]:
        for config in base_configs:
            tmp_config = copy.deepcopy(config)
            tmp_config["dataset"] = dataset
            update_configs.append(tmp_config)
    base_configs = update_configs

    return base_configs


def _find_longest_consecutive_sequence(numbers: List[int]) -> List[int]:
    """
    Finds the longest subsequence of consecutive integers in a list.
    e.g., [0, 1, 5, 6, 7, 2, 3, 4] -> [5, 6, 7]
    This logic is adapted from `_find_longest_consecutive_subsequence` in `bench_vllm_comm.py`.
    """
    if not numbers:
        return []

    longest_seq, current_seq = [], []
    for num in numbers:
        if not current_seq or num == current_seq[-1] + 1:
            current_seq.append(num)
        else:
            # If the sequence breaks, check if the current one was the longest so far
            if len(current_seq) > len(longest_seq):
                longest_seq = current_seq
            # Start a new sequence
            current_seq = [num]

    # Final check for the last sequence
    if len(current_seq) > len(longest_seq):
        longest_seq = current_seq

    return longest_seq


def validate_counting_response(text: str, start: int = 0, end: int = 100) -> bool:
    try:
        numbers_found = [int(num) for num in re.findall(r"\d+", text)]
    except (ValueError, TypeError):
        print("Validation failed: Could not parse numbers from response.")
        return False
    if not numbers_found:
        print("Validation failed: No numbers found in the response.")
        return False
    main_sequence = _find_longest_consecutive_sequence(numbers_found)
    expected_sequence = list(range(start, end + 1))
    is_valid = main_sequence == expected_sequence

    return is_valid


def check_count_res(responses: List[Any]):
    valid_count = 0
    error_count = 0
    for _, res in enumerate(responses):
        if validate_counting_response(res):
            valid_count += 1
        else:
            error_count += 1

    acc = valid_count / len(responses)
    invalid = 1 - acc
    print(f"Accuracy: {acc:.3f}")
    print(f"Invalid: {invalid:.3f}")

    assert valid_count == len(
        responses
    ), f"{error_count} responses failed counting validation."
    print(f"✓ {valid_count}/{len(responses)} responses passed counting validation.")
    return acc


def get_answer_value(answer_str):
    answer_str = answer_str.replace(",", "")
    numbers = re.findall(r"\d+", answer_str)
    if len(numbers) < 1:
        return INVALID
    try:
        return ast.literal_eval(numbers[-1])
    except SyntaxError:
        return INVALID


def check_gsm8k_res(responses: List[Any], labels: List[Any]):
    preds = []
    for res in responses:
        preds.append(get_answer_value(res))
    assert len(preds) == len(labels)
    correct = sum(1 for p, l in zip(preds, labels) if p == l)

    # Compute accuracy
    acc = np.mean(np.array(preds) == np.array(labels))
    invalid = np.mean(np.array(preds) == INVALID)

    print(f"Accuracy: {acc:.3f}")
    print(f"Invalid: {invalid:.3f}")
    return acc


def generate_gsm8k_dataset(num_questions: int):
    def get_one_example(lines, i, include_answer):
        ret = f"Question: {lines[i]['question']}\nAnswer:"
        if include_answer:
            ret += f" {lines[i]['answer']}"
        return ret

    # Read data
    url = "https://raw.githubusercontent.com/openai/grade-school-math/master/grade_school_math/data/test.jsonl"
    filename = download_and_cache_file(url, filename="/data/gsm8k/test.jsonl")
    with open(filename, encoding="utf-8") as fin:
        lines = [json.loads(line) for line in fin if not line.startswith("#")]
    # Get examples
    num_shots = 5
    few_shot_examples = (
        "\n\n".join([get_one_example(lines, i, True) for i in range(num_shots)])
        + "\n\n"
    )
    questions = [get_one_example(lines, i, False) for i in range(len(lines))]
    labels = [get_answer_value(line["answer"]) for line in lines]
    assert all(l != INVALID for l in labels)
    prompts = []
    final_labels = []
    while len(prompts) < num_questions:
        prompts.extend([few_shot_examples + q for q in questions])
        final_labels.extend(labels)
    return prompts[:num_questions], final_labels[:num_questions]


def check_migration_res(dataset: str, results: List[Any], labels: List[Any] = None):
    if dataset == "cnt":
        acc = check_count_res(results)
    elif dataset == "gsm8k":
        acc = check_gsm8k_res(results, labels)
    else:
        raise ValueError(f"Unknown dataset: {dataset}")
    print("✓ Migration correctness check finished!")
    return acc


def dump_res(results: List[Any], dataset: str, acc: float, num_questions: int):
    dump_output_filename = f"{LOG_DIR}/tmp_output.txt"
    res_file = f"{LOG_DIR}/correctness_results.jsonl"
    with open(dump_output_filename, "w", encoding="utf-8") as f:
        for i, pred in enumerate(results):
            f.write(f"{'='*35} {i} {'='*35}\n{pred}\n{'='*72}\n\n")
    print(f"Raw outputs dumped to {dump_output_filename}")

    value = {
        "task": dataset,
        "accuracy": round(acc, 3),
        "num_requests": num_questions,
        "time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime()),
    }
    with open(res_file, "a", encoding="utf-8") as f:
        f.write(json.dumps(value) + "\n")
    print(f"Summary results dumped to {res_file}")


# pylint: disable=unused-argument
@pytest.mark.parametrize("test_config", generate_mig_correct_config(), indirect=True)
def test_migration_correctness(setup_services, test_config):
    num_requests = 500

    def send_single_request(connector_type, prompt, dataset):
        payload = {
            "prompt": prompt,
            "stream": False,
            "max_tokens": 3000,
            "ignore_eos": True,
            "temperature": 0.0,
        }
        if connector_type == "HybridConnector" and not test_config.get(
            "enable_pd", False
        ):
            # HybridConnector may produce longer outputs due to its design
            payload["kv_transfer_params"] = {"ali_llumnix_disagg": False}
        if dataset == "gsm8k":
            payload["stop"] = ["Question", "Assistant:", "<|separator|>"]
        start = time.time()
        result = send_request(payload, endpoint_type="completions", ignore_output=True)
        return result

    print(f"\n=== Starting {num_requests} concurrent requests ===")
    start_time = time.time()
    dataset = test_config.get("dataset")
    if dataset == "gsm8k":
        prompts, labels = generate_gsm8k_dataset(num_requests)
    elif dataset == "cnt":
        prompts = [
            "Please count from 0 to 100 one by one in your response, with each number on a new line."
        ] * num_requests
        labels = None
    else:
        raise ValueError(f"Unknown dataset: {dataset}")
    with concurrent.futures.ThreadPoolExecutor(max_workers=10) as executor:
        futures = [
            executor.submit(
                send_single_request, test_config.get("connector_type"), prompt, dataset
            )
            for prompt in prompts
        ]
        results = [
            future.result() for future in concurrent.futures.as_completed(futures)
        ]
    end_time = time.time()

    assert (
        len(results) == num_requests
    ), f"Only {len(results)}/{num_requests} requests completed"
    acc = check_migration_res(
        dataset=test_config.get("dataset"), results=results, labels=labels
    )
    dump_res(results, test_config.get("dataset"), acc, num_requests)

    print(
        f"\n=== Completed {len(results)}/{num_requests} requests in {end_time - start_time:.2f}s ==="
    )
