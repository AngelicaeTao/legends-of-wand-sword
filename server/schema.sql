-- 杖剑传说初跑版 · 存档分表 schema
-- 设计原则：角色信息分表存储；新增系统走 ext_data 扩展位，不改表结构。
-- 键统一为用户 ID（Nakama user id）。Nakama 自身管理 users 表，本 schema 为业务存档表。

CREATE TABLE IF NOT EXISTS characters (
  user_id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  level INTEGER DEFAULT 1,
  exp INTEGER DEFAULT 0,
  battle_power INTEGER DEFAULT 0,
  class_id TEXT DEFAULT 'warrior',
  gold INTEGER DEFAULT 0,
  gem INTEGER DEFAULT 0,
  material INTEGER DEFAULT 0,
  stamina INTEGER DEFAULT 120,
  updated_at INTEGER
);

CREATE TABLE IF NOT EXISTS world_progress (
  user_id TEXT PRIMARY KEY,
  world_id TEXT DEFAULT 'W1',
  x INTEGER DEFAULT 0,
  y INTEGER DEFAULT 0,
  unlocked TEXT DEFAULT '["W1"]',   -- JSON 数组
  last_seen INTEGER DEFAULT 0,      -- 服务端时间戳，离线结算唯一可信源
  updated_at INTEGER
);

CREATE TABLE IF NOT EXISTS warehouse (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  item_id TEXT NOT NULL,
  qty INTEGER DEFAULT 1,
  slot INTEGER DEFAULT 0,
  kind TEXT
);
CREATE INDEX IF NOT EXISTS idx_wh_user ON warehouse(user_id);

CREATE TABLE IF NOT EXISTS equipment (
  id TEXT PRIMARY KEY,              -- 装备实例ID
  user_id TEXT NOT NULL,
  slot TEXT NOT NULL,               -- weapon/helmet/armor/trinket
  quality INTEGER DEFAULT 0,
  enhance INTEGER DEFAULT 0,
  stats TEXT DEFAULT '{}',          -- JSON
  equipped INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_eq_user ON equipment(user_id);

CREATE TABLE IF NOT EXISTS skills (
  user_id TEXT PRIMARY KEY,
  slots TEXT DEFAULT '[]',          -- 4个已装技能 JSON
  unlocked TEXT DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS pets (
  user_id TEXT PRIMARY KEY,
  owned TEXT DEFAULT '[]',
  active TEXT
);

CREATE TABLE IF NOT EXISTS pet_state (
  user_id TEXT PRIMARY KEY,
  state TEXT DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS home (
  user_id TEXT PRIMARY KEY,
  home_data TEXT DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id TEXT NOT NULL,
  ts INTEGER NOT NULL,
  event TEXT,
  detail TEXT
);
CREATE INDEX IF NOT EXISTS idx_log_user_ts ON logs(user_id, ts);

-- 扩展位：后续新内容（公会/战令等）零改表结构入库
CREATE TABLE IF NOT EXISTS ext_data (
  user_id TEXT NOT NULL,
  key TEXT NOT NULL,
  json TEXT NOT NULL,
  PRIMARY KEY (user_id, key)
);

CREATE TABLE IF NOT EXISTS schema_meta (
  version INTEGER PRIMARY KEY,
  applied_at INTEGER,
  note TEXT
);
INSERT OR IGNORE INTO schema_meta(version, applied_at, note) VALUES (1, strftime('%s','now'), 'initial 11 tables');
