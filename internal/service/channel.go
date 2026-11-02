package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/Mrprogramist/echo/internal/config"
	"github.com/Mrprogramist/echo/internal/database"
	"github.com/Mrprogramist/echo/internal/domain"
)

var (
	ErrCannotDeleteLastTextChannel = errors.New("cannot delete the last text channel of the server")
	ErrChannelNotFound             = errors.New("channel not found")
)

type FormattedReply struct {
	ID          string `json:"id"`
	UserID      string `json:"userId"`
	Content     string `json:"content"`
	Attachments int    `json:"attachments"`
	Deleted     bool   `json:"deleted,omitempty"`
}

type FormattedMessage struct {
	ID          string                   `json:"id"`
	ChannelID   string                   `json:"channelId"`
	UserID      string                   `json:"userId"`
	Content     string                   `json:"content"`
	Attachments []domain.Attachment      `json:"attachments"`
	CreatedAt   int64                    `json:"createdAt"`
	EditedAt    *int64                   `json:"editedAt,omitempty"`
	Reactions   []database.ReactionGroup `json:"reactions"`
	Mentions    []string                 `json:"mentions"`
	MentionsAll bool                     `json:"mentionsAll"`
	ReplyTo     *FormattedReply          `json:"replyTo,omitempty"`
}

type ChannelService struct {
	cfg           *config.Config
	db            *database.DB
	serverService *ServerService
}

func NewChannelService(cfg *config.Config, db *database.DB, serverService *ServerService) *ChannelService {
	return &ChannelService{cfg: cfg, db: db, serverService: serverService}
}

// NormalizeChannelName нормализует имя канала под стиль Discord
func NormalizeChannelName(raw string, kind domain.ChannelKind) (string, error) {
	clean := strings.Join(strings.Fields(raw), " ")
	if kind == domain.ChannelKindText {
		clean = strings.ToLower(clean)
		clean = strings.ReplaceAll(clean, " ", "-")
	}
	if clean == "" || len(clean) > 32 {
		return "", errors.New("channel name must be between 1 and 32 characters")
	}
	return clean, nil
}

// ListServerLayout возвращает каналы и категории сервера
func (s *ChannelService) ListServerLayout(ctx context.Context, user *domain.User, serverID string) ([]*domain.Channel, []*domain.Category, error) {
	canView, err := s.serverService.CanViewServer(ctx, user, serverID)
	if err != nil || !canView {
		return nil, nil, ErrForbidden
	}

	channels, err := s.db.ListChannels(ctx, serverID)
	if err != nil {
		return nil, nil, err
	}

	categories, err := s.db.ListCategories(ctx, serverID)
	if err != nil {
		return nil, nil, err
	}

	return channels, categories, nil
}

// CreateChannel создает новый канал на сервере
func (s *ChannelService) CreateChannel(ctx context.Context, user *domain.User, serverID, rawName string, kind domain.ChannelKind, categoryID *string) (*domain.Channel, error) {
	canManage, err := s.serverService.CanManageServer(ctx, user, serverID)
	if err != nil || !canManage {
		return nil, ErrForbidden
	}

	cleanName, err := NormalizeChannelName(rawName, kind)
	if err != nil {
		return nil, err
	}

	channels, err := s.db.ListChannels(ctx, serverID)
	if err != nil {
		return nil, err
	}

	ch := &domain.Channel{
		ID:        strings.ReplaceAll(uuid.New().String(), "-", ""),
		ServerID:  serverID,
		Name:      cleanName,
		Kind:      kind,
		Position:  len(channels),
		CreatedAt: database.NowMs(),
	}
	if categoryID != nil && *categoryID != "" {
		ch.CategoryID = sql.NullString{String: *categoryID, Valid: true}
	}

	if err := s.db.CreateChannel(ctx, ch); err != nil {
		return nil, err
	}

	return ch, nil
}

