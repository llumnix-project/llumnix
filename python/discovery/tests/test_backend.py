"""Tests for discovery backends."""
import asyncio
import base64
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from discovery.discovery import (
    EtcdDiscoveryBackend,
    RedisDiscoveryBackend,
    DiscoveryBackend,
    build_backend,
)


class TestDiscoveryBackendInterface:
    """Test that both backends implement the interface correctly."""

    def test_redis_backend_is_discovery_backend(self):
        backend = RedisDiscoveryBackend("localhost", 6379)
        assert isinstance(backend, DiscoveryBackend)

    def test_etcd_backend_is_discovery_backend(self):
        backend = EtcdDiscoveryBackend(["localhost:2379"], lease_ttl=60)
        assert isinstance(backend, DiscoveryBackend)


class TestRedisDiscoveryBackend:
    """Tests for RedisDiscoveryBackend."""

    @pytest.mark.asyncio
    async def test_put_and_get(self):
        backend = RedisDiscoveryBackend("localhost", 6379)
        with patch.object(backend, "_client") as mock_client:
            mock_client.set = MagicMock(return_value=asyncio.Future())
            mock_client.set.return_value.set_result(None)
            mock_client.get = MagicMock(return_value=asyncio.Future())
            mock_client.get.return_value.set_result(b"test_value")

            await backend.put("test_key", b"test_value")
            mock_client.set.assert_called_once_with("test_key", b"test_value")

            result = await backend.get("test_key")
            assert result == b"test_value"

    @pytest.mark.asyncio
    async def test_delete(self):
        backend = RedisDiscoveryBackend("localhost", 6379)
        with patch.object(backend, "_client") as mock_client:
            mock_client.delete = MagicMock(return_value=asyncio.Future())
            mock_client.delete.return_value.set_result(None)

            await backend.delete("test_key")
            mock_client.delete.assert_called_once_with("test_key")

    @pytest.mark.asyncio
    async def test_close(self):
        backend = RedisDiscoveryBackend("localhost", 6379)
        with patch.object(backend, "_client") as mock_client:
            mock_client.aclose = MagicMock(return_value=asyncio.Future())
            mock_client.aclose.return_value.set_result(None)

            await backend.close()
            mock_client.aclose.assert_called_once()


def _make_etcd_backend(endpoints=None, lease_ttl=60, **kwargs):
    """Create an EtcdDiscoveryBackend and inject a mock session."""
    backend = EtcdDiscoveryBackend(
        endpoints or ["etcd:2379"], lease_ttl=lease_ttl, **kwargs
    )
    return backend


async def _start_with_mock_session(backend):
    """Simulate start() by injecting a mock aiohttp session."""
    mock_session = MagicMock()
    mock_response = AsyncMock()
    mock_response.raise_for_status = MagicMock()
    mock_response.json = AsyncMock(return_value={})
    mock_response.__aenter__ = AsyncMock(return_value=mock_response)
    mock_response.__aexit__ = AsyncMock(return_value=False)
    mock_session.post = MagicMock(return_value=mock_response)
    mock_session.close = AsyncMock()

    backend._session = mock_session
    backend._active_endpoint = backend._endpoints[0]
    backend._keepalive_task = asyncio.create_task(asyncio.sleep(999))
    return mock_session


