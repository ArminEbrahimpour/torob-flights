from __future__ import annotations

import os
from dataclasses import dataclass


def _float(name: str, default: float) -> float:
    raw = os.getenv(name)
    if raw is None or raw.strip() == "":
        return default
    return float(raw)


def _int(name: str, default: int, minimum: int = 0) -> int:
    raw = os.getenv(name)
    if raw is None or raw.strip() == "":
        return max(default, minimum)
    return max(int(raw), minimum)


def _duration_seconds(name: str, default: float) -> float:
    raw = os.getenv(name, "").strip()
    if not raw:
        return default
    if raw.endswith("ms"):
        return float(raw[:-2]) / 1000.0
    if raw.endswith("s"):
        return float(raw[:-1])
    if raw.endswith("m"):
        return float(raw[:-1]) * 60.0
    return float(raw)


@dataclass(frozen=True)
class Config:
    database_url: str
    migrations_path: str
    multi_collector_migrations_path: str
    max_workers: int
    rate_limit_rps: float
    rate_limit_burst: int
    poll_interval: float
    seed_interval: float
    job_stale_after: float
    collect_job_spacing: float
    adult: int
    flight_class: str
    http_timeout: float
    circuit_breaker_threshold: int
    circuit_breaker_open: float
    rate_limit_backoff_base: float
    rate_limit_backoff_max: float
    transient_backoff_base: float
    transient_backoff_max: float
    metrics_addr: str
    mrbilit_base_url: str
    environment: str
    log_level: str


def load_config() -> Config:
    return Config(
        database_url=os.getenv(
            "DATABASE_URL",
            "postgres://flight:flight@localhost:5432/airbar_flights?sslmode=disable",
        ),
        migrations_path=os.getenv("MIGRATIONS_PATH", "migrations/001_flights_core.sql"),
        multi_collector_migrations_path=os.getenv(
            "MULTI_COLLECTOR_MIGRATION_PATH", "migrations/002_multi_collector.sql"
        ),
        max_workers=_int("MAX_WORKERS", 1, minimum=1),
        rate_limit_rps=max(_float("RATE_LIMIT_RPS", 0.15), 0.01),
        rate_limit_burst=_int("RATE_LIMIT_BURST", 1, minimum=1),
        poll_interval=_duration_seconds("POLL_INTERVAL", 1.0),
        seed_interval=_duration_seconds("SEED_INTERVAL", 180.0),
        job_stale_after=_duration_seconds("JOB_STALE_AFTER", 900.0),
        collect_job_spacing=_duration_seconds("COLLECT_JOB_SPACING", 8.0),
        adult=_int("ADULT", 1, minimum=1),
        flight_class=os.getenv("FLIGHT_CLASS", "Economy"),
        http_timeout=_duration_seconds("HTTP_TIMEOUT", 60.0),
        circuit_breaker_threshold=_int("CIRCUIT_BREAKER_THRESHOLD", 2, minimum=1),
        circuit_breaker_open=_duration_seconds("CIRCUIT_BREAKER_OPEN", 900.0),
        rate_limit_backoff_base=_duration_seconds("RATE_LIMIT_BACKOFF_BASE", 120.0),
        rate_limit_backoff_max=_duration_seconds("RATE_LIMIT_BACKOFF_MAX", 2700.0),
        transient_backoff_base=_duration_seconds("TRANSIENT_BACKOFF_BASE", 30.0),
        transient_backoff_max=_duration_seconds("TRANSIENT_BACKOFF_MAX", 600.0),
        metrics_addr=os.getenv("METRICS_ADDR", ":9094"),
        mrbilit_base_url=os.getenv("MRBILIT_BASE_URL", "https://flight.atighgasht.com").rstrip("/"),
        environment=os.getenv("APP_ENV", "development"),
        log_level=os.getenv("LOG_LEVEL", "info"),
    )
