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
import asyncio
import time

from redis.exceptions import ConnectionError
import pytest

from llumnix.cms.cms_client_async import (
    CMSWriteClient,
    RedisClient,
    LLUMNIX_INSTANCE_METADATA_PREFIX,
    LLUMNIX_INSTANCE_STATUS_PREFIX,
)
from tests.mock_redis import MockAsyncRedisClient
import llumnix.cms.proto.cms_pb2 as cms_pb2

WAITING_TIME_S = 1


async def clear_test_db(redis_client):
    keys = await redis_client.get_keys_by_prefix(
        LLUMNIX_INSTANCE_METADATA_PREFIX + "test:"
    )
    for key in keys:
        await redis_client.remove(key)
    keys = await redis_client.get_keys_by_prefix(
        LLUMNIX_INSTANCE_STATUS_PREFIX + "test:"
    )
    for key in keys:
        await redis_client.remove(key)


async def get_redis_client():
    try:
        redis_client = RedisClient()
        # clear redis data
        await clear_test_db(redis_client)
        print("Redis server successfully connected")
    except ConnectionError as e:
        print(f"Failed to connect to redis server, use mock redis: {e}")
        redis_client = MockAsyncRedisClient()
    return redis_client


def gen_instance_id(index: int):
    return f"test:instance_id{index}"


def gen_instance_metadata(instance_id: str, instance_type: str = "neutral"):
    return cms_pb2.InstanceMetadata(
        instance_id=instance_id,
        instance_type=instance_type,
        utc_create=time.time(),
    )


def gen_instance_status(instance_id: str):
    return cms_pb2.InstanceStatus(
        instance_id=instance_id,
        timestamp_ms=int(time.time() * 1000),
    )


async def test_redis_client():
    redis_client = await get_redis_client()

    if isinstance(redis_client, MockAsyncRedisClient):
        pytest.skip("Only run test_redis_client when redis is available")

    # Test encode_key with string
    key_str = "test_key"
    encoded = redis_client.encode_str_to_bytes(key_str)
    assert encoded == b"test_key"

    # Test encode_key with bytes
    key_bytes = b"test_key"
    encoded = redis_client.encode_str_to_bytes(key_bytes)
    assert encoded == b"test_key"

    # Test decode_key with bytes
    key_bytes = b"test_key"
    decoded = redis_client.decode_bytes_to_str(key_bytes)
    assert decoded == "test_key"

    # Test decode_key with string
    key_str = "test_key"
    decoded = redis_client.decode_bytes_to_str(key_str)
    assert decoded == "test_key"

    # Test set_str and get_str
    key = "test_key"
    value = "test_value"
    await redis_client.set(key, value)
    result = await redis_client.get_str(key)
    assert result == value

    # Test get_str with non-existent key
    result = await redis_client.get_str("non_existent_key")
    assert result is None

    # Test remove_str
    deleted_count = await redis_client.remove(key)
    assert deleted_count == 1

    # Verify the key was removed
    result = await redis_client.get_str(key)
    assert result is None

    # Try to remove non-existent key
    deleted_count = await redis_client.remove("non_existent_key")
    assert deleted_count == 0

    # Test get_keys_by_prefix
    prefix = "test_prefix:"
    keys = [f"{prefix}key1", f"{prefix}key2", "other_key"]
    for k in keys:
        await redis_client.redis_client.set(k, "value")

    # Verify we can get keys by prefix
    result = await redis_client.get_keys_by_prefix(prefix)
    assert len(result) == 2
    assert f"{prefix}key1" in result
    assert f"{prefix}key2" in result
    assert "other_key" not in result

    # Test get_keys_by_prefix with no matching keys
    result = await redis_client.get_keys_by_prefix("non_existent_prefix:")
    assert len(result) == 0


async def test_cms_write_client():
    redis_client = await get_redis_client()
    cms_write_client = CMSWriteClient(redis_client=redis_client)
    instance_id1 = gen_instance_id(1)
    instance_id2 = gen_instance_id(2)

    # test add instance
    instance_metadata1 = gen_instance_metadata(instance_id1)
    await cms_write_client.add_instance(instance_id1, instance_metadata1)
    assert (
        await redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id1}")
        == instance_metadata1.SerializeToString()
    )

    instance_metadata2 = gen_instance_metadata(instance_id2, instance_type="prefill")
    await cms_write_client.add_instance(instance_id2, instance_metadata2)
    assert (
        await redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id1}")
        == instance_metadata1.SerializeToString()
    )
    assert (
        await redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id2}")
        == instance_metadata2.SerializeToString()
    )

    # test update instance metadata
    new_instance_metadata1 = gen_instance_metadata(instance_id1, instance_type="decode")
    await cms_write_client.update_instance_metadata(
        instance_id1, new_instance_metadata1
    )
    assert (
        await redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id1}")
        == new_instance_metadata1.SerializeToString()
    )
    instance_metadata1 = new_instance_metadata1

    # test update instance status
    instance_status1 = gen_instance_status(instance_id1)
    await cms_write_client.update_instance_status(instance_id1, instance_status1)
    assert (
        await redis_client.get(f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id1}")
        == instance_status1.SerializeToString()
    )

    instance_status2 = gen_instance_status(instance_id2)
    await cms_write_client.update_instance_status(instance_id2, instance_status2)
    assert (
        await redis_client.get(f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id1}")
        == instance_status1.SerializeToString()
    )
    assert (
        await redis_client.get(f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id2}")
        == instance_status2.SerializeToString()
    )

    # test remove instance
    await cms_write_client.remove_instance(instance_id1)
    assert (
        await redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id1}")
        is None
    )
    assert (
        await redis_client.get(f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id2}")
        == instance_status2.SerializeToString()
    )
    await cms_write_client.remove_instance(instance_id2)
    assert (
        await redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id1}")
        is None
    )
    assert (
        await redis_client.get(f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id2}")
        is None
    )


async def test_cms_write_client_timeout():
    redis_client = await get_redis_client()
    # Skip this test if we are using the mock client, which may not support expiration.
    if isinstance(redis_client, MockAsyncRedisClient):
        pytest.skip("Timeout test requires a real Redis server to test TTL.")
    expired_time_s = 2
    wait_time_s = expired_time_s + 1
    cms_write_client = CMSWriteClient(redis_client=redis_client)
    instance_id = gen_instance_id(99)
    metadata_key = f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id}"
    status_key = f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id}"
    instance_metadata = gen_instance_metadata(instance_id)
    await cms_write_client.add_instance(instance_id, instance_metadata, expired_time_s)
    instance_status = gen_instance_status(instance_id)
    await cms_write_client.update_instance_status(
        instance_id, instance_status, expired_time_s
    )
    assert await redis_client.get(metadata_key) is not None
    assert await redis_client.get(status_key) is not None
    await asyncio.sleep(wait_time_s)
    assert (
        await redis_client.get(metadata_key) is None
    ), "Metadata key should have expired"
    assert await redis_client.get(status_key) is None, "Status key should have expired"
    await cms_write_client.add_instance(instance_id, instance_metadata, expired=30)
    new_instance_metadata = gen_instance_metadata(instance_id, instance_type="decode")
    await cms_write_client.update_instance_metadata(
        instance_id, new_instance_metadata, expired_time_s
    )
    assert await redis_client.get(metadata_key) is not None
    await asyncio.sleep(wait_time_s)
    assert (
        await redis_client.get(metadata_key) is None
    ), "Updated metadata key should have expired"
