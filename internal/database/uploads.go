package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Mrprogramist/echo/internal/domain"
)

// GetUpload возвращает метаданные файла
func (db *DB) GetUpload(ctx context.Context, id string) (*domain.Upload, error) {
	u := &domain.Upload{}
	query := `SELECT id, user_id, file_name, file_size, file_type, width, height, scope, created_at FROM uploads WHERE id = ?`
	err := db.QueryRowContext(ctx, query, id).Scan(
		&u.ID, &u.UserID, &u.FileName, &u.FileSize, &u.FileType, &u.Width, &u.Height, &u.Scope, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// CreateUpload сохраняет метаданные нового файла
func (db *DB) CreateUpload(ctx context.Context, u *domain.Upload) error {
	query := `
		INSERT INTO uploads (id, user_id, file_name, file_size, file_type, width, height, scope, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := db.ExecContext(ctx, query,
		u.ID, u.UserID, u.FileName, u.FileSize, u.FileType, u.Width, u.Height, u.Scope, u.CreatedAt,
	)
	return err
}

// ClaimUploads привязывает загруженные файлы к скоупу переписки
func (db *DB) ClaimUploads(ctx context.Context, ids []string, scope string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids)+1)
	args[0] = scope
	for i, id := range ids {
		placeholders[i] = "?"
		args[i+1] = id
	}

	query := fmt.Sprintf("UPDATE uploads SET scope = ? WHERE id IN (%s)", strings.Join(placeholders, ", "))
	_, err := db.ExecContext(ctx, query, args...)
	return err
}

// GetUserDailyUploadBytes возвращает суммарный объем файлов, загруженных пользователем за последние 24 часа
func (db *DB) GetUserDailyUploadBytes(ctx context.Context, userID string, since int64) (int64, error) {
	var total sql.NullInt64
	query := "SELECT SUM(file_size) FROM uploads WHERE user_id = ? AND created_at > ?"
	err := db.QueryRowContext(ctx, query, userID, since).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// GetUnsentUploadIDs ищет файлы без скоупа старше указанного времени
func (db *DB) GetUnsentUploadIDs(ctx context.Context, olderThan int64) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT id FROM uploads WHERE scope IS NULL AND created_at < ?", olderThan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// DeleteUploads удаляет метаданные файлов из БД
func (db *DB) DeleteUploads(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf("DELETE FROM uploads WHERE id IN (%s)", strings.Join(placeholders, ", "))
	_, err := db.ExecContext(ctx, query, args...)
	return err
}

// GetAllUploadIDs возвращает множество всех известных ID файлов в базе
func (db *DB) GetAllUploadIDs(ctx context.Context) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, "SELECT id FROM uploads")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	set := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			set[id] = struct{}{}
		}
	}
	return set, rows.Err()
}
