package database

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite" // Pure-Go SQLite без необходимости в CGO
)

//go:embed schema.sql
var schemaSQL string

type DB struct {
	*sql.DB
}

func NowMs() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// Open открывает базу данных и выполняет первичную инициализацию
func Open(dbPath string) (*DB, error) {
	// DSN с PRAGMA параметрами:
	// - busy_timeout: 5000 мс ожидания снятия лока при записи (предотвращает SQLITE_BUSY)
	// - journal_mode: WAL для параллельного чтения
	// - foreign_keys: ON для работы каскадных удалений
	// - synchronous: NORMAL для баланса скорости записи и защиты от сбоев питания
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)", dbPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// SQLite работает оптимальнее всего с небольшим пулом
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	wrapped := &DB{db}
	if err := wrapped.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return wrapped, nil
}

func (db *DB) initSchema() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("schema execution error: %w", err)
	}
	return nil
}

// WithTx оборачивает выполнение запросов в транзакцию с автоматическим коммитом и откатом
func (db *DB) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

// SeedSuperadmins назначает суперадминами пользователей по их никнеймам из конфигурации
func (db *DB) SeedSuperadmins(ctx context.Context, adminNicknames []string) error {
	if len(adminNicknames) == 0 {
		return nil
	}

	var hasSuperadmin int
	err := db.QueryRowContext(ctx, "SELECT 1 FROM users WHERE is_superadmin = 1 LIMIT 1").Scan(&hasSuperadmin)
	if err == nil && hasSuperadmin == 1 {
		return nil // Суперадмин уже назначен
	}

	placeholders := make([]string, len(adminNicknames))
	args := make([]any, len(adminNicknames))
	for i, nick := range adminNicknames {
		placeholders[i] = "?"
		args[i] = strings.ToLower(nick)
	}

	query := fmt.Sprintf(
		"UPDATE users SET is_superadmin = 1, can_create_servers = 1 WHERE name_key IN (%s)",
		strings.Join(placeholders, ", "),
	)

	_, err = db.ExecContext(ctx, query, args...)
	return err
}

// CreateServerTx атомарно создает сервер, привязывает владельца с ролью 'owner' и генерирует каналы по умолчанию
func (db *DB) CreateServerTx(ctx context.Context, name, description string, ownerID string, defaultChannels [][2]string) (string, error) {
	serverID := strings.ReplaceAll(uuid.New().String(), "-", "")
	now := NowMs()

	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		// 1. Создаем запись сервера
		_, err := tx.ExecContext(ctx,
			"INSERT INTO servers (id, name, description, owner_id, created_at) VALUES (?, ?, ?, ?, ?)",
			serverID, name, description, ownerID, now,
		)
		if err != nil {
			return fmt.Errorf("failed to insert server: %w", err)
		}

		// 2. Добавляем создателя с ролью 'owner'
		_, err = tx.ExecContext(ctx,
			"INSERT INTO server_members (server_id, user_id, role, joined_at) VALUES (?, ?, 'owner', ?)",
			serverID, ownerID, now,
		)
		if err != nil {
			return fmt.Errorf("failed to add owner to members: %w", err)
		}

		// 3. Создаем базовые каналы для сервера
		stmt, err := tx.PrepareContext(ctx,
			"INSERT INTO channels (id, server_id, name, kind, position, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		)
		if err != nil {
			return fmt.Errorf("failed to prepare channel stmt: %w", err)
		}
		defer stmt.Close()

		for pos, ch := range defaultChannels {
			chID := strings.ReplaceAll(uuid.New().String(), "-", "")
			chName, chKind := ch[0], ch[1]
			if _, err := stmt.ExecContext(ctx, chID, serverID, chName, chKind, pos, now); err != nil {
				return fmt.Errorf("failed to insert default channel: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	return serverID, nil
}
