package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Mrprogramist/echo/internal/config"
	"github.com/Mrprogramist/echo/internal/database"
	"github.com/Mrprogramist/echo/internal/domain"
)

type LiveKitManager struct {
	Cfg        *config.Config
	DB         *database.DB
	httpClient *http.Client
}

func NewLiveKitManager(cfg *config.Config, db *database.DB) *LiveKitManager {
	return &LiveKitManager{
		Cfg:        cfg,
		DB:         db,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// GenerateToken генерирует короткоживущий токен для подключения к комнате LiveKit
func (m *LiveKitManager) GenerateToken(ctx context.Context, user *domain.User, roomName string) (string, error) {
	now := time.Now().Unix()

	metaBytes, _ := json.Marshal(map[string]any{
		"color":       user.Color,
		"authVersion": user.TokenVersion,
	})

	claims := jwt.MapClaims{
		"iss":      m.Cfg.LiveKitAPIKey,
		"sub":      user.ID,
		"name":     user.Name,
		"nbf":      now - 10,
		"exp":      now + int64(config.VoiceTokenTTL.Seconds()),
		"metadata": string(metaBytes),
		"video": map[string]any{
			"room":                 roomName,
			"roomJoin":             true,
			"canPublish":           true,
			"canSubscribe":         true,
			"canPublishData":       true,
			"canUpdateOwnMetadata": false,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(m.Cfg.LiveKitAPISecret))
	if err != nil {
		return "", err
	}

	// Фиксируем выданную комнату в БД для защиты от несанкционированного доступа после отзыва сессии (R06 fix)
	_, _ = m.DB.ExecContext(ctx, "INSERT OR IGNORE INTO voice_grants (user_id, room) VALUES (?, ?)", user.ID, roomName)

	return signedToken, nil
}

// RemoveParticipant принудительно выкидывает участника из комнаты через Twirp RoomService API
func (m *LiveKitManager) RemoveParticipant(ctx context.Context, roomName, identity string) {
	now := time.Now().Unix()
	adminClaims := jwt.MapClaims{
		"iss": m.Cfg.LiveKitAPIKey,
		"sub": "atrium-server",
		"nbf": now - 10,
		"exp": now + 60,
		"video": map[string]any{
			"room":      roomName,
			"roomAdmin": true,
		},
	}

	adminToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, adminClaims).SignedString([]byte(m.Cfg.LiveKitAPISecret))
	if err != nil {
		return
	}

	reqBody, _ := json.Marshal(map[string]string{
		"room":     roomName,
		"identity": identity,
	})

	reqURL := fmt.Sprintf("%s/twirp/livekit.RoomService/RemoveParticipant", m.Cfg.LiveKitAPI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return
	}

	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}
