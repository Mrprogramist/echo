package service

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/Mrprogramist/echo/internal/apierror"
	"github.com/Mrprogramist/echo/internal/config"
	"github.com/Mrprogramist/echo/internal/database"
	"github.com/Mrprogramist/echo/internal/domain"
	"github.com/Mrprogramist/echo/internal/media"
)

var UploadNameRegex = regexp.MustCompile(`^([0-9a-fA-F]{32})(\.p)?\.(png|bin|webp)$`)

// Поддерживаемые типы для встроенного воспроизведения в браузере
var PlayableMimeTypes = map[string]string{
	"mp4":  "video/mp4",
	"m4v":  "video/mp4",
	"mov":  "video/quicktime",
	"webm": "video/webm",
	"ogv":  "video/ogg",
	"mp3":  "audio/mpeg",
	"m4a":  "audio/mp4",
	"aac":  "audio/aac",
	"ogg":  "audio/ogg",
	"wav":  "audio/wav",
	"flac": "audio/flac",
}

type UploadService struct {
	cfg           *config.Config
	DB            *database.DB
	serverService *ServerService
	imageWorkSem  chan struct{} // Семафор: строго 1 тяжелое декодирование в один момент
}

func NewUploadService(cfg *config.Config, db *database.DB, serverService *ServerService) *UploadService {
	return &UploadService{
		cfg:           cfg,
		DB:            db,
		serverService: serverService,
		imageWorkSem:  make(chan struct{}, 1),
	}
}

func (s *UploadService) PreviewName(uploadID string) string {
	ext := filepath.Ext(uploadID)
	base := strings.TrimSuffix(uploadID, ext)
	return fmt.Sprintf("%s.p%s", base, ext)
}

// CheckUploadRoom проверяет свободное место на диске и дневную квоту пользователя
func (s *UploadService) CheckUploadRoom(ctx context.Context, userID string, size int64) error {
	// Проверка свободного места через statfs
	var stat syscall.Statfs_t
	if err := syscall.Statfs(s.cfg.UploadDir, &stat); err == nil {
		freeBytes := int64(stat.Bavail) * int64(stat.Bsize)
		if freeBytes-size < config.MinFreeDiskBytes {
			return apierror.New(http.StatusInsufficientStorage, "diskFull", "На сервере кончается место, загрузки закрыты")
		}
	}

	// Проверка дневной квоты пользователя
	dayAgo := database.NowMs() - (24 * 3600 * 1000)
	usedBytes, err := s.DB.GetUserDailyUploadBytes(ctx, userID, dayAgo)
	if err == nil && usedBytes+size > s.cfg.UploadBytesPerDay {
		mb := s.cfg.UploadBytesPerDay / 1024 / 1024
		return apierror.New(http.StatusRequestEntityTooLarge, "uploadQuota",
			fmt.Sprintf("Лимит загрузок на сутки: %d МБ", mb),
			map[string]any{"mb": mb},
		)
	}

	return nil
}

// ProcessChatImage нарезает картинку и делает уменьшенное превью (защищено семафором)
func (s *UploadService) ProcessChatImage(data []byte) ([]byte, int, int, []byte, error) {
	s.imageWorkSem <- struct{}{}
	defer func() { <-s.imageWorkSem }()

	// Быстрая проверка конфига на Pixel Flood
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, nil, apierror.New(http.StatusBadRequest, "notAnImage", "Это не похоже на картинку")
	}
	if int64(cfg.Width)*int64(cfg.Height) > config.MaxImagePixels {
		return nil, 0, 0, nil, apierror.New(http.StatusRequestEntityTooLarge, "imageTooBig", "Картинка слишком большая")
	}

	// Декодирование
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, nil, apierror.New(http.StatusBadRequest, "imageFormat", "Не удалось открыть изображение")
	}

	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// 1. Уменьшаем до IMAGE_MAX_SIDE при необходимости
	targetW, targetH := w, h
	if w > config.ImageMaxSide || h > config.ImageMaxSide {
		if w > h {
			targetW = config.ImageMaxSide
			targetH = (h * config.ImageMaxSide) / w
		} else {
			targetH = config.ImageMaxSide
			targetW = (w * config.ImageMaxSide) / h
		}
	}

	mainDst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	draw.CatmullRom.Scale(mainDst, mainDst.Bounds(), src, bounds, draw.Over, nil)

	var mainBuf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&mainBuf, mainDst); err != nil {
		return nil, 0, 0, nil, err
	}

	// 2. Делаем превью, если картинка больше PREVIEW_SIDE
	var previewBytes []byte
	if targetW > config.PreviewSide || targetH > config.PreviewSide {
		prevW, prevH := targetW, targetH
		if targetW > targetH {
			prevW = config.PreviewSide
			prevH = (targetH * config.PreviewSide) / targetW
		} else {
			prevH = config.PreviewSide
			prevW = (targetW * config.PreviewSide) / targetH
		}

		prevDst := image.NewRGBA(image.Rect(0, 0, prevW, prevH))
		draw.CatmullRom.Scale(prevDst, prevDst.Bounds(), mainDst, mainDst.Bounds(), draw.Over, nil)

		var prevBuf bytes.Buffer
		if err := enc.Encode(&prevBuf, prevDst); err == nil {
			previewBytes = prevBuf.Bytes()
		}
	}

	return mainBuf.Bytes(), targetW, targetH, previewBytes, nil
}

