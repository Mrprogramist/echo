package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Mrprogramist/echo/internal/domain"
)

// MemberWithUser содержит данные участника сервера вместе с базовым профилем пользователя
type MemberWithUser struct {
	ServerID string            `json:"server_id"`
	UserID   string            `json:"user_id"`
	Role     domain.ServerRole `json:"role"`
	JoinedAt int64             `json:"joined_at"`
	Name     string            `json:"name"`
	Color    string            `json:"color"`
	Avatar   sql.NullString    `json:"avatar"`
	Status   string            `json:"status"`
}

// GetServer возвращает сервер по ID
func (db *DB) GetServer(ctx context.Context, serverID string) (*domain.Server, error) {
	s := &domain.Server{}
	err := db.QueryRowContext(ctx,
		"SELECT id, name, description, icon, owner_id, created_at FROM servers WHERE id = ?",
		serverID,
	).Scan(&s.ID, &s.Name, &s.Description, &s.Icon, &s.OwnerID, &s.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

// ListUserServers возвращает все серверы, на которых состоит пользователь
func (db *DB) ListUserServers(ctx context.Context, userID string) ([]*domain.Server, error) {
	query := `
		SELECT s.id, s.name, s.description, s.icon, s.owner_id, s.created_at
		FROM servers s
		JOIN server_members sm ON sm.server_id = s.id
		WHERE sm.user_id = ?
		ORDER BY sm.joined_at ASC
	`
	rows, err := db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []*domain.Server
	for rows.Next() {
		s := &domain.Server{}
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Icon, &s.OwnerID, &s.CreatedAt); err != nil {
			return nil, err
		}
		servers = append(servers, s)
	}
	return servers, rows.Err()
}

// UpdateServer обновляет название и описание сервера
func (db *DB) UpdateServer(ctx context.Context, serverID, name, desc string) error {
	_, err := db.ExecContext(ctx,
		"UPDATE servers SET name = ?, description = ? WHERE id = ?",
		name, desc, serverID,
	)
	return err
}

// UpdateServerIcon обновляет или сбрасывает иконку сервера
func (db *DB) UpdateServerIcon(ctx context.Context, serverID string, iconName *string) error {
	_, err := db.ExecContext(ctx, "UPDATE servers SET icon = ? WHERE id = ?", iconName, serverID)
	return err
}

// DeleteServer удаляет сервер (каскадно удаляет каналы, участников и сообщения в БД)
func (db *DB) DeleteServer(ctx context.Context, serverID string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM servers WHERE id = ?", serverID)
	return err
}

// GetMember возвращает запись участника сервера
func (db *DB) GetMember(ctx context.Context, serverID, userID string) (*domain.ServerMember, error) {
	m := &domain.ServerMember{}
	err := db.QueryRowContext(ctx,
		"SELECT server_id, user_id, role, joined_at FROM server_members WHERE server_id = ? AND user_id = ?",
		serverID, userID,
	).Scan(&m.ServerID, &m.UserID, &m.Role, &m.JoinedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

// ListServerMembers возвращает список всех участников сервера с их никами и аватарами
func (db *DB) ListServerMembers(ctx context.Context, serverID string) ([]*MemberWithUser, error) {
	query := `
		SELECT sm.server_id, sm.user_id, sm.role, sm.joined_at, u.name, u.color, u.avatar, u.status
		FROM server_members sm
		JOIN users u ON u.id = sm.user_id
		WHERE sm.server_id = ?
		ORDER BY 
			CASE sm.role
				WHEN 'owner' THEN 1
				WHEN 'admin' THEN 2
				WHEN 'moderator' THEN 3
				ELSE 4
			END,
			u.name_key ASC
	`
	rows, err := db.QueryContext(ctx, query, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*MemberWithUser
	for rows.Next() {
		m := &MemberWithUser{}
		if err := rows.Scan(&m.ServerID, &m.UserID, &m.Role, &m.JoinedAt, &m.Name, &m.Color, &m.Avatar, &m.Status); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// RemoveMember удаляет пользователя с сервера
func (db *DB) RemoveMember(ctx context.Context, serverID, userID string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM server_members WHERE server_id = ? AND user_id = ?", serverID, userID)
	return err
}

// UpdateMemberRole меняет роль участника
func (db *DB) UpdateMemberRole(ctx context.Context, serverID, userID string, role domain.ServerRole) error {
	_, err := db.ExecContext(ctx, "UPDATE server_members SET role = ? WHERE server_id = ? AND user_id = ?", role, serverID, userID)
	return err
}

// CreateInvite сохраняет инвайт на сервер
func (db *DB) CreateInvite(ctx context.Context, inv *domain.ServerInvite) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO server_invites (code, server_id, created_by, allows_registration, max_uses, uses, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		inv.Code, inv.ServerID, inv.CreatedBy, inv.AllowsRegistration, inv.MaxUses, inv.Uses, inv.ExpiresAt, inv.CreatedAt,
	)
	return err
}

// GetInvite возвращает инвайт по его коду
func (db *DB) GetInvite(ctx context.Context, code string) (*domain.ServerInvite, error) {
	inv := &domain.ServerInvite{}
	err := db.QueryRowContext(ctx,
		`SELECT code, server_id, created_by, allows_registration, max_uses, uses, expires_at, created_at
		 FROM server_invites WHERE code = ?`,
		code,
	).Scan(&inv.Code, &inv.ServerID, &inv.CreatedBy, &inv.AllowsRegistration, &inv.MaxUses, &inv.Uses, &inv.ExpiresAt, &inv.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return inv, err
}

// ListServerInvites возвращает активные инвайты сервера
func (db *DB) ListServerInvites(ctx context.Context, serverID string) ([]*domain.ServerInvite, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT code, server_id, created_by, allows_registration, max_uses, uses, expires_at, created_at
		 FROM server_invites WHERE server_id = ? ORDER BY created_at DESC`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*domain.ServerInvite
	for rows.Next() {
		inv := &domain.ServerInvite{}
		if err := rows.Scan(&inv.Code, &inv.ServerID, &inv.CreatedBy, &inv.AllowsRegistration, &inv.MaxUses, &inv.Uses, &inv.ExpiresAt, &inv.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, inv)
	}
	return list, rows.Err()
}

// DeleteInvite удаляет инвайт
func (db *DB) DeleteInvite(ctx context.Context, serverID, code string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM server_invites WHERE server_id = ? AND code = ?", serverID, code)
	return err
}
