import asyncio
import aiohttp
import time
from typing import Dict, List, Optional, Callable
from dataclasses import dataclass, field
from enum import Enum
from datetime import datetime
import logging
import argparse

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


class HealthStatus(str, Enum):
    HEALTHY = "healthy"
    UNHEALTHY = "unhealthy"
    UNKNOWN = "unknown"


@dataclass
class HeartbeatConfig:
    interval: int = 10
    timeout: int = 5
    retry_times: int = 1
    retry_interval: int = 1
    health_path: str = "/health"
    expected_status: int = 200
    max_consecutive_failures: int = 1


@dataclass
class EndpointStatus:
    host: str
    port: int
    status: HealthStatus = HealthStatus.UNKNOWN
    last_check_time: Optional[datetime] = None
    response_time: Optional[float] = None
    consecutive_failures: int = 0
    consecutive_successes: int = 0
    total_checks: int = 0
    successful_checks: int = 0
    error_message: Optional[str] = None


class Heartbeat:
    
    def __init__(self, config: HeartbeatConfig):
        self.config = config
        self.endpoints: Dict[str, EndpointStatus] = {}
        self._running = False
        self._tasks: List[asyncio.Task] = []
        self._callbacks: List[Callable] = []
        self._loop_callbacks: List[Callable] = []
        
    def add_endpoint(self, host: str, port: int) -> None:
        key = f"{host}:{port}"
        if key not in self.endpoints:
            self.endpoints[key] = EndpointStatus(host=host, port=port)
            logger.info(f"Added endpoint: {key}")
    
    def add_endpoints(self, endpoints: List[tuple]) -> None:
        for host, port in endpoints:
            self.add_endpoint(host, port)
    
    def remove_endpoint(self, host: str, port: int) -> None:
        key = f"{host}:{port}"
        if key in self.endpoints:
            del self.endpoints[key]
            logger.info(f"Removed endpoint: {key}")
    
    def register_callback(self, callback: Callable) -> None:
        """Register callback for endpoint status changes"""
        self._callbacks.append(callback)
    
    def register_loop_callback(self, callback: Callable) -> None:
        """Register callback to be executed after each heartbeat loop"""
        self._loop_callbacks.append(callback)
    
    async def _check_endpoint(self, endpoint: EndpointStatus) -> bool:
        url = f"http://{endpoint.host}:{endpoint.port}{self.config.health_path}"
        
        for attempt in range(self.config.retry_times):
            start_time = time.time()
            
            try:
                timeout = aiohttp.ClientTimeout(total=self.config.timeout)
                async with aiohttp.ClientSession(timeout=timeout) as session:
                    async with session.get(url) as response:
                        response_time = (time.time() - start_time) * 1000
                        
                        if response.status == self.config.expected_status:
                            endpoint.response_time = round(response_time, 2)
                            endpoint.error_message = None
                            return True
                        else:
                            endpoint.error_message = f"Unexpected status code: {response.status}"
                            
            except asyncio.TimeoutError:
                endpoint.error_message = f"Timeout after {self.config.timeout}s"
                logger.warning(f"{endpoint.host}:{endpoint.port} - Timeout (attempt {attempt + 1}/{self.config.retry_times})")
                
            except aiohttp.ClientError as e:
                endpoint.error_message = f"Connection error: {str(e)}"
                logger.warning(f"{endpoint.host}:{endpoint.port} - Connection error: {e} (attempt {attempt + 1}/{self.config.retry_times})")
                
            except Exception as e:
                endpoint.error_message = f"Unknown error: {str(e)}"
                logger.error(f"{endpoint.host}:{endpoint.port} - Unknown error: {e}")
            
            if attempt < self.config.retry_times - 1:
                await asyncio.sleep(self.config.retry_interval)
        
        return False
    
    async def _update_endpoint_status(self, key: str, endpoint: EndpointStatus) -> None:
        old_status = endpoint.status
        is_healthy = await self._check_endpoint(endpoint)
        
        endpoint.last_check_time = datetime.now()
        endpoint.total_checks += 1
        
        if is_healthy:
            endpoint.successful_checks += 1
            endpoint.consecutive_successes += 1
            endpoint.consecutive_failures = 0
            endpoint.status = HealthStatus.HEALTHY
        else:
            endpoint.consecutive_failures += 1
            endpoint.consecutive_successes = 0
            
            if endpoint.consecutive_failures >= self.config.max_consecutive_failures:
                endpoint.status = HealthStatus.UNHEALTHY
        
        if old_status != endpoint.status:
            logger.info(f"{key} status changed: {old_status.value} -> {endpoint.status.value}")
            for callback in self._callbacks:
                try:
                    await callback(key, old_status, endpoint.status)
                except Exception as e:
                    logger.error(f"Callback error: {e}")
    
    async def _execute_loop_callbacks(self) -> None:
        """Execute all registered loop callbacks"""
        for callback in self._loop_callbacks:
            try:
                await callback(self.endpoints.copy())
            except Exception as e:
                logger.error(f"Loop callback error: {e}")
    
    async def _heartbeat_loop(self) -> None:
        while self._running:
            start_time = time.time()
            
            tasks = []
            for key, endpoint in self.endpoints.items():
                task = asyncio.create_task(self._update_endpoint_status(key, endpoint))
                tasks.append(task)
            
            if tasks:
                await asyncio.gather(*tasks, return_exceptions=True)
            
            # Execute loop callbacks after all endpoints are checked
            await self._execute_loop_callbacks()
            
            elapsed_time = time.time() - start_time
            sleep_time = max(0, self.config.interval - elapsed_time)
            await asyncio.sleep(sleep_time)
    
    async def start(self) -> None:
        if self._running:
            logger.warning("Heartbeat is already running")
            return
        
        self._running = True
        task = asyncio.create_task(self._heartbeat_loop())
        self._tasks.append(task)
        logger.info("Heartbeat started")
    
    async def stop(self) -> None:
        if not self._running:
            return
        
        self._running = False
        
        for task in self._tasks:
            task.cancel()
        
        await asyncio.gather(*self._tasks, return_exceptions=True)
        self._tasks.clear()
        logger.info("Heartbeat stopped")
    
    def get_status(self, host: str, port: int) -> Optional[EndpointStatus]:
        key = f"{host}:{port}"
        return self.endpoints.get(key)
    
    def get_all_status(self) -> Dict[str, EndpointStatus]:
        return self.endpoints.copy()
    
    def get_healthy_endpoints(self) -> List[tuple]:
        return [
            (endpoint.host, endpoint.port)
            for endpoint in self.endpoints.values()
            if endpoint.status == HealthStatus.HEALTHY
        ]
    
    def get_unhealthy_endpoints(self) -> List[tuple]:
        return [
            (endpoint.host, endpoint.port)
            for endpoint in self.endpoints.values()
            if endpoint.status == HealthStatus.UNHEALTHY
        ]
    
    def print_status(self) -> None:
        print("\n" + "="*80)
        print(f"{'Endpoint':<25} {'Status':<12} {'Response Time':<15} {'Success Rate':<12} {'Error'}")
        print("="*80)
        
        for key, endpoint in self.endpoints.items():
            success_rate = (
                f"{endpoint.successful_checks}/{endpoint.total_checks} "
                f"({endpoint.successful_checks/endpoint.total_checks*100:.1f}%)"
                if endpoint.total_checks > 0 else "N/A"
            )
            
            response_time = f"{endpoint.response_time}ms" if endpoint.response_time else "N/A"
            error = endpoint.error_message or ""
            
            print(f"{key:<25} {endpoint.status.value:<12} {response_time:<15} {success_rate:<12} {error}")
        
        print("="*80 + "\n")


