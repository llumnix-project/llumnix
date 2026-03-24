# pylint: disable=broad-exception-caught
import abc
import argparse
import asyncio
import logging
import time
from typing import Optional

import redis.asyncio as redis

from .heartbeat import (
    Heartbeat,
    HeartbeatConfig,
    HealthStatus,
    add_heartbeat_config_arguments,
)
from .proto.redis_discovery_pb2 import InstanceDiscoveryInfo, PodDiscoveryInfo

logger = logging.getLogger(__name__)

REDIS_DISCOVERY_PREFIX = "llumnix:discovery:"
ETCD_DISCOVERY_PREFIX = "llumnix/discovery/"


class DiscoveryBackend(abc.ABC):
    """Abstract base class for service discovery storage backends."""

    @abc.abstractmethod
    async def put(self, key: str, value: bytes) -> None:
        """Store a key-value pair. Implementations must handle their own TTL/lease logic."""

    @abc.abstractmethod
    async def delete(self, key: str) -> None:
        """Delete a key."""

    @abc.abstractmethod
    async def get(self, key: str) -> Optional[bytes]:
        """Retrieve the value for a key, or None if not found."""

    @abc.abstractmethod
    async def close(self) -> None:
        """Release backend resources."""


class RedisDiscoveryBackend(DiscoveryBackend):
    """Redis-backed discovery storage. Writes without TTL; the Go resolver
    expires entries based on the timestamp_ms field embedded in the protobuf."""

    def __init__(
        self,
        address: str,
        port: int,
        username: Optional[str] = None,
        password: Optional[str] = None,
    ) -> None:
        redis_params = {"host": address, "port": port}
        if username:
            redis_params["username"] = username
        if password:
            redis_params["password"] = password
        self._client = redis.Redis(**redis_params)

    async def put(self, key: str, value: bytes) -> None:
        await self._client.set(key, value)

    async def delete(self, key: str) -> None:
        await self._client.delete(key)

    async def get(self, key: str) -> Optional[bytes]:
        return await self._client.get(key)

    async def close(self) -> None:
        await self._client.aclose()


