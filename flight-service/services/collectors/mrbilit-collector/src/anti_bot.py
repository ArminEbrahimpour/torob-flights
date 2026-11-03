from __future__ import annotations

import os
import time
import random
import threading
import logging
from datetime import datetime, timezone, timedelta
from typing import Dict, Any, Optional, List

logger = logging.getLogger(__name__)

# A curated pool of realistic browser headers with matching Client Hints (sec-ch-ua)
HEADERS_POOL = [
    # Chrome 120 on Windows
    {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
        "Accept": "application/json, text/plain, */*",
        "Accept-Language": "fa-IR,fa;q=0.9,en-US;q=0.8,en;q=0.7",
        "sec-ch-ua": '"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"',
        "sec-ch-ua-mobile": "?0",
        "sec-ch-ua-platform": '"Windows"',
        "sec-fetch-dest": "empty",
        "sec-fetch-mode": "cors",
        "sec-fetch-site": "same-origin",
    },
    # Edge 120 on Windows
    {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0",
        "Accept": "application/json, text/plain, */*",
        "Accept-Language": "fa-IR,fa;q=0.9,en-US;q=0.8,en;q=0.7",
        "sec-ch-ua": '"Not_A Brand";v="8", "Chromium";v="120", "Microsoft Edge";v="120"',
        "sec-ch-ua-mobile": "?0",
        "sec-ch-ua-platform": '"Windows"',
        "sec-fetch-dest": "empty",
        "sec-fetch-mode": "cors",
        "sec-fetch-site": "same-origin",
    },
    # Chrome 119 on macOS
    {
        "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
        "Accept": "application/json, text/plain, */*",
        "Accept-Language": "fa-IR,fa;q=0.9,en-US;q=0.8,en;q=0.7",
        "sec-ch-ua": '"Not_A Brand";v="8", "Chromium";v="119", "Google Chrome";v="119"',
        "sec-ch-ua-mobile": "?0",
        "sec-ch-ua-platform": '"macOS"',
        "sec-fetch-dest": "empty",
        "sec-fetch-mode": "cors",
        "sec-fetch-site": "same-origin",
    },
    # Safari 17 on macOS
    {
        "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
        "Accept": "application/json, text/plain, */*",
        "Accept-Language": "fa-IR,fa;q=0.9,en-US;q=0.8,en;q=0.7",
        "sec-fetch-dest": "empty",
        "sec-fetch-mode": "cors",
        "sec-fetch-site": "same-origin",
    }
]

REFERER_POOL = [
    "https://www.google.com/",
    "https://www.google.ir/",
    "https://yandex.com/",
    "https://safarmarket.com/",
    "https://mrbilit.com/",
    "https://www.bing.com/"
]


class HTTPError(Exception):
    def __init__(
        self,
        status_code: int,
        method: str,
        path: str,
        body: str,
        retry_after: float = 0.0,
    ) -> None:
        self.status_code = status_code
        self.method = method
        self.path = path
        self.body = body
        self.retry_after = retry_after
        super().__init__(
            f"HTTP {status_code} {method} {path}"
            + (f" (retry after {retry_after:.0f}s)" if retry_after else "")
            + f": {body[:400]}"
        )


class CircuitOpenError(Exception):
    def __init__(self, retry_after: float, open_until: datetime) -> None:
        self.retry_after = retry_after
        self.open_until = open_until
        super().__init__(
            f"Circuit breaker open until {open_until.isoformat()} "
            f"(retry after {retry_after:.0f}s)"
        )


def is_circuit_open(err: BaseException) -> bool:
    return isinstance(err, CircuitOpenError)


def is_rate_limited(err: BaseException) -> bool:
    if isinstance(err, HTTPError) and err.status_code in (429, 409, 403):
        return True
    return isinstance(err, CircuitOpenError)


def retry_after(err: BaseException) -> float:
    if isinstance(err, (HTTPError, CircuitOpenError)):
        return err.retry_after
    return 0.0


class RequestLimiter:
    def __init__(self, rps: float, burst: int) -> None:
        self._rps = max(rps, 0.01)
        self._burst = float(max(burst, 1))
        self._tokens = self._burst
        self._last_fill = time.monotonic()
        self._lock = threading.Lock()

    def wait(self) -> None:
        while True:
            with self._lock:
                now = time.monotonic()
                elapsed = now - self._last_fill
                if elapsed > 0:
                    self._tokens = min(self._burst, self._tokens + elapsed * self._rps)
                    self._last_fill = now
                if self._tokens >= 1:
                    self._tokens -= 1
                    return
                wait = (1 - self._tokens) / self._rps
            time.sleep(max(wait, 0.001))


class CircuitBreaker:
    def __init__(self, threshold: int, open_for: float) -> None:
        self._threshold = max(threshold, 1)
        self._open_for = max(open_for, 1.0)
        self._recent_429 = 0
        self._last_rate_limit = 0.0
        self._open_until = 0.0
        self._lock = threading.Lock()

    def before_request(self) -> None:
        with self._lock:
            now = time.monotonic()
            if self._open_until and now < self._open_until:
                remaining = self._open_until - now
                open_until_wall = datetime.now(timezone.utc) + timedelta(seconds=remaining)
                raise CircuitOpenError(remaining, open_until_wall)
            if self._open_until and now >= self._open_until:
                self._open_until = 0.0
                self._recent_429 = 0
                self._last_rate_limit = 0.0

    def record_rate_limit(self, retry_after: float) -> float:
        with self._lock:
            now = time.monotonic()
            if self._last_rate_limit and now - self._last_rate_limit > self._open_for:
                self._recent_429 = 0
            self._last_rate_limit = now
            self._recent_429 += 1
            if self._recent_429 < self._threshold:
                return self._open_until
            pause = max(self._open_for, retry_after)
            candidate = now + pause
            if candidate > self._open_until:
                self._open_until = candidate
            return self._open_until

    def record_success(self) -> None:
        with self._lock:
            now = time.monotonic()
            if self._last_rate_limit and now - self._last_rate_limit > self._open_for:
                self._recent_429 = 0
                self._last_rate_limit = 0.0


class AntiBotManager:
    def __init__(self, proxies_file: str | None = None) -> None:
        self.proxies_file = proxies_file or os.getenv("PROXY_LIST_FILE", "")
        self.proxies_list: List[str] = []
        self.load_proxies()

    def load_proxies(self) -> None:
        """Load proxies from the configured proxy list file if it exists."""
        if not self.proxies_file:
            return
        if not os.path.exists(self.proxies_file):
            return
        try:
            with open(self.proxies_file, "r", encoding="utf-8") as f:
                lines = [
                    line.strip()
                    for line in f
                    if line.strip() and not line.strip().startswith("#")
                ]
                self.proxies_list = lines
            if self.proxies_list:
                logger.info(
                    "Loaded %d proxies from %s",
                    len(self.proxies_list),
                    self.proxies_file,
                )
        except Exception as e:
            logger.error("Error loading proxies from %s: %s", self.proxies_file, e)

    def get_random_headers(self, origin_domain: str) -> Dict[str, str]:
        """Generate random realistic browser headers for a request."""
        headers = random.choice(HEADERS_POOL).copy()
        headers["Origin"] = origin_domain
        headers["Referer"] = random.choice(REFERER_POOL)
        return headers

    def get_random_proxy(self) -> Optional[Dict[str, str]]:
        """Get a random proxy formatted for curl_cffi / requests if available."""
        if not self.proxies_list:
            return None
        proxy_url = random.choice(self.proxies_list)
        return {
            "http": proxy_url,
            "https": proxy_url
        }
