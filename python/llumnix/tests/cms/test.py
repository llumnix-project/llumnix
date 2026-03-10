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
import time
import os
from redis.exceptions import ConnectionError
import pytest

from llumnix.cms.cms_client import (
    CMSWriteClient,
    CMSReadClient,
    RedisClient,
    LLUMNIX_INSTANCE_METADATA_PREFIX,
    LLUMNIX_INSTANCE_STATUS_PREFIX,
)
import llumnix.cms.proto.cms_pb2 as cms_pb2
from tests.mock_redis import MockRedisClient

WAITING_TIME_S = 1


def clear_test_db(redis_client):
    keys = redis_client.get_keys_by_prefix(LLUMNIX_INSTANCE_METADATA_PREFIX + "test:")
    for key in keys:
        redis_client.remove(key)
    keys = redis_client.get_keys_by_prefix(LLUMNIX_INSTANCE_STATUS_PREFIX + "test:")
    for key in keys:
        redis_client.remove(key)


def get_redis_client():
    try:
        redis_client = RedisClient()
        # clear redis data
        clear_test_db(redis_client)
        print("Redis server successfully connected")
    except ConnectionError as e:
        print(f"Failed to connect to redis server, use mock redis: {e}")
        redis_client = MockRedisClient()
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


@pytest.mark.skipif(
    isinstance(get_redis_client(), MockRedisClient),
    reason="Only run test_redis_client when redis is available",
)
def test_redis_client():
    redis_client = get_redis_client()

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
    redis_client.set(key, value)
    result = redis_client.get_str(key)
    assert result == value

    # Test get_str with non-existent key
    result = redis_client.get_str("non_existent_key")
    assert result is None

    # Test remove_str
    deleted_count = redis_client.remove(key)
    assert deleted_count == 1

    # Verify the key was removed
    result = redis_client.get_str(key)
    assert result is None

    # Try to remove non-existent key
    deleted_count = redis_client.remove("non_existent_key")
    assert deleted_count == 0

    # Test get_keys_by_prefix
    prefix = "test_prefix:"
    keys = [f"{prefix}key1", f"{prefix}key2", "other_key"]
    for k in keys:
        redis_client.redis_client.set(k, "value")

    # Verify we can get keys by prefix
    result = redis_client.get_keys_by_prefix(prefix)
    assert len(result) == 2
    assert f"{prefix}key1" in result
    assert f"{prefix}key2" in result
    assert "other_key" not in result

    # Test get_keys_by_prefix with no matching keys
    result = redis_client.get_keys_by_prefix("non_existent_prefix:")
    assert len(result) == 0


def test_cms_write_client():
    redis_client = get_redis_client()
    cms_write_client = CMSWriteClient(redis_client=redis_client)
    instance_id1 = gen_instance_id(1)
    instance_id2 = gen_instance_id(2)

    # test add instance
    instance_metadata1 = gen_instance_metadata(instance_id1)
    cms_write_client.add_instance(instance_id1, instance_metadata1)
    assert (
        redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id1}")
        == instance_metadata1.SerializeToString()
    )

    instance_metadata2 = gen_instance_metadata(instance_id2, instance_type="prefill")
    cms_write_client.add_instance(instance_id2, instance_metadata2)
    assert (
        redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id1}")
        == instance_metadata1.SerializeToString()
    )
    assert (
        redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id2}")
        == instance_metadata2.SerializeToString()
    )

    # test update instance metadata
    new_instance_metadata1 = gen_instance_metadata(instance_id1, instance_type="decode")
    cms_write_client.update_instance_metadata(instance_id1, new_instance_metadata1)
    assert (
        redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id1}")
        == new_instance_metadata1.SerializeToString()
    )
    instance_metadata1 = new_instance_metadata1

    # test update instance status
    instance_status1 = gen_instance_status(instance_id1)
    cms_write_client.update_instance_status(instance_id1, instance_status1)
    assert (
        redis_client.get(f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id1}")
        == instance_status1.SerializeToString()
    )

    instance_status2 = gen_instance_status(instance_id2)
    cms_write_client.update_instance_status(instance_id2, instance_status2)
    assert (
        redis_client.get(f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id1}")
        == instance_status1.SerializeToString()
    )
    assert (
        redis_client.get(f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id2}")
        == instance_status2.SerializeToString()
    )

    # test remove instance
    cms_write_client.remove_instance(instance_id1)
    assert redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id1}") is None
    assert (
        redis_client.get(f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id2}")
        == instance_status2.SerializeToString()
    )
    cms_write_client.remove_instance(instance_id2)
    assert redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id1}") is None
    assert redis_client.get(f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id2}") is None


