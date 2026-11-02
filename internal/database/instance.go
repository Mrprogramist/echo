package database

import (
	"context"
	"database/sql"
	"errors"
)

// GetSetting возвращает значение настройки по ключу
func (db *DB) GetSetting(ctx context.Context, key string) (string, error) {
	var val string
	err := db.QueryRowContext(ctx, "SELECT value FROM instance_settings WHERE key = ?", key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return val, err
}

// SetSetting атомарно сохраняет или обновляет настройку
func (db *DB) SetSetting(ctx context.Context, key, val string) error {
	_, err := db.ExecContext(ctx,
		"INSERT INTO instance_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, val,
	)
	return err
}

// DeleteSetting удаляет настройку
func (db *DB) DeleteSetting(ctx context.Context, key string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM instance_settings WHERE key = ?", key)
	return err
}
