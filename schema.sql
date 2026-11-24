CREATE TABLE IF NOT EXISTS clients (
 id TEXT PRIMARY KEY, active BOOLEAN NOT NULL, cap BIGINT NOT NULL, typical BIGINT NOT NULL, opening BIGINT NOT NULL
);
INSERT INTO clients VALUES ('Ana',true,80000,20000,500000),('Bruno',true,0,50000,2000000),('Carla',true,0,20000,3000000),('Davi',false,0,20000,500000) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS operations (
 id TEXT PRIMARY KEY, request TEXT NOT NULL, client TEXT NOT NULL REFERENCES clients(id), product TEXT NOT NULL,
 destination TEXT NOT NULL, amount BIGINT NOT NULL CHECK(amount>0), created BIGINT NOT NULL,
 state TEXT NOT NULL, reason TEXT NOT NULL, evidence TEXT NOT NULL, elapsed BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS recent_ops ON operations(client,product,created);
CREATE TABLE IF NOT EXISTS entries (
 operation TEXT REFERENCES operations(id), account TEXT, cents BIGINT NOT NULL, PRIMARY KEY(operation,account)
);
CREATE TABLE IF NOT EXISTS gateway_receipts (
 operation TEXT PRIMARY KEY REFERENCES operations(id), confirmed BOOLEAN NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
 id TEXT PRIMARY KEY, operation TEXT NOT NULL, created BIGINT NOT NULL, kind TEXT NOT NULL, detail TEXT NOT NULL, delivered BOOLEAN NOT NULL DEFAULT false
);
CREATE TABLE IF NOT EXISTS disputes (
 operation TEXT PRIMARY KEY REFERENCES operations(id), protocol TEXT UNIQUE NOT NULL, note TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (id TEXT PRIMARY KEY, enabled BOOLEAN NOT NULL);
INSERT INTO settings VALUES ('siem',true) ON CONFLICT DO NOTHING;
