-- Minimal canonical outbox DDL scaffold for Windmill CE Postgres triggers.
-- Phase 0.5 support only; DB-backed kit implementations ship in Phase 2.
CREATE TABLE IF NOT EXISTS chassis_outbox (
    id           BIGSERIAL PRIMARY KEY,
    event_id     TEXT NOT NULL UNIQUE,
    subject      TEXT NOT NULL,
    payload      JSONB NOT NULL,
    entity_refs  TEXT[] NOT NULL DEFAULT '{}',
    trace_id     TEXT NOT NULL DEFAULT '',
    tenant_id    TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'failed', 'quarantine')),
    attempts     INT NOT NULL DEFAULT 0,
    locked_at    TIMESTAMPTZ,
    last_error   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS chassis_outbox_pending
    ON chassis_outbox (created_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS chassis_outbox_event_id
    ON chassis_outbox (event_id);
