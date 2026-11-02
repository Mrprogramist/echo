package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mrprogramist/echo/internal/config"
	"github.com/Mrprogramist/echo/internal/database"
	"github.com/Mrprogramist/echo/internal/domain"
	"github.com/Mrprogramist/echo/internal/media"
)

var (
	ErrForbidden        = errors.New("forbidden: insufficient permissions")
	ErrServerNotFound   = errors.New("server not found")
	ErrCannotKickOwner  = errors.New("cannot kick or demote server owner")
	ErrCannotLeaveOwner = errors.New("owner cannot leave server without transferring ownership or deleting it")
)

type ServerService struct {
	cfg *config.Config
	db  *database.DB
}

func NewServerService(cfg *config.Config, db *database.DB) *ServerService {
	return &ServerService{cfg: cfg, db: db}
}

// CanManageServer проверяет, может ли пользователь настраивать этот сервер
func (s *ServerService) CanManageServer(ctx context.Context, user *domain.User, serverID string) (bool, error) {
	if user.IsSuperadmin {
		return true, nil
	}
	member, err := s.db.GetMember(ctx, serverID, user.ID)
	if err != nil {
		return false, err
	}
	if member == nil {
		return false, nil
	}
	return member.Role == domain.RoleOwner || member.Role == domain.RoleAdmin, nil
}

// CanViewServer проверяет, состоит ли пользователь на сервере
func (s *ServerService) CanViewServer(ctx context.Context, user *domain.User, serverID string) (bool, error) {
	if user.IsSuperadmin {
		return true, nil
	}
	member, err := s.db.GetMember(ctx, serverID, user.ID)
	if err != nil {
		return false, err
	}
	return member != nil, nil
}

// CreateServer создает новый сервер с базовыми каналами
func (s *ServerService) CreateServer(ctx context.Context, user *domain.User, name, desc string) (*domain.Server, error) {
	if !user.MayCreateServer() {
		return nil, errors.New("user does not have permission to create servers")
	}

	cleanName := strings.Join(strings.Fields(name), " ")
	if cleanName == "" || len(cleanName) > 40 {
		return nil, errors.New("invalid server name length (1-40 characters)")
	}

	serverID, err := s.db.CreateServerTx(ctx, cleanName, desc, user.ID, config.DefaultServerChannels)
	if err != nil {
		return nil, err
	}

	return s.db.GetServer(ctx, serverID)
}

// ListUserServers возвращает все серверы пользователя
func (s *ServerService) ListUserServers(ctx context.Context, user *domain.User) ([]*domain.Server, error) {
	return s.db.ListUserServers(ctx, user.ID)
}

// GetServerDetails возвращает информацию о сервере
func (s *ServerService) GetServerDetails(ctx context.Context, user *domain.User, serverID string) (*domain.Server, error) {
	canView, err := s.CanViewServer(ctx, user, serverID)
	if err != nil {
		return nil, err
	}
	if !canView {
		return nil, ErrForbidden
	}

	srv, err := s.db.GetServer(ctx, serverID)
	if err != nil || srv == nil {
		return nil, ErrServerNotFound
	}
	return srv, nil
}

// UpdateServer изменяет название и описание сервера
func (s *ServerService) UpdateServer(ctx context.Context, user *domain.User, serverID, name, desc string) (*domain.Server, error) {
	canManage, err := s.CanManageServer(ctx, user, serverID)
	if err != nil {
		return nil, err
	}
	if !canManage {
		return nil, ErrForbidden
	}

	cleanName := strings.Join(strings.Fields(name), " ")
	if cleanName == "" || len(cleanName) > 40 {
		return nil, errors.New("invalid server name (1-40 characters)")
	}

	if err := s.db.UpdateServer(ctx, serverID, cleanName, desc); err != nil {
		return nil, err
	}
	return s.db.GetServer(ctx, serverID)
}

// DeleteServer удаляет сервер со всеми данными
func (s *ServerService) DeleteServer(ctx context.Context, user *domain.User, serverID string) error {
	srv, err := s.db.GetServer(ctx, serverID)
	if err != nil || srv == nil {
		return ErrServerNotFound
	}

	// Удалять может только Владелец сервера или Суперадмин платформы
	if !user.IsSuperadmin && srv.OwnerID != user.ID {
		return ErrForbidden
	}

	// Удаляем файл иконки, если был
	if srv.Icon.Valid && srv.Icon.String != "" {
		media.RemoveFileIfExists(filepath.Join(s.cfg.AvatarDir, srv.Icon.String))
	}

	return s.db.DeleteServer(ctx, serverID)
}

// UpdateServerIcon загружает и нарезает иконку конкретного сервера
func (s *ServerService) UpdateServerIcon(ctx context.Context, user *domain.User, serverID string, data []byte) (*domain.Server, error) {
	canManage, err := s.CanManageServer(ctx, user, serverID)
	if err != nil {
		return nil, err
	}
	if !canManage {
		return nil, ErrForbidden
	}

	srv, err := s.db.GetServer(ctx, serverID)
	if err != nil || srv == nil {
		return nil, ErrServerNotFound
	}

	if len(data) == 0 || int64(len(data)) > config.MaxAvatarUploadBytes {
		return nil, errors.New("invalid image size")
	}

	pngData, err := media.ProcessSquareImage(data, config.ServerIconSize)
	if err != nil {
		return nil, fmt.Errorf("image processing error: %w", err)
	}

	token := make([]byte, 6)
	_, _ = rand.Read(token)
	newFileName := fmt.Sprintf("server-%s-%s.png", serverID, hex.EncodeToString(token))
	newFilePath := filepath.Join(s.cfg.AvatarDir, newFileName)

	if err := os.WriteFile(newFilePath, pngData, 0644); err != nil {
		return nil, fmt.Errorf("failed to save server icon: %w", err)
	}

	if srv.Icon.Valid && srv.Icon.String != "" {
		media.RemoveFileIfExists(filepath.Join(s.cfg.AvatarDir, srv.Icon.String))
	}

	if err := s.db.UpdateServerIcon(ctx, serverID, &newFileName); err != nil {
		return nil, err
	}

	return s.db.GetServer(ctx, serverID)
}

