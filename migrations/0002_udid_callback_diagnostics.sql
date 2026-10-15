CREATE TABLE udid_callback_diagnostics (
  id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL,
  reason TEXT NOT NULL,
  detail TEXT NOT NULL,
  client TEXT NOT NULL DEFAULT ''
);
CREATE INDEX udid_callback_diagnostics_age ON udid_callback_diagnostics(created_at);
