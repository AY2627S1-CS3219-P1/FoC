-- Opt-in UI scenarios, isolated by fixed UUIDs and [Demo] labels.
-- Do not add these workflow states to production migrations or the campus CSV importer.
BEGIN;
SET LOCAL lock_timeout = '5s';
SELECT pg_advisory_xact_lock(18003219, 2);
CREATE TEMP TABLE demo_options ON COMMIT DROP AS SELECT :'reset_demo'::boolean AS reset;
CREATE FUNCTION pg_temp.demo_id(kind INTEGER, n INTEGER) RETURNS UUID LANGUAGE SQL IMMUTABLE AS
$$ SELECT ('f0c0000' || kind || '-0000-4000-8000-' || lpad(n::text,12,'0'))::uuid $$;

-- Real venue names; map pins are approximate building centres, not storefront surveys.
-- Sources and weekday/term-time caveats: data/demo/README.md.
CREATE TEMP TABLE demo_buildings (
    n INTEGER PRIMARY KEY, name TEXT, lat DOUBLE PRECISION, lon DOUBLE PRECISION
) ON COMMIT DROP;
INSERT INTO demo_buildings VALUES
    (1,'Hon Sui Sen Memorial Library',1.293126,103.771994),
    (2,'Prince George''s Park',1.290888,103.777828),
    (3,'Central Library',1.296262,103.773029),
    (4,'Yusof Ishak House',1.298411,103.774554),
    (5,'COM2',1.293656,103.774299),
    (6,'AS8',1.296252,103.772093),
    (7,'COM3',1.294831,103.771631),
    (8,'The Ridge',1.294678,103.770787),
    (9,'NUS College (16 College Avenue West)',1.3065,103.7735),
    (10,'C J Koh Law Library (20 College Avenue West)',1.3065,103.7710),
    (11,'Education Resource Centre',1.3065,103.7730),
    (12,'Engineering Block E4',1.299152,103.769064),
    (13,'Frontier',1.294782,103.770444),
    (14,'Innovation 4.0',1.294298,103.770881),
    (15,'Medicine+Science Library',1.296799,103.779434),
    (16,'Engineering Block EA',1.300567,103.770758),
    (17,'Town Plaza',1.3045,103.7730),
    (18,'Stephen Riady Centre',1.3040,103.7737);
INSERT INTO buildings(id,name,center,radius_m)
SELECT pg_temp.demo_id(2,n),'[Demo] ' || name,ST_SetSRID(ST_MakePoint(lon,lat),4326)::geography,150
FROM demo_buildings
ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,center=EXCLUDED.center,radius_m=EXCLUDED.radius_m
WHERE (SELECT reset FROM demo_options);
INSERT INTO categories(id,name) VALUES
    (pg_temp.demo_id(3,1),'[Demo] Food'),(pg_temp.demo_id(3,2),'[Demo] Drinks'),
    (pg_temp.demo_id(3,3),'[Demo] Dessert'),(pg_temp.demo_id(3,4),'[Demo] Coffee')
ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name WHERE (SELECT reset FROM demo_options);

