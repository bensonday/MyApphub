PRAGMA foreign_keys = ON;

CREATE TABLE apps (
  slug TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  password_hash TEXT,
  password_version INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT
);

CREATE TABLE variants (
  slug TEXT NOT NULL REFERENCES apps(slug),
  platform TEXT NOT NULL CHECK (platform IN ('ios', 'android')),
  identifier TEXT NOT NULL,
  icon_key TEXT,
  current_version_id TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (slug, platform),
  UNIQUE (platform, identifier)
);

CREATE TABLE versions (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL,
  platform TEXT NOT NULL,
  version_name TEXT NOT NULL,
  build_number TEXT NOT NULL,
  package_key TEXT NOT NULL UNIQUE,
  sha256 TEXT NOT NULL,
  file_size INTEGER NOT NULL,
  minimum_os_version TEXT,
  distribution_type TEXT,
  provisioning_expires_at TEXT,
  signing_certificate_sha256 TEXT,
  release_notes TEXT,
  uploaded_at TEXT NOT NULL,
  deleted_at TEXT,
  FOREIGN KEY (slug, platform) REFERENCES variants(slug, platform),
  UNIQUE (slug, platform, version_name, build_number)
);
CREATE INDEX versions_by_variant ON versions(slug, platform, uploaded_at DESC);
CREATE INDEX versions_by_trash ON versions(deleted_at);

CREATE TABLE uploads (
  id TEXT PRIMARY KEY,
  object_key TEXT NOT NULL UNIQUE,
  r2_upload_id TEXT NOT NULL,
  metadata_json TEXT NOT NULL,
  icon_key TEXT,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

CREATE TABLE udid_sessions (
  challenge TEXT PRIMARY KEY,
  payload_uuid TEXT NOT NULL,
  udid TEXT,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX udid_sessions_expiry ON udid_sessions(expires_at);

CREATE TABLE rate_limits (
  key TEXT PRIMARY KEY,
  attempts INTEGER NOT NULL,
  resets_at TEXT NOT NULL
);
CREATE INDEX rate_limits_expiry ON rate_limits(resets_at);
