CREATE TABLE tailnets (
  id INTEGER PRIMARY KEY,
  key TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
) STRICT;

CREATE TABLE site_ids (
  tailnet_id INTEGER NOT NULL REFERENCES tailnets(id),
  site_id INTEGER NOT NULL CHECK (site_id BETWEEN 0 AND 65535),
  site TEXT NOT NULL CHECK (site <> ''),
  owner TEXT,
  description TEXT,
  ticket TEXT,
  reserved_at TEXT,
  PRIMARY KEY (tailnet_id, site_id)
) STRICT, WITHOUT ROWID;

-- Addresses are 16 bytes, with IPv4 in its IPv4-mapped form, so comparing
-- BLOBs within one family compares addresses numerically.
CREATE TABLE subnet_pools (
  tailnet_id INTEGER NOT NULL REFERENCES tailnets(id),
  cidr TEXT NOT NULL,
  family INTEGER NOT NULL CHECK (family IN (4, 6)),
  range_start BLOB NOT NULL CHECK (length(range_start) = 16),
  range_end BLOB NOT NULL CHECK (length(range_end) = 16),
  site TEXT NOT NULL CHECK (site <> ''),
  owner TEXT,
  description TEXT,
  ticket TEXT,
  reserved_at TEXT,
  PRIMARY KEY (tailnet_id, cidr),
  CHECK (range_start <= range_end)
) STRICT, WITHOUT ROWID;

CREATE INDEX subnet_pools_range ON subnet_pools (tailnet_id, family, range_start);

CREATE TRIGGER subnet_pools_overlap_insert
BEFORE INSERT ON subnet_pools
WHEN EXISTS (
  SELECT 1 FROM subnet_pools
  WHERE tailnet_id = NEW.tailnet_id
    AND family = NEW.family
    AND range_start <= NEW.range_end
    AND range_end >= NEW.range_start
    AND site <> NEW.site
)
BEGIN
  SELECT RAISE(ABORT, 'subnet pool overlaps a pool held by a different site');
END;

CREATE TRIGGER subnet_pools_overlap_update
BEFORE UPDATE OF tailnet_id, site, range_start, range_end, family ON subnet_pools
WHEN EXISTS (
  SELECT 1 FROM subnet_pools
  WHERE tailnet_id = NEW.tailnet_id
    AND family = NEW.family
    AND range_start <= NEW.range_end
    AND range_end >= NEW.range_start
    AND site <> NEW.site
    AND NOT (tailnet_id = OLD.tailnet_id AND cidr = OLD.cidr)
)
BEGIN
  SELECT RAISE(ABORT, 'subnet pool overlaps a pool held by a different site');
END;

CREATE TABLE service_addresses (
  tailnet_id INTEGER NOT NULL REFERENCES tailnets(id),
  address TEXT NOT NULL,
  service TEXT NOT NULL CHECK (service LIKE 'svc:_%'),
  owner TEXT,
  description TEXT,
  ticket TEXT,
  reserved_at TEXT,
  PRIMARY KEY (tailnet_id, address)
) STRICT, WITHOUT ROWID;

CREATE TABLE udp_ports (
  tailnet_id INTEGER NOT NULL REFERENCES tailnets(id),
  site TEXT NOT NULL CHECK (site <> ''),
  port INTEGER NOT NULL CHECK (port BETWEEN 1024 AND 65535),
  device TEXT NOT NULL CHECK (device <> ''),
  PRIMARY KEY (tailnet_id, site, port)
) STRICT, WITHOUT ROWID;

CREATE TABLE events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tailnet_id INTEGER NOT NULL REFERENCES tailnets(id),
  at TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('reserve', 'allocate', 'release', 'import')),
  kind TEXT NOT NULL CHECK (kind IN ('site_id', 'subnet_pool', 'service_address', 'udp_port')),
  key TEXT NOT NULL,
  site TEXT,
  before_value TEXT,
  after_value TEXT,
  actor TEXT
) STRICT;

CREATE INDEX events_by_tailnet ON events (tailnet_id, id DESC);
CREATE INDEX events_by_key ON events (tailnet_id, kind, key, id DESC);

CREATE TRIGGER events_no_update
BEFORE UPDATE ON events
BEGIN
  SELECT RAISE(ABORT, 'registry events are append-only');
END;

CREATE TRIGGER events_no_delete
BEFORE DELETE ON events
BEGIN
  SELECT RAISE(ABORT, 'registry events are append-only');
END;

-- INSERT OR REPLACE deletes the old row without firing delete triggers.
CREATE TRIGGER events_no_replace
BEFORE INSERT ON events
WHEN EXISTS (SELECT 1 FROM events WHERE id = NEW.id)
BEGIN
  SELECT RAISE(ABORT, 'registry events are append-only');
END;
