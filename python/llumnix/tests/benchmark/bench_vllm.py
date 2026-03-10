# bench_vllm.py

import argparse
import ast
import asyncio
import json
import logging
import os
import random
import re
import sys
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional, Protocol

import grpc
import numpy as np
import requests
from tqdm import tqdm

from llumnix.llumlet.proto import llumlet_server_pb2, llumlet_server_pb2_grpc
from llumnix.utils import MigrationType

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s | %(levelname)-8s | %(name)-15s | %(message)s",
)
logger = logging.getLogger("Benchmark")

INVALID_ANSWER = -9999999


@dataclass
class KVTInfo:
    id: str = "src"
    kvt_host: str = ""
    kvt_port: int = 28000
    llumlet_url: str = ""


@dataclass
class BenchmarkConfig:

    # mode and dataset
    mode: str = "cnt"
    num_questions: int = 1
    num_shots: int = 5
    data_path: str = "test.jsonl"
    max_tokens: int = 3000

    # backend API
    url: str = "http://127.0.0.1:8000"

    # parallel
    parallel: int = 64
    n_ctx: int = 4096

    # migration
    send_migration: bool = False
    mutual_mig: bool = False
    src_kvt_host: str = ""
    dst_kvt_host: str = ""
    dst_port: int = 29876
    src_llumlet_url: str = ""
    dst_llumlet_url: int = ""
    src_kvt_port: int = 28000
    dst_kvt_port: int = 29876

    # result
    result_file: str = "result.jsonl"

    # server interface
    api_url: str = field(init=False)

    def __post_init__(self):
        if self.mode == "cnt":
            self.api_url = f"{self.url}/v1/chat/completions"
        else:
            self.api_url = f"{self.url}/v1/completions"


def parse_args() -> BenchmarkConfig:
    parser = argparse.ArgumentParser(description="LLM Benchmark Script")
    parser.add_argument(
        "--url", type=str, default="http://127.0.0.1:8000", help="url for service"
    )
    parser.add_argument("--src-kvt-host", type=str, help="kvt host ip (net0)")
    parser.add_argument("--dst-kvt-host", type=str, help="kvt host ip (net0)")
    parser.add_argument("--n-ctx", type=int, default=4096)
    parser.add_argument("--result-file", type=str, default="result.jsonl")
    parser.add_argument("--mode", type=str, default="cnt", choices=["gsm8k", "cnt"])
    parser.add_argument("--num-questions", type=int, default=1)
    parser.add_argument("--parallel", type=int, default=64)
    parser.add_argument("--send-migration", action="store_true")
    parser.add_argument("--mutual-mig", action="store_true")
    parser.add_argument("--src-llumlet-url", type=str)
    parser.add_argument("--dst-llumlet-url", type=str)
    parser.add_argument("--max-tokens", type=int, default=3000)
    parser.add_argument("--src-kvt-port", type=int)
    parser.add_argument("--dst-kvt-port", type=int)

    args = parser.parse_args()
    if args.send_migration:
        required_for_migration = [
            "src_llumlet_url",
            "dst_kvt_port",
            "dst_kvt_host",
        ]
        missing_args = []
        for arg_name in required_for_migration:
            if getattr(args, arg_name) is None:
                missing_args.append(f"--{arg_name.replace('_', '-')}")
        if missing_args:
            parser.error(
                f"The following arguments are required when --send_migration is set: {', '.join(missing_args)}"
            )

    if args.mutual_mig:
        required_for_migration = [
            "send_migration",
            "dst_llumlet_url",
            "src_kvt_port",
            "src_kvt_host",
        ]
        for arg_name in required_for_migration:
            if getattr(args, arg_name) is None:
                missing_args.append(f"--{arg_name.replace('_', '-')}")
        if missing_args:
            parser.error(
                f"The following arguments are required when --mutual-mig is set: {', '.join(missing_args)}"
            )

    return BenchmarkConfig(**vars(args))