CREATE TEMP TABLE demo_venues (
    n INTEGER PRIMARY KEY, name TEXT, building_n INTEGER, category_n INTEGER,
    floor TEXT, open_from TIME, open_to TIME, contact TEXT, notes TEXT
) ON COMMIT DROP;
-- NULL category means an ordinary campus location. NULL hours mean unspecified,
-- never closed. Only representative term-time weekday hours are stored.
INSERT INTO demo_venues VALUES
    (1,'TOMORO COFFEE - HSSML',1,4,'2','08:15','18:00',NULL,'Weekdays in term; vacation 08:00–17:00; weekends/PH closed.'),
    (2,'Supersnacks - PGP',2,1,NULL,'11:00','02:00',NULL,'Weekdays; weekends/PH 18:00–02:00. Overnight closing time is the next day.'),
    (3,'Central Library',3,NULL,NULL,NULL,NULL,NULL,''),
    (4,'CHAGEE',4,2,'1','10:30','20:30',NULL,'Daily hours.'),
    (5,'Yusof Ishak House',4,NULL,NULL,NULL,NULL,NULL,''),
    (6,'Cool Spot',5,1,NULL,'07:30','19:30',NULL,'Daily during term; vacation closes at 18:00.'),
    (7,'The Coffee Roaster',6,4,NULL,'08:00','17:30',NULL,'Term-time weekdays; vacation 08:30–17:00; weekends/PH closed.'),
    (8,'The Terrace',7,1,NULL,'07:30','19:00',NULL,'Monday–Saturday; Sunday closed. Individual stall hours vary.'),
    (9,'Bakehaus / Aurea',8,1,NULL,'08:00','21:00',NULL,'Daily hours.'),
    (10,'TOMORO COFFEE',9,4,NULL,'08:00','18:00',NULL,'NUS College outlet, 16 College Avenue West. Saturday 08:00–13:00; Sunday/PH closed.'),
    (11,'TOMORO COFFEE',10,4,'02-402',NULL,NULL,NULL,'New outlet outside NUS Law Library, 20 College Avenue West, Singapore 138529. NUS Foodies announced its 10–14 August 2026 opening promotion. Hours not verified; deliberately unspecified.'),
    (12,'Starbucks - ERC',11,4,NULL,NULL,NULL,'6910 1127','Official directory lists 24/7 during term and 07:30–21:00 in vacation. The single unequal time-pair model cannot represent 24 hours; times deliberately unspecified.'),
    (13,'Hon Sui Sen Memorial Library',1,NULL,NULL,NULL,NULL,'6516 3131','1 Hon Sui Sen Drive, Singapore 117588. See the library website for date-specific access hours.'),
    (14,'COM2',5,NULL,NULL,NULL,NULL,NULL,''),
    (15,'Central Square @ YIH',4,1,NULL,'08:00','20:00',NULL,'Weekdays; Saturday 08:30–14:30. Individual stall hours vary.'),
    (16,'Arise and Shine',12,4,NULL,'08:00','18:00',NULL,'Term-time weekdays; Saturday 08:30–15:30; Sunday/PH closed.'),
    (17,'Pasta Express',13,1,NULL,'09:30','19:30','9025 8148','Term-time weekdays; Saturday 09:30–13:30; Sunday/PH closed. Vacation weekday opening 10:00.'),
    (18,'Nami',14,4,NULL,'08:00','17:30',NULL,'Term-time weekdays; vacation 08:30–17:00; weekends/PH closed.'),
    (19,'Good Day Cafe',15,4,NULL,'07:30','18:30',NULL,'Weekdays; weekends/PH closed.'),
    (20,'He by He Brews',16,4,NULL,'08:00','17:00',NULL,'Weekdays; weekends/PH closed.'),
    (21,'Li Ji Coffeehouse',13,1,NULL,'07:00','19:30',NULL,'Weekdays; Saturday 07:00–15:00; Sunday/PH closed.'),
    (22,'Mixue',13,3,NULL,'10:00','19:00',NULL,'Weekdays; weekends/PH closed.'),
    (23,'Mini Wok & Dim Sum',4,1,'1','10:00','21:00',NULL,'Daily hours; closing time is last order.'),
    (24,'Fine Food',17,1,NULL,'08:00','20:30',NULL,'Daily hours. Individual stall hours vary.'),
    (25,'Flavours @ UTown',18,1,NULL,'07:30','20:30',NULL,'Daily hours. Individual stall hours vary.'),
    (26,'Hwang’s Korean Restaurant',17,1,NULL,'10:30','21:00','9833 0603','Monday–Saturday; Sunday closed.'),
    (27,'Jollibee',17,1,NULL,'09:00','21:00',NULL,'Daily hours.'),
    (28,'Subway',18,1,NULL,'08:30','21:30',NULL,'Daily hours.'),
    (29,'The Royals Bistro',17,1,NULL,'11:00','20:00','9777 1353','Monday–Saturday; Sunday/PH closed.'),
    (30,'Stephen Riady Centre',18,NULL,NULL,NULL,NULL,NULL,''),
    (31,'The Coffee Bean and Tea Leaf',7,4,NULL,'09:00','18:00',NULL,'Term-time weekdays; weekends/PH and vacation closed.'),
    (32,'Uncle Penyet Fusion',13,1,NULL,'07:00','19:30',NULL,'Weekdays; Saturday 07:00–15:00; Sunday/PH closed.'),
    (33,'C J Koh Law Library',10,NULL,NULL,NULL,NULL,NULL,'20 College Avenue West. See the library website for current access hours.'),
    (34,'Smooy',7,3,NULL,'11:00','21:00',NULL,'Term-time weekdays; weekends/PH and vacation closed.');

