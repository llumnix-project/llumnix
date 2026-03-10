import argparse
import asyncio
import json
import grpc
import httpx
from llumnix.llumlet.proto import llumlet_server_pb2_grpc, llumlet_server_pb2
from llumnix.utils import MigrationTriggerPolicy, MigrationType, RequestMigrationPolicy


class LlumletClient:
    def __init__(self, port=50051):
        self.port = port
        self.channel = None
        self.stub = None

    async def connect(self):
        print(f"Connecting to llumlet server at localhost:{self.port}...")
        self.channel = grpc.aio.insecure_channel(f"localhost:{self.port}")
        self.stub = llumlet_server_pb2_grpc.LlumletStub(self.channel)

    async def close(self):
        if self.channel:
            await self.channel.close()
            print("Channel closed.")

    async def send_migrate(self, dst_host, dst_port, num_mig):
        if not self.stub:
            raise RuntimeError("Client is not connected. Call connect() first.")

        print("Sending migrate request...")
        request = llumlet_server_pb2.MigrateRequest(
            src_engine_id="src",
            dst_engine_id="dst",
            dst_engine_ip=dst_host,
            dst_engine_port=dst_port,
            migration_req_policy=RequestMigrationPolicy.FCWSR,
            migration_type=MigrationType.NUM_REQ,
            num_reqs=20,
            trigger_policy=MigrationTriggerPolicy.NEUTRAL_LOAD,
        )
        try:
            response = await self.stub.Migrate(request)
            print("Response received:")
            print(response)
        except grpc.aio.AioRpcError as e:
            print(f"An RPC error occurred: {e.code()} - {e.details()}")


async def main(num_req, host, port, llumlet_port, dst_host, dst_port, num_mig):
    url = f"http://{host}:{port}/v1/completions"
    grpc_client = LlumletClient(port=llumlet_port)
    try:
        async with httpx.AsyncClient() as http_client:
            payloads = [
                {
                    "prompt": "'Please count from 1 to 100 one by one in your response.",
                    "max_tokens": 1000,
                    "temperature": 0,
                    "kv_transfer_params": {"ali_llumnix_disagg": False},
                }
                for _ in range(num_req)
            ]

            http_tasks = [
                asyncio.create_task(http_client.post(url, json=p, timeout=60))
                for p in payloads
            ]
            print(
                f"{len(http_tasks)} HTTP requests have been dispatched to the background."
            )
            await asyncio.sleep(5)
            await grpc_client.connect()
            await grpc_client.send_migrate(dst_host, dst_port, num_mig)
            responses = await asyncio.gather(*http_tasks, return_exceptions=True)
            for i, response in enumerate(responses):
                print(f"\n--- Response for Prompt #{i+1} ---")
                if isinstance(response, Exception):
                    print(f"An error occurred: {response}")
                elif response.status_code == 200:
                    print(json.dumps(response.json(), indent=2))
                else:
                    print(f"Request failed with status code: {response.status_code}")
                    print("Response content:", response.text)
    finally:
        await grpc_client.close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--num_requests",
        type=int,
        default=1,
        help="total number of requests for each client",
    )
    parser.add_argument(
        "--num_mig",
        type=int,
        default=1,
        help="total number of requests to be migrate out",
    )
    parser.add_argument(
        "--host", type=str, default="localhost", help="host ip for src instance"
    )
    parser.add_argument(
        "--port", type=int, default=8000, help="web port for src instance service"
    )
    parser.add_argument(
        "--llumlet_port",
        type=int,
        default=50051,
        help="llumlet grpc port for migration",
    )
    parser.add_argument(
        "--dst_host", type=str, default="localhost", help="host ip for dst instance"
    )
    parser.add_argument(
        "--dst_port", type=int, default=29876, help="kvt port for dst instance"
    )
    args = parser.parse_args()
    asyncio.run(
        main(
            args.num_requests,
            args.host,
            args.port,
            args.llumlet_port,
            args.dst_host,
            args.dst_port,
            args.num_mig,
        )
    )
