-- suanpan 记账软件 schema
-- Money is stored as INTEGER in minor units (cents/分) to avoid float drift.

PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS family (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    currency    TEXT NOT NULL DEFAULT 'CNY',
    note        TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS person (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    family_id   INTEGER REFERENCES family(id) ON DELETE SET NULL,
    note        TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(name, family_id)
);

CREATE TABLE IF NOT EXISTS account (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    type            TEXT NOT NULL CHECK (type IN ('cash','bank','credit','investment','virtual')),
    currency        TEXT NOT NULL DEFAULT 'CNY',
    initial_balance INTEGER NOT NULL DEFAULT 0, -- minor units
    owner_kind      TEXT NOT NULL CHECK (owner_kind IN ('person','family')),
    owner_id        INTEGER NOT NULL,
    archived        INTEGER NOT NULL DEFAULT 0,
    note            TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(name, owner_kind, owner_id)
);

CREATE INDEX IF NOT EXISTS idx_account_owner ON account(owner_kind, owner_id);

CREATE TABLE IF NOT EXISTS category (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('income','expense','transfer')),
    parent_id   INTEGER REFERENCES category(id) ON DELETE SET NULL,
    owner_kind  TEXT CHECK (owner_kind IN ('person','family')),
    owner_id    INTEGER,
    icon        TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_category_owner ON category(owner_kind, owner_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_category_name_kind_scope
    ON category(name, kind, COALESCE(owner_kind,''), COALESCE(owner_id,0));

CREATE TABLE IF NOT EXISTS txn (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at         TEXT NOT NULL,
    kind                TEXT NOT NULL CHECK (kind IN ('income','expense','transfer')),
    amount              INTEGER NOT NULL CHECK (amount > 0), -- minor units, always positive
    currency            TEXT NOT NULL DEFAULT 'CNY',
    account_id          INTEGER NOT NULL REFERENCES account(id) ON DELETE RESTRICT,
    counter_account_id  INTEGER REFERENCES account(id) ON DELETE RESTRICT, -- for transfers
    category_id         INTEGER REFERENCES category(id) ON DELETE SET NULL,
    person_id           INTEGER REFERENCES person(id) ON DELETE SET NULL,
    family_id           INTEGER REFERENCES family(id) ON DELETE SET NULL,
    payee               TEXT,
    note                TEXT,
    tags                TEXT, -- comma separated
    created_at          TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_txn_occurred ON txn(occurred_at);
CREATE INDEX IF NOT EXISTS idx_txn_account  ON txn(account_id);
CREATE INDEX IF NOT EXISTS idx_txn_person   ON txn(person_id);
CREATE INDEX IF NOT EXISTS idx_txn_family   ON txn(family_id);
CREATE INDEX IF NOT EXISTS idx_txn_category ON txn(category_id);

CREATE TABLE IF NOT EXISTS budget (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    period      TEXT NOT NULL CHECK (period IN ('weekly','monthly','yearly')),
    amount      INTEGER NOT NULL CHECK (amount > 0),
    currency    TEXT NOT NULL DEFAULT 'CNY',
    category_id INTEGER REFERENCES category(id) ON DELETE CASCADE,
    owner_kind  TEXT NOT NULL CHECK (owner_kind IN ('person','family')),
    owner_id    INTEGER NOT NULL,
    start_date  TEXT NOT NULL,
    end_date    TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_budget_owner ON budget(owner_kind, owner_id);

-- Seed some default global categories if none exist (done from code on init).
