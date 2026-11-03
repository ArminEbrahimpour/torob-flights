-- Alibaba flight engine schema
-- Optimized for: high-throughput upserts + flight-number validation lookups

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ---------------------------------------------------------------------------
-- Reference data
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS airports (
    code            TEXT PRIMARY KEY,          -- IKA, IST, ISTALL, THRALL
    city_code       TEXT,                      -- THR, IST
    city_name_fa    TEXT,
    city_name_en    TEXT,
    country_code    TEXT,
    is_city_group   BOOLEAN NOT NULL DEFAULT FALSE, -- ISTALL / THRALL
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Metadata added idempotently for databases created by older versions.
ALTER TABLE airports ADD COLUMN IF NOT EXISTS airport_name_en TEXT;
ALTER TABLE airports ADD COLUMN IF NOT EXISTS is_international BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE airports ADD COLUMN IF NOT EXISTS has_scheduled_international BOOLEAN NOT NULL DEFAULT FALSE;

-- Map city-group codes to concrete airports for validation matching
CREATE TABLE IF NOT EXISTS airport_aliases (
    alias_code      TEXT NOT NULL REFERENCES airports(code) ON DELETE CASCADE,
    airport_code    TEXT NOT NULL REFERENCES airports(code) ON DELETE CASCADE,
    PRIMARY KEY (alias_code, airport_code)
);

CREATE TABLE IF NOT EXISTS routes (
    id              BIGSERIAL PRIMARY KEY,
    origin          TEXT NOT NULL REFERENCES airports(code),
    destination     TEXT NOT NULL REFERENCES airports(code),
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    priority        INT NOT NULL DEFAULT 100,          -- lower = higher priority
    refresh_minutes INT NOT NULL DEFAULT 360,          -- how often to recollect
    window_days     INT NOT NULL DEFAULT 30,           -- rolling forward window
    last_enqueued_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (origin, destination)
);

CREATE INDEX IF NOT EXISTS idx_routes_enabled_priority
    ON routes (enabled, priority) WHERE enabled;

-- ---------------------------------------------------------------------------
-- Job queue (Postgres SKIP LOCKED)
-- ---------------------------------------------------------------------------

DO $$ BEGIN
    CREATE TYPE job_status AS ENUM (
        'pending',
        'running',
        'done',
        'failed',
        'dead'
    );
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE job_kind AS ENUM (
        'seed_route',
        'collect_day',
        'publish_redis'
    );
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;

-- Upgrade existing databases created before Redis output jobs existed.
ALTER TYPE job_kind ADD VALUE IF NOT EXISTS 'publish_redis';

CREATE TABLE IF NOT EXISTS jobs (
    id              BIGSERIAL PRIMARY KEY,
    kind            job_kind NOT NULL,
    status          job_status NOT NULL DEFAULT 'pending',
    priority        INT NOT NULL DEFAULT 100,
    route_id        BIGINT REFERENCES routes(id) ON DELETE SET NULL,
    origin          TEXT NOT NULL,
    destination     TEXT NOT NULL,
    flight_date     DATE,                              -- null for seed_route
    attempts        INT NOT NULL DEFAULT 0,
    max_attempts    INT NOT NULL DEFAULT 5,
    run_after       TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_by       TEXT,
    locked_at       TIMESTAMPTZ,
    last_error      TEXT,
    payload         JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

-- Dedup: one pending/running collect_day per OD+date
CREATE UNIQUE INDEX IF NOT EXISTS uq_jobs_collect_day_active
    ON jobs (origin, destination, flight_date)
    WHERE kind = 'collect_day' AND status IN ('pending', 'running');

CREATE INDEX IF NOT EXISTS idx_jobs_claim
    ON jobs (status, run_after, priority, id)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_jobs_running_locked
    ON jobs (status, locked_at)
    WHERE status = 'running';

-- Idempotency for durable Redis publication jobs. Normal collection jobs have no event_id.
CREATE UNIQUE INDEX IF NOT EXISTS uq_jobs_payload_event_id
    ON jobs ((payload->>'event_id'))
    WHERE payload ? 'event_id';

-- ---------------------------------------------------------------------------
-- Collected flights (searchable / validation source of truth)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS flights (
    id                  BIGSERIAL PRIMARY KEY,
    origin              TEXT NOT NULL,                 -- concrete airport e.g. IKA
    destination         TEXT NOT NULL,                 -- concrete airport e.g. IST
    origin_query        TEXT NOT NULL,                 -- query used e.g. IKA / THRALL
    destination_query   TEXT NOT NULL,                 -- query used e.g. ISTALL
    flight_date         DATE NOT NULL,
    airline_code        TEXT NOT NULL,
    airline_name        TEXT NOT NULL DEFAULT '',
    flight_number       TEXT NOT NULL,                 -- raw from API e.g. 6617
    flight_number_norm  TEXT NOT NULL,                 -- normalized e.g. I36617 or 6617
    depart_at           TIMESTAMPTZ NOT NULL,
    arrive_at           TIMESTAMPTZ NOT NULL,
    depart_minute       INT NOT NULL,                   -- minutes from midnight local-ish
    arrive_minute       INT NOT NULL,
    baggage_label       TEXT NOT NULL DEFAULT '',
    baggage_kg          INT,
    price_rial          BIGINT NOT NULL DEFAULT 0,
    price_toman         BIGINT NOT NULL DEFAULT 0,
    is_charter          BOOLEAN NOT NULL DEFAULT FALSE,
    stops               INT NOT NULL DEFAULT 0,
    duration_min        INT,
    proposal_id         TEXT NOT NULL DEFAULT '',
    source              TEXT NOT NULL DEFAULT 'alibaba',
    collected_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Natural key for upsert (same offer refreshes in place)
CREATE UNIQUE INDEX IF NOT EXISTS uq_flights_natural
    ON flights (
        origin, destination, flight_date,
        airline_code, flight_number,
        depart_at, arrive_at, price_rial, is_charter
    );

-- Validation / search indexes
CREATE INDEX IF NOT EXISTS idx_flights_number_date
    ON flights (flight_number_norm, flight_date);

CREATE INDEX IF NOT EXISTS idx_flights_number_raw_date
    ON flights (flight_number, flight_date);

CREATE INDEX IF NOT EXISTS idx_flights_od_date
    ON flights (origin, destination, flight_date);

CREATE INDEX IF NOT EXISTS idx_flights_odq_date
    ON flights (origin_query, destination_query, flight_date);

CREATE INDEX IF NOT EXISTS idx_flights_depart
    ON flights (flight_date, depart_minute);

CREATE INDEX IF NOT EXISTS idx_flights_collected
    ON flights (collected_at DESC);

CREATE INDEX IF NOT EXISTS idx_flights_airline_date
    ON flights (airline_code, flight_date);

-- ---------------------------------------------------------------------------
-- Collection runs (observability)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS collection_runs (
    id              BIGSERIAL PRIMARY KEY,
    job_id          BIGINT REFERENCES jobs(id) ON DELETE SET NULL,
    origin          TEXT NOT NULL,
    destination     TEXT NOT NULL,
    flight_date     DATE NOT NULL,
    request_id      TEXT,
    flights_upserted INT NOT NULL DEFAULT 0,
    duration_ms     INT NOT NULL DEFAULT 0,
    ok              BOOLEAN NOT NULL DEFAULT TRUE,
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_runs_od_date ON collection_runs (origin, destination, flight_date, created_at DESC);

-- ---------------------------------------------------------------------------
-- International airport and route seed
-- ---------------------------------------------------------------------------
-- Schedule snapshot verified for July 2026. Airline schedules are volatile;
-- keep routes editable through the existing routes API/table. The seed includes
-- Iranian airports with scheduled nonstop international passenger service and
-- every scheduled international city pair published for those airports. The
-- source schedule contains 48 physical outbound routes to 27 foreign airports;
-- Istanbul's IST and SAW airports are represented by the existing ISTALL query
-- code, producing 43 collector query pairs. Dubai and Najaf also retain the
-- collector's existing city-group query codes. Reverse query pairs are seeded too.

INSERT INTO airports (
    code, city_code, city_name_en, city_name_fa, country_code,
    is_city_group, airport_name_en, is_international, has_scheduled_international
) VALUES
    -- Iran: scheduled international origins
    ('IKA', 'THR', 'Tehran',       'تهران',       'IR', FALSE, 'Imam Khomeini International Airport', TRUE, TRUE),
    ('MHD', 'MHD', 'Mashhad',      'مشهد',        'IR', FALSE, 'Mashhad International Airport', TRUE, TRUE),
    ('AWZ', 'AWZ', 'Ahwaz',        'اهواز',       'IR', FALSE, 'Ahwaz Airport', TRUE, TRUE),
    ('SYZ', 'SYZ', 'Shiraz',       'شیراز',       'IR', FALSE, 'Shiraz International Airport', TRUE, TRUE),
    ('TBZ', 'TBZ', 'Tabriz',       'تبریز',       'IR', FALSE, 'Tabriz International Airport', TRUE, TRUE),
    ('BND', 'BND', 'Bandar Abbas', 'بندرعباس',    'IR', FALSE, 'Bandar Abbas International Airport', TRUE, TRUE),
    ('IFN', 'IFN', 'Isfahan',      'اصفهان',      'IR', FALSE, 'Isfahan International Airport', TRUE, TRUE),
    ('ZBR', 'ZBR', 'Chabahar',     'چابهار',      'IR', FALSE, 'Konarak Airport', TRUE, TRUE),
    ('GBT', 'GBT', 'Gorgan',       'گرگان',       'IR', FALSE, 'Gorgan Airport', TRUE, TRUE),

    -- Existing Tehran domestic/city-group references
    ('THR',    'THR', 'Tehran',   'تهران',    'IR', FALSE, 'Mehrabad International Airport', TRUE, FALSE),
    ('THRALL', 'THR', 'Tehran',   'تهران',    'IR', TRUE,  'Tehran city group', TRUE, TRUE),

    -- Turkey
    ('IST',    'IST', 'Istanbul', 'استانبول', 'TR', FALSE, 'Istanbul Airport', TRUE, TRUE),
    ('SAW',    'IST', 'Istanbul', 'استانبول', 'TR', FALSE, 'Sabiha Gokcen International Airport', TRUE, TRUE),
    ('ISTALL', 'IST', 'Istanbul', 'استانبول', 'TR', TRUE,  'Istanbul city group', TRUE, TRUE),
    ('ESB',    'ANK', 'Ankara',   'آنکارا',   'TR', FALSE, 'Esenboga International Airport', TRUE, TRUE),
    ('COV',    'COV', 'Cukurova', NULL,       'TR', FALSE, 'Cukurova International Airport', TRUE, TRUE),
    ('AYT',    'AYT', 'Antalya',  'آنتالیا',  'TR', FALSE, 'Antalya Airport', TRUE, TRUE),
    ('AYTALL', 'AYT', 'Antalya',  'آنتالیا',  'TR', TRUE,  'Antalya city group', TRUE, TRUE),

    -- United Arab Emirates
    ('DXB',    'DXB', 'Dubai', 'دبی', 'AE', FALSE, 'Dubai International Airport', TRUE, TRUE),
    ('DXBALL', 'DXB', 'Dubai', 'دبی', 'AE', TRUE,  'Dubai city group', TRUE, TRUE),

    -- Iraq
    ('NJF',    'NJF', 'Najaf',        'نجف',      'IQ', FALSE, 'Al Najaf International Airport', TRUE, TRUE),
    ('NJFALL', 'NJF', 'Najaf',        'نجف',      'IQ', TRUE,  'Najaf city group', TRUE, TRUE),
    ('BGW',    'BGW', 'Baghdad',      'بغداد',    'IQ', FALSE, 'Baghdad International Airport', TRUE, TRUE),
    ('EBL',    'EBL', 'Erbil',        'اربیل',    'IQ', FALSE, 'Erbil International Airport', TRUE, TRUE),
    ('ISU',    'ISU', 'Sulaymaniyah', 'سلیمانیه', 'IQ', FALSE, 'Sulaymaniyah International Airport', TRUE, TRUE),

    -- Other scheduled international destinations
    ('DOH', 'DOH', 'Doha',                'دوحه',       'QA', FALSE, 'Hamad International Airport', TRUE, TRUE),
    ('KWI', 'KWI', 'Kuwait City',         'کویت',       'KW', FALSE, 'Kuwait International Airport', TRUE, TRUE),
    ('MCT', 'MCT', 'Muscat',              'مسقط',       'OM', FALSE, 'Muscat International Airport', TRUE, TRUE),
    ('GYD', 'BAK', 'Baku',                'باکو',       'AZ', FALSE, 'Heydar Aliyev International Airport', TRUE, TRUE),
    ('EVN', 'EVN', 'Yerevan',             'ایروان',     'AM', FALSE, 'Zvartnots International Airport', TRUE, TRUE),
    ('VIE', 'VIE', 'Vienna',              'وین',        'AT', FALSE, 'Vienna International Airport', TRUE, TRUE),
    ('FRA', 'FRA', 'Frankfurt',           'فرانکفورت',  'DE', FALSE, 'Frankfurt Airport', TRUE, TRUE),
    ('SVO', 'MOW', 'Moscow',              'مسکو',       'RU', FALSE, 'Sheremetyevo International Airport', TRUE, TRUE),
    ('DEL', 'DEL', 'New Delhi',           'دهلی نو',    'IN', FALSE, 'Indira Gandhi International Airport', TRUE, TRUE),
    ('LHE', 'LHE', 'Lahore',              'لاهور',      'PK', FALSE, 'Allama Iqbal International Airport', TRUE, TRUE),
    ('HKT', 'HKT', 'Phuket',              'پوکت',       'TH', FALSE, 'Phuket International Airport', TRUE, TRUE),
    ('SCO', 'SCO', 'Aktau',               'آکتائو',     'KZ', FALSE, 'Aktau International Airport', TRUE, TRUE),
    ('PEK', 'BJS', 'Beijing',             'پکن',        'CN', FALSE, 'Beijing Capital International Airport', TRUE, TRUE),
    ('PKX', 'BJS', 'Beijing',             'پکن',        'CN', FALSE, 'Beijing Daxing International Airport', TRUE, TRUE),
    ('CAN', 'CAN', 'Guangzhou',           'گوانگژو',    'CN', FALSE, 'Guangzhou Baiyun International Airport', TRUE, TRUE),
    ('PVG', 'SHA', 'Shanghai',            'شانگهای',    'CN', FALSE, 'Shanghai Pudong International Airport', TRUE, TRUE),
    ('SZX', 'SZX', 'Shenzhen',            'شنژن',       'CN', FALSE, 'Shenzhen Baoan International Airport', TRUE, TRUE),
    ('URC', 'URC', 'Urumqi',              'اورومچی',    'CN', FALSE, 'Urumqi Tianshan International Airport', TRUE, TRUE)
ON CONFLICT (code) DO UPDATE SET
    city_code = EXCLUDED.city_code,
    city_name_en = EXCLUDED.city_name_en,
    city_name_fa = COALESCE(EXCLUDED.city_name_fa, airports.city_name_fa),
    country_code = EXCLUDED.country_code,
    is_city_group = EXCLUDED.is_city_group,
    airport_name_en = EXCLUDED.airport_name_en,
    is_international = EXCLUDED.is_international,
    has_scheduled_international = EXCLUDED.has_scheduled_international;

INSERT INTO airport_aliases (alias_code, airport_code) VALUES
    ('THRALL', 'IKA'),
    ('THRALL', 'THR'),
    ('ISTALL', 'IST'),
    ('ISTALL', 'SAW'),
    ('DXBALL', 'DXB'),
    ('AYTALL', 'AYT'),
    ('NJFALL', 'NJF')
ON CONFLICT DO NOTHING;

-- One row represents a queryable city pair. The reverse direction is inserted
-- automatically because the collector must cover both departures from Iran and
-- international arrivals into Iran.
WITH international_pairs (iran_airport, foreign_airport, priority, refresh_minutes, window_days) AS (
    VALUES
        -- Tehran / IKA: all 26 scheduled concrete destinations, with Istanbul
        -- represented once by ISTALL.
        ('IKA', 'ISTALL', 10, 180, 30),
        ('IKA', 'DXBALL', 10, 180, 30),
        ('IKA', 'DOH',    10, 180, 30),
        ('IKA', 'NJFALL', 20, 240, 30),
        ('IKA', 'BGW',    20, 240, 30),
        ('IKA', 'KWI',    20, 240, 30),
        ('IKA', 'MCT',    20, 240, 30),
        ('IKA', 'GYD',    20, 240, 30),
        ('IKA', 'EVN',    20, 240, 30),
        ('IKA', 'ESB',    20, 240, 30),
        ('IKA', 'COV',    30, 360, 21),
        ('IKA', 'EBL',    30, 360, 21),
        ('IKA', 'ISU',    30, 360, 21),
        ('IKA', 'VIE',    30, 360, 30),
        ('IKA', 'FRA',    30, 360, 30),
        ('IKA', 'SVO',    30, 360, 30),
        ('IKA', 'DEL',    30, 360, 30),
        ('IKA', 'LHE',    30, 360, 30),
        ('IKA', 'HKT',    40, 720, 30),
        ('IKA', 'PEK',    40, 720, 30),
        ('IKA', 'PKX',    40, 720, 30),
        ('IKA', 'CAN',    40, 720, 30),
        ('IKA', 'PVG',    40, 720, 30),
        ('IKA', 'SZX',    40, 720, 30),
        ('IKA', 'URC',    40, 720, 30),

        -- Mashhad
        ('MHD', 'ISTALL', 20, 240, 30),
        ('MHD', 'DOH',    20, 240, 30),
        ('MHD', 'KWI',    20, 240, 30),
        ('MHD', 'NJFALL', 20, 240, 30),
        ('MHD', 'BGW',    30, 360, 21),

        -- Shiraz
        ('SYZ', 'ISTALL', 30, 360, 30),
        ('SYZ', 'DOH',    30, 360, 30),
        ('SYZ', 'MCT',    30, 360, 30),
        ('SYZ', 'NJFALL', 30, 360, 21),

        -- Tabriz
        ('TBZ', 'ISTALL', 30, 360, 30),
        ('TBZ', 'GYD',    30, 360, 30),

        -- Isfahan
        ('IFN', 'ISTALL', 30, 360, 30),
        ('IFN', 'NJFALL', 30, 360, 21),

        -- Ahwaz, Bandar Abbas, Chabahar and Gorgan
        ('AWZ', 'MCT',    40, 360, 21),
        ('AWZ', 'DXBALL', 40, 360, 21),
        ('BND', 'MCT',    40, 360, 21),
        ('ZBR', 'DXBALL', 50, 720, 21),
        ('GBT', 'SCO',    60, 720, 21)
), bidirectional_routes AS (
    SELECT iran_airport AS origin, foreign_airport AS destination,
           priority, refresh_minutes, window_days
    FROM international_pairs
    UNION ALL
    SELECT foreign_airport AS origin, iran_airport AS destination,
           priority, refresh_minutes, window_days
    FROM international_pairs
)
INSERT INTO routes (origin, destination, priority, refresh_minutes, window_days)
SELECT origin, destination, priority, refresh_minutes, window_days
FROM bidirectional_routes
ON CONFLICT (origin, destination) DO NOTHING;
