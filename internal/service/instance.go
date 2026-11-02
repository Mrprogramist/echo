package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mrprogramist/echo/internal/config"
	"github.com/Mrprogramist/echo/internal/database"
	"github.com/Mrprogramist/echo/internal/domain"
	"github.com/Mrprogramist/echo/internal/media"
)

type InstanceInfo struct {
	Name    string  `json:"serverName"` // Сохраняем ключи JSON для совместимости с веб-клиентом
	IconURL *string `json:"serverIcon"`
}

type InstanceService struct {
	cfg *config.Config
	db  *database.DB
}

func NewInstanceService(cfg *config.Config, db *database.DB) *InstanceService {
	return &InstanceService{cfg: cfg, db: db}
}

// GetInfo возвращает глобальное имя и ссылку на иконку
func (s *InstanceService) GetInfo(ctx context.Context) (*InstanceInfo, error) {
	name, err := s.db.GetSetting(ctx, "instance_name")
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = s.cfg.InstanceName
	}

	iconFile, err := s.db.GetSetting(ctx, "instance_icon")
	if err != nil {
		return nil, err
	}

	var iconURL *string
	if iconFile != "" {
		url := fmt.Sprintf("/api/instance/icon?v=%s", extractVersion(iconFile))
		iconURL = &url
	}

	return &InstanceInfo{
		Name:    name,
		IconURL: iconURL,
	}, nil
}

// UpdateName меняет имя платформы (только суперадмин)
func (s *InstanceService) UpdateName(ctx context.Context, user *domain.User, newName string) (*InstanceInfo, error) {
	if !user.IsSuperadmin {
		return nil, errors.New("forbidden: only superadmins can rename the instance")
	}

	cleanName := strings.Join(strings.Fields(newName), " ")
	if cleanName == "" || len(cleanName) > 40 {
		return nil, errors.New("invalid instance name length (1-40 characters)")
	}

	if err := s.db.SetSetting(ctx, "instance_name", cleanName); err != nil {
		return nil, err
	}

	return s.GetInfo(ctx)
}

// UpdateIcon обрабатывает и сохраняет новую иконку платформы (только суперадмин)
func (s *InstanceService) UpdateIcon(ctx context.Context, user *domain.User, data []byte) (*InstanceInfo, error) {
	if !user.IsSuperadmin {
		return nil, errors.New("forbidden: only superadmins can change the instance icon")
	}

	if len(data) == 0 || int64(len(data)) > config.MaxAvatarUploadBytes {
		return nil, errors.New("invalid image size")
	}

	// Нарезаем под квадрат 512x512
	pngData, err := media.ProcessSquareImage(data, config.ServerIconSize)
	if err != nil {
		return nil, fmt.Errorf("image processing error: %w", err)
	}

	// Генерируем уникальное имя файла
	token := make([]byte, 8)
	_, _ = rand.Read(token)
	newFileName := fmt.Sprintf("instance-%s.png", hex.EncodeToString(token))
	newFilePath := filepath.Join(s.cfg.AvatarDir, newFileName)

	if err := os.WriteFile(newFilePath, pngData, 0644); err != nil {
		return nil, fmt.Errorf("failed to write icon file: %w", err)
	}

	// Удаляем старую иконку
	oldFile, _ := s.db.GetSetting(ctx, "instance_icon")
	if oldFile != "" {
		media.RemoveFileIfExists(filepath.Join(s.cfg.AvatarDir, oldFile))
	}

	if err := s.db.SetSetting(ctx, "instance_icon", newFileName); err != nil {
		return nil, err
	}

	return s.GetInfo(ctx)
}

// DeleteIcon удаляет глобальную иконку
func (s *InstanceService) DeleteIcon(ctx context.Context, user *domain.User) (*InstanceInfo, error) {
	if !user.IsSuperadmin {
		return nil, errors.New("forbidden: only superadmins can remove the instance icon")
	}

	oldFile, err := s.db.GetSetting(ctx, "instance_icon")
	if err != nil {
		return nil, err
	}

	if oldFile != "" {
		media.RemoveFileIfExists(filepath.Join(s.cfg.AvatarDir, oldFile))
		_ = s.db.DeleteSetting(ctx, "instance_icon")
	}

	return s.GetInfo(ctx)
}

// GetIconPath возвращает путь к файлу иконки на диске и ее версию для кэширования
func (s *InstanceService) GetIconPath(ctx context.Context) (string, string, error) {
	iconFile, err := s.db.GetSetting(ctx, "instance_icon")
	if err != nil || iconFile == "" {
		return "", "", errors.New("icon not found")
	}

	fullPath := filepath.Join(s.cfg.AvatarDir, iconFile)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return "", "", errors.New("icon file missing on disk")
	}

	return fullPath, extractVersion(iconFile), nil
}

// GenerateManifest возвращает манифест приложения с подставленной иконкой
func (s *InstanceService) GenerateManifest(ctx context.Context) ([]byte, error) {
	manifestPath := filepath.Join(s.cfg.WebDistPath, "manifest.webmanifest")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		// Дефолтный минимальный манифест, если фронтенд еще не собран
		manifestBytes = []byte(`{"name":"Atrium","display":"standalone","start_url":"/"}`)
	}

	var manifestMap map[string]any
	if err := json.Unmarshal(manifestBytes, &manifestMap); err != nil {
		return manifestBytes, nil
	}

	info, err := s.GetInfo(ctx)
	if err == nil {
		manifestMap["name"] = info.Name
		if info.IconURL != nil {
			sizeStr := fmt.Sprintf("%dx%d", config.ServerIconSize, config.ServerIconSize)
			manifestMap["icons"] = []map[string]any{
				{
					"src":     *info.IconURL,
					"sizes":   sizeStr,
					"type":    "image/png",
					"purpose": "any",
				},
			}
		}
	}

	return json.Marshal(manifestMap)
}

func extractVersion(filename string) string {
	res := strings.TrimPrefix(filename, "instance-")
	res = strings.TrimSuffix(res, ".png")
	return res
}
