package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/scrypt"

	"github.com/Mrprogramist/echo/internal/config"
	"github.com/Mrprogramist/echo/internal/database"
	"github.com/Mrprogramist/echo/internal/domain"
)

const (
	ScryptN = 32768
	ScryptR = 8
	ScryptP = 1
	KeyLen  = 32
)

type Claims struct {
	jwt.RegisteredClaims
	Scope   string `json:"scope"` // "api" или "media"
	Version int    `json:"v"`     // token_version
}

type AuthManager struct {
	cfg       *config.Config
	db        *database.DB
	sem       chan struct{} // Семафор для ограничения параллельных расчетов scrypt
	dummyHash string
	logger    *slog.Logger

	// Реестр Rate Limiter'ов
	LoginByIP       *RateLimiter
	LoginByName     *RateLimiter
	LoginByNick     *RateLimiter
	RegisterByIP    *RateLimiter
	UploadsByUser   *RateLimiter
	AvatarsByUser   *RateLimiter
	MessagesByUser  *RateLimiter
	InviteChecksIP  *RateLimiter
	ResetByIP       *RateLimiter
	ResetLinkByUser *RateLimiter
}

func NewAuthManager(cfg *config.Config, db *database.DB) *AuthManager {
	// Инициализируем JSON аудит-логгер в stdout
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	am := &AuthManager{
		cfg:    cfg,
		db:     db,
		sem:    make(chan struct{}, 3), // Максимум 3 параллельных scrypt (около 100 МБ RAM)
		logger: logger,

		LoginByIP:       NewRateLimiter(20, 10*time.Minute),
		LoginByName:     NewRateLimiter(8, 10*time.Minute),
		LoginByNick:     NewRateLimiter(40, 10*time.Minute),
		RegisterByIP:    NewRateLimiter(6, time.Hour),
		UploadsByUser:   NewRateLimiter(60, 10*time.Minute),
		AvatarsByUser:   NewRateLimiter(10, 10*time.Minute),
		MessagesByUser:  NewRateLimiter(20, 10*time.Second),
		InviteChecksIP:  NewRateLimiter(30, 10*time.Minute),
		ResetByIP:       NewRateLimiter(30, 10*time.Minute),
		ResetLinkByUser: NewRateLimiter(20, time.Hour),
	}

	// Генерируем DUMMY_HASH при старте для защиты от тайминг-атак
	dummySalt := make([]byte, 16)
	_, _ = rand.Read(dummySalt)
	h, _ := scrypt.Key([]byte("dummy-password"), dummySalt, ScryptN, ScryptR, ScryptP, KeyLen)
	am.dummyHash = fmt.Sprintf("scrypt$%d$%d$%d$%s$%s", ScryptN, ScryptR, ScryptP, hex.EncodeToString(dummySalt), hex.EncodeToString(h))

	return am
}

// LogEvent записывает структурированное событие безопасности в аудит-лог
func (am *AuthManager) LogEvent(event string, fields map[string]any) {
	attrs := make([]any, 0, len(fields)+2)
	attrs = append(attrs, "event", event, "at", time.Now().Unix())
	for k, v := range fields {
		attrs = append(attrs, k, v)
	}
	am.logger.Info("security_event", attrs...)
}

// HashPassword хеширует пароль через семафор
func (am *AuthManager) HashPassword(password string) (string, error) {
	am.sem <- struct{}{}
	defer func() { <-am.sem }()

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	digest, err := scrypt.Key([]byte(password), salt, ScryptN, ScryptR, ScryptP, KeyLen)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("scrypt$%d$%d$%d$%s$%s", ScryptN, ScryptR, ScryptP, hex.EncodeToString(salt), hex.EncodeToString(digest)), nil
}

// VerifyPassword проверяет пароль через семафор (защита от timing attacks через dummy)
func (am *AuthManager) VerifyPassword(password, storedHash string) bool {
	am.sem <- struct{}{}
	defer func() { <-am.sem }()

	targetHash := storedHash
	if targetHash == "" {
		targetHash = am.dummyHash
	}

	parts := strings.Split(targetHash, "$")
	var n, r, p int
	var saltHex, expectedHex string

	if len(parts) == 6 && parts[0] == "scrypt" {
		n, _ = strconv.Atoi(parts[1])
		r, _ = strconv.Atoi(parts[2])
		p, _ = strconv.Atoi(parts[3])
		saltHex = parts[4]
		expectedHex = parts[5]
	} else if len(parts) == 2 { // Устаревший формат salt$digest
		n = 16384
		r = 8
		p = 1
		saltHex = parts[0]
		expectedHex = parts[1]
	} else {
		return false
	}

	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return false
	}

	digest, err := scrypt.Key([]byte(password), salt, n, r, p, len(expected))
	if err != nil {
		return false
	}

	return subtle.ConstantTimeCompare(digest, expected) == 1
}

// IssueToken выпускает JWT токен со скоупом ("api" или "media")
func (am *AuthManager) IssueToken(userID, scope string, version int) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(config.TokenTTL)),
		},
		Scope:   scope,
		Version: version,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(am.cfg.AppSecret))
}

// ParseAndValidateToken проверяет токен, его скоуп и актуальность версии в БД
func (am *AuthManager) ParseAndValidateToken(ctx context.Context, tokenStr, expectedScope string) (*domain.User, error) {
	if tokenStr == "" {
		return nil, errors.New("empty token")
	}

	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(am.cfg.AppSecret), nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("invalid token")
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || claims.Scope != expectedScope {
		return nil, errors.New("invalid token scope")
	}

	user, err := am.db.GetUserByID(ctx, claims.Subject)
	if err != nil || user == nil {
		return nil, errors.New("user not found")
	}

	// Токены, выпущенные до смены пароля/сброса сессий, недействительны (R03 fix)
	if user.TokenVersion != claims.Version || user.Removed {
		return nil, errors.New("token invalidated or user banned")
	}

	return user, nil
}
