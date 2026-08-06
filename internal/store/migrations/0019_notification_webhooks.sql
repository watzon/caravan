CREATE TABLE notification_webhooks (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL,
    url           TEXT    NOT NULL,
    on_grab       INTEGER NOT NULL DEFAULT 1,
    on_import     INTEGER NOT NULL DEFAULT 1,
    on_health     INTEGER NOT NULL DEFAULT 1,
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_event_id INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL,
    CHECK (length(trim(name)) > 0),
    CHECK (on_grab IN (0, 1)),
    CHECK (on_import IN (0, 1)),
    CHECK (on_health IN (0, 1)),
    CHECK (enabled IN (0, 1)),
    CHECK (on_grab = 1 OR on_import = 1 OR on_health = 1),
    CHECK (last_event_id >= 0)
);

CREATE INDEX idx_notification_webhooks_enabled ON notification_webhooks (enabled);
