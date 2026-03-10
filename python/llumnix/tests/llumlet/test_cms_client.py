import unittest

from llumnix.cms.cms_client import (
    CMSWriteClient,
    CMSReadClient,
    RedisClient,
    LLUMNIX_INSTANCE_METADATA_PREFIX,
    LLUMNIX_INSTANCE_STATUS_PREFIX,
)
from llumnix.instance_info import InstanceMetaData, InstanceStatus
from llumnix.converters import to_cms_metadata, to_cms_status
from tests.mock_redis import MockRedisClient


class TestCmsClient(unittest.TestCase):

    def setUp(self):
        self.redis_client = MockRedisClient()
        self.write_client = CMSWriteClient(redis_client=self.redis_client)
        self.read_client = CMSReadClient(redis_client=self.redis_client)
        self.port = "6379"

    def tearDown(self):
        self.write_client = None
        self.read_client = None

    def test_client_initialization(self):
        self.assertIsNotNone(self.write_client, "Client object should not be None")

    def test_register_instance(self):
        instance_id = "test"
        metadata_llumnix = InstanceMetaData(instance_id=instance_id)
        metadata = to_cms_metadata(metadata_llumnix)
        self.write_client.add_instance(metadata_llumnix.instance_id, metadata)
        assert (
            self.redis_client.get(f"{LLUMNIX_INSTANCE_METADATA_PREFIX}{instance_id}")
            == metadata.SerializeToString()
        )

    def test_update_status(self):
        instance_id = "test"
        status_llumnix = InstanceStatus(step_id=3)
        status = to_cms_status(status_llumnix)
        self.write_client.update_instance_status(instance_id, status)
        assert (
            self.redis_client.get(f"{LLUMNIX_INSTANCE_STATUS_PREFIX}{instance_id}")
            == status.SerializeToString()
        )


if __name__ == "__main__":
    unittest.main()