// DeleteServerIcon сбрасывает иконку сервера
func (s *ServerService) DeleteServerIcon(ctx context.Context, user *domain.User, serverID string) (*domain.Server, error) {
	canManage, err := s.CanManageServer(ctx, user, serverID)
	if err != nil {
		return nil, err
	}
	if !canManage {
		return nil, ErrForbidden
	}

	srv, err := s.db.GetServer(ctx, serverID)
	if err != nil || srv == nil {
		return nil, ErrServerNotFound
	}

	if srv.Icon.Valid && srv.Icon.String != "" {
		media.RemoveFileIfExists(filepath.Join(s.cfg.AvatarDir, srv.Icon.String))
		_ = s.db.UpdateServerIcon(ctx, serverID, nil)
	}

	return s.db.GetServer(ctx, serverID)
}

// GetServerIconPath возвращает путь к файлу иконки на диске
func (s *ServerService) GetServerIconPath(ctx context.Context, serverID string) (string, error) {
	srv, err := s.db.GetServer(ctx, serverID)
	if err != nil || srv == nil || !srv.Icon.Valid || srv.Icon.String == "" {
		return "", errors.New("icon not found")
	}

	fullPath := filepath.Join(s.cfg.AvatarDir, srv.Icon.String)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return "", errors.New("file not on disk")
	}

	return fullPath, nil
}

// ListMembers возвращает список участников
func (s *ServerService) ListMembers(ctx context.Context, user *domain.User, serverID string) ([]*database.MemberWithUser, error) {
	canView, err := s.CanViewServer(ctx, user, serverID)
	if err != nil {
		return nil, err
	}
	if !canView {
		return nil, ErrForbidden
	}
	return s.db.ListServerMembers(ctx, serverID)
}

// KickMember исключает участника
func (s *ServerService) KickMember(ctx context.Context, user *domain.User, serverID, targetUserID string) error {
	canManage, err := s.CanManageServer(ctx, user, serverID)
	if err != nil {
		return err
	}
	if !canManage {
		return ErrForbidden
	}

	targetMember, err := s.db.GetMember(ctx, serverID, targetUserID)
	if err != nil || targetMember == nil {
		return errors.New("member not found")
	}

	// Владельца сервера нельзя выгнать
	if targetMember.Role == domain.RoleOwner {
		return ErrCannotKickOwner
	}

	return s.db.RemoveMember(ctx, serverID, targetUserID)
}

// LeaveServer позволяет участнику покинуть сервер
func (s *ServerService) LeaveServer(ctx context.Context, user *domain.User, serverID string) error {
	member, err := s.db.GetMember(ctx, serverID, user.ID)
	if err != nil || member == nil {
		return errors.New("not a member of this server")
	}

	if member.Role == domain.RoleOwner {
		return ErrCannotLeaveOwner
	}

	return s.db.RemoveMember(ctx, serverID, user.ID)
}

// CreateInvite генерирует инвайт вида "XXXX-XXXX"
func (s *ServerService) CreateInvite(ctx context.Context, user *domain.User, serverID string, allowsReg bool, maxUses int, expiresAfterHours int) (*domain.ServerInvite, error) {
	canManage, err := s.CanManageServer(ctx, user, serverID)
	if err != nil {
		return nil, err
	}
	if !canManage {
		return nil, ErrForbidden
	}

	code, err := generateInviteCode()
	if err != nil {
		return nil, err
	}

	now := database.NowMs()
	var expiresAt sql.NullInt64
	if expiresAfterHours > 0 {
		expiresAt = sql.NullInt64{
			Int64: now + int64(expiresAfterHours)*3600*1000,
			Valid: true,
		}
	}

	inv := &domain.ServerInvite{
		Code:               code,
		ServerID:           serverID,
		CreatedBy:          user.ID,
		AllowsRegistration: allowsReg,
		MaxUses:            maxUses,
		Uses:               0,
		ExpiresAt:          expiresAt,
		CreatedAt:          now,
	}

	if err := s.db.CreateInvite(ctx, inv); err != nil {
		return nil, err
	}

	return inv, nil
}

// ListInvites возвращает инвайты сервера
func (s *ServerService) ListInvites(ctx context.Context, user *domain.User, serverID string) ([]*domain.ServerInvite, error) {
	canManage, err := s.CanManageServer(ctx, user, serverID)
	if err != nil {
		return nil, err
	}
	if !canManage {
		return nil, ErrForbidden
	}
	return s.db.ListServerInvites(ctx, serverID)
}

// DeleteInvite аннулирует инвайт
func (s *ServerService) DeleteInvite(ctx context.Context, user *domain.User, serverID, code string) error {
	canManage, err := s.CanManageServer(ctx, user, serverID)
	if err != nil {
		return err
	}
	if !canManage {
		return ErrForbidden
	}
	return s.db.DeleteInvite(ctx, serverID, code)
}

// generateInviteCode создает удобный код из алфавита Base32 (формат: ABCD-EFGH)
func generateInviteCode() (string, error) {
	alphabet := config.InviteAlphabet
	alphabetLen := big.NewInt(int64(len(alphabet)))
	var b strings.Builder

	for i := 0; i < 8; i++ {
		if i == 4 {
			b.WriteByte('-')
		}
		num, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", err
		}
		b.WriteByte(alphabet[num.Int64()])
	}
	return b.String(), nil
}
