-- Domestic Iranian passenger network.
-- This migration is intentionally separate from 001 so existing deployments
-- can add domestic coverage without rebuilding the core schema.

INSERT INTO airports (code, city_code, city_name_en, city_name_fa, country_code,
                      is_city_group, airport_name_en, is_international,
                      has_scheduled_international)
VALUES
    ('ABD', 'ABD', 'Abadan',       'آبادان',       'IR', FALSE, 'Abadan Airport', FALSE, FALSE),
    ('ADU', 'ADU', 'Ardabil',      'اردبیل',       'IR', FALSE, 'Ardabil Airport', FALSE, FALSE),
    ('AZD', 'AZD', 'Yazd',         'یزد',          'IR', FALSE, 'Yazd Shahid Sadooghi Airport', FALSE, FALSE),
    ('BUZ', 'BUZ', 'Bushehr',      'بوشهر',        'IR', FALSE, 'Bushehr Airport', FALSE, FALSE),
    ('DEF', 'DEF', 'Dezful',       'دزفول',        'IR', FALSE, 'Dezful Airport', FALSE, FALSE),
    ('GCH', 'GCH', 'Gachsaran',    'گچساران',      'IR', FALSE, 'Gachsaran Airport', FALSE, FALSE),
    ('GSM', 'GSM', 'Qeshm',        'قشم',          'IR', FALSE, 'Qeshm International Airport', FALSE, FALSE),
    ('IIL', 'IIL', 'Ilam',         'ایلام',        'IR', FALSE, 'Ilam Airport', FALSE, FALSE),
    ('KER', 'KER', 'Kerman',       'کرمان',        'IR', FALSE, 'Kerman Airport', FALSE, FALSE),
    ('KIH', 'KIH', 'Kish Island',  'کیش',          'IR', FALSE, 'Kish International Airport', FALSE, FALSE),
    ('KSH', 'KSH', 'Kermanshah',   'کرمانشاه',     'IR', FALSE, 'Kermanshah Airport', FALSE, FALSE),
    ('OMH', 'OMH', 'Urmia',        'ارومیه',       'IR', FALSE, 'Urmia Airport', FALSE, FALSE),
    ('PGU', 'PGU', 'Asalouyeh',    'عسلویه',       'IR', FALSE, 'Persian Gulf Airport', FALSE, FALSE),
    ('AJK', 'AJK', 'Arak',         'اراک',         'IR', FALSE, 'Arak Airport', FALSE, FALSE),
    ('KKS', 'KKS', 'Kashan',       'کاشان',        'IR', FALSE, 'Kashan Airport', FALSE, FALSE),
    ('KHK', 'KHK', 'Kharg Island', 'خارک',         'IR', FALSE, 'Kharg Airport', FALSE, FALSE),
    ('JSK', 'JSK', 'Jask',         'جاسک',         'IR', FALSE, 'Jask Airport', FALSE, FALSE),
    ('NSH', 'NSH', 'Noshahr',      'نوشهر',        'IR', FALSE, 'Noshahr Airport', FALSE, FALSE),
    ('RFS', 'RFS', 'Rafsanjan',    'رفسنجان',      'IR', FALSE, 'Rafsanjan Airport', FALSE, FALSE),
    ('RAS', 'RAS', 'Rasht',        'رشت',          'IR', FALSE, 'Rasht Airport', FALSE, FALSE),
    ('RZR', 'RZR', 'Ramsar',       'رامسر',        'IR', FALSE, 'Ramsar Airport', FALSE, FALSE),
    ('SRY', 'SRY', 'Sari',         'ساری',         'IR', FALSE, 'Dasht-e Naz Airport', FALSE, FALSE),
    ('SYJ', 'SYJ', 'Sirjan',       'سیرجان',       'IR', FALSE, 'Sirjan Airport', FALSE, FALSE),
    ('TCX', 'TCX', 'Tabas',        'طبس',          'IR', FALSE, 'Tabas Airport', FALSE, FALSE),
    ('ZAH', 'ZAH', 'Zahedan',      'زاهدان',       'IR', FALSE, 'Zahedan International Airport', FALSE, FALSE)
ON CONFLICT (code) DO UPDATE SET
    city_code = EXCLUDED.city_code,
    city_name_en = EXCLUDED.city_name_en,
    city_name_fa = COALESCE(EXCLUDED.city_name_fa, airports.city_name_fa),
    airport_name_en = EXCLUDED.airport_name_en,
    country_code = EXCLUDED.country_code,
    is_international = EXCLUDED.is_international,
    has_scheduled_international = EXCLUDED.has_scheduled_international;

