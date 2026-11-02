package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Mrprogramist/echo/internal/domain"
)

// GetUserByID возвращает пользователя по ID
func (db *DB) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	u := &domain.User{}
	query := `
		SELECT id, name, name_key, password_hash, color, bio, avatar, token_version, recovery_hash,
		       is_superadmin, can_create_servers, invited_by, registered_via_invite,
		       status, status_text, status_until, removed, created_at
		FROM users WHERE id = ?
	`
	err := db.QueryRowContext(ctx, query, id).Scan(
		&u.ID, &u.Name, &u.NameKey, &u.PasswordHash, &u.Color, &u.Bio, &u.Avatar, &u.TokenVersion, &u.RecoveryHash,
		&u.IsSuperadmin, &u.CanCreateServers, &u.InvitedBy, &u.RegisteredViaInvite,
		&u.Status, &u.StatusText, &u.StatusUntil, &u.Removed, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// GetUserByNameKey ищет пользователя по нормализованному нику
func (db *DB) GetUserByNameKey(ctx context.Context, nameKey string) (*domain.User, error) {
	u := &domain.User{}
	query := `
		SELECT id, name, name_key, password_hash, color, bio, avatar, token_version, recovery_hash,
		       is_superadmin, can_create_servers, invited_by, registered_via_invite,
		       status, status_text, status_until, removed, created_at
		FROM users WHERE name_key = ?
	`
	err := db.QueryRowContext(ctx, query, strings.ToLower(nameKey)).Scan(
		&u.ID, &u.Name, &u.NameKey, &u.PasswordHash, &u.Color, &u.Bio, &u.Avatar, &u.TokenVersion, &u.RecoveryHash,
		&u.IsSuperadmin, &u.CanCreateServers, &u.InvitedBy, &u.RegisteredViaInvite,
		&u.Status, &u.StatusText, &u.StatusUntil, &u.Removed, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// HasAnyUsers проверяет, настроен ли уже сервер
func (db *DB) HasAnyUsers(ctx context.Context) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx, "SELECT 1 FROM users LIMIT 1").Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return exists == 1, err
}

// CreateUserTx атомарно создает пользователя, списывает инвайт и добавляет на сервер
func (db *DB) CreateUserTx(ctx context.Context, u *domain.User, invite *domain.ServerInvite) error {
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		// 1. Создаем пользователя
		query := `
			INSERT INTO users (id, name, name_key, password_hash, color, bio, avatar, token_version,
			                   recovery_hash, is_superadmin, can_create_servers, invited_by, 
			                   registered_via_invite, status, status_text, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		_, err := tx.ExecContext(ctx, query,
			u.ID, u.Name, u.NameKey, u.PasswordHash, u.Color, u.Bio, u.Avatar, u.TokenVersion,
			u.RecoveryHash, u.IsSuperadmin, u.CanCreateServers, u.InvitedBy,
			u.RegisteredViaInvite, u.Status, u.StatusText, u.CreatedAt,
		)
		if err != nil {
			return err
		}

		// 2. Если регистрация по серверному инвайту — добавляем на сервер и увеличиваем счетчик
		if invite != nil {
			_, err = tx.ExecContext(ctx,
				"INSERT INTO server_members (server_id, user_id, role, joined_at) VALUES (?, ?, 'member', ?)",
				invite.ServerID, u.ID, u.CreatedAt,
			)
			if err != nil {
				return err
			}

			_, err = tx.ExecContext(ctx, "UPDATE server_invites SET uses = uses + 1 WHERE code = ?", invite.Code)
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// UpdatePasswordAtomically обновляет пароль, повышает token_version и очищает reset_links
func (db *DB) UpdatePasswordAtomically(ctx context.Context, userID, newHash, oldHash string, oldVersion int) (bool, error) {
	var updated bool
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			"UPDATE users SET password_hash = ?, token_version = token_version + 1 WHERE id = ? AND password_hash = ? AND token_version = ?",
			newHash, userID, oldHash, oldVersion,
		)
		if err != nil {
			return err
		}
		rows, err := res.RowsAffected()
		if err != nil || rows == 0 {
			return nil
		}

		_, _ = tx.ExecContext(ctx, "DELETE FROM password_resets WHERE user_id = ?", userID)
		updated = true
		return nil
	})
	return updated, err
}

// UpdateRecoveryKeyAtomically обновляет ключ восстановления и сбрасывает сессии
func (db *DB) UpdateRecoveryKeyAtomically(ctx context.Context, userID, newPassHash, newRecHash, oldRecHash string, oldVersion int) (bool, error) {
	var updated bool
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE users SET password_hash = ?, recovery_hash = ?, token_version = token_version + 1 
			 WHERE id = ? AND recovery_hash = ? AND token_version = ?`,
			newPassHash, newRecHash, userID, oldRecHash, oldVersion,
		)
		if err != nil {
			return err
		}
		rows, err := res.RowsAffected()
		if err != nil || rows == 0 {
			return nil
		}

		_, _ = tx.ExecContext(ctx, "DELETE FROM password_resets WHERE user_id = ?", userID)
		updated = true
		return nil
	})
	return updated, err
}

// InvalidateAllSessions повышает token_version, завершая все сессии
func (db *DB) InvalidateAllSessions(ctx context.Context, userID string) error {
	_, err := db.ExecContext(ctx, "UPDATE users SET token_version = token_version + 1 WHERE id = ?", userID)
	return err
}