async def status_change_callback(endpoint: str, old_status: HealthStatus, new_status: HealthStatus):
    logger.info(f"Alert: {endpoint} changed from {old_status.value} to {new_status.value}")


async def loop_completion_callback(endpoints: Dict[str, EndpointStatus]):
    """Example callback that executes after each complete heartbeat loop"""
    healthy_count = sum(1 for ep in endpoints.values() if ep.status == HealthStatus.HEALTHY)
    total_count = len(endpoints)
    logger.info(f"Heartbeat loop completed. Healthy endpoints: {healthy_count}/{total_count}")


def add_heartbeat_config_arguments(parser: argparse.ArgumentParser):
    parser.add_argument('--interval', type=int, default=5, help='Check interval in seconds (default: 10)')
    parser.add_argument('--timeout', type=int, default=3, help='Request timeout in seconds (default: 5)')
    parser.add_argument('--retry-times', type=int, default=1, help='Retry attempts per check (default: 1)')
    parser.add_argument('--retry-interval', type=int, default=1, help='Interval between retries (default: 1)')
    parser.add_argument('--health-path', type=str, default='/health', help='Health check path (default: /health)')
    parser.add_argument('--expected-status', type=int, default=200, help='Expected status code (default: 200)')
    parser.add_argument('--max-failures', type=int, default=1, help='Max consecutive failures (default: 1)')

async def main():
    parser = argparse.ArgumentParser(description='Heartbeat monitor for checking endpoint health')
    parser.add_argument('--endpoints', nargs='+', required=True, help='Endpoints in format host:port')
    parser.add_argument('--verbose', action='store_true', help='Enable verbose logging')
    add_heartbeat_config_arguments(parser)
    
    args = parser.parse_args()
    
    if args.verbose:
        logging.getLogger().setLevel(logging.DEBUG)
    
    endpoints = []
    for endpoint in args.endpoints:
        try:
            host, port = endpoint.rsplit(':', 1)
            endpoints.append((host, int(port)))
        except ValueError:
            logger.error(f"Invalid endpoint format: {endpoint}. Expected: host:port")
            return
    
    config = HeartbeatConfig(
        interval=args.interval,
        timeout=args.timeout,
        retry_times=args.retry_times,
        retry_interval=args.retry_interval,
        health_path=args.health_path,
        expected_status=args.expected_status,
        max_consecutive_failures=args.max_failures
    )
    
    heartbeat = Heartbeat(config)
    heartbeat.add_endpoints(endpoints)
    heartbeat.register_callback(status_change_callback)
    heartbeat.register_loop_callback(loop_completion_callback)
    
    await heartbeat.start()
    
    try:
        logger.info("Monitoring indefinitely. Press Ctrl+C to stop...")
        while True:
            await asyncio.sleep(args.interval)
            heartbeat.print_status()
    except KeyboardInterrupt:
        logger.info("Received interrupt signal")
    finally:
        await heartbeat.stop()
        heartbeat.print_status()
        logger.info("Heartbeat stopped")

if __name__ == "__main__":
    asyncio.run(main())