class LlumletClient:
    def __init__(self, url: str):
        self._url = url
        self._channel: Optional[grpc.aio.Channel] = None
        self._stub: Optional[llumlet_server_pb2_grpc.LlumletStub] = None
        self._logger = logging.getLogger(self.__class__.__name__)

    async def connect(self):
        self._logger.info(f"Connecting to llumlet server at {self._url}...")
        self._channel = grpc.aio.insecure_channel(f"{self._url}")
        self._stub = llumlet_server_pb2_grpc.LlumletStub(self._channel)

    async def close(self):
        if self._channel:
            await self._channel.close()
            self._logger.info("gRPC channel closed.")

    async def send_migrate(self, src, dst):
        if not self._stub:
            raise RuntimeError("Client is not connected. Call connect() first.")

        self._logger.info("Sending migrate request from %s to %s...", src.id, dst.id)
        request = llumlet_server_pb2.MigrateRequest(
            src_engine_id=src.id,
            dst_engine_id=dst.id,
            dst_engine_ip=dst.kvt_host,
            dst_engine_port=dst.kvt_port,
            migration_type=MigrationType.NUM_REQ,
            num_reqs=5,
        )
        try:
            response = await self._stub.Migrate(request)
            self._logger.info(f"Migration response received: {response}")
        except grpc.aio.AioRpcError as e:
            self._logger.error(f"An RPC error occurred: {e.code()} - {e.details()}")


def download_and_cache_file(url: str, filename: Optional[str] = None) -> str:
    local_logger = logging.getLogger("Downloader")
    if filename is None:
        filename = os.path.join("/tmp", url.split("/")[-1])
    if os.path.exists(filename):
        return filename
    local_logger.info(f"Downloading from {url} to {filename}")
    response = requests.get(url, stream=True)
    response.raise_for_status()
    total_size = int(response.headers.get("content-length", 0))
    with open(filename, "wb") as f, tqdm(
        total=total_size, unit="B", unit_scale=True
    ) as bar:
        for chunk in response.iter_content(chunk_size=1024):
            f.write(chunk)
            bar.update(len(chunk))
    return filename


class Dataset(Protocol):
    def get_prompts(self) -> List[str]: ...
    def get_labels(self) -> List[Any]: ...


class GSM8KDataset:
    def __init__(self, num_questions: int, num_shots: int):
        self._num_questions = num_questions
        self._num_shots = num_shots
        self._lines = self._load_data()
        self._prompts, self._labels = self._prepare_data()

    def _load_data(self):
        url = "https://raw.githubusercontent.com/openai/grade-school-math/master/grade_school_math/data/test.jsonl"
        filename = download_and_cache_file(url)
        with open(filename) as fin:
            return [json.loads(line) for line in fin if not line.startswith("#")]

    def _prepare_data(self):
        def get_one_example(lines, i, include_answer):
            ret = f"Question: {lines[i]['question']}\nAnswer:"
            if include_answer:
                ret += f" {lines[i]['answer']}"
            return ret

        few_shot_examples = (
            "\n\n".join(
                [get_one_example(self._lines, i, True) for i in range(self._num_shots)]
            )
            + "\n\n"
        )

        questions = [
            get_one_example(self._lines, i, False) for i in range(len(self._lines))
        ]
        labels = [self._get_answer_value(line["answer"]) for line in self._lines]

        # extend prompts to num_questions
        prompts = []
        final_labels = []
        while len(prompts) < self._num_questions:
            prompts.extend([few_shot_examples + q for q in questions])
            final_labels.extend(labels)

        return prompts[: self._num_questions], final_labels[: self._num_questions]

    @staticmethod
    def _get_answer_value(answer_str: str) -> int:
        answer_str = answer_str.replace(",", "")
        numbers = re.findall(r"\d+", answer_str)
        try:
            return ast.literal_eval(numbers[-1]) if numbers else INVALID_ANSWER
        except (SyntaxError, ValueError):
            return INVALID_ANSWER

    def get_prompts(self) -> List[str]:
        return self._prompts

    def get_labels(self) -> List[Any]:
        return self._labels


class CountingDataset:
    def __init__(self, num_questions: int):
        self._num_questions = num_questions

    def get_prompts(self) -> List[str]:
        return [""] * self._num_questions

    def get_labels(self) -> List[Any]:
        return [True] * self._num_questions


class APIClient(Protocol):
    async def generate(
        self,
        prompt: str,
        loop: asyncio.AbstractEventLoop,
        executor: ThreadPoolExecutor,
        max_tokens: int,
    ) -> str: ...


class VLLMAPIClient:
    def __init__(self, url: str):
        self._url = url

    def _sync_call(self, prompt: str, max_tokens):
        data = {
            "prompt": prompt,
            "temperature": 0,
            "max_tokens": max_tokens,
            "stop": ["Question", "Assistant:", "<|separator|>"],
            "kv_transfer_params": {"ali_llumnix_disagg": False},
        }
        res = requests.post(self._url, json=data)
        res.raise_for_status()
        return res.json()["choices"][0]["text"]

    async def generate(
        self,
        prompt: str,
        loop: asyncio.AbstractEventLoop,
        executor: ThreadPoolExecutor,
        max_tokens: int,
    ) -> str:
        return await loop.run_in_executor(executor, self._sync_call, prompt, max_tokens)


