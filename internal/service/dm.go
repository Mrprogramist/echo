package service

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/Mrprogramist/echo/internal/config"
	"github.com/Mrprogramist/echo/internal/database"
	"github.com/Mrprogramist/echo/internal/domain"
)

var (
	ErrDMNotFound  = errors.New("dm conversation not found")
	ErrUserBlocked = errors.New("cannot create dm or group with a blocked user")
	ErrGroupTooBig = errors.New("group cannot exceed maximum member limit")
	ErrGroupTooFew = errors.New("group must contain at least two other members")
)

type DMService struct {
	cfg *config.Config
	db  *database.DB
}

func NewDMService(cfg *config.Config, db *database.DB) *DMService {
	return &DMService{cfg: cfg, db: db}
}

// ListDMs возвращает список всех личных и групповых чатов пользователя, отсортированных по свежести
func (s *DMService) ListDMs(ctx context.Context, user *domain.User) ([]*database.DMWithDetails, error) {
	dms, err := s.db.ListUserDMs(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	sort.Slice(dms, func(i, j int) bool {
		return dms[i].LastMessageAt > dms[j].LastMessageAt
	})

	return dms, nil
}

// OpenDM открывает или находит существующий диалог 1-на-1
func (s *DMService) OpenDM(ctx context.Context, user *domain.User, targetUserID string) (*database.DMWithDetails, error) {
	if user.ID == targetUserID {
		return nil, errors.New("cannot open dm with yourself")
	}

	// Проверяем блокировку (R05 fix)
	blocked, err := s.db.IsBlocked(ctx, user.ID, targetUserID)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, ErrUserBlocked
	}

	dm, err := s.db.GetOrCreateDM(ctx, user.ID, targetUserID)
	if err != nil {
		return nil, err
	}

	return &database.DMWithDetails{
		ID:            dm.ID,
		Kind:          dm.Kind,
		Name:          dm.Name,
		Members:       []string{user.ID, targetUserID},
		OtherUserID:   targetUserID,
		LastMessageAt: dm.CreatedAt,
	}, nil
}

// CreateGroup создает новый групповой чат
func (s *DMService) CreateGroup(ctx context.Context, user *domain.User, rawName string, memberIDs []string) (*database.DMWithDetails, error) {
	// Исключаем создателя и дубликаты
	unique := make(map[string]struct{})
	for _, id := range memberIDs {
		if id != user.ID && id != "" {
			unique[id] = struct{}{}
		}
	}

	if len(unique) < 2 {
		return nil, ErrGroupTooFew
	}
	if len(unique)+1 > config.MaxGroupMembers {
		return nil, ErrGroupTooBig
	}

	cleanMembers := make([]string, 0, len(unique)+1)
	cleanMembers = append(cleanMembers, user.ID)

	// Проверяем отсутствие блокировок
	for id := range unique {
		blocked, err := s.db.IsBlocked(ctx, user.ID, id)
		if err != nil {
			return nil, err
		}
		if blocked {
			return nil, ErrUserBlocked
		}
		cleanMembers = append(cleanMembers, id)
	}

	cleanName := strings.Join(strings.Fields(rawName), " ")
	if len(cleanName) > 48 {
		cleanName = cleanName[:48]
	}

	groupID := strings.ReplaceAll(uuid.New().String(), "-", "")
	dm, err := s.db.CreateGroup(ctx, groupID, cleanName, user.ID, cleanMembers)
	if err != nil {
		return nil, err
	}

	return &database.DMWithDetails{
		ID:            dm.ID,
		Kind:          dm.Kind,
		Name:          dm.Name,
		OwnerID:       dm.OwnerID,
		Members:       cleanMembers,
		LastMessageAt: dm.CreatedAt,
	}, nil
}

