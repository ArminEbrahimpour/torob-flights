from __future__ import annotations

import logging
import re
from dataclasses import dataclass
from datetime import date, datetime
from typing import List, Tuple

from curl_cffi import requests as cffi_requests

from anti_bot import AntiBotManager, CircuitBreaker, HTTPError, RequestLimiter
from jobs_store import FlightRow

logger = logging.getLogger(__name__)

SOURCE_MRBILIT = "mrbilit"
DIGITS_ONLY = re.compile(r"\D+")


@dataclass(frozen=True)
class SearchEndpoint:
    code: str


def expand_query_code(code: str) -> list[SearchEndpoint]:
    code = code.strip().upper()
    mapping = {
        "THRALL": ["IKA"],
        "ISTALL": ["IST", "SAW"],
        "DXBALL": ["DXB"],
        "NJFALL": ["NJF"],
        "AYTALL": ["AYT"],
    }
    airports = mapping.get(code, [code] if code else [])
    return [SearchEndpoint(code=airport) for airport in airports]


def normalize_flight_number(airline_code: str, flight_number: str) -> str:
    code = airline_code.strip().upper()
    raw = flight_number.strip().upper().rstrip(".")
    raw = raw.replace(" ", "")
    if not raw:
        return ""
    if code and raw.startswith(code):
        return raw
    digits = DIGITS_ONLY.sub("", raw)
    if code and digits:
        return f"{code}{digits}"
    return raw