CREATE TEMP TABLE demo_locations ON COMMIT DROP AS
SELECT v.n,pg_temp.demo_id(4,v.n) AS id,'[Demo] ' || v.name AS name,
    v.category_n IS NOT NULL AS is_supplier,pg_temp.demo_id(2,v.building_n) AS building_id,
    v.category_n,v.floor,ST_SetSRID(ST_MakePoint(b.lon,b.lat),4326)::geography AS coordinates,
    v.open_from,v.open_to,v.contact,
    'SIMULATED TEST RECORD. Archive/review/closure states are fictional. Approximate building-centre map pin. '
        || v.notes || E'\nSources: data/demo/README.md.'
        || CASE WHEN v.n=9 THEN E'\nUI-only sample directions: 咖啡 ☕\n' || repeat('Sample text for checking long detail wrapping. ',25) ELSE '' END AS details,
    CASE WHEN v.n IN (4,5,33) THEN now()-interval '7 days' ELSE NULL END AS archived_at
FROM demo_venues v JOIN demo_buildings b ON b.n=v.building_n;
CREATE TEMP TABLE demo_written_locations(id UUID PRIMARY KEY) ON COMMIT DROP;
WITH written AS (
    INSERT INTO locations(id,name,is_supplier,building_id,floor,coordinates,open_from,open_to,contact,details,archived_at,revision,created_at,updated_at)
    SELECT id,name,is_supplier,building_id,floor,coordinates,open_from,open_to,contact,details,archived_at,1,now()-interval '30 days',now()-interval '2 days'
    FROM demo_locations
    ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,is_supplier=EXCLUDED.is_supplier,building_id=EXCLUDED.building_id,
        floor=EXCLUDED.floor,coordinates=EXCLUDED.coordinates,open_from=EXCLUDED.open_from,open_to=EXCLUDED.open_to,
        contact=EXCLUDED.contact,details=EXCLUDED.details,archived_at=EXCLUDED.archived_at,revision=locations.revision+1
    WHERE (SELECT reset FROM demo_options)
    RETURNING id
) INSERT INTO demo_written_locations SELECT id FROM written;
DELETE FROM location_categories WHERE location_id IN (SELECT id FROM demo_written_locations);
INSERT INTO location_categories(location_id,category_id)
SELECT l.id,pg_temp.demo_id(3,l.category_n) FROM demo_locations l
JOIN demo_written_locations w USING(id) WHERE l.is_supplier;
INSERT INTO location_categories(location_id,category_id)
SELECT pg_temp.demo_id(4,1),pg_temp.demo_id(3,1)
WHERE EXISTS(SELECT 1 FROM demo_written_locations WHERE id=pg_temp.demo_id(4,1))
ON CONFLICT DO NOTHING;