// AddGroupMembers добавляет новых людей в группу
func (s *DMService) AddGroupMembers(ctx context.Context, user *domain.User, groupID string, newMemberIDs []string) ([]string, error) {
	group, err := s.db.GetDM(ctx, groupID, user.ID)
	if err != nil || group == nil || group.Kind != domain.DMKindGroup {
		return nil, ErrDMNotFound
	}

	currentMembers, err := s.db.GetDMMemberIDs(ctx, groupID)
	if err != nil {
		return nil, err
	}

	memberMap := make(map[string]struct{})
	for _, m := range currentMembers {
		memberMap[m] = struct{}{}
	}

	toAdd := make([]string, 0)
	for _, id := range newMemberIDs {
		if _, exists := memberMap[id]; !exists && id != "" {
			// Проверяем блокировку
			blocked, err := s.db.IsBlocked(ctx, user.ID, id)
			if err != nil {
				return nil, err
			}
			if blocked {
				return nil, ErrUserBlocked
			}
			toAdd = append(toAdd, id)
		}
	}

	if len(toAdd) == 0 {
		return currentMembers, nil
	}

	if len(currentMembers)+len(toAdd) > config.MaxGroupMembers {
		return nil, ErrGroupTooBig
	}

	if err := s.db.AddGroupMembers(ctx, groupID, toAdd); err != nil {
		return nil, err
	}

	return s.db.GetDMMemberIDs(ctx, groupID)
}

// RenameGroup изменяет название группы
func (s *DMService) RenameGroup(ctx context.Context, user *domain.User, groupID, rawName string) (string, error) {
	group, err := s.db.GetDM(ctx, groupID, user.ID)
	if err != nil || group == nil || group.Kind != domain.DMKindGroup {
		return "", ErrDMNotFound
	}

	cleanName := strings.Join(strings.Fields(rawName), " ")
	if len(cleanName) > 48 {
		cleanName = cleanName[:48]
	}

	if err := s.db.RenameGroup(ctx, groupID, cleanName); err != nil {
		return "", err
	}
	return cleanName, nil
}

// LeaveGroup выходит из группы
func (s *DMService) LeaveGroup(ctx context.Context, user *domain.User, groupID string) error {
	group, err := s.db.GetDM(ctx, groupID, user.ID)
	if err != nil || group == nil || group.Kind != domain.DMKindGroup {
		return ErrDMNotFound
	}

	_, _, err = s.db.LeaveGroup(ctx, groupID, user.ID)
	return err
}

// GetDMMessages возвращает историю сообщений диалога с поддержкой курсора
func (s *DMService) GetDMMessages(ctx context.Context, user *domain.User, dmID string, beforeTime int64, beforeID string, limit int) ([]*FormattedMessage, error) {
	group, err := s.db.GetDM(ctx, dmID, user.ID)
	if err != nil || group == nil {
		return nil, ErrDMNotFound
	}

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rawMessages, err := s.db.GetDMMessages(ctx, dmID, beforeTime, beforeID, limit)
	if err != nil {
		return nil, err
	}

	msgIDs := make([]string, len(rawMessages))
	for i, m := range rawMessages {
		msgIDs[i] = m.ID
	}
	reactions, _ := s.db.GetReactionsForMessages(ctx, msgIDs)

	// Разворачиваем в хронологическом порядке (старые -> новые)
	formatted := make([]*FormattedMessage, len(rawMessages))
	for i := len(rawMessages) - 1; i >= 0; i-- {
		m := rawMessages[i]
		mentions, mentionsAll := parseMentions(m.Content)

		fm := &FormattedMessage{
			ID:          m.ID,
			ChannelID:   m.DMID,
			UserID:      m.UserID,
			Content:     m.Content,
			Attachments: m.Attachments,
			CreatedAt:   m.CreatedAt,
			Reactions:   reactions[m.ID],
			Mentions:    mentions,
			MentionsAll: mentionsAll,
		}
		if m.EditedAt.Valid {
			fm.EditedAt = &m.EditedAt.Int64
		}
		formatted[len(rawMessages)-1-i] = fm
	}

	return formatted, nil
}
