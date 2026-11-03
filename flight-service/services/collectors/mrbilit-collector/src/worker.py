from __future__ import annotations

import logging
import math
import random
import socket
import threading
import time
from datetime import date, datetime, timedelta, timezone
from typing import Any

from config import Config
from jobs_store import Job, JobsStore, Route
from anti_bot import (
    is_circuit_open,
    is_rate_limited,
    retry_after,
)

logger = logging.getLogger(__name__)


class CollectorWorker:
    def __init__(self, cfg: Config, store: JobsStore, client: Any) -> None:
        self._cfg = cfg
        self._store = store
        self._client = client
        self._worker_id = f"{socket.gethostname()}-{datetime.now().strftime('%H%M%S')}"
        self._stop = threading.Event()

    def stop(self) -> None:
        self._stop.set()

    def run(self) -> None:
        threads = [
            threading.Thread(target=self._seed_loop, name="seed-loop", daemon=True),
            threading.Thread(
                target=self._maintenance_loop, name="maintenance-loop", daemon=True
            ),
        ]
        for index in range(self._cfg.max_workers):
            threads.append(
                threading.Thread(
                    target=self._worker_loop, args=(index,), name=f"worker-{index}", daemon=True
                )
            )
        for thread in threads:
            thread.start()
        try:
            while not self._stop.wait(1.0):
                pass
        except KeyboardInterrupt:
            self.stop()
        for thread in threads:
            thread.join(timeout=5.0)

    def _seed_loop(self) -> None:
        self._seed_once()
        while not self._stop.wait(self._cfg.seed_interval):
            self._seed_once()

    def _seed_once(self) -> None:
        try:
            routes = self._store.list_enabled_routes()
        except Exception as exc:  # noqa: BLE001
            logger.error("list routes failed: %s", exc)
            return

        now = datetime.now(timezone.utc)
        due: list[Route] = []
        for route in routes:
            if route.last_enqueued_at is None:
                due.append(route)
                continue
            refresh = timedelta(minutes=route.refresh_minutes)
            if now - route.last_enqueued_at.replace(tzinfo=timezone.utc) >= refresh:
                due.append(route)
        if not due:
            return
        try:
            count = self._store.enqueue_seed_jobs(due, self._cfg.collect_job_spacing)
        except Exception as exc:  # noqa: BLE001
            logger.error("enqueue seed jobs failed: %s", exc)
            return
        if count:
            logger.info("enqueued seed jobs count=%s", count)

    def _maintenance_loop(self) -> None:
        while not self._stop.wait(30.0):
            try:
                reclaimed = self._store.reclaim_stale(self._cfg.job_stale_after)
                if reclaimed:
                    logger.warning("reclaimed stale jobs count=%s", reclaimed)
                requeued = self._store.requeue_failed()
                if requeued:
                    logger.info("requeued failed jobs count=%s", requeued)
                dead = self._store.mark_exhausted_dead()
                if dead:
                    logger.warning("marked exhausted jobs dead count=%s", dead)
            except Exception as exc:  # noqa: BLE001
                logger.error("maintenance loop failed: %s", exc)

    def _worker_loop(self, worker_id: int) -> None:
        while not self._stop.is_set():
            try:
                jobs = self._store.claim_jobs(self._worker_id, 1)
            except Exception as exc:  # noqa: BLE001
                logger.error("claim jobs failed worker=%s err=%s", worker_id, exc)
                self._stop.wait(self._cfg.poll_interval)
                continue
            if not jobs:
                self._stop.wait(self._cfg.poll_interval)
                continue
            for job in jobs:
                self._handle(worker_id, job)

    def _handle(self, worker_id: int, job: Job) -> None:
        started = time.monotonic()
        try:
            if job.kind == "seed_route":
                self._handle_seed(job)
            elif job.kind == "collect_day":
                self._handle_collect(job)
            else:
                raise RuntimeError(f"unsupported job kind: {job.kind}")
        except Exception as exc:  # noqa: BLE001
            delay = self._retry_delay(job, exc)
            if is_circuit_open(exc):
                logger.warning(
                    "job deferred worker=%s job=%s retry_in=%.1fs err=%s",
                    worker_id,
                    job.id,
                    delay,
                    exc,
                )
                self._store.defer_job(job.id, delay, str(exc))
                return
            logger.error(
                "job failed worker=%s job=%s kind=%s attempt=%s retry_in=%.1fs err=%s",
                worker_id,
                job.id,
                job.kind,
                job.attempts,
                delay,
                exc,
            )
            self._store.fail_job(job.id, job.attempts, job.max_attempts, str(exc), delay)
            return

        self._store.complete_job(job.id)
        logger.info(
            "job done worker=%s job=%s kind=%s dur=%.2fs",
            worker_id,
            job.id,
            job.kind,
            time.monotonic() - started,
        )

    def _handle_seed(self, job: Job) -> None:
        routes = self._store.list_enabled_routes()
        route: Route | None = None
        for candidate in routes:
            if job.route_id is not None and candidate.id == job.route_id:
                route = candidate
                break
            if candidate.origin == job.origin and candidate.destination == job.destination:
                route = candidate
                break
        if route is None:
            route = Route(
                id=job.route_id or 0,
                origin=job.origin,
                destination=job.destination,
                enabled=True,
                priority=job.priority,
                refresh_minutes=360,
                window_days=30,
            )
        today = datetime.now(timezone.utc).date()
        dates = [today + timedelta(days=offset) for offset in range(route.window_days)]
        count = self._store.enqueue_collect_days(route, dates, self._cfg.collect_job_spacing)
        if job.route_id:
            self._store.mark_route_enqueued(job.route_id)
        logger.info(
            "seeded collect_day jobs route=%s-%s new=%s",
            route.origin,
            route.destination,
            count,
        )

    def _handle_collect(self, job: Job) -> None:
        if job.flight_date is None:
            raise RuntimeError("collect_day without date")
        day = job.flight_date
        started = time.monotonic()
        try:
            request_id, flights = self._client.collect_day(job.origin, job.destination, day)
        except Exception as exc:  # noqa: BLE001
            duration_ms = int((time.monotonic() - started) * 1000)
            self._store.log_run(
                job.id,
                job.origin,
                job.destination,
                day,
                "",
                0,
                duration_ms,
                False,
                str(exc),
            )
            raise
        duration_ms = int((time.monotonic() - started) * 1000)
        for flight in flights:
            flight.source = self._store._source
        upserted = self._store.upsert_flights(flights)
        self._store.log_run(
            job.id,
            job.origin,
            job.destination,
            day,
            request_id,
            upserted,
            duration_ms,
            True,
            "",
        )
        logger.info(
            "collected od=%s-%s date=%s flights=%s upserted=%s",
            job.origin,
            job.destination,
            day.isoformat(),
            len(flights),
            upserted,
        )

    def _retry_delay(self, job: Job, err: BaseException) -> float:
        attempt = max(job.attempts, 1)
        if is_rate_limited(err):
            delay = exponential(
                self._cfg.rate_limit_backoff_base,
                self._cfg.rate_limit_backoff_max,
                attempt,
            )
            delay = add_positive_jitter(delay, 0.20)
            server_delay = retry_after(err)
            return max(delay, server_delay)
        return add_jitter(
            exponential(
                self._cfg.transient_backoff_base,
                self._cfg.transient_backoff_max,
                attempt,
            ),
            0.20,
        )


def exponential(base: float, maximum: float, attempt: int) -> float:
    if base <= 0:
        base = 1.0
    if maximum < base:
        maximum = base
    exponent = min(attempt - 1, 20)
    delay = base * math.pow(2, exponent)
    return min(delay, maximum)


def add_positive_jitter(delay: float, fraction: float) -> float:
    if delay <= 0 or fraction <= 0:
        return delay
    return delay + random.random() * delay * fraction


def add_jitter(delay: float, fraction: float) -> float:
    if delay <= 0 or fraction <= 0:
        return delay
    span = delay * fraction
    jittered = delay + (random.random() * 2 - 1) * span
    return max(jittered, 1.0)
