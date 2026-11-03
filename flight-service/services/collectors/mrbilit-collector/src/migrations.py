from __future__ import annotations

import logging
import os

from psycopg_pool import ConnectionPool

logger = logging.getLogger(__name__)

SCHEMA_MIGRATIONS_DDL = """
CREATE TABLE IF NOT EXISTS schema_migrations (
    name TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
"""


def run_migrations(pool: ConnectionPool, paths: list[str]) -> None:
    for path in paths:
        if not path or not os.path.exists(path):
            continue
        apply_migration(pool, path)


def apply_migration(pool: ConnectionPool, path: str) -> None:
    name = os.path.basename(path)
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute(SCHEMA_MIGRATIONS_DDL)

        cur.execute("SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = %s)", (name,))
        if cur.fetchone()[0]:
            logger.debug("migration already applied: %s", name)
            conn.commit()
            return

        if _migration_applied_in_schema(cur, name):
            logger.info("migration bootstrapped from existing schema: %s", name)
            cur.execute(
                "INSERT INTO schema_migrations (name) VALUES (%s) ON CONFLICT DO NOTHING",
                (name,),
            )
            conn.commit()
            return

        logger.info("applying migration %s", name)
        with open(path, encoding="utf-8") as handle:
            sql = handle.read()
        cur.execute(sql)
        cur.execute("INSERT INTO schema_migrations (name) VALUES (%s)", (name,))
        conn.commit()
        logger.info("migration applied: %s", name)


def _migration_applied_in_schema(cur, name: str) -> bool:
    if name == "001_flights_core.sql":
        return _table_exists(cur, "jobs")
    if name == "002_multi_collector.sql":
        return _column_exists(cur, "jobs", "source")
    if name == "004_maintenance_indexes.sql":
        return _index_exists(cur, "idx_collection_runs_created_at")
    return False


def _index_exists(cur, index: str) -> bool:
    cur.execute(
        """
        SELECT EXISTS (
            SELECT 1
            FROM pg_indexes
            WHERE schemaname = 'public' AND indexname = %s
        )
        """,
        (index,),
    )
    return bool(cur.fetchone()[0])


def _table_exists(cur, table: str) -> bool:
    cur.execute(
        """
        SELECT EXISTS (
            SELECT 1
            FROM information_schema.tables
            WHERE table_schema = 'public' AND table_name = %s
        )
        """,
        (table,),
    )
    return bool(cur.fetchone()[0])


def _column_exists(cur, table: str, column: str) -> bool:
    cur.execute(
        """
        SELECT EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = %s AND column_name = %s
        )
        """,
        (table, column),
    )
    return bool(cur.fetchone()[0])
