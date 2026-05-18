-- v0 → v1 migration: drop the account layer and the transfer kind.
--
-- Wrapped in BEGIN/COMMIT by the caller, with foreign_keys=OFF for the
-- duration so we can rebuild tables that other tables reference.
--
-- Carried over: txn.id, txn.occurred_at, amount, currency, payee, note, tags,
-- created_at, category_id; person_id / family_id are filled in from the old
-- account.owner_kind/owner_id when the original row didn't set them.
--
-- Dropped: transfer txns, transfer-kind categories, the whole account table.

CREATE TABLE category_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('income','expense')),
    parent_id   INTEGER REFERENCES category_new(id) ON DELETE SET NULL,
    owner_kind  TEXT CHECK (owner_kind IN ('person','family')),
    owner_id    INTEGER,
    icon        TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO category_new (id, name, kind, parent_id, owner_kind, owner_id, icon, created_at)
SELECT id, name, kind, parent_id, owner_kind, owner_id, icon, created_at
FROM category
WHERE kind != 'transfer';

DROP TABLE category;
ALTER TABLE category_new RENAME TO category;

CREATE TABLE txn_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at  TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('income','expense')),
    amount       INTEGER NOT NULL CHECK (amount > 0),
    currency     TEXT NOT NULL DEFAULT 'CNY',
    category_id  INTEGER REFERENCES category(id) ON DELETE SET NULL,
    person_id    INTEGER REFERENCES person(id) ON DELETE SET NULL,
    family_id    INTEGER REFERENCES family(id) ON DELETE SET NULL,
    payee        TEXT,
    note         TEXT,
    tags         TEXT,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO txn_new (id, occurred_at, kind, amount, currency, category_id, person_id, family_id, payee, note, tags, created_at)
SELECT t.id, t.occurred_at, t.kind, t.amount, t.currency,
       CASE WHEN t.category_id IN (SELECT id FROM category) THEN t.category_id END,
       COALESCE(t.person_id, CASE WHEN a.owner_kind='person' THEN a.owner_id END),
       COALESCE(t.family_id, CASE WHEN a.owner_kind='family' THEN a.owner_id END),
       t.payee, t.note, t.tags, t.created_at
FROM txn t
LEFT JOIN account a ON a.id = t.account_id
WHERE t.kind != 'transfer';

DROP TABLE txn;
ALTER TABLE txn_new RENAME TO txn;

DROP TABLE account;

INSERT OR REPLACE INTO schema_meta(key, value) VALUES ('version', '1');
