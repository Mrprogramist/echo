-- internal/database/schema.sql

-- ============================================================================
-- ГЛОБАЛЬНЫЙ УРОВЕНЬ (ПЛАТФОРМА И ПОЛЬЗОВАТЕЛИ)
-- ============================================================================

-- Таблица пользователей с правами на создание серверов и связью с пригласителем
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  name_key TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  color TEXT NOT NULL,
  bio TEXT NOT NULL DEFAULT '',
  avatar TEXT,
  token_version INTEGER NOT NULL DEFAULT 0,
  recovery_hash TEXT,
  
  -- Права платформенного уровня:
  is_superadmin INTEGER NOT NULL DEFAULT 0,      -- Полный контроль над платформой и всеми серверами
  can_create_servers INTEGER NOT NULL DEFAULT 0, -- Разрешение создавать собственные серверы
  
  -- Откуда пришел пользователь:
  invited_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  registered_via_invite TEXT,
  
  status TEXT NOT NULL DEFAULT 'online',
  status_text TEXT NOT NULL DEFAULT '',
  status_until INTEGER,
  removed INTEGER NOT NULL DEFAULT 0,            -- Глобальный бан
  created_at INTEGER NOT NULL
);

-- Дружба (глобальная, между двумя пользователями платформы)
CREATE TABLE IF NOT EXISTS friendships (
  user_a TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  user_b TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  requested_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status TEXT NOT NULL CHECK (status IN ('pending', 'accepted')),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_a, user_b)
);

-- Черный список (глобальный)
CREATE TABLE IF NOT EXISTS blocks (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  blocked_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, blocked_id)
);

-- Личные переписки и группы вне серверов
CREATE TABLE IF NOT EXISTS dms (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL DEFAULT 'dm' CHECK (kind IN ('dm', 'group')),
  name TEXT NOT NULL DEFAULT '',
  owner_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  pair_key TEXT UNIQUE,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS dm_members (
  dm_id TEXT NOT NULL REFERENCES dms(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  joined_at INTEGER NOT NULL,
  PRIMARY KEY (dm_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_dm_members_user ON dm_members(user_id);

CREATE TABLE IF NOT EXISTS dm_messages (
  id TEXT PRIMARY KEY,
  dm_id TEXT NOT NULL REFERENCES dms(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  content TEXT NOT NULL,
  attachments TEXT NOT NULL DEFAULT '[]',
  reply_to TEXT,
  edited_at INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_dm_messages_time ON dm_messages(dm_id, created_at);

-- ============================================================================
-- УРОВЕНЬ СЕРВЕРОВ (GUILDS / COMMUNITIES)
-- ============================================================================

-- Серверы (аналог серверов в Discord)
CREATE TABLE IF NOT EXISTS servers (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  icon TEXT,
  owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_servers_owner ON servers(owner_id);

-- Участники серверов и их локальные роли
CREATE TABLE IF NOT EXISTS server_members (
  server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'moderator', 'member')),
  joined_at INTEGER NOT NULL,
  PRIMARY KEY (server_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_server_members_user ON server_members(user_id);

-- Ссылки-приглашения на конкретные серверы
CREATE TABLE IF NOT EXISTS server_invites (
  code TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  created_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  max_uses INTEGER NOT NULL DEFAULT 0, -- 0 = бесконечно
  uses INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER,                 -- NULL = бессрочно
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_server_invites_server ON server_invites(server_id);

-- Категории каналов внутри сервера
CREATE TABLE IF NOT EXISTS categories (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  position INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_categories_server ON categories(server_id, position);

-- Каналы внутри сервера
CREATE TABLE IF NOT EXISTS channels (
  id TEXT PRIMARY KEY,
  server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  category_id TEXT REFERENCES categories(id) ON DELETE SET NULL,
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('text', 'voice')),
  position INTEGER NOT NULL,
  deleted_at INTEGER,                 -- Мягкое удаление (корзина 7 дней)
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_channels_server ON channels(server_id, position);

-- Сообщения в каналах серверов
CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  content TEXT NOT NULL,
  attachments TEXT NOT NULL DEFAULT '[]',
  reply_to TEXT,
  edited_at INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_channel_time ON messages(channel_id, created_at);
CREATE INDEX IF NOT EXISTS idx_messages_time ON messages(created_at);
CREATE INDEX IF NOT EXISTS idx_messages_user ON messages(user_id);

-- ============================================================================
-- ВСПОМОГАТЕЛЬНЫЕ ТАБЛИЦЫ (РЕАКЦИИ, ФАЙЛЫ, СТАТУСЫ)
-- ============================================================================

CREATE TABLE IF NOT EXISTS reactions (
  message_id TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  emoji TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (message_id, user_id, emoji)
);
CREATE INDEX IF NOT EXISTS idx_reactions_message ON reactions(message_id);

CREATE TABLE IF NOT EXISTS read_states (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  conversation_id TEXT NOT NULL,
  read_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, conversation_id)
);

CREATE TABLE IF NOT EXISTS channel_mutes (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  PRIMARY KEY (user_id, channel_id)
);

CREATE TABLE IF NOT EXISTS voice_grants (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  room TEXT NOT NULL,
  PRIMARY KEY (user_id, room)
);

CREATE TABLE IF NOT EXISTS uploads (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  file_name TEXT,
  file_size INTEGER,
  file_type TEXT,
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  scope TEXT, -- 'channel:<id>', 'dm:<id>', 'avatar', 'server_icon'
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_uploads_user_time ON uploads(user_id, created_at);

CREATE TABLE IF NOT EXISTS password_resets (
  user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);

-- Глобальные системные настройки инстанса
CREATE TABLE IF NOT EXISTS instance_settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);