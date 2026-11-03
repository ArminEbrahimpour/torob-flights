from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import date, datetime, timedelta, timezone
from typing import Any

import psycopg
from psycopg.rows import dict_row
from psycopg_pool import ConnectionPool

UPSERT_FLIGHT_SQL = """
INSERT INTO flights (
    origin, destination, origin_query, destination_query, flight_date,
    airline_code, airline_name, flight_number, flight_number_norm,
    depart_at, arrive_at, depart_minute, arrive_minute,
    baggage_label, baggage_kg, price_rial, price_toman,
    is_charter, stops, duration_min, proposal_id, source, collected_at, updated_at
) VALUES (
    %s,%s,%s,%s,%s::date,
    %s,%s,%s,%s,
    %s,%s,%s,%s,
    %s,%s,%s,%s,
    %s,%s,%s,%s,%s, now(), now()
)
ON CONFLICT (
    source, origin, destination, flight_date, airline_code, flight_number,
    depart_at, arrive_at, price_rial, is_charter
) DO UPDATE SET
    airline_name = EXCLUDED.airline_name,
    flight_number_norm = EXCLUDED.flight_number_norm,
    baggage_label = EXCLUDED.baggage_label,
    baggage_kg = EXCLUDED.baggage_kg,
    price_rial = EXCLUDED.price_rial,
    price_toman = EXCLUDED.price_toman,
    duration_min = EXCLUDED.duration_min,
    proposal_id = EXCLUDED.proposal_id,
    origin_query = EXCLUDED.origin_query,
    destination_query = EXCLUDED.destination_query,
    collected_at = now(),
    updated_at = now()
"""


@dataclass
class Route:
    id: int
    origin: str
    destination: str
    enabled: bool
    priority: int
    refresh_minutes: int
    window_days: int
    last_enqueued_at: datetime | None = None


@dataclass
class Job:
    id: int
    kind: str
    status: str
    priority: int
    route_id: int | None
    origin: str
    destination: str
    flight_date: date | None
    attempts: int
    max_attempts: int
    run_after: datetime
    payload: dict[str, Any]


@dataclass
class FlightRow:
    origin: str
    destination: str
    origin_query: str
    destination_query: str
    flight_date: date
    airline_code: str
    airline_name: str
    flight_number: str
    flight_number_norm: str
    depart_at: datetime
    arrive_at: datetime
    depart_minute: int
    arrive_minute: int
    baggage_label: str = ""
    baggage_kg: int | None = None
    price_rial: int = 0
    price_toman: int = 0
    is_charter: bool = False
    stops: int = 0
    duration_min: int | None = None
    proposal_id: str = ""
    source: str = ""