def test_cms_read_client():
    os.environ["CMS_REDIS_PULL_STATUS_INTERVAL_MS"] = "100"
    os.environ["CMS_REDIS_PULL_METADATA_INTERVAL_MS"] = "100"
    redis_client = get_redis_client()
    cms_write_client = CMSWriteClient(redis_client=redis_client)
    cms_read_client = CMSReadClient(redis_client=redis_client)

    instance_id1 = gen_instance_id(1)
    instance_metadata1 = gen_instance_metadata(instance_id1)
    instance_id2 = gen_instance_id(2)
    instance_metadata2 = gen_instance_metadata(instance_id2, instance_type="prefill")

    # test add instance
    cms_write_client.add_instance(instance_id1, instance_metadata1)
    time.sleep(WAITING_TIME_S)
    assert cms_read_client.get_instance_ids() == [instance_id1]
    assert cms_read_client.get_instance_metadatas() == {
        instance_id1: instance_metadata1
    }
    assert (
        cms_read_client.get_instance_metadata_by_id(instance_id1) == instance_metadata1
    )
    instance_status = cms_read_client.get_instance_status()
    assert len(instance_status) == 1 and instance_status[instance_id1] is None

    # test update instance metadata
    new_instance_metadata1 = gen_instance_metadata(instance_id1, instance_type="decode")
    cms_write_client.update_instance_metadata(instance_id1, new_instance_metadata1)
    time.sleep(WAITING_TIME_S)
    assert cms_read_client.get_instance_ids() == [instance_id1]
    assert cms_read_client.get_instance_metadatas() == {
        instance_id1: new_instance_metadata1
    }
    assert (
        cms_read_client.get_instance_metadata_by_id(instance_id1)
        == new_instance_metadata1
    )
    instance_status = cms_read_client.get_instance_status()
    assert len(instance_status) == 1 and instance_status[instance_id1] is None
    instance_metadata1 = new_instance_metadata1

    # test update instance status
    instance_status1 = gen_instance_status(instance_id1)
    cms_write_client.update_instance_status(instance_id1, instance_status1)
    time.sleep(WAITING_TIME_S)
    assert cms_read_client.get_instance_ids() == [instance_id1]
    assert cms_read_client.get_instance_metadatas() == {
        instance_id1: new_instance_metadata1
    }
    assert (
        cms_read_client.get_instance_metadata_by_id(instance_id1)
        == new_instance_metadata1
    )
    assert cms_read_client.get_instance_status() == {instance_id1: instance_status1}
    assert cms_read_client.get_instance_status_by_id(instance_id1) == instance_status1

    # test add instance2
    cms_write_client.add_instance(instance_id2, instance_metadata2)
    instance_status2 = gen_instance_status(instance_id2)
    cms_write_client.update_instance_status(instance_id2, instance_status2)
    time.sleep(WAITING_TIME_S)
    assert len(cms_read_client.get_instance_ids()) == 2
    assert (
        instance_id1 in cms_read_client.get_instance_ids()
        and instance_id2 in cms_read_client.get_instance_ids()
    )
    assert cms_read_client.get_instance_metadatas() == {
        instance_id1: instance_metadata1,
        instance_id2: instance_metadata2,
    }
    assert (
        cms_read_client.get_instance_metadata_by_id(instance_id1) == instance_metadata1
        and cms_read_client.get_instance_metadata_by_id(instance_id2)
        == instance_metadata2
    )
    assert cms_read_client.get_instance_status() == {
        instance_id1: instance_status1,
        instance_id2: instance_status2,
    }
    assert (
        cms_read_client.get_instance_status_by_id(instance_id1) == instance_status1
        and cms_read_client.get_instance_status_by_id(instance_id2) == instance_status2
    )

    # test remove instance
    cms_write_client.remove_instance(instance_id1)
    time.sleep(WAITING_TIME_S)
    assert cms_read_client.get_instance_ids() == [instance_id2]
    assert cms_read_client.get_instance_metadatas() == {
        instance_id2: instance_metadata2
    }
    assert cms_read_client.get_instance_metadata_by_id(instance_id1) is None
    assert cms_read_client.get_instance_status() == {instance_id2: instance_status2}
    assert cms_read_client.get_instance_status_by_id(instance_id1) is None


def test_cms_read_client_refresh_loop_performance():
    redis_client = get_redis_client()

    # Skip test if using mock Redis client
    if isinstance(redis_client, MockRedisClient):
        pytest.skip("Skipping performance test with mock Redis client")

    # Number of instances to test
    num_instances = 1000

    # Clean up any existing test data
    clear_test_db(redis_client)

    # Generate test data: instance IDs, metadata and status
    instance_ids = []
    instance_metadata_map = {}
    instance_status_map = {}

    # Make len of instance_type very large, to make sure data of metadata and status not too short
    instance_type = "prefill" * 100

    # Prepare test data
    for i in range(num_instances):
        instance_id = gen_instance_id(i)
        instance_ids.append(instance_id)
        metadata_key = f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id}"
        status_key = f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id}"

        # Create metadata
        metadata = gen_instance_metadata(instance_id, instance_type)
        instance_metadata_map[instance_id] = metadata

        # Serialize metadata to bytes
        metadata_bytes = metadata.SerializeToString()

        # Create status
        status = gen_instance_status(instance_id)
        instance_status_map[instance_id] = status

        # Serialize status to bytes
        status_bytes = status.SerializeToString()

        # Store in Redis
        redis_client.set(metadata_key, metadata_bytes)
        redis_client.set(status_key, status_bytes)

    # Create a CMSReadClient to test refresh_loop
    # Disable automatic refresh by setting a very large interval
    os.environ["CMS_REDIS_PULL_STATUS_INTERVAL_MS"] = "10000000"
    os.environ["CMS_REDIS_PULL_METADATA_INTERVAL_MS"] = "10000000"

    cms_read_client: CMSReadClient = CMSReadClient(redis_client=redis_client)

    # Force a refresh data call to measure performance
    start_time = time.time()
    cms_read_client._refresh_metadata()  # Run one iteration of refresh loop
    elapsed_time = time.time() - start_time
    print(
        f"Time to refresh {num_instances} instances metadata: {elapsed_time:.4f} seconds"
    )

    start_time = time.time()
    cms_read_client._refresh_status()  # Run one iteration of refresh loop
    elapsed_time = time.time() - start_time
    print(
        f"Time to refresh {num_instances} instances status: {elapsed_time:.4f} seconds"
    )