class CountingAPIClient:
    def __init__(self, url: str):
        self._url = url

    def _sync_call(self, prompt: str, max_tokens):
        # 'prompt' is unused for this mode
        messages = [
            {
                "role": "system",
                "content": "Please count from 0 to 100 one by one in your response with",
            },
        ]
        data = {
            "messages": messages,
            "stream": False,
            "temperature": 0,
            "max_tokens": max_tokens,
            "kv_transfer_params": {"ali_llumnix_disagg": False},
        }
        res = requests.post(self._url, json=data)
        res.raise_for_status()
        return res.json()["choices"][0]["message"]["content"]

    async def generate(
        self,
        prompt: str,
        loop: asyncio.AbstractEventLoop,
        executor: ThreadPoolExecutor,
        max_tokens: int,
    ) -> str:
        return await loop.run_in_executor(executor, self._sync_call, prompt, max_tokens)


class Evaluator(Protocol):
    def evaluate(
        self, predictions: List[str], labels: List[Any]
    ) -> Dict[str, float]: ...


class GSM8KEvaluator:
    def evaluate(self, predictions: List[str], labels: List[int]) -> Dict[str, float]:
        parsed_preds = [GSM8KDataset._get_answer_value(p) for p in predictions]

        correct = np.array(parsed_preds) == np.array(labels)
        invalid = np.array(parsed_preds) == INVALID_ANSWER

        accuracy = np.mean(correct)
        invalid_rate = np.mean(invalid)

        logger.info(f"Accuracy: {accuracy:.4f}")
        logger.info(f"Invalid Rate: {invalid_rate:.4f}")
        return {"accuracy": accuracy, "invalid_rate": invalid_rate}


class CountingEvaluator:
    def _find_longest_consecutive_subsequence(self, numbers: list):
        if not numbers:
            return []
        longest, current = [], []
        for num in numbers:
            if not current or num == current[-1] + 1:
                current.append(num)
            else:
                current = [num]
            if len(current) > len(longest):
                longest = current
        return longest

    def _validate(self, text: str, start: int, end: int) -> bool:
        try:
            numbers_found = [int(num) for num in re.findall(r"\d+", text)]
        except (ValueError, TypeError):
            return False

        expected = list(range(start, end + 1))
        main_sequence = self._find_longest_consecutive_subsequence(numbers_found)
        return main_sequence == expected

    def evaluate(self, predictions: List[str], labels: List[bool]) -> Dict[str, float]:
        # 'labels' is a list of True values
        results = [self._validate(p, 0, 100) for p in predictions]
        accuracy = np.mean(np.array(results) == np.array(labels))

        logger.info(f"Accuracy: {accuracy:.3f}")
        return {"accuracy": accuracy}


class ResultLogger:
    def __init__(self, config: BenchmarkConfig):
        self._config = config
        self._logger = logging.getLogger(self.__class__.__name__)

    def dump_raw_output(self, predictions: List[str]):
        filename = f"tmp_output.txt"
        with open(filename, "w") as f:
            for i, pred in enumerate(predictions):
                f.write(f"{'='*35} {i} {'='*35}\n{pred}\n{'='*72}\n\n")
        self._logger.info(f"Raw outputs dumped to {filename}")

    def log_final_results(self, metrics: Dict[str, float], latency: float):
        self._logger.info(f"Total Latency: {latency:.3f} s")
        value = {
            "task": self._config.mode,
            "num_gpus": 1,
            "latency": round(latency, 3),
            "accuracy": round(metrics.get("accuracy", 0.0), 3),
            "num_requests": self._config.num_questions,
            "other": {
                "parallel": self._config.parallel,
                "num_shots": self._config.num_shots,
            },
        }
        with open(self._config.result_file, "a") as f:
            f.write(json.dumps(value) + "\n")
        self._logger.info(f"Final results appended to {self._config.result_file}")


