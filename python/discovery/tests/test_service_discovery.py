"""Tests for ServiceDiscovery."""
import pytest
from unittest.mock import MagicMock, patch

from discovery.heartbeat import HeartbeatConfig
from discovery.discovery import ServiceDiscovery, REDIS_DISCOVERY_PREFIX
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


def make_mock_backend():
    """Create a mock backend for testing."""
    backend = MagicMock()
    backend.put = MagicMock(return_value=MagicMock())
    backend.get = MagicMock(return_value=MagicMock())
    backend.close = MagicMock(return_value=MagicMock())
    return backend


@patch("discovery.discovery.Heartbeat")
def test_discovery_key_format(mock_heartbeat_cls):
    """Test that discovery_key uses the correct prefix and format."""
    mock_heartbeat_cls.return_value = MagicMock()

    sd = ServiceDiscovery(
        pod_name="test-pod",
        entrypoint_ip="1.2.3.4",
        entrypoint_port=8000,
        kv_transfer_ip="1.2.3.4",
        kv_transfer_port=9000,
        instance_type="decode",
        dp_size_local=1,
        heartbeat_config=make_heartbeat_config(),
        backend=make_mock_backend(),
    )

    assert sd.discovery_key == f"{REDIS_DISCOVERY_PREFIX}test-pod"
    assert sd.discovery_key == "llumnix:discovery:test-pod"


@patch("discovery.discovery.Heartbeat")
def test_instance_infos_dp_size(mock_heartbeat_cls):
    """Test that instance_infos are generated correctly for the given dp_size."""
    mock_heartbeat_cls.return_value = MagicMock()

    dp_size = 3
    sd = ServiceDiscovery(
        pod_name="test-pod",
        entrypoint_ip="1.2.3.4",
        entrypoint_port=8000,
        kv_transfer_ip="1.2.3.4",
        kv_transfer_port=9000,
        instance_type="prefill",
        dp_size_local=dp_size,
        heartbeat_config=make_heartbeat_config(),
        backend=make_mock_backend(),
    )

    assert len(sd.instance_infos) == dp_size
    for i, info in enumerate(sd.instance_infos):
        assert info.dp_rank == i
        assert info.dp_size == dp_size
        assert info.entrypoint_port == 8000 + i


@patch("discovery.discovery.Heartbeat")
def test_instance_info_fields(mock_heartbeat_cls):
    """Test that instance info fields are correctly populated."""
    mock_heartbeat_cls.return_value = MagicMock()

    sd = ServiceDiscovery(
        pod_name="pod-xyz",
        entrypoint_ip="10.0.0.1",
        entrypoint_port=7000,
        kv_transfer_ip="10.0.0.2",
        kv_transfer_port=8000,
        instance_type="decode",
        dp_size_local=1,
        heartbeat_config=make_heartbeat_config(),
        backend=make_mock_backend(),
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


@patch("discovery.discovery.Heartbeat")
def test_custom_ports(mock_heartbeat_cls):
    """Test that explicit ports override the default entrypoint_port + i."""
    mock_heartbeat_cls.return_value = MagicMock()

    custom_ports = [8080, 9090, 7070]
    sd = ServiceDiscovery(
        pod_name="test-pod",
        entrypoint_ip="1.2.3.4",
        entrypoint_port=8000,
        kv_transfer_ip="1.2.3.4",
        kv_transfer_port=9000,
        instance_type="prefill",
        dp_size_local=3,
        heartbeat_config=make_heartbeat_config(),
        backend=make_mock_backend(),
        ports=custom_ports,
    )

    assert len(sd.instance_infos) == 3
    for i, info in enumerate(sd.instance_infos):
        assert info.entrypoint_port == custom_ports[i]
        assert info.dp_rank == i


@patch("discovery.discovery.Heartbeat")
def test_ports_length_mismatch(mock_heartbeat_cls):
    """Test that mismatched ports length raises ValueError."""
    mock_heartbeat_cls.return_value = MagicMock()

    with pytest.raises(ValueError, match="ports length"):
        ServiceDiscovery(
            pod_name="test-pod",
            entrypoint_ip="1.2.3.4",
            entrypoint_port=8000,
            kv_transfer_ip="1.2.3.4",
            kv_transfer_port=9000,
            instance_type="prefill",
            dp_size_local=3,
            heartbeat_config=make_heartbeat_config(),
            backend=make_mock_backend(),
            ports=[8080, 9090],  # only 2 ports for dp_size_local=3
        )