class TestEtcdDiscoveryBackend:
    """Tests for EtcdDiscoveryBackend (per-key lease via HTTP API)."""

    def test_init_with_auth(self):
        backend = EtcdDiscoveryBackend(
            ["etcd:2379"],
            lease_ttl=60,
            username="user",
            password="pass",
        )
        assert backend._lease_ttl == 60
        assert backend._username == "user"
        assert backend._password == "pass"
        assert backend._endpoints == ["http://etcd:2379"]
        assert backend._leases == {}

    def test_normalize_endpoint_adds_scheme(self):
        backend = EtcdDiscoveryBackend(["etcd:2379"])
        assert backend._endpoints == ["http://etcd:2379"]

    def test_normalize_endpoint_preserves_scheme(self):
        backend = EtcdDiscoveryBackend(["http://etcd:2379"])
        assert backend._endpoints == ["http://etcd:2379"]

    def test_parse_endpoint_with_port(self):
        host, port = EtcdDiscoveryBackend._parse_endpoint("etcd:2379")
        assert host == "etcd"
        assert port == 2379

    def test_parse_endpoint_with_http_prefix(self):
        host, port = EtcdDiscoveryBackend._parse_endpoint("http://etcd:2379")
        assert host == "etcd"
        assert port == 2379

    def test_parse_endpoint_without_port(self):
        host, port = EtcdDiscoveryBackend._parse_endpoint("etcd")
        assert host == "etcd"
        assert port == 2379

    @pytest.mark.asyncio
    async def test_put_requires_start(self):
        backend = _make_etcd_backend()
        with pytest.raises(RuntimeError, match="start\\(\\) must be called"):
            await backend.put("key", b"value")

    @pytest.mark.asyncio
    async def test_put_creates_per_key_lease(self):
        """First put() for a key grants a new lease for that key."""
        backend = _make_etcd_backend(lease_ttl=60)
        mock_session = await _start_with_mock_session(backend)

        lease_grant_response = AsyncMock()
        lease_grant_response.raise_for_status = MagicMock()
        lease_grant_response.json = AsyncMock(return_value={"ID": 12345})
        lease_grant_response.__aenter__ = AsyncMock(return_value=lease_grant_response)
        lease_grant_response.__aexit__ = AsyncMock(return_value=False)

        put_response = AsyncMock()
        put_response.raise_for_status = MagicMock()
        put_response.json = AsyncMock(return_value={})
        put_response.__aenter__ = AsyncMock(return_value=put_response)
        put_response.__aexit__ = AsyncMock(return_value=False)

        mock_session.post = MagicMock(side_effect=[lease_grant_response, put_response])

        await backend.put("key_a", b"value_a")

        assert "key_a" in backend._leases
        assert backend._leases["key_a"] == 12345
        assert mock_session.post.call_count == 2

        backend._keepalive_task.cancel()

    @pytest.mark.asyncio
    async def test_put_reuses_existing_lease(self):
        """Second put() for the same key reuses the existing lease."""
        backend = _make_etcd_backend(lease_ttl=60)
        mock_session = await _start_with_mock_session(backend)
        backend._leases["key_a"] = 12345

        put_response = AsyncMock()
        put_response.raise_for_status = MagicMock()
        put_response.json = AsyncMock(return_value={})
        put_response.__aenter__ = AsyncMock(return_value=put_response)
        put_response.__aexit__ = AsyncMock(return_value=False)
        mock_session.post = MagicMock(return_value=put_response)

        await backend.put("key_a", b"value_2")

        # Only one call (put), no lease/grant call
        mock_session.post.assert_called_once()
        call_url = mock_session.post.call_args[0][0]
        assert "/v3/kv/put" in call_url

        backend._keepalive_task.cancel()

    @pytest.mark.asyncio
    async def test_get_returns_value(self):
        backend = _make_etcd_backend()
        mock_session = await _start_with_mock_session(backend)

        encoded_value = base64.b64encode(b"test_value").decode()
        range_response = AsyncMock()
        range_response.raise_for_status = MagicMock()
        range_response.json = AsyncMock(
            return_value={"kvs": [{"value": encoded_value}]}
        )
        range_response.__aenter__ = AsyncMock(return_value=range_response)
        range_response.__aexit__ = AsyncMock(return_value=False)
        mock_session.post = MagicMock(return_value=range_response)

        result = await backend.get("test_key")
        assert result == b"test_value"

        backend._keepalive_task.cancel()

    @pytest.mark.asyncio
    async def test_get_returns_none_when_key_missing(self):
        backend = _make_etcd_backend()
        mock_session = await _start_with_mock_session(backend)

        range_response = AsyncMock()
        range_response.raise_for_status = MagicMock()
        range_response.json = AsyncMock(return_value={})
        range_response.__aenter__ = AsyncMock(return_value=range_response)
        range_response.__aexit__ = AsyncMock(return_value=False)
        mock_session.post = MagicMock(return_value=range_response)

        result = await backend.get("missing_key")
        assert result is None

        backend._keepalive_task.cancel()

    @pytest.mark.asyncio
    async def test_close_revokes_all_leases(self):
        """close() revokes every per-key lease and closes the session."""
        backend = _make_etcd_backend(lease_ttl=60)
        mock_session = await _start_with_mock_session(backend)
        backend._leases = {"key_a": 111, "key_b": 222}

        revoke_response = AsyncMock()
        revoke_response.raise_for_status = MagicMock()
        revoke_response.json = AsyncMock(return_value={})
        revoke_response.__aenter__ = AsyncMock(return_value=revoke_response)
        revoke_response.__aexit__ = AsyncMock(return_value=False)
        mock_session.post = MagicMock(return_value=revoke_response)

        await backend.close()

        assert backend._leases == {}
        mock_session.close.assert_called_once()

    @pytest.mark.asyncio
    async def test_recover_leases(self):
        """_recover_leases grants new leases for all tracked keys."""
        backend = _make_etcd_backend(lease_ttl=60)
        mock_session = await _start_with_mock_session(backend)
        backend._leases = {"key_a": 111, "key_b": 222}

        new_lease_ids = iter([333, 444])

        async def mock_json():
            return {"ID": next(new_lease_ids)}

        grant_response = AsyncMock()
        grant_response.raise_for_status = MagicMock()
        grant_response.json = mock_json
        grant_response.__aenter__ = AsyncMock(return_value=grant_response)
        grant_response.__aexit__ = AsyncMock(return_value=False)
        mock_session.post = MagicMock(return_value=grant_response)

        backend._consecutive_keepalive_failures = (
            EtcdDiscoveryBackend._MAX_KEEPALIVE_FAILURES
        )
        await backend._recover_leases()

        assert backend._consecutive_keepalive_failures == 0
        assert len(backend._leases) == 2

        backend._keepalive_task.cancel()


class TestBuildBackend:
    """Tests for the build_backend factory function."""

    def test_build_redis_backend(self):
        args = MagicMock()
        args.backend = "redis"
        args.redis_address = "redis"
        args.redis_port = 6379
        args.redis_username = ""
        args.redis_password = ""

        backend = build_backend(args)
        assert isinstance(backend, RedisDiscoveryBackend)

    def test_build_etcd_backend(self):
        args = MagicMock()
        args.backend = "etcd"
        args.etcd_address = "etcd"
        args.etcd_port = 2379
        args.etcd_username = ""
        args.etcd_password = ""
        args.etcd_lease_ttl = 60

        backend = build_backend(args)
        assert isinstance(backend, EtcdDiscoveryBackend)

    def test_build_etcd_backend_multi_address(self):
        args = MagicMock()
        args.backend = "etcd"
        args.etcd_address = "etcd-0.etcd,etcd-1.etcd,etcd-2.etcd"
        args.etcd_port = 2379
        args.etcd_username = ""
        args.etcd_password = ""
        args.etcd_lease_ttl = 60

        backend = build_backend(args)
        assert isinstance(backend, EtcdDiscoveryBackend)
        assert len(backend._endpoints) == 3