class BenchmarkRunner:
    def __init__(self, config: BenchmarkConfig):
        self._config = config
        self._logger = logging.getLogger(self.__class__.__name__)
        self._dataset: Dataset = self._create_dataset()
        self._api_client: APIClient = self._create_api_client()
        self._evaluator: Evaluator = self._create_evaluator()
        self._result_logger = ResultLogger(config)
        self._grpc_client: Optional[LlumletClient] = None
        self.src = KVTInfo(
            id="src",
            kvt_host=self._config.src_kvt_host,
            kvt_port=self._config.src_kvt_port,
            llumlet_url=self._config.src_llumlet_url,
        )
        self.dst = KVTInfo(
            id="dst",
            kvt_host=self._config.dst_kvt_host,
            kvt_port=self._config.dst_kvt_port,
            llumlet_url=self._config.dst_llumlet_url,
        )

    def _create_dataset(self) -> Dataset:
        if self._config.mode == "gsm8k":
            return GSM8KDataset(self._config.num_questions, self._config.num_shots)
        elif self._config.mode == "cnt":
            return CountingDataset(self._config.num_questions)
        else:
            raise ValueError(f"Unsupported mode: {self._config.mode}")

    def _create_api_client(self) -> APIClient:
        if self._config.mode == "cnt":
            return CountingAPIClient(self._config.api_url)
        return VLLMAPIClient(self._config.api_url)

    def _create_evaluator(self) -> Evaluator:
        if self._config.mode == "gsm8k":
            return GSM8KEvaluator()
        elif self._config.mode == "cnt":
            return CountingEvaluator()
        else:
            raise ValueError(f"Unsupported mode: {self._config.mode}")

    async def _run_migration_task(
        self, stop_event: asyncio.Event, grpc_client, src, dst
    ):
        self._logger.info("Background migration task started.")
        while not stop_event.is_set():
            try:
                await asyncio.wait_for(
                    stop_event.wait(), timeout=random.randint(200, 500) * 0.001
                )
            except asyncio.TimeoutError:
                if grpc_client:
                    await grpc_client.send_migrate(src, dst)
            except Exception as e:
                self._logger.error(f"Error in migration task loop: {e}")
        self._logger.info("Background migration task has been stopped.")

    async def run(self):
        prompts = self._dataset.get_prompts()
        labels = self._dataset.get_labels()
        predictions = [""] * len(prompts)

        loop = asyncio.get_running_loop()
        stop_event = asyncio.Event()
        migration_task_src = None
        migration_task_dst = None

        if self._config.send_migration:
            self._grpc_client_src = LlumletClient(self._config.src_llumlet_url)
            await self._grpc_client_src.connect()
            migration_task_src = asyncio.create_task(
                self._run_migration_task(
                    stop_event, self._grpc_client_src, self.src, self.dst
                )
            )
            if self._config.mutual_mig:
                self._grpc_client_dst = LlumletClient(self._config.dst_llumlet_url)
                await self._grpc_client_dst.connect()
                migration_task_dst = asyncio.create_task(
                    self._run_migration_task(
                        stop_event, self._grpc_client_dst, self.dst, self.src
                    )
                )

        self._logger.info(
            f"Starting benchmark for mode '{self._config.mode}' with {len(prompts)} requests..."
        )
        start_time = time.perf_counter()

        with ThreadPoolExecutor(self._config.parallel) as executor:

            async def process_request(i):
                try:
                    result = await self._api_client.generate(
                        prompts[i], loop, executor, self._config.max_tokens
                    )
                    predictions[i] = result
                except Exception:
                    self._logger.error(f"Request {i} failed.", exc_info=True)

            main_tasks = [
                asyncio.create_task(process_request(i)) for i in range(len(prompts))
            ]

            for future in tqdm(
                asyncio.as_completed(main_tasks),
                total=len(main_tasks),
                desc="Processing Requests",
            ):
                await future

        latency = time.perf_counter() - start_time

        if migration_task_src:
            self._logger.info("All main tasks are done. Stopping migration task src...")
            stop_event.set()
            await migration_task_src
        if migration_task_dst:
            self._logger.info("All main tasks are done. Stopping migration task dst...")
            stop_event.set()
            await migration_task_dst
        if self._grpc_client:
            await self._grpc_client.close()

        self._logger.info("Evaluating results...")
        metrics = self._evaluator.evaluate(predictions, labels)

        self._result_logger.dump_raw_output(predictions)
        self._result_logger.log_final_results(metrics, latency)


if __name__ == "__main__":
    try:
        config = parse_args()
        runner = BenchmarkRunner(config)
        asyncio.run(runner.run())
    except Exception as e:
        logger.critical("An unhandled exception occurred.", exc_info=True)
        sys.exit(1)