class EtcdDiscoveryBackend(DiscoveryBackend):
    """etcd-backed discovery storage using the etcd v3 HTTP/JSON API.

    Each key written via ``put()`` gets its own etcd lease, so individual
    instances expire independently when their heartbeat stops.  A background
    keepalive loop refreshes all active leases every ``lease_ttl / 3``
    seconds.

    Implementation note: this backend calls the etcd v3 gRPC-gateway HTTP
    endpoints directly via ``aiohttp`` instead of using a Python etcd gRPC
    client library.  The only maintained Python etcd client (``etcd3``) pins
    ``protobuf<4``, which conflicts with ``grpcio-tools`` (requires
    ``protobuf>=5`` on Python 3.12+) used to compile our proto files.
    Since the discovery use-case only needs put/get/delete/lease operations,
    the HTTP/JSON API is a simpler and dependency-free alternative that also
    provides native async support without ``run_in_executor``.
    """

    _MAX_CONNECT_RETRIES = 5
    _CONNECT_RETRY_INTERVAL = 3  # seconds
    _MAX_KEEPALIVE_FAILURES = 3

    def __init__(
        self,
        endpoints: list[str],
        lease_ttl: int = 60,
        username: Optional[str] = None,
        password: Optional[str] = None,
    ) -> None:
        import aiohttp  # pylint: disable=import-outside-toplevel
        self._aiohttp = aiohttp

        self._lease_ttl = lease_ttl
        self._keepalive_interval = max(1, lease_ttl // 3)
        self._endpoints = [self._normalize_endpoint(ep) for ep in endpoints]
        self._username = username
        self._password = password

        self._session: Optional[aiohttp.ClientSession] = None
        self._active_endpoint: Optional[str] = None
        # key → lease_id (int)
        self._leases: dict[str, int] = {}
        self._keepalive_task: Optional[asyncio.Task] = None
        self._consecutive_keepalive_failures = 0

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    @staticmethod
    def _normalize_endpoint(endpoint: str) -> str:
        """Ensure the endpoint has an http:// scheme."""
        endpoint = endpoint.strip()
        if not endpoint.startswith("http://") and not endpoint.startswith("https://"):
            endpoint = f"http://{endpoint}"
        return endpoint.rstrip("/")

    @staticmethod
    def _parse_endpoint(endpoint: str) -> tuple[str, int]:
        endpoint = endpoint.removeprefix("http://").removeprefix("https://")
        if ":" in endpoint:
            host, port_str = endpoint.rsplit(":", 1)
            return host, int(port_str)
        return endpoint, 2379

    @staticmethod
    def _to_base64(data: bytes | str) -> str:
        """Encode bytes or str to base64 string for etcd HTTP API."""
        import base64  # pylint: disable=import-outside-toplevel
        if isinstance(data, str):
            data = data.encode()
        return base64.b64encode(data).decode()

    @staticmethod
    def _from_base64(data: str) -> bytes:
        """Decode base64 string from etcd HTTP API response."""
        import base64  # pylint: disable=import-outside-toplevel
        return base64.b64decode(data)

    async def _post(self, path: str, payload: dict) -> dict:
        """POST to the active etcd endpoint with automatic failover.

        Tries the current active endpoint first.  On connection failure,
        iterates through all configured endpoints and switches to the first
        one that responds.  Raises the last exception if all endpoints fail.
        """
        # Try the active endpoint first
        try:
            url = f"{self._active_endpoint}{path}"
            async with self._session.post(url, json=payload) as response:
                response.raise_for_status()
                return await response.json()
        except self._aiohttp.ClientConnectionError:
            logger.warning(
                "etcd endpoint %s unreachable, attempting failover",
                self._active_endpoint,
            )

        # Failover: try all other endpoints
        last_error: Optional[Exception] = None
        for endpoint in self._endpoints:
            if endpoint == self._active_endpoint:
                continue
            try:
                url = f"{endpoint}{path}"
                async with self._session.post(url, json=payload) as response:
                    response.raise_for_status()
                    self._active_endpoint = endpoint
                    logger.info("etcd failover: switched to %s", endpoint)
                    return await response.json()
            except Exception as exc:
                last_error = exc

        # All endpoints failed — raise so callers can handle it
        raise last_error or RuntimeError("All etcd endpoints unreachable")

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def start(self) -> None:
        """Create HTTP session and verify connectivity with retry."""
        self._session = self._aiohttp.ClientSession()
        last_error: Optional[Exception] = None

        for attempt in range(1, self._MAX_CONNECT_RETRIES + 1):
            for endpoint in self._endpoints:
                try:
                    url = f"{endpoint}/v3/maintenance/status"
                    async with self._session.post(url, json={}) as response:
                        response.raise_for_status()
                    self._active_endpoint = endpoint
                    break
                except Exception as exc:
                    last_error = exc
                    logger.warning(
                        "etcd connect attempt %d/%d failed: %s",
                        attempt, self._MAX_CONNECT_RETRIES, exc,
                    )

            if self._active_endpoint is not None:
                break

            if attempt < self._MAX_CONNECT_RETRIES:
                await asyncio.sleep(self._CONNECT_RETRY_INTERVAL)

        if self._active_endpoint is None:
            await self._session.close()
            self._session = None
            raise RuntimeError(
                f"Failed to connect to etcd after {self._MAX_CONNECT_RETRIES} "
                f"attempts: {last_error}"
            )

        self._consecutive_keepalive_failures = 0
        self._keepalive_task = asyncio.create_task(self._keepalive_loop())
        logger.info(
            "EtcdDiscoveryBackend started, lease_ttl=%ds, keepalive_interval=%ds, "
            "active_endpoint=%s",
            self._lease_ttl,
            self._keepalive_interval,
            self._active_endpoint,
        )

    # ------------------------------------------------------------------
    # Keepalive with per-key lease recovery
    # ------------------------------------------------------------------

    async def _keepalive_loop(self) -> None:
        while True:
            await asyncio.sleep(self._keepalive_interval)
            if not self._leases:
                continue
            try:
                for lease_id in self._leases.values():
                    await self._post("/v3/lease/keepalive", {"ID": lease_id})
                self._consecutive_keepalive_failures = 0
                logger.debug("etcd leases refreshed, count=%d", len(self._leases))
            except Exception as exc:
                self._consecutive_keepalive_failures += 1
                logger.warning(
                    "etcd lease refresh failed (%d/%d): %s",
                    self._consecutive_keepalive_failures,
                    self._MAX_KEEPALIVE_FAILURES,
                    exc,
                )
                if self._consecutive_keepalive_failures >= self._MAX_KEEPALIVE_FAILURES:
                    await self._recover_leases()

    async def _recover_leases(self) -> None:
        """Grant new leases for all tracked keys after repeated keepalive
        failures."""
        logger.warning(
            "Attempting to recover %d etcd leases after %d consecutive failures",
            len(self._leases),
            self._consecutive_keepalive_failures,
        )
        try:
            new_leases: dict[str, int] = {}
            for key in self._leases:
                result = await self._post(
                    "/v3/lease/grant", {"TTL": self._lease_ttl}
                )
                new_leases[key] = int(result["ID"])
            self._leases = new_leases
            self._consecutive_keepalive_failures = 0
            logger.info("etcd leases recovered, count=%d", len(self._leases))
        except Exception as exc:
            logger.error("etcd lease recovery failed: %s", exc)

    # ------------------------------------------------------------------
    # CRUD via etcd v3 HTTP/JSON API
    # ------------------------------------------------------------------

    async def _grant_lease(self, key: str) -> int:
        """Grant a new etcd lease for the given key and cache it."""
        result = await self._post(
            "/v3/lease/grant", {"TTL": self._lease_ttl}
        )
        lease_id = int(result["ID"])
        self._leases[key] = lease_id
        return lease_id

    async def put(self, key: str, value: bytes) -> None:
        if self._session is None or self._active_endpoint is None:
            raise RuntimeError("EtcdDiscoveryBackend.start() must be called before put()")

        lease_id = self._leases.get(key)
        if lease_id is None:
            lease_id = await self._grant_lease(key)

        try:
            await self._post("/v3/kv/put", {
                "key": self._to_base64(key),
                "value": self._to_base64(value),
                "lease": lease_id,
            })
        except Exception:
            # The lease may have expired or been lost (e.g. after a full etcd
            # cluster restart with emptyDir).  Revoke the old lease to avoid
            # leaking it, then grant a fresh one and retry.
            logger.warning(
                "etcd put failed with lease %d for key %s, granting new lease",
                lease_id, key,
            )
            self._leases.pop(key, None)
            try:
                await self._post("/v3/lease/revoke", {"ID": lease_id})
            except Exception as revoke_exc:
                logger.debug(
                    "etcd old lease %d revoke failed (may already be expired): %s",
                    lease_id, revoke_exc,
                )
            lease_id = await self._grant_lease(key)
            await self._post("/v3/kv/put", {
                "key": self._to_base64(key),
                "value": self._to_base64(value),
                "lease": lease_id,
            })

    async def delete(self, key: str) -> None:
        if self._session is None or self._active_endpoint is None:
            return

        lease_id = self._leases.pop(key, None)
        if lease_id is not None:
            try:
                await self._post("/v3/lease/revoke", {"ID": lease_id})
            except Exception as exc:
                logger.warning("etcd lease revoke for key %s failed: %s", key, exc)

        await self._post("/v3/kv/deleterange", {
            "key": self._to_base64(key),
        })

    async def get(self, key: str) -> Optional[bytes]:
        if self._session is None or self._active_endpoint is None:
            return None

        result = await self._post("/v3/kv/range", {
            "key": self._to_base64(key),
        })
        kvs = result.get("kvs")
        if not kvs:
            return None
        return self._from_base64(kvs[0]["value"])

    async def close(self) -> None:
        if self._keepalive_task is not None:
            self._keepalive_task.cancel()
            try:
                await self._keepalive_task
            except asyncio.CancelledError:
                pass  # Expected on close(); suppress to allow clean shutdown

        for key, lease_id in self._leases.items():
            try:
                await self._post("/v3/lease/revoke", {"ID": lease_id})
            except Exception as exc:
                logger.warning("etcd lease revoke for key %s failed: %s", key, exc)
        self._leases.clear()

        if self._session is not None:
            await self._session.close()
            self._session = None


def build_backend(args: argparse.Namespace) -> DiscoveryBackend:
    """Factory: choose the backend from CLI flags."""
    if args.backend == "etcd":
        addresses = [addr.strip() for addr in args.etcd_address.split(",")]
        endpoints = [f"{addr}:{args.etcd_port}" for addr in addresses]
        return EtcdDiscoveryBackend(
            endpoints=endpoints,
            lease_ttl=args.etcd_lease_ttl,
            username=args.etcd_username or None,
            password=args.etcd_password or None,
        )
    # default: redis
    return RedisDiscoveryBackend(
        address=args.redis_address,
        port=args.redis_port,
        username=args.redis_username or None,
        password=args.redis_password or None,
    )


class ServiceDiscovery:

    def __init__(
        self,
        pod_name: str,
        entrypoint_ip: str,
        entrypoint_port: int,
        kv_transfer_ip: str,
        kv_transfer_port: int,
        instance_type: str,
        dp_size_local: int,
        heartbeat_config: HeartbeatConfig,
        backend: DiscoveryBackend,
        model: str = "",
        version: int = 1,
        ports: Optional[list[int]] = None,
    ):
        self.pod_name = pod_name
        self.dp_size_local = dp_size_local
        self.backend = backend

        if isinstance(backend, EtcdDiscoveryBackend):
            prefix = ETCD_DISCOVERY_PREFIX
        else:
            prefix = REDIS_DISCOVERY_PREFIX
        self.discovery_key = f"{prefix}{pod_name}"

        self.heartbeat = Heartbeat(heartbeat_config)
        self.heartbeat.register_loop_callback(self._update_backend)

        if ports is not None:
            if len(ports) != dp_size_local:
                raise ValueError(
                    f"ports length ({len(ports)}) must equal "
                    f"dp_size_local ({dp_size_local})"
                )
            instance_ports = ports
        else:
            instance_ports = [entrypoint_port + i for i in range(dp_size_local)]

        self.instance_infos: list[InstanceDiscoveryInfo] = []
        for i, instance_port in enumerate(instance_ports):
            instance_info = InstanceDiscoveryInfo(
                instance_type=instance_type,
                entrypoint_ip=entrypoint_ip,
                entrypoint_port=instance_port,
                kv_transfer_ip=kv_transfer_ip,
                kv_transfer_port=kv_transfer_port,
                dp_rank=i,
                dp_size=dp_size_local,
                model=model,
                version=version,
            )
            self.instance_infos.append(instance_info)
            self.heartbeat.add_endpoint(entrypoint_ip, instance_port)

    # pylint: disable=unused-argument
    async def _update_backend(self, *args, **kwargs) -> None:
        """Write healthy instances to the discovery backend."""
        healthy_instances = [
            info
            for info in self.instance_infos
            if (
                status := self.heartbeat.get_status(info.entrypoint_ip, info.entrypoint_port)
            )
            and status.status == HealthStatus.HEALTHY
        ]

        if not healthy_instances:
            logger.warning("No healthy instances to update")
            return

        pod_discovery_info = PodDiscoveryInfo(
            pod_name=self.pod_name,
            timestamp_ms=int(time.time() * 1000),
            instances=healthy_instances,
        )

        try:
            await self.backend.put(
                self.discovery_key, pod_discovery_info.SerializeToString()
            )
            logger.info(
                "Updated %d healthy instances in backend", len(healthy_instances)
            )
        except Exception as exc:
            logger.error("Failed to update backend: %s", exc)

    async def get_pod_info(self) -> Optional[PodDiscoveryInfo]:
        """Retrieve the current pod discovery info from the backend."""
        try:
            data = await self.backend.get(self.discovery_key)
            if not data:
                return None
            pod_info = PodDiscoveryInfo()
            pod_info.ParseFromString(data)
            return pod_info
        except Exception as exc:
            logger.error("Failed to get pod info from backend: %s", exc)
            return None

    async def start(self) -> None:
        """Start service discovery."""
        if isinstance(self.backend, EtcdDiscoveryBackend):
            await self.backend.start()
        await self.heartbeat.start()
        logger.info("Service discovery started")

    async def stop(self) -> None:
        """Stop service discovery."""
        await self.heartbeat.stop()
        await self.backend.close()
        logger.info("Service discovery stopped")


def add_backend_arguments(parser: argparse.ArgumentParser) -> None:
    parser.add_argument(
        "--backend",
        type=str,
        default="redis",
        choices=["redis", "etcd"],
        help="Discovery backend: 'redis' (default) or 'etcd'",
    )
    # Redis args
    parser.add_argument("--redis_address", type=str, default="localhost")
    parser.add_argument("--redis_port", type=int, default=6379)
    parser.add_argument("--redis_username", type=str, default="")
    parser.add_argument("--redis_password", type=str, default="")
    # etcd args
    parser.add_argument("--etcd_address", type=str, default="etcd",
                        help="etcd host(s), comma-separated for multiple endpoints")
    parser.add_argument("--etcd_port", type=int, default=2379, help="etcd client port")
    parser.add_argument("--etcd_username", type=str, default="")
    parser.add_argument("--etcd_password", type=str, default="")
    parser.add_argument(
        "--etcd_lease_ttl",
        type=int,
        default=60,
        help="etcd lease TTL in seconds (default: 60)",
    )


async def main():
    parser = argparse.ArgumentParser(description="Service Discovery")
    parser.add_argument("--pod_name", type=str, required=True)
    parser.add_argument("--entrypoint_ip", type=str, required=True)
    parser.add_argument("--entrypoint_port", type=int, required=True)
    parser.add_argument("--kv_transfer_ip", type=str, required=True)
    parser.add_argument("--kv_transfer_port", type=int, required=True)
    parser.add_argument("--instance_type", type=str, required=True)
    parser.add_argument("--dp_size_local", type=int, required=True)
    parser.add_argument("--model", type=str, default="")
    parser.add_argument("--version", type=int, default=1)
    add_backend_arguments(parser)
    add_heartbeat_config_arguments(parser)

    args = parser.parse_args()

    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
    )

    heartbeat_config = HeartbeatConfig(
        interval=args.interval,
        timeout=args.timeout,
        retry_times=args.retry_times,
        retry_interval=args.retry_interval,
        health_path=args.health_path,
        expected_status=args.expected_status,
        max_consecutive_failures=args.max_failures,
    )

    backend = build_backend(args)

    service_discovery = ServiceDiscovery(
        pod_name=args.pod_name,
        entrypoint_ip=args.entrypoint_ip,
        entrypoint_port=args.entrypoint_port,
        kv_transfer_ip=args.kv_transfer_ip,
        kv_transfer_port=args.kv_transfer_port,
        instance_type=args.instance_type,
        dp_size_local=args.dp_size_local,
        heartbeat_config=heartbeat_config,
        backend=backend,
        model=args.model,
        version=args.version,
    )

    await service_discovery.start()

    try:
        while True:
            await asyncio.sleep(args.interval)
            pod_discovery_info = await service_discovery.get_pod_info()
            if pod_discovery_info:
                logger.info(
                    "Current healthy instances: %d, detail: \n%s",
                    len(pod_discovery_info.instances),
                    pod_discovery_info,
                )
    except KeyboardInterrupt:
        logger.info("Received interrupt signal")
    finally:
        await service_discovery.stop()


if __name__ == "__main__":
    asyncio.run(main())
