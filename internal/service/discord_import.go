package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Mrprogramist/echo/internal/apierror"
	"github.com/Mrprogramist/echo/internal/database"
	"github.com/Mrprogramist/echo/internal/domain"
)

const (
	DiscordTemplateURL = "https://discord.com/api/v10/guilds/templates/%s"
	MaxImportChannels  = 150
)

var DiscordCodeRegex = regexp.MustCompile(`(?:discord\.new/|discord(?:app)?\.com/template/)?([A-Za-z0-9]{4,40})/?$`)

type DiscordChannelRaw struct {
	ID       any    `json:"id"`
	Type     int    `json:"type"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	ParentID any    `json:"parent_id"`
}

type DiscordTemplateResponse struct {
	Name                  string `json:"name"`
	SerializedSourceGuild struct {
		Name     string              `json:"name"`
		Channels []DiscordChannelRaw `json:"channels"`
		Roles    []struct {
			Name string `json:"name"`
		} `json:"roles"`
	} `json:"serialized_source_guild"`
}

type TemplateChannel struct {
	Key  string             `json:"key"`
	Name string             `json:"name"`
	Kind domain.ChannelKind `json:"kind"`
}

type TemplateCategory struct {
	Key      string            `json:"key"`
	Name     string            `json:"name"`
	Channels []TemplateChannel `json:"channels"`
}

type TemplateStructure struct {
	Name       string             `json:"name"`
	Categories []TemplateCategory `json:"categories"`
	Loose      []TemplateChannel  `json:"loose"` // Каналы без категории
	Roles      int                `json:"roles"`
}

type DiscordImportService struct {
	httpClient    *http.Client
	db            *database.DB
	serverService *ServerService
}

func NewDiscordImportService(db *database.DB, serverService *ServerService) *DiscordImportService {
	return &DiscordImportService{
		httpClient:    &http.Client{Timeout: 10 * time.Second},
		db:            db,
		serverService: serverService,
	}
}

// ExtractCode извлекает чистый код шаблона из ссылки
func ExtractCode(link string) (string, error) {
	match := DiscordCodeRegex.FindStringSubmatch(strings.TrimSpace(link))
	if len(match) < 2 {
		return "", apierror.New(http.StatusBadRequest, "templateLink", "Нужна ссылка на шаблон сервера Discord")
	}
	return match[1], nil
}

// FetchStructure скачивает и преобразует структуру шаблона Discord
func (s *DiscordImportService) FetchStructure(ctx context.Context, link string) (*TemplateStructure, error) {
	code, err := ExtractCode(link)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(DiscordTemplateURL, code), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Atrium (server import)")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, apierror.New(http.StatusBadGateway, "templateUnavailable", "Discord не ответил, попробуйте ещё раз")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest {
		return nil, apierror.New(http.StatusNotFound, "templateNotFound", "Шаблон не найден: проверьте ссылку")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apierror.New(http.StatusBadGateway, "templateUnavailable", "Ошибка обращения к Discord")
	}

	var data DiscordTemplateResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	return s.parseTemplate(&data), nil
}

func (s *DiscordImportService) parseTemplate(t *DiscordTemplateResponse) *TemplateStructure {
	channels := t.SerializedSourceGuild.Channels
	sort.Slice(channels, func(i, j int) bool {
		return channels[i].Position < channels[j].Position
	})

	categoriesMap := make(map[string]*TemplateCategory)
	var categoriesOrder []string

	// 1. Ищем категории (Discord Type 4)
	for _, c := range channels {
		if c.Type == 4 {
			key := fmt.Sprintf("c%v", c.ID)
			cat := &TemplateCategory{Key: key, Name: c.Name, Channels: []TemplateChannel{}}
			categoriesMap[fmt.Sprintf("%v", c.ID)] = cat
			categoriesOrder = append(categoriesOrder, fmt.Sprintf("%v", c.ID))
		}
	}

	var loose []TemplateChannel
	// 2. Ищем каналы (0, 5, 15, 16 -> text; 2, 13 -> voice)
	for _, c := range channels {
		var kind domain.ChannelKind
		switch c.Type {
		case 0, 5, 15, 16:
			kind = domain.ChannelKindText
		case 2, 13:
			kind = domain.ChannelKindVoice
		default:
			continue
		}

		item := TemplateChannel{
			Key:  fmt.Sprintf("h%v", c.ID),
			Name: c.Name,
			Kind: kind,
		}

		parentIDStr := fmt.Sprintf("%v", c.ParentID)
		if parent, exists := categoriesMap[parentIDStr]; exists {
			parent.Channels = append(parent.Channels, item)
		} else {
			loose = append(loose, item)
		}
	}

	var categories []TemplateCategory
	for _, id := range categoriesOrder {
		categories = append(categories, *categoriesMap[id])
	}

	name := t.Name
	if name == "" {
		name = t.SerializedSourceGuild.Name
	}

	return &TemplateStructure{
		Name:       name,
		Categories: categories,
		Loose:      loose,
		Roles:      len(t.SerializedSourceGuild.Roles),
	}
}

// ImportToServer выполняет перенос шаблона в конкретный сервер
func (s *DiscordImportService) ImportToServer(ctx context.Context, user *domain.User, serverID, link string, skipKeys []string) error {
	canManage, err := s.serverService.CanManageServer(ctx, user, serverID)
	if err != nil || !canManage {
		return apierror.New(http.StatusForbidden, "adminImport", "Импортировать структуру может только администратор")
	}

	structure, err := s.FetchStructure(ctx, link)
	if err != nil {
		return err
	}

	skip := make(map[string]struct{})
	for _, k := range skipKeys {
		skip[k] = struct{}{}
	}

	now := database.NowMs()

	// Начинаем транзакцию импорта
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		existingChannels, err := s.db.ListChannels(ctx, serverID)
		if err != nil {
			return err
		}
		chPos := len(existingChannels)

		existingCategories, err := s.db.ListCategories(ctx, serverID)
		if err != nil {
			return err
		}
		catPos := len(existingCategories)

		// 1. Импортируем категории и их каналы
		for _, cat := range structure.Categories {
			if _, ignored := skip[cat.Key]; ignored {
				continue
			}

			catID := strings.ReplaceAll(uuid.New().String(), "-", "")
			catName := strings.Join(strings.Fields(cat.Name), " ")
			if len(catName) > 32 {
				catName = catName[:32]
			}

			_, err := tx.ExecContext(ctx,
				"INSERT INTO categories (id, server_id, name, position, created_at) VALUES (?, ?, ?, ?, ?)",
				catID, serverID, catName, catPos, now,
			)
			if err != nil {
				return err
			}
			catPos++

			for _, ch := range cat.Channels {
				if _, ignored := skip[ch.Key]; ignored {
					continue
				}
				normName, _ := NormalizeChannelName(ch.Name, ch.Kind)
				chID := strings.ReplaceAll(uuid.New().String(), "-", "")
				_, err := tx.ExecContext(ctx,
					`INSERT INTO channels (id, server_id, category_id, name, kind, position, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
					chID, serverID, catID, normName, ch.Kind, chPos, now,
				)
				if err != nil {
					return err
				}
				chPos++
			}
		}

		// 2. Импортируем свободные каналы (loose)
		for _, ch := range structure.Loose {
			if _, ignored := skip[ch.Key]; ignored {
				continue
			}
			normName, _ := NormalizeChannelName(ch.Name, ch.Kind)
			chID := strings.ReplaceAll(uuid.New().String(), "-", "")
			_, err := tx.ExecContext(ctx,
				`INSERT INTO channels (id, server_id, category_id, name, kind, position, created_at)
			 VALUES (?, ?, NULL, ?, ?, ?, ?)`,
				chID, serverID, normName, ch.Kind, chPos, now,
			)
			if err != nil {
				return err
			}
			chPos++
		}

		return nil
	})
}
