# Copyright (c) 2024, Alibaba Group;
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
# http://www.apache.org/licenses/LICENSE-2.0

# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# pylint: disable=missing-function-docstring
import os
import logging
from typing import Union

import redis.asyncio as redis
from redis.retry import Retry
from redis.backoff import ConstantBackoff
from llumnix.cms.proto.cms_pb2 import InstanceMetadata, InstanceStatus

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

LLUMNIX_INSTANCE_METADATA_PREFIX = "llumnix:instance_metadata:"
LLUMNIX_INSTANCE_STATUS_PREFIX = "llumnix:instance_status:"
CMS_REDIS_PULL_METADATA_INTERVAL_MS_DEFAULT = "10000"
CMS_REDIS_PULL_STATUS_INTERVAL_MS_DEFAULT = "500"

REDIS_SOCKET_TIMEOUT = float(os.getenv("REDIS_SOCKET_TIMEOUT", "1"))
REDIS_RETRY_TIMES = int(os.getenv("REDIS_RETRY_TIMES", "1"))
REDIS_RETRY_INTERVAL = float(os.getenv("REDIS_RETRY_INTERVAL", "0.1"))


class RedisClient:
    """
    Provides Redis connection and read/write operation interfaces
    """

    def __init__(self):
        host: str = os.getenv("LLUMNIX_CMS_REDIS_ADDRESS", "redis.roles")
        port: int = int(os.getenv("LLUMNIX_CMS_REDIS_PORT", "10000"))
        username: str = os.getenv("LLUMNIX_CMS_REDIS_USERNAME", "default")
        password: str = os.getenv("LLUMNIX_CMS_REDIS_PASSWORD", "")

        # enable retry and timeout
        redis_params = {
            "host": host,
            "port": port,
            "socket_timeout": REDIS_SOCKET_TIMEOUT,
        }
        if username:
            redis_params["username"] = username
        if password:
            redis_params["password"] = password
        if REDIS_RETRY_TIMES > 0:
            redis_params["retry"] = Retry(
                ConstantBackoff(REDIS_RETRY_INTERVAL), REDIS_RETRY_TIMES
            )

        self.redis_client = redis.Redis(**redis_params)
        logger.info("Redis client initialized with host=%s, port=%s", host, port)

    def encode_str_to_bytes(self, key: Union[str, bytes]):
        if isinstance(key, str):
            return key.encode("utf-8")
        return key

    def decode_bytes_to_str(self, key: Union[str, bytes]):
        if isinstance(key, bytes):
            return key.decode("utf-8")
        return key

    async def set(self, key, value, ex=0):
        """
        Sets a key-value pair in Redis with an optional expiration.
        If 'ex' is positive, the key will have a timeout in ex seconds.
        Otherwise, it will be permanent.
        """
        if ex > 0:
            await self.redis_client.set(key, value, ex=ex)
        else:
            await self.redis_client.set(key, value)

    async def get(self, key):
        return await self.redis_client.get(key)

    async def mget_bytes(self, keys):
        res = await self.redis_client.mget(keys)
        for i, value in enumerate(res):
            if value:
                res[i] = self.encode_str_to_bytes(value)
        return res

    async def mget(self, keys):
        return await self.redis_client.mget(keys)

    async def get_str(self, key):
        value = await self.redis_client.get(key)
        return self.decode_bytes_to_str(value)

    async def remove(self, key):
        return await self.redis_client.delete(key)

    async def get_keys_by_prefix(self, prefix: str):
        keys = []
        async for key in self.redis_client.scan_iter(match=prefix + "*"):
            key = self.decode_bytes_to_str(key)
            keys.append(key)
        return keys


class CMSWriteClient:
    """
    Provides CMS write operation interfaces:
    - Support instance runtime info (current)
    - Support KV cache meta info (future)
    """

    def __init__(self, redis_client: RedisClient = None):
        self.redis_client = redis_client if redis_client else RedisClient()
        logger.info("CMSWriteClient initialized")

    async def add_instance(self, instance_id: str, instance_metadata: InstanceMetadata, expired: int = 0):
        logger.info("Adding instance: %s", instance_id)
        key = LLUMNIX_INSTANCE_METADATA_PREFIX + instance_id
        value = instance_metadata.SerializeToString()
        await self.redis_client.set(key, value, expired)

    async def update_instance_metadata(
        self, instance_id: str, instance_metadata: InstanceMetadata, expired: int = 0
    ):
        logger.debug("Update instance metadata: %s", instance_id)
        key = LLUMNIX_INSTANCE_METADATA_PREFIX + instance_id
        value = instance_metadata.SerializeToString()
        await self.redis_client.set(key, value, expired)

    async def update_instance_status(self, instance_id, instance_status: InstanceStatus, expired: int = 0):
        logger.debug("Update instance status: %s, update_id: %d", instance_id, instance_status.update_id)
        key = LLUMNIX_INSTANCE_STATUS_PREFIX + instance_id
        value = instance_status.SerializeToString()
        await self.redis_client.set(key, value, expired)

    async def remove_instance(self, instance_id):
        logger.info("Removing instance: %s", instance_id)
        # remove metadata
        await self.redis_client.remove(LLUMNIX_INSTANCE_METADATA_PREFIX + instance_id)
        # remove status
        await self.redis_client.remove(LLUMNIX_INSTANCE_STATUS_PREFIX + instance_id)
