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

type DMWithDetails struct {
	ID            string            `json:"id"`
	Kind          domain.DMKind     `json:"kind"`
	Name          string            `json:"name"`
	OwnerID       sql.NullString    `json:"ownerId,omitempty"`
	Members       []string          `json:"members"`
	OtherUserID   string            `json:"userId,omitempty"` // ID собеседника для 1-на-1
	LastMessageAt int64             `json:"lastMessageAt"`
	Preview       *DMLastMsgPreview `json:"preview,omitempty"`
}

type DMLastMsgPreview struct {
	UserID string `json:"userId"`
	Text   string `json:"text"`
	Images int    `json:"images"`
}

// OrderedPair возвращает пару ID в лексикографическом порядке для уникального pair_key
func OrderedPair(a, b string) (string, string) {
	if a < b {
		return a, b
	}
	return b, a
}

// IsBlocked проверяет наличие взаимной блокировки между двумя пользователями
func (db *DB) IsBlocked(ctx context.Context, userA, userB string) (bool, error) {
	var count int
	query := "SELECT COUNT(*) FROM blocks WHERE (user_id = ? AND blocked_id = ?) OR (user_id = ? AND blocked_id = ?)"
	err := db.QueryRowContext(ctx, query, userA, userB, userB, userA).Scan(&count)
	return count > 0, err
}

// GetDM возвращает переписку, если пользователь является ее участником
func (db *DB) GetDM(ctx context.Context, dmID, userID string) (*domain.DM, error) {
	query := `
		SELECT d.id, d.kind, d.name, d.owner_id, d.pair_key, d.created_at
		FROM dms d
		JOIN dm_members m ON m.dm_id = d.id
		WHERE d.id = ? AND m.user_id = ?
	`
	dm := &domain.DM{}
	err := db.QueryRowContext(ctx, query, dmID, userID).Scan(
		&dm.ID, &dm.Kind, &dm.Name, &dm.OwnerID, &dm.PairKey, &dm.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return dm, err
}

// GetDMMemberIDs возвращает список всех ID участников переписки
func (db *DB) GetDMMemberIDs(ctx context.Context, dmID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT user_id FROM dm_members WHERE dm_id = ? ORDER BY joined_at ASC", dmID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		ids = append(ids, uid)
	}
	return ids, rows.Err()
}

// GetOrCreateDM атомарно находит или создает личный чат 1-на-1
func (db *DB) GetOrCreateDM(ctx context.Context, userA, userB string) (*domain.DM, error) {
	u1, u2 := OrderedPair(userA, userB)
	pairKey := fmt.Sprintf("%s:%s", u1, u2)
	now := NowMs()

	var dmID string
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		// 1. Проверяем, существует ли уже
		err := tx.QueryRowContext(ctx, "SELECT id FROM dms WHERE pair_key = ?", pairKey).Scan(&dmID)
		if err == nil {
			return nil // Чат уже есть
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		// 2. Создаем новый DM
		dmID = strings.ReplaceAll(fmt.Sprintf("%s%s", u1[:16], u2[:16]), "-", "")
		_, err = tx.ExecContext(ctx,
			"INSERT INTO dms (id, kind, pair_key, created_at) VALUES (?, 'dm', ?, ?)",
			dmID, pairKey, now,
		)
		if err != nil {
			return err
		}

		// 3. Добавляем обоих участников
		stmt, err := tx.PrepareContext(ctx, "INSERT INTO dm_members (dm_id, user_id, joined_at) VALUES (?, ?, ?)")
		if err != nil {
			return err
		}
		defer stmt.Close()

		if _, err := stmt.ExecContext(ctx, dmID, userA, now); err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, dmID, userB, now); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return &domain.DM{
		ID:        dmID,
		Kind:      domain.DMKindDirect,
		PairKey:   sql.NullString{String: pairKey, Valid: true},
		CreatedAt: now,
	}, nil
}

