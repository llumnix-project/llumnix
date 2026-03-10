from unittest.mock import MagicMock, patch

from discovery.heartbeat import HeartbeatConfig
from discovery.discovery import ServiceDiscovery, LLUMNIX_DISCOVERY_PREFIX
from discovery.proto.redis_discovery_pb2 import InstanceDiscoveryInfo


def make_heartbeat_config():
    return HeartbeatConfig(
        interval=5,
        timeout=3,
        retry_times=3,
        retry_interval=1,
        health_path="/health",
        expected_status=200,
        max_consecutive_failures=3,
    )


@patch("discovery.discovery.redis.Redis")
@patch("discovery.discovery.Heartbeat")
def test_redis_key_format(mock_heartbeat_cls, mock_redis_cls):
    mock_heartbeat = MagicMock()
    mock_heartbeat_cls.return_value = mock_heartbeat

    sd = ServiceDiscovery(
        pod_name="test-pod",
        entrypoint_ip="1.2.3.4",
        entrypoint_port=8000,
        kv_transfer_ip="1.2.3.4",
        kv_transfer_port=9000,
        instance_type="decode",
        dp_size_local=1,
        redis_address="localhost",
        redis_port=6379,
        heartbeat_config=make_heartbeat_config(),
    )

    assert sd.redis_key == f"{LLUMNIX_DISCOVERY_PREFIX}test-pod"


@patch("discovery.discovery.redis.Redis")
@patch("discovery.discovery.Heartbeat")
def test_instance_infos_dp_size(mock_heartbeat_cls, mock_redis_cls):
    mock_heartbeat = MagicMock()
    mock_heartbeat_cls.return_value = mock_heartbeat

    dp_size = 3
    sd = ServiceDiscovery(
        pod_name="test-pod",
        entrypoint_ip="1.2.3.4",
        entrypoint_port=8000,
        kv_transfer_ip="1.2.3.4",
        kv_transfer_port=9000,
        instance_type="prefill",
        dp_size_local=dp_size,
        redis_address="localhost",
        redis_port=6379,
        heartbeat_config=make_heartbeat_config(),
    )

    assert len(sd.instance_infos) == dp_size
    for i, info in enumerate(sd.instance_infos):
        assert info.dp_rank == i
        assert info.dp_size == dp_size
        assert info.entrypoint_port == 8000 + i


@patch("discovery.discovery.redis.Redis")
@patch("discovery.discovery.Heartbeat")
def test_instance_info_fields(mock_heartbeat_cls, mock_redis_cls):
    mock_heartbeat = MagicMock()
    mock_heartbeat_cls.return_value = mock_heartbeat

    sd = ServiceDiscovery(
        pod_name="pod-xyz",
        entrypoint_ip="10.0.0.1",
        entrypoint_port=7000,
        kv_transfer_ip="10.0.0.2",
        kv_transfer_port=8000,
        instance_type="decode",
        dp_size_local=1,
        redis_address="localhost",
        redis_port=6379,
        heartbeat_config=make_heartbeat_config(),
        model="llama3",
        version=2,
    )

    info: InstanceDiscoveryInfo = sd.instance_infos[0]
    assert info.instance_type == "decode"
    assert info.entrypoint_ip == "10.0.0.1"
    assert info.kv_transfer_ip == "10.0.0.2"
    assert info.kv_transfer_port == 8000
    assert info.model == "llama3"
    assert info.version == 2
