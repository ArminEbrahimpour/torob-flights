from __future__ import annotations

import json
import logging
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Callable

from psycopg_pool import ConnectionPool

logger = logging.getLogger(__name__)


def start_health_server(
    addr: str,
    pool: ConnectionPool,
    service_name: str = "collector",
    ready_check: Callable[[], bool] | None = None,
) -> ThreadingHTTPServer:
    host, port = parse_listen_addr(addr)

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, format: str, *args) -> None:  # noqa: A003
            logger.debug("health %s", format % args)

        def do_GET(self) -> None:  # noqa: N802
            if self.path == "/health/live":
                self._write_json(200, {"status": "ok", "service": f"{service_name}-collector"})
                return
            if self.path == "/health/ready":
                if ready_check is not None and not ready_check():
                    self._write_text(503, "migrations pending")
                    return
                try:
                    with pool.connection() as conn, conn.cursor() as cur:
                        cur.execute("SELECT 1")
                except Exception as exc:  # noqa: BLE001
                    self._write_text(503, f"postgres unavailable: {exc}")
                    return
                self._write_json(200, {"status": "ready"})
                return
            self._write_text(404, "not found")

        def _write_json(self, status: int, payload: dict) -> None:
            body = json.dumps(payload).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def _write_text(self, status: int, message: str) -> None:
            body = message.encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    server = ThreadingHTTPServer((host, port), Handler)
    thread = threading.Thread(target=server.serve_forever, name="health-server", daemon=True)
    thread.start()
    logger.info("health server listening on %s:%s", host, port)
    return server


def parse_listen_addr(addr: str) -> tuple[str, int]:
    addr = addr.strip()
    if addr.startswith(":"):
        return "", int(addr[1:])
    if ":" not in addr:
        return "", int(addr)
    host, port_text = addr.rsplit(":", 1)
    return host, int(port_text)