// CreateGroup создает групповой чат
func (db *DB) CreateGroup(ctx context.Context, groupID, name, ownerID string, memberIDs []string) (*domain.DM, error) {
	now := NowMs()
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			"INSERT INTO dms (id, kind, name, owner_id, created_at) VALUES (?, 'group', ?, ?, ?)",
			groupID, name, ownerID, now,
		)
		if err != nil {
			return err
		}

		stmt, err := tx.PrepareContext(ctx, "INSERT INTO dm_members (dm_id, user_id, joined_at) VALUES (?, ?, ?)")
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, m := range memberIDs {
			if _, err := stmt.ExecContext(ctx, groupID, m, now); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return &domain.DM{
		ID:        groupID,
		Kind:      domain.DMKindGroup,
		Name:      name,
		OwnerID:   sql.NullString{String: ownerID, Valid: true},
		CreatedAt: now,
	}, nil
}

// AddGroupMembers добавляет пользователей в группу
func (db *DB) AddGroupMembers(ctx context.Context, groupID string, userIDs []string) error {
	now := NowMs()
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO dm_members (dm_id, user_id, joined_at) VALUES (?, ?, ?)")
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, uid := range userIDs {
			if _, err := stmt.ExecContext(ctx, groupID, uid, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// RenameGroup переименовывает группу
func (db *DB) RenameGroup(ctx context.Context, groupID, newName string) error {
	_, err := db.ExecContext(ctx, "UPDATE dms SET name = ? WHERE id = ?", newName, groupID)
	return err
}

// LeaveGroup выходит из группы (с передачей прав или каскадной очисткой)
func (db *DB) LeaveGroup(ctx context.Context, groupID, userID string) (bool, []string, error) {
	var remaining []string
	var wasDeleted bool

	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		// 1. Удаляем участника
		if _, err := tx.ExecContext(ctx, "DELETE FROM dm_members WHERE dm_id = ? AND user_id = ?", groupID, userID); err != nil {
			return err
		}

		// 2. Получаем оставшихся участников
		rows, err := tx.QueryContext(ctx, "SELECT user_id FROM dm_members WHERE dm_id = ? ORDER BY joined_at ASC", groupID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var u string
			if err := rows.Scan(&u); err == nil {
				remaining = append(remaining, u)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}

		// 3. Если участников не осталось — удаляем данные группы
		if len(remaining) == 0 {
			wasDeleted = true
			_, _ = tx.ExecContext(ctx, "DELETE FROM reactions WHERE message_id IN (SELECT id FROM dm_messages WHERE dm_id = ?)", groupID)
			_, _ = tx.ExecContext(ctx, "DELETE FROM read_states WHERE conversation_id = ?", groupID)
			_, _ = tx.ExecContext(ctx, "DELETE FROM voice_grants WHERE room = ?", fmt.Sprintf("dm-%s", groupID))
			_, _ = tx.ExecContext(ctx, "DELETE FROM uploads WHERE scope = ?", fmt.Sprintf("dm:%s", groupID))
			_, _ = tx.ExecContext(ctx, "DELETE FROM dm_messages WHERE dm_id = ?", groupID)
			_, _ = tx.ExecContext(ctx, "DELETE FROM dms WHERE id = ?", groupID)
			return nil
		}

		// 4. Если вышел владелец — передаем права первому оставшемуся участнику
		var ownerID sql.NullString
		_ = tx.QueryRowContext(ctx, "SELECT owner_id FROM dms WHERE id = ?", groupID).Scan(&ownerID)
		if ownerID.Valid && ownerID.String == userID {
			_, err = tx.ExecContext(ctx, "UPDATE dms SET owner_id = ? WHERE id = ?", remaining[0], groupID)
			return err
		}

		return nil
	})

	return wasDeleted, remaining, err
}

// ListUserDMs возвращает список всех личных и групповых чатов пользователя с превью
func (db *DB) ListUserDMs(ctx context.Context, userID string) ([]*DMWithDetails, error) {
	query := `
		SELECT d.id, d.kind, d.name, d.owner_id, d.created_at
		FROM dms d
		JOIN dm_members m ON m.dm_id = d.id
		WHERE m.user_id = ?
	`
	rows, err := db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dmList []*DMWithDetails
	var dmIDs []string
	for rows.Next() {
		dm := &DMWithDetails{}
		if err := rows.Scan(&dm.ID, &dm.Kind, &dm.Name, &dm.OwnerID, &dm.LastMessageAt); err != nil {
			return nil, err
		}
		dmList = append(dmList, dm)
		dmIDs = append(dmIDs, dm.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(dmIDs) == 0 {
		return []*DMWithDetails{}, nil
	}

	// Батч-запрос участников
	placeholders := make([]string, len(dmIDs))
	args := make([]any, len(dmIDs))
	for i, id := range dmIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	membersQuery := fmt.Sprintf(
		"SELECT dm_id, user_id FROM dm_members WHERE dm_id IN (%s) ORDER BY joined_at ASC",
		strings.Join(placeholders, ", "),
	)
	mRows, err := db.QueryContext(ctx, membersQuery, args...)
	if err == nil {
		defer mRows.Close()
		membersMap := make(map[string][]string)
		for mRows.Next() {
			var did, uid string
			if err := mRows.Scan(&did, &uid); err == nil {
				membersMap[did] = append(membersMap[did], uid)
			}
		}
		_ = mRows.Err()

		for _, dm := range dmList {
			dm.Members = membersMap[dm.ID]
			if dm.Kind == domain.DMKindDirect {
				for _, m := range dm.Members {
					if m != userID {
						dm.OtherUserID = m
						break
					}
				}
				if dm.OtherUserID == "" {
					dm.OtherUserID = userID // Чат с самим собой
				}
			}
		}
	}

	// Батч-запрос последнего сообщения для каждого чата
	lastMsgQuery := fmt.Sprintf(`
		SELECT m.dm_id, m.user_id, m.content, m.attachments, m.created_at
		FROM dm_messages m
		WHERE m.dm_id IN (%s)
		  AND m.id = (SELECT id FROM dm_messages WHERE dm_id = m.dm_id ORDER BY created_at DESC, id DESC LIMIT 1)
	`, strings.Join(placeholders, ", "))

	lRows, err := db.QueryContext(ctx, lastMsgQuery, args...)
	if err == nil {
		defer lRows.Close()
		for lRows.Next() {
			var did, uid, content, attachJSON string
			var createdAt int64
			if err := lRows.Scan(&did, &uid, &content, &attachJSON, &createdAt); err == nil {
				for _, dm := range dmList {
					if dm.ID == did {
						dm.LastMessageAt = createdAt
						textPreview := content
						if len(textPreview) > 140 {
							textPreview = textPreview[:140]
						}
						var attaches []domain.Attachment
						_ = json.Unmarshal([]byte(attachJSON), &attaches)

						dm.Preview = &DMLastMsgPreview{
							UserID: uid,
							Text:   textPreview,
							Images: len(attaches),
						}
						break
					}
				}
			}
		}
		_ = lRows.Err()
	}

	return dmList, nil
}

// GetDMMessages возвращает сообщения в ЛС с составным курсором (защита от бага R10)
func (db *DB) GetDMMessages(ctx context.Context, dmID string, beforeTime int64, beforeID string, limit int) ([]*domain.DMMessage, error) {
	var query string
	var args []any

	if beforeID != "" && beforeTime > 0 {
		query = `
			SELECT id, dm_id, user_id, content, attachments, reply_to, edited_at, created_at
			FROM dm_messages
			WHERE dm_id = ? AND (created_at < ? OR (created_at = ? AND id < ?))
			ORDER BY created_at DESC, id DESC LIMIT ?
		`
		args = []any{dmID, beforeTime, beforeTime, beforeID, limit}
	} else if beforeTime > 0 {
		query = `
			SELECT id, dm_id, user_id, content, attachments, reply_to, edited_at, created_at
			FROM dm_messages
			WHERE dm_id = ? AND created_at < ?
			ORDER BY created_at DESC, id DESC LIMIT ?
		`
		args = []any{dmID, beforeTime, limit}
	} else {
		query = `
			SELECT id, dm_id, user_id, content, attachments, reply_to, edited_at, created_at
			FROM dm_messages
			WHERE dm_id = ?
			ORDER BY created_at DESC, id DESC LIMIT ?
		`
		args = []any{dmID, limit}
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*domain.DMMessage
	for rows.Next() {
		m := &domain.DMMessage{}
		var attachJSON string
		if err := rows.Scan(&m.ID, &m.DMID, &m.UserID, &m.Content, &attachJSON, &m.ReplyTo, &m.EditedAt, &m.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(attachJSON), &m.Attachments)
		messages = append(messages, m)
	}

	return messages, rows.Err()
}