// UpdateStatus обновляет статус пользователя
func (db *DB) UpdateStatus(ctx context.Context, userID, status, text string, until *int64) error {
	var untilVal sql.NullInt64
	if until != nil {
		untilVal = sql.NullInt64{Int64: *until, Valid: true}
	}
	_, err := db.ExecContext(ctx,
		"UPDATE users SET status = ?, status_text = ?, status_until = ? WHERE id = ?",
		status, text, untilVal, userID,
	)
	return err
}

// ExpireDueStatuses возвращает пользователей с истекшим таймером статуса
func (db *DB) ExpireDueStatuses(ctx context.Context, now int64) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT id FROM users WHERE status_until IS NOT NULL AND status_until <= ?", now)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(ids) > 0 {
		_, _ = db.ExecContext(ctx, "UPDATE users SET status = 'online', status_until = NULL WHERE status_until IS NOT NULL AND status_until <= ?", now)
	}

	return ids, nil
}

// BlockUser добавляет пользователя в черный список
func (db *DB) BlockUser(ctx context.Context, userID, blockedID string) error {
	_, err := db.ExecContext(ctx,
		"INSERT OR IGNORE INTO blocks (user_id, blocked_id, created_at) VALUES (?, ?, ?)",
		userID, blockedID, NowMs(),
	)
	return err
}

// UnblockUser убирает блокировку
func (db *DB) UnblockUser(ctx context.Context, userID, blockedID string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM blocks WHERE user_id = ? AND blocked_id = ?", userID, blockedID)
	return err
}

// GetBlockedIDs возвращает список ID заблокированных пользователей
func (db *DB) GetBlockedIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT blocked_id FROM blocks WHERE user_id = ? ORDER BY created_at ASC", userID)
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

// SetUserRemoved блокирует/разблокирует пользователя глобально
func (db *DB) SetUserRemoved(ctx context.Context, userID string, removed bool) error {
	removedVal := 0
	if removed {
		removedVal = 1
	}
	_, err := db.ExecContext(ctx,
		"UPDATE users SET removed = ?, token_version = token_version + 1 WHERE id = ?",
		removedVal, userID,
	)
	return err
}

// CreatePasswordReset сохраняет SHA-256 токена сброса пароля (живет 24 часа)
func (db *DB) CreatePasswordReset(ctx context.Context, userID, tokenHash string, expiresAt, now int64) error {
	query := `
		INSERT INTO password_resets (user_id, token_hash, expires_at, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET token_hash = excluded.token_hash, expires_at = excluded.expires_at, created_at = excluded.created_at
	`
	_, err := db.ExecContext(ctx, query, userID, tokenHash, expiresAt, now)
	return err
}

// FindPasswordReset ищет активный токен сброса и возвращает имя пользователя
func (db *DB) FindPasswordReset(ctx context.Context, tokenHash string, now int64) (string, string, error) {
	var userID, name string
	query := `
		SELECT pr.user_id, u.name 
		FROM password_resets pr
		JOIN users u ON u.id = pr.user_id
		WHERE pr.token_hash = ? AND pr.expires_at > ?
	`
	err := db.QueryRowContext(ctx, query, tokenHash, now).Scan(&userID, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return userID, name, err
}

// ConsumePasswordResetTx атомарно удаляет использованный токен, обновляет пароль и инвалидирует сессии
func (db *DB) ConsumePasswordResetTx(ctx context.Context, tokenHash, newPassHash, newRecHash string) (string, int, error) {
	var userID string
	var newVersion int

	now := NowMs()
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		// 1. Проверяем токен и удаляем его в один шаг (защита от повторного использования)
		err := tx.QueryRowContext(ctx,
			"SELECT user_id FROM password_resets WHERE token_hash = ? AND expires_at > ?",
			tokenHash, now,
		).Scan(&userID)
		if err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx, "DELETE FROM password_resets WHERE token_hash = ?", tokenHash)
		if err != nil {
			return err
		}
		rows, err := res.RowsAffected()
		if err != nil || rows == 0 {
			return sql.ErrNoRows
		}

		// 2. Обновляем пароль и recovery hash
		_, err = tx.ExecContext(ctx,
			`UPDATE users SET password_hash = ?, recovery_hash = ?, token_version = token_version + 1 
			 WHERE id = ?`,
			newPassHash, newRecHash, userID,
		)
		if err != nil {
			return err
		}

		// 3. Получаем новую версию токена
		return tx.QueryRowContext(ctx, "SELECT token_version FROM users WHERE id = ?", userID).Scan(&newVersion)
	})

	return userID, newVersion, err
}

// UpdateBio обновляет био пользователя
func (db *DB) UpdateBio(ctx context.Context, userID, bio string) error {
	_, err := db.ExecContext(ctx, "UPDATE users SET bio = ? WHERE id = ?", bio, userID)
	return err
}

// UpdateName обновляет никнейм и name_key
func (db *DB) UpdateName(ctx context.Context, userID, name string) error {
	_, err := db.ExecContext(ctx, "UPDATE users SET name = ?, name_key = ? WHERE id = ?", name, strings.ToLower(name), userID)
	return err
}

// UpdateAvatar сохраняет новое имя файла аватара и возвращает старый файл для удаления
func (db *DB) UpdateAvatar(ctx context.Context, userID string, avatarName *string) (string, error) {
	var oldAvatar sql.NullString
	_ = db.QueryRowContext(ctx, "SELECT avatar FROM users WHERE id = ?", userID).Scan(&oldAvatar)

	_, err := db.ExecContext(ctx, "UPDATE users SET avatar = ? WHERE id = ?", avatarName, userID)
	if err != nil {
		return "", err
	}
	return oldAvatar.String, nil
}