-- 24 pending proposals make the default review queue span two pages.
CREATE TEMP TABLE demo_requests ON COMMIT DROP AS
SELECT series.n,pg_temp.demo_id(5,series.n) AS id,
    pg_temp.demo_id(1,CASE WHEN series.n%2=1 THEN 3 ELSE 5 END)::text AS submitted_by,
    CASE WHEN series.n<=24 THEN 'pending' WHEN series.n<=28 THEN 'approved' WHEN series.n<=31 THEN 'rejected' ELSE 'withdrawn' END AS status,
    CASE WHEN series.n BETWEEN 25 AND 28 THEN pg_temp.demo_id(4,series.n+6) ELSE NULL END AS resulting_location_id,
    l.name,l.is_supplier,l.building_id,l.floor,l.coordinates,l.open_from,l.open_to,l.contact,
    l.category_n,l.details || E'\nSimulated proposal ' || series.n || '.'
        || CASE WHEN series.n=4 THEN ' Open in two admin sessions and decide in one before confirming in the other to test a stale revision.' ELSE '' END AS details
FROM generate_series(1,34) AS series(n) JOIN demo_locations l ON l.n=CASE WHEN series.n BETWEEN 25 AND 28 THEN series.n+6 ELSE series.n END;
CREATE TEMP TABLE demo_written_requests(id UUID PRIMARY KEY) ON COMMIT DROP;
WITH written AS (
    INSERT INTO location_addition_requests(id,submitted_by,name,is_supplier,building_id,floor,coordinates,open_from,open_to,contact,details,status,reviewed_by,reviewed_at,review_note,resulting_location_id,revision,created_at,updated_at)
    SELECT id,submitted_by,name,is_supplier,building_id,floor,coordinates,open_from,open_to,contact,details,status,
        CASE WHEN status IN ('approved','rejected') THEN pg_temp.demo_id(1,2)::text ELSE NULL END,
        CASE WHEN status IN ('approved','rejected') THEN now()-interval '1 day' ELSE NULL END,
        CASE WHEN status='rejected' THEN '[Demo] Duplicate or unsuitable proposal; try submitting a corrected location.' ELSE NULL END,
        resulting_location_id,CASE WHEN status='pending' THEN 1 ELSE 2 END,
        now()-interval '2 days'-n*interval '1 minute',now()-interval '1 day'
    FROM demo_requests
    ON CONFLICT(id) DO UPDATE SET submitted_by=EXCLUDED.submitted_by,name=EXCLUDED.name,is_supplier=EXCLUDED.is_supplier,
        building_id=EXCLUDED.building_id,floor=EXCLUDED.floor,coordinates=EXCLUDED.coordinates,
        open_from=EXCLUDED.open_from,open_to=EXCLUDED.open_to,contact=EXCLUDED.contact,details=EXCLUDED.details,
        status=EXCLUDED.status,reviewed_by=EXCLUDED.reviewed_by,reviewed_at=EXCLUDED.reviewed_at,review_note=EXCLUDED.review_note,
        resulting_location_id=EXCLUDED.resulting_location_id,revision=location_addition_requests.revision+1,
        created_at=EXCLUDED.created_at,updated_at=EXCLUDED.updated_at
    WHERE (SELECT reset FROM demo_options)
    RETURNING id
) INSERT INTO demo_written_requests SELECT id FROM written;
DELETE FROM location_addition_request_categories WHERE request_id IN (SELECT id FROM demo_written_requests);
INSERT INTO location_addition_request_categories(request_id,category_id)
SELECT r.id,pg_temp.demo_id(3,r.category_n)
FROM demo_requests r JOIN demo_written_requests w USING(id) WHERE r.is_supplier;

-- Intervals do not overlap for the same location. History spans two pages.
CREATE TEMP TABLE demo_closures ON COMMIT DROP AS
SELECT n,pg_temp.demo_id(6,n) AS id,pg_temp.demo_id(4,8) AS location_id,
    now()-(n+10)*interval '1 day' AS starts_at,
    now()-(n+10)*interval '1 day'+interval '2 hours' AS ends_at,
    NULL::timestamptz AS ended_at,NULL::timestamptz AS cancelled_at,
    '[Demo] Completed closure ' || lpad(n::text,2,'0') AS reason
