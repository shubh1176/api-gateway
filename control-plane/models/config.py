from pydantic import BaseModel
from typing import List

class RouteConfig(BaseModel):
    path: str
    backend: str
    methods: List[str]
    rate_limit_per_minute: int = 100
    timeout_seconds: int = 30
    enable_cache: bool = False
    health_check: bool = False

class RateLimitConfig(BaseModel):
    enabled: bool = True
    burst_size: int = 10
    default_rate_per_minute: int = 1000
    num_shards: int = 16

class CircuitConfig(BaseModel):
    enabled: bool = True
    failure_threshold: int = 5
    success_threshold: int = 3
    timeout_seconds: int = 60
    health_decay: float = 0.95

class CacheConfig(BaseModel):
    enabled: bool = True
    max_size_mb: int = 100
    ttl_seconds: int = 300

class PoolConfig(BaseModel):
    max_connections: int = 1000
    max_idle: int = 100
    idle_timeout_seconds: int = 60

class TimeoutConfig(BaseModel):
    connect: int = 2
    read: int = 30
    write: int = 10
    total: int = 60

class LoadShedConfig(BaseModel):
    enabled: bool = True
    max_queue_depth: int = 1000
    cpu_percent_limit: int = 90

class GatewayConfig(BaseModel):
    listen_addr: str = ":8080"
    metrics_addr: str = ":9090"
    routes: List[RouteConfig]
    rate_limit: RateLimitConfig = RateLimitConfig()
    circuit_breaker: CircuitConfig = CircuitConfig()
    cache: CacheConfig = CacheConfig()
    connection_pool: PoolConfig = PoolConfig()
    timeouts: TimeoutConfig = TimeoutConfig()
    load_shedding: LoadShedConfig = LoadShedConfig()