// UploadFileOrImage сохраняет файл или картинку на диск
func (s *UploadService) UploadFileOrImage(ctx context.Context, user *domain.User, rawName, contentType string, data []byte) (*domain.Attachment, error) {
	size := int64(len(data))
	if size == 0 {
		return nil, apierror.New(http.StatusBadRequest, "emptyFile", "Пустой файл")
	}
	if size > config.MaxFileUploadBytes {
		return nil, apierror.New(http.StatusRequestEntityTooLarge, "fileOver25", "Файл больше 25 МБ")
	}

	if err := s.CheckUploadRoom(ctx, user.ID, size); err != nil {
		return nil, err
	}

	now := database.NowMs()
	rawID := strings.ReplaceAll(uuid.New().String(), "-", "")

	// 1. Попытка обработать как изображение
	if strings.HasPrefix(contentType, "image/") && contentType != "image/svg+xml" && size <= config.MaxImageUploadBytes {
		imgBytes, w, h, previewBytes, err := s.ProcessChatImage(data)
		if err == nil {
			fileID := fmt.Sprintf("%s.png", rawID)
			filePath := filepath.Join(s.cfg.UploadDir, fileID)

			if err := os.WriteFile(filePath, imgBytes, 0644); err != nil {
				return nil, err
			}

			if len(previewBytes) > 0 {
				prevPath := filepath.Join(s.cfg.UploadDir, s.PreviewName(fileID))
				_ = os.WriteFile(prevPath, previewBytes, 0644)
			}

			upload := &domain.Upload{
				ID:        fileID,
				UserID:    user.ID,
				Width:     w,
				Height:    h,
				FileSize:  sql.NullInt64{Int64: int64(len(imgBytes) + len(previewBytes)), Valid: true},
				CreatedAt: now,
			}
			if err := s.DB.CreateUpload(ctx, upload); err != nil {
				return nil, err
			}

			return &domain.Attachment{
				URL:    fmt.Sprintf("/api/uploads/%s", fileID),
				Width:  w,
				Height: h,
				Size:   upload.FileSize.Int64,
			}, nil
		}
	}

	// 2. Сохраняем как обычный бинарный файл (.bin)
	cleanName := strings.Join(strings.Fields(filepath.Base(rawName)), " ")
	if cleanName == "" || cleanName == "." {
		cleanName = "файл"
	}
	if len(cleanName) > config.MaxFileNameLen {
		cleanName = cleanName[:config.MaxFileNameLen]
	}

	fileID := fmt.Sprintf("%s.bin", rawID)
	filePath := filepath.Join(s.cfg.UploadDir, fileID)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return nil, err
	}

	upload := &domain.Upload{
		ID:        fileID,
		UserID:    user.ID,
		FileName:  sql.NullString{String: cleanName, Valid: true},
		FileSize:  sql.NullInt64{Int64: size, Valid: true},
		FileType:  sql.NullString{String: contentType, Valid: true},
		CreatedAt: now,
	}
	if err := s.DB.CreateUpload(ctx, upload); err != nil {
		return nil, err
	}

	return &domain.Attachment{
		URL:  fmt.Sprintf("/api/uploads/%s", fileID),
		Name: cleanName,
		Size: size,
	}, nil
}

// CanSeeUpload проверяет права доступа пользователя к файлу
func (s *UploadService) CanSeeUpload(ctx context.Context, user *domain.User, upload *domain.Upload) bool {
	if !upload.Scope.Valid || upload.Scope.String == "" {
		return upload.UserID == user.ID
	}

	scope := upload.Scope.String
	// Доступ к файлу в канале
	if strings.HasPrefix(scope, "channel") {
		return true
	}

	// Доступ к файлу в ЛС / группе
	if strings.HasPrefix(scope, "dm:") {
		dmID := strings.TrimPrefix(scope, "dm:")
		dm, err := s.DB.GetDM(ctx, dmID, user.ID)
		return err == nil && dm != nil
	}

	return false
}

// StartHousekeepingLoop запускает ежечасную фоновую очистку
func (s *UploadService) StartHousekeepingLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				s.sweepUnsentUploads(ctx)
			}
		}
	}()
}

func (s *UploadService) sweepUnsentUploads(ctx context.Context) {
	cutoff := database.NowMs() - (24 * 3600 * 1000)
	ids, err := s.DB.GetUnsentUploadIDs(ctx, cutoff)
	if err != nil || len(ids) == 0 {
		return
	}

	var paths []string
	for _, id := range ids {
		paths = append(paths, filepath.Join(s.cfg.UploadDir, id))
		paths = append(paths, filepath.Join(s.cfg.UploadDir, s.PreviewName(id)))
	}

	if media.RemoveMediaFiles(s.cfg.DataDir, paths) {
		_ = s.DB.DeleteUploads(ctx, ids)
	}
}
