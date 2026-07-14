package storage

const schemaSQL = `
CREATE TABLE IF NOT EXISTS metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS app_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  download_dir TEXT NOT NULL,
  filename_template TEXT NOT NULL,
  concurrency INTEGER NOT NULL CHECK (concurrency BETWEEN 1 AND 4),
  retry_count INTEGER NOT NULL CHECK (retry_count BETWEEN 0 AND 5),
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS library_items (
  id INTEGER PRIMARY KEY,
  source_key TEXT NOT NULL UNIQUE,
  post_id TEXT NOT NULL DEFAULT '',
  author TEXT NOT NULL DEFAULT '',
  post_url TEXT NOT NULL DEFAULT '',
  page_url TEXT NOT NULL DEFAULT '',
  post_created_at INTEGER,
  post_text TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  first_seen_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS library_items_last_seen_idx
  ON library_items(last_seen_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS library_items_author_idx
  ON library_items(author, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS library_items_post_id_idx
  ON library_items(post_id);

CREATE TABLE IF NOT EXISTS media_items (
  id INTEGER PRIMARY KEY,
  library_item_id INTEGER NOT NULL REFERENCES library_items(id) ON DELETE CASCADE,
  candidate_id TEXT NOT NULL UNIQUE,
  media_id TEXT NOT NULL UNIQUE,
  media_index INTEGER NOT NULL DEFAULT 0,
  thumbnail_url TEXT NOT NULL DEFAULT '',
  first_seen_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  seen_count INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS media_items_library_idx
  ON media_items(library_item_id, media_index);

CREATE TABLE IF NOT EXISTS candidates (
  candidate_id TEXT PRIMARY KEY REFERENCES media_items(candidate_id) ON DELETE CASCADE,
  master_url TEXT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  discovered_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS candidates_last_seen_idx
  ON candidates(last_seen_at DESC, candidate_id DESC);

CREATE TABLE IF NOT EXISTS media_variants (
  candidate_id TEXT NOT NULL REFERENCES candidates(candidate_id) ON DELETE CASCADE,
  variant_id TEXT NOT NULL,
  video_url TEXT NOT NULL,
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  bandwidth INTEGER NOT NULL DEFAULT 0,
  average_bandwidth INTEGER NOT NULL DEFAULT 0,
  codecs TEXT NOT NULL DEFAULT '',
  audio_group TEXT NOT NULL DEFAULT '',
  audio_name TEXT NOT NULL DEFAULT '',
  audio_url TEXT NOT NULL DEFAULT '',
  audio_bitrate INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(candidate_id, variant_id)
);

CREATE TABLE IF NOT EXISTS download_jobs (
  id TEXT PRIMARY KEY,
  candidate_id TEXT NOT NULL REFERENCES candidates(candidate_id) ON DELETE CASCADE,
  variant_id TEXT NOT NULL,
  media_id TEXT NOT NULL,
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  video_url TEXT NOT NULL DEFAULT '',
  audio_url TEXT NOT NULL DEFAULT '',
  audio_bitrate INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  out_time_seconds REAL NOT NULL DEFAULT 0,
  duration_seconds REAL NOT NULL DEFAULT 0,
  percent REAL NOT NULL DEFAULT 0,
  speed TEXT NOT NULL DEFAULT '',
  phase TEXT NOT NULL DEFAULT '',
  output_path TEXT NOT NULL DEFAULT '',
  temp_path TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  attempt INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 1,
  file_size INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  started_at INTEGER,
  finished_at INTEGER,
  revision INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS download_jobs_status_created_idx
  ON download_jobs(status, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS download_jobs_created_idx
  ON download_jobs(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS download_jobs_candidate_idx
  ON download_jobs(candidate_id, created_at DESC);
CREATE INDEX IF NOT EXISTS download_jobs_finished_idx
  ON download_jobs(finished_at DESC);

CREATE TABLE IF NOT EXISTS job_attempts (
  job_id TEXT NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
  attempt_no INTEGER NOT NULL,
  started_at INTEGER NOT NULL,
  finished_at INTEGER,
  outcome TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(job_id, attempt_no)
);

CREATE INDEX IF NOT EXISTS job_attempts_finished_idx
  ON job_attempts(finished_at DESC);

CREATE TABLE IF NOT EXISTS tags (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  normalized_name TEXT NOT NULL UNIQUE,
  color TEXT NOT NULL DEFAULT '#1d9bf0',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS library_item_tags (
  library_item_id INTEGER NOT NULL REFERENCES library_items(id) ON DELETE CASCADE,
  tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY(library_item_id, tag_id)
);

CREATE INDEX IF NOT EXISTS library_item_tags_tag_idx
  ON library_item_tags(tag_id, library_item_id);

CREATE TABLE IF NOT EXISTS history_search_documents (
  library_item_id INTEGER PRIMARY KEY REFERENCES library_items(id) ON DELETE CASCADE,
  author TEXT NOT NULL DEFAULT '',
  post_id TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  filenames TEXT NOT NULL DEFAULT ''
);

CREATE VIRTUAL TABLE IF NOT EXISTS history_fts USING fts5(
  author,
  post_id,
  note,
  filenames,
  content='history_search_documents',
  content_rowid='library_item_id',
  tokenize='trigram'
);

CREATE TRIGGER IF NOT EXISTS history_search_documents_ai AFTER INSERT ON history_search_documents BEGIN
  INSERT INTO history_fts(rowid, author, post_id, note, filenames)
  VALUES (new.library_item_id, new.author, new.post_id, new.note, new.filenames);
END;
CREATE TRIGGER IF NOT EXISTS history_search_documents_ad AFTER DELETE ON history_search_documents BEGIN
  INSERT INTO history_fts(history_fts, rowid, author, post_id, note, filenames)
  VALUES ('delete', old.library_item_id, old.author, old.post_id, old.note, old.filenames);
END;
CREATE TRIGGER IF NOT EXISTS history_search_documents_au AFTER UPDATE ON history_search_documents BEGIN
  INSERT INTO history_fts(history_fts, rowid, author, post_id, note, filenames)
  VALUES ('delete', old.library_item_id, old.author, old.post_id, old.note, old.filenames);
  INSERT INTO history_fts(rowid, author, post_id, note, filenames)
  VALUES (new.library_item_id, new.author, new.post_id, new.note, new.filenames);
END;
`
