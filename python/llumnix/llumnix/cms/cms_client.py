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


import os
import threading
import time
from typing import Union

import redis
from readerwriterlock import rwlock
from redis.backoff import ConstantBackoff
from redis.retry import Retry

from llumnix.cms.proto.cms_pb2 import InstanceMetadata, InstanceStatus
from llumnix.logging.logger import init_logger

logger = init_logger(__name__)

LLUMNIX_INSTANCE_METADATA_PREFIX = "llumnix:instance_metadata:"
LLUMNIX_INSTANCE_STATUS_PREFIX = "llumnix:instance_status:"
CMS_REDIS_PULL_METADATA_INTERVAL_MS_DEFAULT = "10000"
CMS_REDIS_PULL_STATUS_INTERVAL_MS_DEFAULT = "500"
CMS_REFRESH_LOOP_SLEEP_TIME_MS_DEFAULT = (
    "100"  # must smaller than METADATA_INTERVAL_MS and STATUS_INTERVAL_MS
)

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

    def set(self, key, value):
        self.redis_client.set(key, value)

    def get(self, key):
        return self.redis_client.get(key)

    def mget_bytes(self, keys):
        res = self.redis_client.mget(keys)
        for i, value in enumerate(res):
            if value:
                res[i] = self.encode_str_to_bytes(value)
        return res

    def mget(self, keys):
        return self.redis_client.mget(keys)

    def get_str(self, key):
        value = self.redis_client.get(key)
        return self.decode_bytes_to_str(value)

    def remove(self, key):
        return self.redis_client.delete(key)

    def get_keys_by_prefix(self, prefix: str):
        keys = []
        for key in self.redis_client.scan_iter(match=prefix + "*"):
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

    def add_instance(self, instance_id: str, instance_metadata: "InstanceMetadata"):
        logger.info("Adding instance: %s", instance_id)
        key = LLUMNIX_INSTANCE_METADATA_PREFIX + instance_id
        value = instance_metadata.SerializeToString()
        self.redis_client.set(key, value)

    def update_instance_metadata(
        self, instance_id: str, instance_metadata: "InstanceMetadata"
    ):
        logger.info("Update instance metadata: %s", instance_id)
        key = LLUMNIX_INSTANCE_METADATA_PREFIX + instance_id
        value = instance_metadata.SerializeToString()
        self.redis_client.set(key, value)

    def update_instance_status(self, instance_id, instance_status: "InstanceStatus"):
        logger.debug(
            "Update instance status: %s, update_id: %d",
            instance_id,
            instance_status.update_id,
        )
        key = LLUMNIX_INSTANCE_STATUS_PREFIX + instance_id
        value = instance_status.SerializeToString()
        self.redis_client.set(key, value)

    def remove_instance(self, instance_id):
        logger.info("Removing instance: %s", instance_id)
        # remove metadata
        self.redis_client.remove(LLUMNIX_INSTANCE_METADATA_PREFIX + instance_id)
        # remove status
        self.redis_client.remove(LLUMNIX_INSTANCE_STATUS_PREFIX + instance_id)