-- Mehrabad is the domestic hub. The seed includes the currently scheduled
-- destinations plus the domestic regional airports commonly exposed by
-- Iranian ticket providers; collectors simply return no fares when a seasonal
-- route is not operating.
WITH hub_routes(destination) AS (VALUES
    ('ABD'), ('ADU'), ('AWZ'), ('AZD'), ('BUZ'), ('BND'), ('DEF'),
    ('GCH'), ('GBT'), ('GSM'), ('IIL'), ('IFN'), ('KER'), ('KIH'),
    ('KSH'), ('MHD'), ('OMH'), ('PGU'), ('RFS'), ('RAS'), ('RZR'),
    ('SRY'), ('SYJ'), ('AJK'), ('KKS'), ('KHK'), ('JSK'), ('NSH'),
    ('SYZ'), ('TCX'), ('TBZ'), ('ZAH'), ('ZBR')
), domestic_pairs(origin, destination, priority, refresh_minutes, window_days) AS (
    SELECT 'THR', destination, 10, 30, 30 FROM hub_routes
    UNION ALL
    SELECT destination, 'THR', 10, 30, 30 FROM hub_routes
    UNION ALL
    VALUES
        ('MHD', 'ABD', 20, 45, 30), ('MHD', 'AWZ', 20, 45, 30),
        ('MHD', 'AJK', 20, 45, 30), ('MHD', 'ADU', 20, 45, 30),
        ('MHD', 'PGU', 20, 45, 30), ('MHD', 'BND', 20, 45, 30),
        ('MHD', 'ZBR', 20, 45, 30), ('MHD', 'IFN', 20, 45, 30),
        ('MHD', 'KKS', 20, 45, 30), ('MHD', 'KIH', 20, 45, 30),
        ('MHD', 'NSH', 20, 45, 30), ('MHD', 'RZR', 20, 45, 30),
        ('MHD', 'RAS', 20, 45, 30), ('MHD', 'SRY', 20, 45, 30),
        ('MHD', 'TBZ', 20, 45, 30), ('MHD', 'AZD', 20, 45, 30),
        ('MHD', 'ZAH', 20, 45, 30),
        ('AWZ', 'BND', 30, 60, 30), ('AWZ', 'IFN', 30, 60, 30),
        ('AWZ', 'KHK', 30, 60, 30), ('AWZ', 'RFS', 30, 60, 30),
        ('AWZ', 'RAS', 30, 60, 30), ('AWZ', 'SYJ', 30, 60, 30),
        ('SYZ', 'PGU', 30, 60, 30), ('SYZ', 'ZBR', 30, 60, 30),
        ('SYZ', 'JSK', 30, 60, 30), ('SYZ', 'RAS', 30, 60, 30),
        ('SYZ', 'SRY', 30, 60, 30), ('SYZ', 'TBZ', 30, 60, 30),
        ('TBZ', 'AWZ', 30, 60, 30), ('TBZ', 'PGU', 30, 60, 30),
        ('TBZ', 'KIH', 30, 60, 30), ('KIH', 'ABD', 30, 60, 30),
        ('KIH', 'KKS', 30, 60, 30), ('IFN', 'PGU', 30, 60, 30),
        ('BND', 'RAS', 30, 60, 30)
), bidirectional AS (
    SELECT * FROM domestic_pairs
    UNION ALL
    SELECT destination, origin, priority, refresh_minutes, window_days
    FROM domestic_pairs
)
INSERT INTO routes (origin, destination, priority, refresh_minutes, window_days)
-- De-duplicate the seed before the upsert. A route can be present in both
-- the hub seed and an explicit city-pair seed (or be listed in both
-- directions); PostgreSQL rejects multiple input rows targeting the same
-- ON CONFLICT row in one statement.
SELECT origin,
       destination,
       MIN(priority) AS priority,
       MIN(refresh_minutes) AS refresh_minutes,
       MAX(window_days) AS window_days
FROM bidirectional
WHERE origin <> destination
GROUP BY origin, destination
ON CONFLICT (origin, destination) DO UPDATE SET
    priority = LEAST(routes.priority, EXCLUDED.priority),
    refresh_minutes = LEAST(routes.refresh_minutes, EXCLUDED.refresh_minutes),
    window_days = GREATEST(routes.window_days, EXCLUDED.window_days),
    enabled = TRUE;