// DeleteChannel мягко удаляет канал (с проверкой на последний текстовый канал)
func (s *ChannelService) DeleteChannel(ctx context.Context, user *domain.User, channelID string) error {
	ch, err := s.db.GetChannel(ctx, channelID)
	if err != nil || ch == nil {
		return ErrChannelNotFound
	}

	canManage, err := s.serverService.CanManageServer(ctx, user, ch.ServerID)
	if err != nil || !canManage {
		return ErrForbidden
	}

	// Проверяем: если это текстовый канал, нельзя удалять последний
	if ch.Kind == domain.ChannelKindText {
		channels, err := s.db.ListChannels(ctx, ch.ServerID)
		if err == nil {
			textCount := 0
			for _, c := range channels {
				if c.Kind == domain.ChannelKindText {
					textCount++
				}
			}
			if textCount <= 1 {
				return ErrCannotDeleteLastTextChannel
			}
		}
	}

	return s.db.SoftDeleteChannel(ctx, channelID, database.NowMs())
}

// GetChannelMessages возвращает сообщения с реакциями, реплаями и разбором упоминаний
func (s *ChannelService) GetChannelMessages(ctx context.Context, user *domain.User, channelID string, beforeTime int64, beforeID string, limit int) ([]*FormattedMessage, error) {
	ch, err := s.db.GetChannel(ctx, channelID)
	if err != nil || ch == nil {
		return nil, ErrChannelNotFound
	}

	canView, err := s.serverService.CanViewServer(ctx, user, ch.ServerID)
	if err != nil || !canView {
		return nil, ErrForbidden
	}

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rawMessages, err := s.db.GetChannelMessages(ctx, channelID, beforeTime, beforeID, limit)
	if err != nil {
		return nil, err
	}

	// Собираем ID для реакций
	msgIDs := make([]string, len(rawMessages))
	for i, m := range rawMessages {
		msgIDs[i] = m.ID
	}
	reactions, _ := s.db.GetReactionsForMessages(ctx, msgIDs)

	// Разворачиваем в хронологическом порядке для клиента (от старых к новым)
	formatted := make([]*FormattedMessage, len(rawMessages))
	for i := len(rawMessages) - 1; i >= 0; i-- {
		m := rawMessages[i]
		mentions, mentionsAll := parseMentions(m.Content)

		fm := &FormattedMessage{
			ID:          m.ID,
			ChannelID:   m.ChannelID,
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

func parseMentions(content string) ([]string, bool) {
	matches := config.MentionRegex.FindAllStringSubmatch(content, -1)
	mentionsAll := false
	uniqueIDs := make(map[string]struct{})

	for _, match := range matches {
		if len(match) > 1 {
			if match[1] == "all" {
				mentionsAll = true
			} else {
				uniqueIDs[match[1]] = struct{}{}
			}
		}
	}

	var list []string
	for id := range uniqueIDs {
		list = append(list, id)
	}
	return list, mentionsAll
}

func (s *ChannelService) MuteChannel(ctx context.Context, user *domain.User, channelID string, mute bool) ([]string, error) {
	ch, err := s.db.GetChannel(ctx, channelID)
	if err != nil || ch == nil {
		return nil, ErrChannelNotFound
	}

	canView, err := s.serverService.CanViewServer(ctx, user, ch.ServerID)
	if err != nil || !canView {
		return nil, ErrForbidden
	}

	if err := s.db.MuteChannel(ctx, user.ID, channelID, mute); err != nil {
		return nil, err
	}

	return s.db.ListMutedChannels(ctx, user.ID)
}

// MarkRead фиксирует метку прочтения сообщений
func (s *ChannelService) MarkRead(ctx context.Context, userID, conversationID string, at int64) error {
	now := database.NowMs()
	if at <= 0 || at > now {
		at = now
	}
	return s.db.MarkRead(ctx, userID, conversationID, at)
}

// GetDeletedChannels возвращает список удаленных каналов сервера (только для админов)
func (s *ChannelService) GetDeletedChannels(ctx context.Context, user *domain.User, serverID string) ([]*database.DeletedChannelInfo, error) {
	canManage, err := s.serverService.CanManageServer(ctx, user, serverID)
	if err != nil || !canManage {
		return nil, ErrForbidden
	}
	return s.db.GetDeletedChannels(ctx, serverID)
}
