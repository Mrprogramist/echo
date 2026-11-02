package http

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Mrprogramist/echo/internal/apierror"
	"github.com/Mrprogramist/echo/internal/domain"
	"github.com/Mrprogramist/echo/internal/service"
)

type UploadHandler struct {
	uploadService *service.UploadService
	uploadDir     string
}

func NewUploadHandler(uploadService *service.UploadService, uploadDir string) *UploadHandler {
	return &UploadHandler{
		uploadService: uploadService,
		uploadDir:     uploadDir,
	}
}

func (h *UploadHandler) RegisterRoutes(r chi.Router, authMiddleware, mediaAuthMiddleware func(http.Handler) http.Handler) {
	// 1. Загрузка файлов (требуется Bearer токен)
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)
		r.Post("/api/uploads", h.Upload)
	})

	// 2. Отдача файлов (проверяется кука gx_media)
	r.Group(func(r chi.Router) {
		r.Use(mediaAuthMiddleware)
		r.Get("/api/uploads/{name}", h.GetFile)
	})
}

func (h *UploadHandler) Upload(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(UserContextKey).(*domain.User)

	rawName := r.URL.Query().Get("name")
	contentType := r.Header.Get("Content-Type")

	data, err := io.ReadAll(r.Body)
	if err != nil {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Не удалось прочитать тело запроса"))
		return
	}

	attachment, err := h.uploadService.UploadFileOrImage(r.Context(), user, rawName, contentType, data)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(attachment)
}

func (h *UploadHandler) GetFile(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(UserContextKey).(*domain.User)
	fileName := chi.URLParam(r, "name")

	match := service.UploadNameRegex.FindStringSubmatch(fileName)
	if len(match) == 0 {
		http.NotFound(w, r)
		return
	}

	rawID := match[1]
	ext := match[3]
	baseUploadID := fmt.Sprintf("%s.%s", rawID, ext)

	upload, err := h.uploadService.DB.GetUpload(r.Context(), baseUploadID)
	// 404 вместо 403, чтобы не подтверждать существование файла посторонним
	if err != nil || upload == nil || !h.uploadService.CanSeeUpload(r.Context(), user, upload) {
		http.NotFound(w, r)
		return
	}

	fullPath := filepath.Join(h.uploadDir, fileName)
	fileInfo, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}

	file, err := os.Open(fullPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")

	// Если это бинарный файл пользователя — принудительный download disposition для защиты от XSS
	if upload.FileName.Valid && upload.FileName.String != "" {
		encodedName := url.PathEscape(upload.FileName.String)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", encodedName))

		mimeType := "application/octet-stream"
		fileExt := strings.ToLower(strings.TrimPrefix(filepath.Ext(upload.FileName.String), "."))
		if playable, ok := service.PlayableMimeTypes[fileExt]; ok {
			mimeType = playable
		}
		w.Header().Set("Content-Type", mimeType)
	} else {
		w.Header().Set("Content-Type", "image/png")
	}

	// http.ServeContent поддерживает HTTP Range (перемотку видео/аудио) и 304 Not Modified
	http.ServeContent(w, r, fileName, fileInfo.ModTime(), file)
}
