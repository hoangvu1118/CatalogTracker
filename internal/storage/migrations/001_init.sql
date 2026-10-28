-- Products: the shop's copy of the supplier catalog.
-- Withdrawn products stay as rows (tombstones); nothing is ever deleted.
CREATE TABLE products (
    sku         text PRIMARY KEY,
    name        text   NOT NULL DEFAULT '',
    description text   NOT NULL DEFAULT '',
    price_cents bigint NOT NULL DEFAULT 0,
    currency    text   NOT NULL DEFAULT '',
    status      text   NOT NULL CHECK (status IN ('active', 'withdrawn')),
    version     bigint NOT NULL CHECK (version >= 1),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Updates: one row per press of "Update".
CREATE TABLE updates (
    id             bigserial PRIMARY KEY,
    status         text   NOT NULL CHECK (status IN ('running', 'completed', 'failed', 'interrupted')),
    start_bookmark bigint NOT NULL,
    bookmark       bigint NOT NULL,      -- last batch fully saved
    target         bigint NOT NULL DEFAULT 0, -- end of the current Window 2
    refills        int    NOT NULL DEFAULT 0,
    batches_done   int    NOT NULL DEFAULT 0,
    error          text   NOT NULL DEFAULT '',
    started_at     timestamptz NOT NULL,
    finished_at    timestamptz
);

-- At most one unfinished update at a time: staff must resume it first.
CREATE UNIQUE INDEX updates_one_unfinished ON updates ((true))
    WHERE status IN ('running', 'failed', 'interrupted');

-- What changed: one row per supplier change processed.
CREATE TABLE update_changes (
    id         bigserial PRIMARY KEY,
    update_id  bigint NOT NULL REFERENCES updates (id),
    batch_seq  bigint NOT NULL,
    sku        text   NOT NULL,
    version    bigint NOT NULL,
    outcome    text   NOT NULL,
    detail     text   NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL
);
CREATE INDEX update_changes_by_update ON update_changes (update_id, id);

-- Problems that need staff attention until resolved.
CREATE TABLE problems (
    id          bigserial PRIMARY KEY,
    update_id   bigint NOT NULL REFERENCES updates (id),
    batch_seq   bigint NOT NULL,
    sku         text   NOT NULL DEFAULT '',
    version     bigint NOT NULL DEFAULT 0,
    kind        text   NOT NULL,
    message     text   NOT NULL,
    created_at  timestamptz NOT NULL,
    resolved_at timestamptz
);
CREATE INDEX problems_open ON problems (sku, kind) WHERE resolved_at IS NULL;