class CMSReadClient:
    """
    Provides CMS read operation interfaces:
    - Support instance runtime info (current)
    - Support KV cache meta info (future)
    Uses local cache to reduce Redis calls.
    Starts a separate thread that periodically fetches data from Redis and updates the local cache.
    """

    def __init__(self, redis_client: RedisClient = None):
        self.redis_client = redis_client if redis_client else RedisClient()
        self.instance_ids = []
        self.instance_id_set = set()
        self.instance_metadata_dict = {}
        self.instance_status_dict = {}
        self.rwlock = rwlock.RWLockFair()  # Read-write lock
        self.redis_pull_metadata_interval_ms = int(
            os.getenv(
                "CMS_REDIS_PULL_METADATA_INTERVAL_MS",
                CMS_REDIS_PULL_METADATA_INTERVAL_MS_DEFAULT,
            )
        )
        self.redis_pull_status_interval_ms = int(
            os.getenv(
                "CMS_REDIS_PULL_STATUS_INTERVAL_MS",
                CMS_REDIS_PULL_STATUS_INTERVAL_MS_DEFAULT,
            )
        )
        self.refresh_loop_sleep_time_ms = int(
            os.getenv(
                "CMS_REFRESH_LOOP_SLEEP_TIME_MS",
                CMS_REFRESH_LOOP_SLEEP_TIME_MS_DEFAULT,
            )
        )

        self._refresh_instance_thread = threading.Thread(
            target=self._refresh_instance_loop, daemon=True
        )
        self._refresh_instance_thread.start()

        logger.info("CMSReadClient initialized")

    def _refresh_instance_loop(self):
        instance_status_last_fresh_time = 0
        instance_metadata_last_fresh_time = 0
        while True:
            time_now = time.time()
            if (
                time_now - instance_metadata_last_fresh_time
                > self.redis_pull_metadata_interval_ms / 1000.0
            ):
                self._refresh_metadata()
                instance_metadata_last_fresh_time = time_now

            if (
                time_now - instance_status_last_fresh_time
                > self.redis_pull_status_interval_ms / 1000.0
            ):
                self._refresh_status()
                instance_status_last_fresh_time = time_now

            time.sleep(self.refresh_loop_sleep_time_ms / 1000.0)

    def _refresh_metadata(self):
        try:
            logger.debug("Refreshing data from Redis")

            # get instance ids
            instance_metadata_keys = self.redis_client.get_keys_by_prefix(
                LLUMNIX_INSTANCE_METADATA_PREFIX
            )
            instance_ids_new = [
                instance_id[len(LLUMNIX_INSTANCE_METADATA_PREFIX) :]
                for instance_id in instance_metadata_keys
            ]
            with self.rwlock.gen_wlock():
                self.instance_ids = instance_ids_new
                self.instance_id_set = set(instance_ids_new)

            # update instance metadata
            instance_metadata_bytes_list = self.redis_client.mget(
                instance_metadata_keys
            )
            with self.rwlock.gen_wlock():
                for instance_id in list(self.instance_metadata_dict):
                    if instance_id not in self.instance_id_set:
                        del self.instance_metadata_dict[instance_id]

                for i, instance_metadata_bytes in enumerate(
                    instance_metadata_bytes_list
                ):
                    instance_id = instance_ids_new[i]
                    if not instance_metadata_bytes:
                        self.instance_metadata_dict[instance_id] = None
                        continue
                    if (
                        instance_id not in self.instance_metadata_dict
                        or not self.instance_metadata_dict[instance_id]
                    ):
                        self.instance_metadata_dict[instance_id] = InstanceMetadata()
                    instance_metadata = self.instance_metadata_dict[instance_id]
                    instance_metadata.ParseFromString(instance_metadata_bytes)
        # pylint: disable=broad-except
        except Exception as e:
            logger.error("Error refreshing metadata from Redis: %s", e)

    def _refresh_status(self):
        try:
            # update instance status
            with self.rwlock.gen_wlock():
                instance_status_keys = [
                    LLUMNIX_INSTANCE_STATUS_PREFIX + instance_id
                    for instance_id in self.instance_ids
                ]
                instance_status_bytes_list = self.redis_client.mget(
                    instance_status_keys
                )
                for instance_id in list(self.instance_status_dict):
                    if instance_id not in self.instance_id_set:
                        del self.instance_status_dict[instance_id]

                for i, instance_status_bytes in enumerate(instance_status_bytes_list):
                    instance_id = self.instance_ids[i]
                    if not instance_status_bytes:
                        self.instance_status_dict[instance_id] = None
                        continue
                    if (
                        instance_id not in self.instance_status_dict
                        or not self.instance_status_dict[instance_id]
                    ):
                        self.instance_status_dict[instance_id] = InstanceStatus()
                    instance_status = self.instance_status_dict[instance_id]
                    instance_status.ParseFromString(instance_status_bytes)
        # pylint: disable=broad-except
        except Exception as e:
            logger.error("Error refreshing status from Redis: %s", e)

    def get_instance_ids(self):
        logger.debug("Getting instance IDs")
        with self.rwlock.gen_rlock():
            return self.instance_ids

    def get_instance_metadatas(self):
        logger.debug("Getting instance metadatas")
        with self.rwlock.gen_rlock():
            return self.instance_metadata_dict

    def get_instance_metadata_by_id(self, instance_id):
        logger.debug("Getting instance metadata for ID: %s", instance_id)
        with self.rwlock.gen_rlock():
            return self.instance_metadata_dict.get(instance_id, None)

    def get_instance_status(self):
        logger.debug("Getting instance statuses")
        with self.rwlock.gen_rlock():
            return self.instance_status_dict

    def get_instance_status_by_id(self, instance_id):
        logger.debug("Getting instance status for ID: %s", instance_id)
        with self.rwlock.gen_rlock():
            return self.instance_status_dict.get(instance_id, None)