class JobsStore:
    def __init__(self, pool: ConnectionPool, source: str) -> None:
        self._pool = pool
        self._source = source

    def list_enabled_routes(self) -> list[Route]:
        with self._pool.connection() as conn, conn.cursor(row_factory=dict_row) as cur:
            cur.execute(
                """
                SELECT r.id, r.origin, r.destination, r.enabled, r.priority,
                       r.refresh_minutes, r.window_days, crs.last_enqueued_at
                FROM routes r
                LEFT JOIN collector_route_state crs
                  ON crs.route_id = r.id AND crs.source = %s
                WHERE r.enabled = TRUE
                ORDER BY r.priority, r.id
                """,
                (self._source,),
            )
            rows = cur.fetchall()
        return [
            Route(
                id=row["id"],
                origin=row["origin"],
                destination=row["destination"],
                enabled=row["enabled"],
                priority=row["priority"],
                refresh_minutes=row["refresh_minutes"],
                window_days=row["window_days"],
                last_enqueued_at=row["last_enqueued_at"],
            )
            for row in rows
        ]

    def mark_route_enqueued(self, route_id: int) -> None:
        with self._pool.connection() as conn, conn.cursor() as cur:
            cur.execute(
                """
                WITH state AS (
                    INSERT INTO collector_route_state (source, route_id, last_enqueued_at)
                    VALUES (%s, %s, now())
                    ON CONFLICT (source, route_id)
                    DO UPDATE SET last_enqueued_at = EXCLUDED.last_enqueued_at
                    RETURNING route_id, last_enqueued_at
                )
                UPDATE routes r
                SET last_enqueued_at = state.last_enqueued_at
                FROM state
                WHERE r.id = state.route_id
                """,
                (self._source, route_id),
            )
            conn.commit()

    def enqueue_seed_jobs(self, routes: list[Route], spacing: float) -> int:
        if spacing <= 0:
            spacing = 5.0
        total = 0
        cumulative_days = 0
        now = datetime.now(timezone.utc)
        with self._pool.connection() as conn, conn.cursor() as cur:
            for route in routes:
                run_after = now + timedelta(seconds=cumulative_days * spacing)
                cumulative_days += max(route.window_days, 1)
                cur.execute(
                    """
                    INSERT INTO jobs (source, kind, status, priority, route_id, origin, destination, run_after)
                    SELECT %s, 'seed_route', 'pending', %s, %s, %s, %s, %s
                    WHERE NOT EXISTS (
                        SELECT 1 FROM jobs
                        WHERE source = %s AND kind = 'seed_route' AND route_id = %s
                          AND status IN ('pending','running')
                    )
                    """,
                    (
                        self._source,
                        route.priority,
                        route.id,
                        route.origin,
                        route.destination,
                        run_after,
                        self._source,
                        route.id,
                    ),
                )
                total += cur.rowcount
            conn.commit()
        return total

    def enqueue_collect_days(
        self, route: Route, dates: list[date], spacing: float
    ) -> int:
        if spacing <= 0:
            spacing = 5.0
        total = 0
        base = datetime.now(timezone.utc)
        with self._pool.connection() as conn, conn.cursor() as cur:
            for index, day in enumerate(dates):
                run_after = base + timedelta(seconds=index * spacing)
                cur.execute(
                    """
                    INSERT INTO jobs (source, kind, status, priority, route_id, origin, destination, flight_date, run_after)
                    SELECT %s, 'collect_day', 'pending', %s, %s, %s, %s, %s::date, %s
                    WHERE NOT EXISTS (
                        SELECT 1 FROM jobs
                        WHERE source = %s AND kind = 'collect_day'
                          AND origin = %s AND destination = %s AND flight_date = %s::date
                          AND status IN ('pending', 'running')
                    )
                    """,
                    (
                        self._source,
                        route.priority,
                        route.id,
                        route.origin,
                        route.destination,
                        day,
                        run_after,
                        self._source,
                        route.origin,
                        route.destination,
                        day,
                    ),
                )
                total += cur.rowcount
            conn.commit()
        return total

    def claim_jobs(self, worker_id: str, limit: int) -> list[Job]:
        with self._pool.connection() as conn:
            conn.autocommit = False
            with conn.cursor(row_factory=dict_row) as cur:
                cur.execute(
                    """
                    SELECT id, kind, status, priority, route_id, origin, destination,
                           flight_date, attempts, max_attempts, run_after, payload
                    FROM jobs
                    WHERE source = %s
                      AND status = 'pending' AND run_after <= now() AND attempts < max_attempts
                      AND kind IN ('seed_route', 'collect_day')
                    ORDER BY priority ASC, id ASC
                    FOR UPDATE SKIP LOCKED
                    LIMIT %s
                    """,
                    (self._source, limit),
                )
                rows = cur.fetchall()
                if not rows:
                    conn.commit()
                    return []
                ids = [row["id"] for row in rows]
                cur.execute(
                    """
                    UPDATE jobs
                    SET status = 'running', locked_by = %s, locked_at = now(),
                        attempts = attempts + 1, updated_at = now()
                    WHERE source = %s AND id = ANY(%s)
                    """,
                    (worker_id, self._source, ids),
                )
                conn.commit()

        jobs: list[Job] = []
        for row in rows:
            payload = row["payload"]
            if isinstance(payload, str):
                payload = json.loads(payload or "{}")
            jobs.append(
                Job(
                    id=row["id"],
                    kind=row["kind"],
                    status="running",
                    priority=row["priority"],
                    route_id=row["route_id"],
                    origin=row["origin"],
                    destination=row["destination"],
                    flight_date=row["flight_date"],
                    attempts=row["attempts"] + 1,
                    max_attempts=row["max_attempts"],
                    run_after=row["run_after"],
                    payload=payload or {},
                )
            )
        return jobs

    def complete_job(self, job_id: int) -> None:
        with self._pool.connection() as conn, conn.cursor() as cur:
            cur.execute(
                """
                UPDATE jobs
                SET status='done', finished_at=now(), updated_at=now(),
                    locked_by=NULL, locked_at=NULL, last_error=NULL
                WHERE id=%s AND source=%s
                """,
                (job_id, self._source),
            )
            conn.commit()

    def fail_job(
        self,
        job_id: int,
        attempts: int,
        max_attempts: int,
        err_msg: str,
        retry_delay: float,
    ) -> None:
        err_msg = truncate(err_msg, 1000)
        with self._pool.connection() as conn, conn.cursor() as cur:
            if attempts >= max_attempts:
                cur.execute(
                    """
                    UPDATE jobs
                    SET status='dead', last_error=%s, finished_at=now(), updated_at=now(),
                        locked_by=NULL, locked_at=NULL
                    WHERE id=%s AND source=%s
                    """,
                    (err_msg, job_id, self._source),
                )
            else:
                if retry_delay <= 0:
                    retry_delay = 15.0
                run_after = datetime.now(timezone.utc) + timedelta(seconds=retry_delay)
                cur.execute(
                    """
                    UPDATE jobs
                    SET status='pending', last_error=%s, run_after=%s, updated_at=now(),
                        locked_by=NULL, locked_at=NULL
                    WHERE id=%s AND source=%s
                    """,
                    (err_msg, run_after, job_id, self._source),
                )
            conn.commit()

    def defer_job(self, job_id: int, retry_delay: float, reason: str) -> None:
        if retry_delay <= 0:
            retry_delay = 60.0
        reason = truncate(reason, 1000)
        run_after = datetime.now(timezone.utc) + timedelta(seconds=retry_delay)
        with self._pool.connection() as conn, conn.cursor() as cur:
            cur.execute(
                """
                UPDATE jobs
                SET status='pending', attempts=GREATEST(attempts-1, 0), last_error=%s,
                    run_after=%s, updated_at=now(), locked_by=NULL, locked_at=NULL
                WHERE id=%s AND source=%s
                """,
                (reason, run_after, job_id, self._source),
            )
            conn.commit()

    def mark_exhausted_dead(self) -> int:
        with self._pool.connection() as conn, conn.cursor() as cur:
            cur.execute(
                """
                UPDATE jobs
                SET status='dead', finished_at=now(), updated_at=now(),
                    locked_by=NULL, locked_at=NULL,
                    last_error=COALESCE(last_error, 'maximum attempts exhausted')
                WHERE source=%s AND status='pending' AND attempts >= max_attempts
                """,
                (self._source,),
            )
            count = cur.rowcount
            conn.commit()
        return count

    def requeue_failed(self) -> int:
        with self._pool.connection() as conn, conn.cursor() as cur:
            cur.execute(
                """
                UPDATE jobs SET status='pending', updated_at=now()
                WHERE source=%s AND status='failed' AND run_after <= now()
                  AND attempts < max_attempts
                """,
                (self._source,),
            )
            count = cur.rowcount
            conn.commit()
        return count

    def reclaim_stale(self, after_seconds: float) -> int:
        with self._pool.connection() as conn, conn.cursor() as cur:
            cur.execute(
                """
                UPDATE jobs
                SET status='pending', locked_by=NULL, locked_at=NULL, updated_at=now(),
                    last_error = COALESCE(last_error,'') || ' | reclaimed stale lock'
                WHERE source=%s AND status='running'
                  AND locked_at < now() - make_interval(secs => %s)
                """,
                (self._source, after_seconds),
            )
            count = cur.rowcount
            conn.commit()
        return count

    def upsert_flights(self, flights: list[FlightRow]) -> int:
        if not flights:
            return 0
        total = 0
        with self._pool.connection() as conn, conn.cursor() as cur:
            for flight in flights:
                source = flight.source or self._source
                cur.execute(
                    UPSERT_FLIGHT_SQL,
                    (
                        flight.origin,
                        flight.destination,
                        flight.origin_query,
                        flight.destination_query,
                        flight.flight_date,
                        flight.airline_code,
                        flight.airline_name,
                        flight.flight_number,
                        flight.flight_number_norm,
                        flight.depart_at,
                        flight.arrive_at,
                        flight.depart_minute,
                        flight.arrive_minute,
                        flight.baggage_label,
                        flight.baggage_kg,
                        flight.price_rial,
                        flight.price_toman,
                        flight.is_charter,
                        flight.stops,
                        flight.duration_min,
                        flight.proposal_id,
                        source,
                    ),
                )
                total += cur.rowcount
            conn.commit()
        return total

    def log_run(
        self,
        job_id: int,
        origin: str,
        destination: str,
        day: date,
        request_id: str,
        upserted: int,
        duration_ms: int,
        ok: bool,
        err_msg: str,
    ) -> None:
        with self._pool.connection() as conn, conn.cursor() as cur:
            cur.execute(
                """
                INSERT INTO collection_runs (
                    source, job_id, origin, destination, flight_date, request_id,
                    flights_upserted, duration_ms, ok, error
                )
                VALUES (%s,%s,%s,%s,%s::date,%s,%s,%s,%s,NULLIF(%s,''))
                """,
                (
                    self._source,
                    job_id,
                    origin,
                    destination,
                    day,
                    request_id,
                    upserted,
                    duration_ms,
                    ok,
                    err_msg,
                ),
            )
            conn.commit()


def truncate(value: str, limit: int) -> str:
    if len(value) <= limit:
        return value
    return value[:limit]
