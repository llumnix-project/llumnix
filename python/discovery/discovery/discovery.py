import asyncio
import time
from typing import List, Optional
import redis.asyncio as redis
import logging
import argparse

from .heartbeat import Heartbeat, HeartbeatConfig, HealthStatus, add_heartbeat_config_arguments
from .proto.redis_discovery_pb2 import InstanceDiscoveryInfo, PodDiscoveryInfo

logger = logging.getLogger(__name__)

LLUMNIX_DISCOVERY_PREFIX = "llumnix:discovery:"


class ServiceDiscovery:
    
    def __init__(
        self, 
        pod_name: str,
        entrypoint_ip: str,
        entrypoint_port: int,
        kv_transfer_ip: str,
        kv_transfer_port: int,
        role: str,
        dp_size_local: int,
        redis_address: str,
        redis_port: int,
        heartbeat_config: HeartbeatConfig,
        redis_username: Optional[str] = None,
        redis_password: Optional[str] = None,
        model: str = "",
        version: int = 1,
    ):
        self.pod_name = pod_name
        self.dp_size_local = dp_size_local
        
        redis_params = {
            "host": redis_address,
            "port": redis_port,
        }
        if redis_username:
            redis_params["username"] = redis_username
        if redis_password:
            redis_params["password"] = redis_password

        self.redis_client = redis.Redis(**redis_params)
        
        self.redis_key = f"{LLUMNIX_DISCOVERY_PREFIX}{pod_name}"
        
        self.heartbeat = Heartbeat(heartbeat_config)
        self.heartbeat.register_loop_callback(self._update_redis)
        
        # Create instance infos and register endpoints
        self.instance_infos: list[InstanceDiscoveryInfo] = []
        for i in range(dp_size_local):
            instance_port = entrypoint_port + i
            instance_info = InstanceDiscoveryInfo(
                role=role,
                entrypoint_ip=entrypoint_ip,
                entrypoint_port=instance_port,
                kv_transfer_ip=kv_transfer_ip,
                kv_transfer_port=kv_transfer_port,
                dp_rank=i,
                dp_size=dp_size_local,
                model=model,
                version=version
            )
            self.instance_infos.append(instance_info)
            self.heartbeat.add_endpoint(entrypoint_ip, instance_port)
    
    async def _update_redis(self, *args, **kwargs) -> None:
        """Update Redis with healthy instances."""
        healthy_instances = []
        
        for instance_info in self.instance_infos:
            endpoint_status = self.heartbeat.get_status(
                instance_info.entrypoint_ip, 
                instance_info.entrypoint_port
            )
            if endpoint_status and endpoint_status.status == HealthStatus.HEALTHY:
                healthy_instances.append(instance_info)
        
        if not healthy_instances:
            logger.warning("No healthy instances to update")
            return
        
        pod_discovery_info = PodDiscoveryInfo(
            pod_name=self.pod_name,
            timestamp_ms=int(time.time() * 1000),
            instances=healthy_instances
        )
        
        try:
            await self.redis_client.set(
                self.redis_key, 
                pod_discovery_info.SerializeToString()
            )
            await self.redis_client.expire(self.redis_key, 3600)
            logger.info(f"Updated {len(healthy_instances)} healthy instances in Redis")
        except Exception as e:
            logger.error(f"Failed to update Redis: {e}")
    
    async def get_pod_info(self) -> PodDiscoveryInfo | None:
        """Get healthy instances from Redis."""
        try:
            data = await self.redis_client.get(self.redis_key)
            if not data:
                return []
            
            pod_info = PodDiscoveryInfo()
            pod_info.ParseFromString(data)
            return pod_info
        except Exception as e:
            logger.error(f"Failed to get instances from Redis: {e}")
            return None
    
    async def start(self) -> None:
        """Start service discovery."""
        await self.heartbeat.start()
        logger.info("Service discovery started")
    
    async def stop(self) -> None:
        """Stop service discovery."""
        await self.heartbeat.stop()
        await self.redis_client.close()
        logger.info("Service discovery stopped")


async def main():
    parser = argparse.ArgumentParser(description='Service Discovery')
    parser.add_argument('--pod_name', type=str, required=True)
    parser.add_argument('--entrypoint_ip', type=str, required=True)
    parser.add_argument('--entrypoint_port', type=int, required=True)
    parser.add_argument('--kv_transfer_ip', type=str, required=True)
    parser.add_argument('--kv_transfer_port', type=int, required=True)
    parser.add_argument('--role', type=str, required=True)
    parser.add_argument('--dp_size_local', type=int, required=True)
    parser.add_argument('--redis_address', type=str, required=True)
    parser.add_argument('--redis_port', type=int, required=True)
    parser.add_argument('--redis_username', type=str, default='')
    parser.add_argument('--redis_password', type=str, default='')
    parser.add_argument('--model', type=str, default='')
    parser.add_argument('--version', type=int, default=1)
    add_heartbeat_config_arguments(parser)
    
    args = parser.parse_args()
    
    logging.basicConfig(
        level=logging.INFO,
        format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
    )
    
    heartbeat_config = HeartbeatConfig(
        interval=args.interval,
        timeout=args.timeout,
        retry_times=args.retry_times,
        retry_interval=args.retry_interval,
        health_path=args.health_path,
        expected_status=args.expected_status,
        max_consecutive_failures=args.max_failures
    )

    service_discovery = ServiceDiscovery(
        pod_name=args.pod_name,
        entrypoint_ip=args.entrypoint_ip,
        entrypoint_port=args.entrypoint_port,
        kv_transfer_ip=args.kv_transfer_ip,
        kv_transfer_port=args.kv_transfer_port,
        role=args.role,
        dp_size_local=args.dp_size_local,
        redis_address=args.redis_address,
        redis_port=args.redis_port,
        redis_username=args.redis_username,
        redis_password=args.redis_password,
        model=args.model,
        version=args.version,
        heartbeat_config=heartbeat_config
    )
    
    await service_discovery.start()
    
    try:
        while True:
            await asyncio.sleep(args.interval)
            pod_discovery_info = await service_discovery.get_pod_info()
            if pod_discovery_info:
                logger.info(f"Current healthy instances: {len(pod_discovery_info.instances)}, detail: \n{pod_discovery_info}")
    except KeyboardInterrupt:
        logger.info("Received interrupt signal")
    finally:
        await service_discovery.stop()


if __name__ == "__main__":
    asyncio.run(main())
