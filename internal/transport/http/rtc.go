package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"

	"github.com/Mrprogramist/echo/internal/apierror"
	"github.com/Mrprogramist/echo/internal/domain"
	"github.com/Mrprogramist/echo/internal/realtime"
	"github.com/Mrprogramist/echo/internal/service"
)

type RTCHandler struct {
	livekitMgr    *realtime.LiveKitManager
	serverService *service.ServerService
	dmService     *service.DMService
}

func NewRTCHandler(livekitMgr *realtime.LiveKitManager, serverService *service.ServerService, dmService *service.DMService) *RTCHandler {
	return &RTCHandler{
		livekitMgr:    livekitMgr,
		serverService: serverService,
		dmService:     dmService,
	}
}

func (h *RTCHandler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	// 1. Проверка сигналинга Caddy forward_auth (публичный роут, проверяет токен внутри)
	r.Get("/api/rtc/authorize", h.AuthorizeRTC)

	// 2. Клиентские запросы токенов (требуют авторизации)
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)
		r.Post("/api/voice-token", h.GetVoiceToken)
		r.Post("/api/check-token", h.GetCheckToken)
		r.Post("/api/call-quality", h.ReportQuality)
	})
}

// AuthorizeRTC проверяет подлинность токена при WebSocket подключении к LiveKit через Caddy
func (h *RTCHandler) AuthorizeRTC(w http.ResponseWriter, r *http.Request) {
	uri := r.Header.Get("X-Forwarded-Uri")
	tokenStr := ""

	if uri != "" {
		if parsedURL, err := url.Parse(uri); err == nil {
			tokenStr = parsedURL.Query().Get("access_token")
		}
	}
	if tokenStr == "" {
		tokenStr = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	tokenStr = strings.TrimSpace(tokenStr)

	if tokenStr == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Парсим токен LiveKit
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		return []byte(h.livekitMgr.Cfg.LiveKitAPISecret), nil
	})
	if err != nil || !token.Valid {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	sub, _ := claims["sub"].(string)
	metadataStr, _ := claims["metadata"].(string)

	var meta struct {
		AuthVersion int `json:"authVersion"`
	}
	_ = json.Unmarshal([]byte(metadataStr), &meta)

	// Проверяем версию токена в БД (R06 fix)
	user, err := h.livekitMgr.DB.GetUserByID(r.Context(), sub)
	if err != nil || user == nil || user.TokenVersion != meta.AuthVersion || user.Removed {
		http.Error(w, "Session Expired", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (h *RTCHandler) GetVoiceToken(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(UserContextKey).(*domain.User)

	var body struct {
		ChannelID string `json:"channelId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ChannelID == "" {
		apierror.WriteJSON(w, apierror.New(http.StatusBadRequest, "badRequest", "Не указан channelId"))
		return
	}

	// Определяем тип комнаты: канал сервера или личный звонок
	var roomName string
	ch, _ := h.livekitMgr.DB.GetChannel(r.Context(), body.ChannelID)
	if ch != nil && ch.Kind == domain.ChannelKindVoice {
		// Проверяем членство на сервере
		canView, _ := h.serverService.CanViewServer(r.Context(), user, ch.ServerID)
		if !canView {
			apierror.WriteJSON(w, apierror.New(http.StatusForbidden, "forbidden", "Нет доступа к каналу"))
			return
		}
		roomName = fmt.Sprintf("voice-%s", ch.ID)
	} else {
		// Проверяем доступ к DM
		dm, _ := h.livekitMgr.DB.GetDM(r.Context(), body.ChannelID, user.ID)
		if dm == nil {
			apierror.WriteJSON(w, apierror.New(http.StatusNotFound, "noCall", "Звонок не найден"))
			return
		}
		roomName = fmt.Sprintf("dm-%s", dm.ID)
	}

	token, err := h.livekitMgr.GenerateToken(r.Context(), user, roomName)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"token": token,
		"url":   h.livekitMgr.Cfg.LiveKitURL,
	})
}

func (h *RTCHandler) GetCheckToken(w http.ResponseWriter, r *http.Request) {
	user := r.Context().Value(UserContextKey).(*domain.User)
	roomName := fmt.Sprintf("check-%s", user.ID)

	token, err := h.livekitMgr.GenerateToken(r.Context(), user, roomName)
	if err != nil {
		apierror.WriteJSON(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"token": token,
		"url":   h.livekitMgr.Cfg.LiveKitURL,
	})
}

func (h *RTCHandler) ReportQuality(w http.ResponseWriter, r *http.Request) {
	// Прием телеметрии WebRTC качества соединения от браузера
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
