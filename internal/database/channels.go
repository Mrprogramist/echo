package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Mrprogramist/echo/internal/domain"
)

type ReactionGroup struct {
	Emoji string   `json:"emoji"`
	Users []string `json:"users"`
}

type DeletedChannelInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	DeletedAt int64  `json:"deletedAt"`
	Messages  int    `json:"messages"`
}

// ListChannels возвращает активные каналы сервера
func (db *DB) ListChannels(ctx context.Context, serverID string) ([]*domain.Channel, error) {
	query := `
		SELECT id, server_id, category_id, name, kind, position, deleted_at, created_at
		FROM channels
		WHERE server_id = ? AND deleted_at IS NULL
		ORDER BY position ASC, created_at ASC
	`
	rows, err := db.QueryContext(ctx, query, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*domain.Channel
	for rows.Next() {
		ch := &domain.Channel{}
		if err := rows.Scan(&ch.ID, &ch.ServerID, &ch.CategoryID, &ch.Name, &ch.Kind, &ch.Position, &ch.DeletedAt, &ch.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, ch)
	}
	return list, rows.Err()
}

// ListCategories возвращает категории сервера
func (db *DB) ListCategories(ctx context.Context, serverID string) ([]*domain.Category, error) {
	query := `
		SELECT id, server_id, name, position, created_at
		FROM categories
		WHERE server_id = ?
		ORDER BY position ASC, created_at ASC
	`
	rows, err := db.QueryContext(ctx, query, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*domain.Category
	for rows.Next() {
		cat := &domain.Category{}
		if err := rows.Scan(&cat.ID, &cat.ServerID, &cat.Name, &cat.Position, &cat.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, cat)
	}
	return list, rows.Err()
}

// GetChannel возвращает канал по ID
func (db *DB) GetChannel(ctx context.Context, channelID string) (*domain.Channel, error) {
	ch := &domain.Channel{}
	err := db.QueryRowContext(ctx,
		`SELECT id, server_id, category_id, name, kind, position, deleted_at, created_at
		 FROM channels WHERE id = ? AND deleted_at IS NULL`,
		channelID,
	).Scan(&ch.ID, &ch.ServerID, &ch.CategoryID, &ch.Name, &ch.Kind, &ch.Position, &ch.DeletedAt, &ch.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return ch, err
}

// CreateChannel добавляет канал на сервер
func (db *DB) CreateChannel(ctx context.Context, ch *domain.Channel) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO channels (id, server_id, category_id, name, kind, position, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ch.ID, ch.ServerID, ch.CategoryID, ch.Name, ch.Kind, ch.Position, ch.CreatedAt,
	)
	return err
}

// UpdateChannel обновляет имя и категорию канала
func (db *DB) UpdateChannel(ctx context.Context, channelID, name string, categoryID *string) error {
	_, err := db.ExecContext(ctx,
		"UPDATE channels SET name = ?, category_id = ? WHERE id = ?",
		name, categoryID, channelID,
	)
	return err
}

// SoftDeleteChannel отправляет канал в корзину
func (db *DB) SoftDeleteChannel(ctx context.Context, channelID string, deletedAt int64) error {
	_, err := db.ExecContext(ctx, "UPDATE channels SET deleted_at = ? WHERE id = ?", deletedAt, channelID)
	return err
}

// RestoreChannel восстанавливает канал из корзины
func (db *DB) RestoreChannel(ctx context.Context, channelID string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE channels SET deleted_at = NULL, 
		 category_id = CASE WHEN category_id IN (SELECT id FROM categories) THEN category_id ELSE NULL END
		 WHERE id = ?`,
		channelID,
	)
	return err
}

// ReorderChannels обновляет порядок каналов
func (db *DB) ReorderChannels(ctx context.Context, channelIDs []string) error {
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, "UPDATE channels SET position = ? WHERE id = ?")
		if err != nil {
			return err
		}
		defer stmt.Close()

		for pos, id := range channelIDs {
			if _, err := stmt.ExecContext(ctx, pos, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetChannelMessages получает страницу истории с составным курсором (защита R10)
func (db *DB) GetChannelMessages(ctx context.Context, channelID string, beforeTime int64, beforeID string, limit int) ([]*domain.Message, error) {
	var query string
	var args []any

	if beforeID != "" && beforeTime > 0 {
		query = `
			SELECT id, channel_id, user_id, content, attachments, reply_to, edited_at, created_at
			FROM messages
			WHERE channel_id = ? AND (created_at < ? OR (created_at = ? AND id < ?))
			ORDER BY created_at DESC, id DESC LIMIT ?
		`
		args = []any{channelID, beforeTime, beforeTime, beforeID, limit}
	} else if beforeTime > 0 {
		query = `
			SELECT id, channel_id, user_id, content, attachments, reply_to, edited_at, created_at
			FROM messages
			WHERE channel_id = ? AND created_at < ?
			ORDER BY created_at DESC, id DESC LIMIT ?
		`
		args = []any{channelID, beforeTime, limit}
	} else {
		query = `
			SELECT id, channel_id, user_id, content, attachments, reply_to, edited_at, created_at
			FROM messages
			WHERE channel_id = ?
			ORDER BY created_at DESC, id DESC LIMIT ?
		`
		args = []any{channelID, limit}
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*domain.Message
	for rows.Next() {
		m := &domain.Message{}
		var attachJSON string
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.UserID, &m.Content, &attachJSON, &m.ReplyTo, &m.EditedAt, &m.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(attachJSON), &m.Attachments)
		messages = append(messages, m)
	}

	return messages, rows.Err()
}

// GetReactionsForMessages батчем собирает реакции для списка сообщений
func (db *DB) GetReactionsForMessages(ctx context.Context, messageIDs []string) (map[string][]ReactionGroup, error) {
	result := make(map[string][]ReactionGroup)
	if len(messageIDs) == 0 {
		return result, nil
	}

	placeholders := make([]string, len(messageIDs))
	args := make([]any, len(messageIDs))
	for i, id := range messageIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT message_id, emoji, user_id FROM reactions WHERE message_id IN (%s) ORDER BY created_at ASC`,
		strings.Join(placeholders, ", "),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	temp := make(map[string]map[string][]string)
	for rows.Next() {
		var mid, emoji, uid string
		if err := rows.Scan(&mid, &emoji, &uid); err != nil {
			return nil, err
		}
		if _, ok := temp[mid]; !ok {
			temp[mid] = make(map[string][]string)
		}
		temp[mid][emoji] = append(temp[mid][emoji], uid)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	for mid, byEmoji := range temp {
		var groups []ReactionGroup
		for e, users := range byEmoji {
			groups = append(groups, ReactionGroup{Emoji: e, Users: users})
		}
		result[mid] = groups
	}

	return result, nil
}

// MarkRead фиксирует метку прочтения
func (db *DB) MarkRead(ctx context.Context, userID, convID string, at int64) error {
	query := `
		INSERT INTO read_states (user_id, conversation_id, read_at) VALUES (?, ?, ?)
		ON CONFLICT (user_id, conversation_id) DO UPDATE SET read_at = MAX(read_at, excluded.read_at)
	`
	_, err := db.ExecContext(ctx, query, userID, convID, at)
	return err
}

// MuteChannel включает/выключает уведомления канала
func (db *DB) MuteChannel(ctx context.Context, userID, channelID string, mute bool) error {
	if mute {
		_, err := db.ExecContext(ctx, "INSERT OR IGNORE INTO channel_mutes (user_id, channel_id) VALUES (?, ?)", userID, channelID)
		return err
	}
	_, err := db.ExecContext(ctx, "DELETE FROM channel_mutes WHERE user_id = ? AND channel_id = ?", userID, channelID)
	return err
}

// ListMutedChannels возвращает заглушенные каналы
func (db *DB) ListMutedChannels(ctx context.Context, userID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT channel_id FROM channel_mutes WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			res = append(res, id)
		}
	}
	return res, rows.Err()
}

// GetDeletedChannels возвращает каналы сервера в корзине
func (db *DB) GetDeletedChannels(ctx context.Context, serverID string) ([]*DeletedChannelInfo, error) {
	query := `
		SELECT c.id, c.name, c.kind, c.deleted_at,
		       (SELECT COUNT(*) FROM messages m WHERE m.channel_id = c.id) AS messages
		FROM channels c
		WHERE c.server_id = ? AND c.deleted_at IS NOT NULL
		ORDER BY c.deleted_at DESC
	`
	rows, err := db.QueryContext(ctx, query, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*DeletedChannelInfo
	for rows.Next() {
		info := &DeletedChannelInfo{}
		if err := rows.Scan(&info.ID, &info.Name, &info.Kind, &info.DeletedAt, &info.Messages); err != nil {
			return nil, err
		}
		list = append(list, info)
	}
	return list, rows.Err()
}
