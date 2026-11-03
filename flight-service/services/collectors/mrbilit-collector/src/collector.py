#!/usr/bin/env python3
from __future__ import annotations

import logging
import os
import signal
import sys
import threading
from pathlib import Path

from dotenv import load_dotenv
from psycopg_pool import ConnectionPool

from http.server import ThreadingHTTPServer

from config import load_config
from health_server import start_health_server
from jobs_store import JobsStore
from migrations import run_migrations
from worker import CollectorWorker
from mrbilit_client import MrBilitClient

SOURCE = "mrbilit-collector"


def setup_logging(level: str) -> None:
    logging.basicConfig(
        level=getattr(logging, level.upper(), logging.INFO),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )


def resolve_path(path: str) -> str:
    candidate = Path(path)
    if candidate.is_absolute():
        return str(candidate)
    return str((Path.cwd() / candidate).resolve())


def main() -> int:
    load_dotenv()
    cfg = load_config()
    setup_logging(cfg.log_level)

    logger = logging.getLogger(SOURCE)
    logger.info("Initializing MrBilit standalone collector")

    pool = ConnectionPool(conninfo=cfg.database_url, min_size=1, max_size=2, open=True)
    runtime: dict[str, object] = {"health_server": None, "worker": None}
    migrations_ready = threading.Event()

    def shutdown(_signum: int | None = None, _frame=None) -> None:
        logger.info("shutting down")
        worker_obj = runtime.get("worker")
        if isinstance(worker_obj, CollectorWorker):
            worker_obj.stop()
        health = runtime.get("health_server")
        if isinstance(health, ThreadingHTTPServer):
            health.shutdown()
        pool.close()

    signal.signal(signal.SIGINT, shutdown)
    signal.signal(signal.SIGTERM, shutdown)

    if cfg.metrics_addr:
        runtime["health_server"] = start_health_server(
            cfg.metrics_addr,
            pool,
            "mrbilit",
            ready_check=migrations_ready.is_set,
        )

    migrations_dir = Path(resolve_path(cfg.migrations_path)).parent
    migration_paths = [
        resolve_path(cfg.migrations_path),
        resolve_path(cfg.multi_collector_migrations_path),
        str((migrations_dir / "004_maintenance_indexes.sql").resolve()),
    ]
    try:
        run_migrations(pool, migration_paths)
    except Exception as e:
        logger.error("migration failed: %s", e)
        return 1
    migrations_ready.set()

    store = JobsStore(pool, "mrbilit")

    client = MrBilitClient(
        adult=cfg.adult,
        flight_class=cfg.flight_class,
        rps=cfg.rate_limit_rps,
        burst=cfg.rate_limit_burst,
        http_timeout=cfg.http_timeout,
        circuit_breaker_threshold=cfg.circuit_breaker_threshold,
        circuit_breaker_open=cfg.circuit_breaker_open,
        base_url=cfg.mrbilit_base_url,
    )

    worker = CollectorWorker(cfg, store, client)
    runtime["worker"] = worker

    logger.info(
        "starting mrbilit-collector: workers=%s rps=%s database=%s",
        cfg.max_workers,
        cfg.rate_limit_rps,
        cfg.database_url.split("@")[-1] if "@" in cfg.database_url else cfg.database_url,
    )

    try:
        worker.run()
    finally:
        if hasattr(client, "close"):
            client.close()
        shutdown()
    return 0


if __name__ == "__main__":
    sys.exit(main())