FROM generate_series(6,30) n;
INSERT INTO demo_closures VALUES
    (1,pg_temp.demo_id(6,1),pg_temp.demo_id(4,6),now()-interval '2 days',NULL,NULL,NULL,'[Demo] Active indefinite repairs'),
    (2,pg_temp.demo_id(6,2),pg_temp.demo_id(4,7),now()+interval '2 days',now()+interval '3 days',NULL,NULL,'[Demo] Simulated scheduled maintenance'),
    (3,pg_temp.demo_id(6,3),pg_temp.demo_id(4,8),now()-interval '12 hours',now()+interval '12 hours',NULL,NULL,'[Demo] Active finite closure'),
    (4,pg_temp.demo_id(6,4),pg_temp.demo_id(4,8),now()+interval '3 days',now()+interval '4 days',NULL,now()-interval '1 day','[Demo] Cancelled future maintenance'),
    (5,pg_temp.demo_id(6,5),pg_temp.demo_id(4,8),now()-interval '5 days',now()-interval '3 days',now()-interval '4 days',NULL,'[Demo] Closure ended early'),
    (31,pg_temp.demo_id(6,31),pg_temp.demo_id(4,8),now()+interval '7 days',NULL,NULL,NULL,'[Demo] Scheduled indefinite closure'),
    (32,pg_temp.demo_id(6,32),pg_temp.demo_id(4,34),now()-interval '4 hours',now()+interval '1 day',NULL,NULL,'[Demo] Approved location temporarily closed');
-- Match the application's per-location writer lock before checking overlaps.
SELECT id FROM locations WHERE id IN (SELECT location_id FROM demo_closures) ORDER BY id FOR UPDATE;
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM demo_closures d JOIN location_disablements existing ON existing.location_id=d.location_id
        WHERE d.cancelled_at IS NULL AND existing.cancelled_at IS NULL
        AND ((SELECT reset FROM demo_options) OR NOT EXISTS(SELECT 1 FROM location_disablements WHERE id=d.id))
        AND NOT ((SELECT reset FROM demo_options) AND EXISTS(SELECT 1 FROM demo_closures WHERE id=existing.id))
        AND tstzrange(d.starts_at,LEAST(d.ends_at,d.ended_at),'[)') &&
            tstzrange(existing.starts_at,LEAST(existing.ends_at,existing.ended_at),'[)')
    ) THEN RAISE EXCEPTION 'Demo closures overlap manually created history. Resolve closures on demo locations before seeding/resetting; no supplier fixtures were changed.'; END IF;
END $$;
INSERT INTO location_disablements(id,location_id,starts_at,ends_at,ended_at,cancelled_at,reason,created_by,revision,created_at,updated_at)
SELECT id,location_id,starts_at,ends_at,ended_at,cancelled_at,reason,pg_temp.demo_id(1,2)::text,1,
    LEAST(starts_at,now()-interval '3 days'),now()-interval '1 day'
FROM demo_closures
ON CONFLICT(id) DO UPDATE SET location_id=EXCLUDED.location_id,starts_at=EXCLUDED.starts_at,ends_at=EXCLUDED.ends_at,
    ended_at=EXCLUDED.ended_at,cancelled_at=EXCLUDED.cancelled_at,reason=EXCLUDED.reason,
    created_by=EXCLUDED.created_by,revision=location_disablements.revision+1,
    created_at=EXCLUDED.created_at,updated_at=now()
WHERE (SELECT reset FROM demo_options);

SELECT 'demo_locations' AS fixture,count(*) FROM locations WHERE id IN (SELECT id FROM demo_locations)
UNION ALL SELECT 'demo_requests',count(*) FROM location_addition_requests WHERE id IN (SELECT id FROM demo_requests)
UNION ALL SELECT 'demo_closures',count(*) FROM location_disablements WHERE id IN (SELECT id FROM demo_closures);
SELECT status,count(*) FROM location_addition_requests WHERE id IN (SELECT id FROM demo_requests) GROUP BY status ORDER BY status;
COMMIT;