class MrBilitClient:
    def __init__(
        self,
        *,
        adult: int = 1,
        flight_class: str = "Economy",
        rps: float = 0.2,
        burst: int = 1,
        http_timeout: float = 60.0,
        circuit_breaker_threshold: int = 3,
        circuit_breaker_open: float = 600.0,
        base_url: str = "https://flight.atighgasht.com",
    ) -> None:
        self._adult = max(adult, 1)
        self._class = "Economy" if flight_class.lower() == "economy" else "Business"
        self._base_url = base_url.rstrip("/")
        self._http_timeout = max(http_timeout, 5.0)
        self._limiter = RequestLimiter(rps, burst)
        self._breaker = CircuitBreaker(circuit_breaker_threshold, circuit_breaker_open)
        self.anti_bot = AntiBotManager()

    def close(self) -> None:
        pass

    def collect_day(
        self, origin_q: str, destination_q: str, day: date
    ) -> Tuple[str, List[FlightRow]]:
        origins = expand_query_code(origin_q)
        destinations = expand_query_code(destination_q)

        request_ids: list[str] = []
        flights: list[FlightRow] = []
        for origin in origins:
            for destination in destinations:
                if origin.code == destination.code:
                    continue
                rid, batch = self._search_concrete(origin_q, destination_q, origin.code, destination.code, day)
                request_ids.append(rid)
                flights.extend(batch)
        return ",".join(request_ids), dedupe_flights(flights)

    def _search_concrete(
        self,
        origin_q: str,
        destination_q: str,
        origin: str,
        destination: str,
        day: date,
    ) -> Tuple[str, List[FlightRow]]:
        self._breaker.before_request()
        self._limiter.wait()
        self._breaker.before_request()

        date_str = day.strftime("%Y-%m-%d")
        url = f"{self._base_url}/api/Flights"
        payload = {
            "AdultCount": self._adult,
            "ChildCount": 0,
            "InfantCount": 0,
            "CabinClass": "All",
            "Routes": [
                {
                    "OriginCode": origin,
                    "DestinationCode": destination,
                    "DepartureDate": date_str,
                }
            ],
            "Baggage": True,
            "IncludeFlightsWithHigherCapacity": False,
        }

        headers = self.anti_bot.get_random_headers("https://mrbilit.com")
        headers["Content-Type"] = "application/json-patch+json"
        proxy = self.anti_bot.get_random_proxy()

        session = cffi_requests.Session(impersonate="chrome131")
        if proxy:
            session.proxies = proxy

        try:
            response = session.post(
                url,
                json=payload,
                headers=headers,
                timeout=self._http_timeout,
            )
            if response.status_code >= 400:
                retry_after = 120.0 if response.status_code in (429, 409, 403) else 0.0
                if response.status_code in (429, 409, 403):
                    self._breaker.record_rate_limit(retry_after)
                raise HTTPError(
                    response.status_code,
                    "POST",
                    "/api/Flights",
                    response.text,
                    retry_after,
                )

            self._breaker.record_success()
            data = response.json()
            raw_flights = data.get("Flights") or []
            flights: list[FlightRow] = []
            for item in raw_flights:
                mapped = self._map_flight(item, origin_q, destination_q, day)
                if mapped is not None:
                    flights.append(mapped)
            return f"mrbilit_{origin}_{destination}_{date_str}", flights
        except HTTPError:
            raise
        except Exception as exc:
            raise RuntimeError(f"mrbilit search failed: {exc}") from exc

    def _map_flight(
        self, item: dict, origin_q: str, destination_q: str, day: date
    ) -> FlightRow | None:
        segments = item.get("Segments") or []
        prices = item.get("Prices") or []
        if not segments or not prices:
            return None

        segment = segments[0]
        legs = segment.get("Legs") or []
        if not legs:
            return None
        leg = legs[0]

        price_info = prices[0]
        baggage_kg = price_info.get("Baggage")
        is_charter = price_info.get("IsCharter", False)

        pax_fares = price_info.get("PassengerFares") or []
        adult_fare = next(
            (p for p in pax_fares if p.get("PaxType") == "ADL"),
            pax_fares[0] if pax_fares else {},
        )
        price_rial = int(adult_fare.get("TotalFare", 0) or 0)
        price_toman = price_rial // 10

        dep_dt = self._parse_iso_datetime(leg.get("DepartureTime", ""))
        arr_dt = self._parse_iso_datetime(leg.get("ArrivalTime", ""))

        airline_code = str(leg.get("AirlineCode", "")).strip().upper()
        raw_fn = str(leg.get("FlightNumber", "")).strip()
        airline_info = leg.get("Airline") or {}
        airline_name = str(airline_info.get("EnglishTitle", "")).strip()

        origin_code = str(leg.get("OriginCode", origin_q)).strip().upper()
        dest_code = str(leg.get("DestinationCode", destination_q)).strip().upper()

        duration_min = self._duration_to_minutes(segment.get("TotalTime", ""))
        stops = int(leg.get("Stops", 0) or 0)
        tech_stops = leg.get("TechnicalStops") or []
        if tech_stops:
            stops = max(stops, len(tech_stops))

        baggage_label = f"{baggage_kg} KG" if baggage_kg else ""

        return FlightRow(
            origin=origin_code,
            destination=dest_code,
            origin_query=origin_q.strip().upper(),
            destination_query=destination_q.strip().upper(),
            flight_date=day,
            airline_code=airline_code,
            airline_name=airline_name,
            flight_number=raw_fn,
            flight_number_norm=normalize_flight_number(airline_code, raw_fn),
            depart_at=dep_dt,
            arrive_at=arr_dt,
            depart_minute=dep_dt.hour * 60 + dep_dt.minute,
            arrive_minute=arr_dt.hour * 60 + arr_dt.minute,
            baggage_label=baggage_label,
            baggage_kg=int(baggage_kg) if baggage_kg else None,
            price_rial=price_rial,
            price_toman=price_toman,
            is_charter=bool(is_charter),
            stops=stops,
            duration_min=duration_min,
            proposal_id=f"mrbilit_{item.get('Id', '')}",
            source=SOURCE_MRBILIT,
        )

    def _parse_iso_datetime(self, value: str) -> datetime:
        if not value:
            return datetime.now()
        value = value.replace("T", " ")
        for fmt in ("%Y-%m-%d %H:%M:%S", "%Y-%m-%d %H:%M", "%Y-%m-%dT%H:%M:%S"):
            try:
                return datetime.strptime(value[:19], fmt)
            except ValueError:
                continue
        return datetime.now()

    def _duration_to_minutes(self, value: str) -> int:
        if not value:
            return 0
        matches = re.findall(r"(\d+)\s*(h|m|hour|minute|ساعت|دقیقه)", value)
        minutes = 0
        for amount, unit in matches:
            amount = int(amount)
            if unit in ("h", "hour", "ساعت"):
                minutes += amount * 60
            else:
                minutes += amount
        return minutes


def dedupe_flights(flights: list[FlightRow]) -> list[FlightRow]:
    seen: set[str] = set()
    out: list[FlightRow] = []
    for flight in flights:
        key = "|".join(
            [
                flight.origin,
                flight.destination,
                flight.flight_date.isoformat(),
                flight.airline_code,
                flight.flight_number,
                flight.depart_at.isoformat(),
                flight.arrive_at.isoformat(),
            ]
        )
        if key in seen:
            continue
        seen.add(key)
        out.append(flight)
    return out
