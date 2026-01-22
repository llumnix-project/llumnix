import argparse
import json
import multiprocessing
import requests
import asyncio
import grpc

from llumnix.llumlet.proto import llumlet_server_pb2_grpc, llumlet_server_pb2
from llumnix.utils import MigrationType, RequestMigrationPolicy


class LlumletClient:
    def __init__(self, port=50051):
        self.port = port
        self.channel = None
        self.stub = None

    async def connect(self):
        print(f"Connecting to llumlet server at localhost:{self.port}...")
        self.channel = grpc.aio.insecure_channel(f'localhost:{self.port}')
        self.stub = llumlet_server_pb2_grpc.LlumletStub(self.channel)

    async def close(self):
        if self.channel:
            await self.channel.close()
            print("Channel closed.")

    async def send_migrate(self, dst_host, dst_port):
        if not self.stub:
            raise RuntimeError("Client is not connected. Call connect() first.")

        print("Sending migrate request...")
        request = llumlet_server_pb2.MigrateRequest(
            src_engine_id="src",
            dst_engine_id="dst",
            dst_engine_ip=dst_host,
            dst_engine_port=dst_port,
            migration_req_policy=RequestMigrationPolicy.SR,
            migration_type=MigrationType.NUM_REQ,
            num_reqs=1,
            trigger_policy="NEUTRAL_LOAD",
        )
        try:
            response = await self.stub.Migrate(request)
            print("Response received:")
            print(response)
        except grpc.aio.AioRpcError as e:
            print(f"An RPC error occurred: {e.code()} - {e.details()}")


async def migrate(llumlet_port, dst_host, dst_port):
    grpc_client = LlumletClient(llumlet_port)
    await asyncio.sleep(2)
    await grpc_client.connect()
    await grpc_client.send_migrate(dst_host, dst_port)
    await grpc_client.close()


def trigger_migration(llumlet_port, dst_host, dst_port):
    asyncio.run(migrate(llumlet_port, dst_host, dst_port))


async def async_run_stream_generate(num_requests: int, host: str, port: int):
    for _ in range(num_requests):
        url = f"http://{host}:{port}/v1/chat/completions"
        messages = [
            {'role': 'system', 'content': 'Please count from 1 to 500 one by one in your response.'},
        ]
        req = {
            "messages": messages,
            "stream": True,
            "ignore_eos": False,
            "temperature": 0.0,
            "top_p": 0.5,
            "top_k": 10,
            "max_tokens": 2000,
            "kv_transfer_params": {"ali_llumnix_disagg": False},
        }

        response = requests.post(
            url,
            json=req,
            headers={"Content-Type": "application/json"},
            stream=True,
        )

        last_resp = ""
        tokens = ""

        for chunk in response.iter_lines(chunk_size=8192, decode_unicode=False):
            msg = chunk.decode("utf-8")
            try:
                if msg.startswith('data'):
                    info = msg[6:]
                    if info == '[DONE]':
                        break
                    else:
                        last_resp = json.loads(info)
                        tokens += last_resp['choices'][0]['delta']['content']
                        print(last_resp['choices'][0]['delta']['content'], end='', flush=True)
            except Exception:
                print(msg)
                raise


def run_stream_generate(num_requests: int, host: str, port: int):
    asyncio.run(async_run_stream_generate(num_requests, host, port))


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--num_requests', type=int, default=1, help='total number of requests for each client')
    parser.add_argument('--host', type=str, default='localhost', help='host ip for src instance')
    parser.add_argument('--port', type=int, default=8000, help='web port for src instance service')
    parser.add_argument('--llumlet_port', type=int, default=50051, help='llumlet grpc port for migration')
    parser.add_argument('--dst_host', type=str, default='localhost', help='host ip for dst instance')
    parser.add_argument('--dst_port', type=int, default=29876, help='kvt port for dst instance')
    args = parser.parse_args()

    stream_process = multiprocessing.Process(target=run_stream_generate, args=(args.num_requests, args.host, args.port))
    stream_process.start()

    migration_process = multiprocessing.Process(target=trigger_migration, args=(args.llumlet_port, args.dst_host, args.dst_port))
    migration_process.start()

    stream_process.join()
    migration_process.join()

    print("\ndone")
