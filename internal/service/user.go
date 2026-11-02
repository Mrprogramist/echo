package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/scrypt"

	"github.com/Mrprogramist/echo/internal/apierror"
	"github.com/Mrprogramist/echo/internal/config"
	"github.com/Mrprogramist/echo/internal/database"
	"github.com/Mrprogramist/echo/internal/domain"
	"github.com/Mrprogramist/echo/internal/media"
)

type PublicUser struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Color     string  `json:"color"`
	Bio       string  `json:"bio"`
	Avatar    *string `json:"avatar"`
	Status    string  `json:"status"`
	CreatedAt int64   `json:"createdAt"`
}

type AuthPayload struct {
	Token       string      `json:"token"`
	User        *PublicUser `json:"user"`
	RecoveryKey string      `json:"recoveryKey,omitempty"`
}

type UserService struct {
	Cfg *config.Config
	DB  *database.DB
}

func NewUserService(cfg *config.Config, db *database.DB) *UserService {
	return &UserService{Cfg: cfg, DB: db}
}

// TokenDigest хеширует одноразовый токен сброса пароля через SHA-256
func TokenDigest(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// CheckNick защищает от спуфинга (смешения кириллицы и латиницы в одном слове)
func CheckNick(name string) error {
	clean := strings.TrimSpace(name)
	if clean == "" || len(clean) > 32 {
		return apierror.New(http.StatusBadRequest, "badNick", "Ник должен быть от 1 до 32 символов")
	}

	for _, word := range strings.Fields(clean) {
		hasCyrillic := false
		hasLatin := false
		for _, r := range word {
			if !unicode.IsPrint(r) {
				return apierror.New(http.StatusBadRequest, "badNick", "Ник содержит невидимые символы")
			}
			if unicode.Is(unicode.Cyrillic, r) {
				hasCyrillic = true
			}
			if unicode.Is(unicode.Latin, r) {
				hasLatin = true
			}
		}
		if hasCyrillic && hasLatin {
			return apierror.New(http.StatusBadRequest, "mixedScripts", "В одном слове ника смешаны русские и латинские буквы")
		}
	}
	return nil
}

// HashPassword хеширует пароль через scrypt (N=32768, r=8, p=1)
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("scrypt$%s$%s", hex.EncodeToString(salt), hex.EncodeToString(hash)), nil
}

// VerifyPassword проверяет пароль в константное время
func VerifyPassword(password, storedHash string) bool {
	parts := strings.Split(storedHash, "$")
	if len(parts) != 3 || parts[0] != "scrypt" {
		return false
	}
	salt, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	expectedHash, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}

	hash, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(hash, expectedHash) == 1
}

// GenerateRecoveryKey генерирует 16-значный ключ XXXX-XXXX-XXXX-XXXX
func GenerateRecoveryKey() (string, string, error) {
	alphabet := config.InviteAlphabet
	alphabetLen := big.NewInt(int64(len(alphabet)))
	var raw strings.Builder

	for i := 0; i < 16; i++ {
		num, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", "", err
		}
		raw.WriteByte(alphabet[num.Int64()])
	}

	str := raw.String()
	formatted := fmt.Sprintf("%s-%s-%s-%s", str[0:4], str[4:8], str[8:12], str[12:16])
	normalized := strings.ReplaceAll(formatted, "-", "")
	hash, err := HashPassword(normalized)
	return formatted, hash, err
}

// IssueToken выпускает JWT токен
func (s *UserService) IssueToken(userID string, version int) (string, error) {
	claims := jwt.MapClaims{
		"sub":     userID,
		"version": version,
		"exp":     time.Now().Add(config.TokenTTL).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.Cfg.AppSecret))
}

// Setup создает первого пользователя и дефолтный сервер
func (s *UserService) Setup(ctx context.Context, setupCode, instanceName, adminNick, password string) (*AuthPayload, error) {
	hasUsers, err := s.DB.HasAnyUsers(ctx)
	if err != nil {
		return nil, err
	}
	if hasUsers {
		return nil, apierror.New(http.StatusConflict, "alreadySetup", "Сервер уже настроен")
	}

	if !hmac.Equal([]byte(strings.TrimSpace(setupCode)), []byte(s.Cfg.InviteCode)) {
		return nil, apierror.New(http.StatusForbidden, "badSetupCode", "Неверный код установки")
	}

	if err := CheckNick(adminNick); err != nil {
		return nil, err
	}

	passHash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	recKey, recHash, err := GenerateRecoveryKey()
	if err != nil {
		return nil, err
	}

	userID := strings.ReplaceAll(uuid.New().String(), "-", "")
	now := database.NowMs()
	user := &domain.User{
		ID:               userID,
		Name:             adminNick,
		NameKey:          strings.ToLower(adminNick),
		PasswordHash:     passHash,
		RecoveryHash:     sql.NullString{String: recHash, Valid: true},
		Color:            config.UserColors[0],
		IsSuperadmin:     true,
		CanCreateServers: true,
		Status:           "online",
		CreatedAt:        now,
	}

	err = s.DB.WithTx(ctx, func(tx *sql.Tx) error {
		query := `
			INSERT INTO users (id, name, name_key, password_hash, recovery_hash, color, 
			                   is_superadmin, can_create_servers, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, 1, 1, 'online', ?)
		`
		if _, err := tx.ExecContext(ctx, query, user.ID, user.Name, user.NameKey, user.PasswordHash, user.RecoveryHash, user.Color, now); err != nil {
			return err
		}
		_ = s.DB.SetSetting(ctx, "instance_name", instanceName)
		return nil
	})
	if err != nil {
		return nil, err
	}

	_, _ = s.DB.CreateServerTx(ctx, instanceName, "Основное сообщество", user.ID, config.DefaultServerChannels)
	token, _ := s.IssueToken(user.ID, 0)

	return &AuthPayload{
		Token:       token,
		User:        ToPublicUser(user),
		RecoveryKey: recKey,
	}, nil
}

// Register регистрирует пользователя по инвайту сервера
func (s *UserService) Register(ctx context.Context, inviteCode, name, password string) (*AuthPayload, error) {
	if err := CheckNick(name); err != nil {
		return nil, err
	}

	inv, err := s.DB.GetInvite(ctx, strings.ToUpper(strings.TrimSpace(inviteCode)))
	if err != nil || inv == nil {
		return nil, apierror.New(http.StatusForbidden, "inviteGone", "Инвайт недействителен")
	}

	if !inv.AllowsRegistration {
		return nil, apierror.New(http.StatusForbidden, "inviteNoReg", "Этот инвайт не разрешает регистрацию новых аккаунтов")
	}

	now := database.NowMs()
	if inv.ExpiresAt.Valid && inv.ExpiresAt.Int64 < now {
		return nil, apierror.New(http.StatusForbidden, "inviteExpired", "Срок действия инвайта истек")
	}

	if inv.MaxUses > 0 && inv.Uses >= inv.MaxUses {
		return nil, apierror.New(http.StatusForbidden, "inviteFull", "Все места по инвайту заняты")
	}

	existing, err := s.DB.GetUserByNameKey(ctx, name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, apierror.New(http.StatusConflict, "nickTaken", "Этот ник уже занят")
	}

	passHash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	recKey, recHash, err := GenerateRecoveryKey()
	if err != nil {
		return nil, err
	}

	userID := strings.ReplaceAll(uuid.New().String(), "-", "")
	user := &domain.User{
		ID:                  userID,
		Name:                name,
		NameKey:             strings.ToLower(name),
		PasswordHash:        passHash,
		RecoveryHash:        sql.NullString{String: recHash, Valid: true},
		Color:               config.UserColors[time.Now().UnixNano()%int64(len(config.UserColors))],
		InvitedBy:           sql.NullString{String: inv.CreatedBy, Valid: true},
		RegisteredViaInvite: sql.NullString{String: inv.Code, Valid: true},
		CanCreateServers:    s.Cfg.DefaultCanCreateServers,
		Status:              "online",
		CreatedAt:           now,
	}

	if err := s.DB.CreateUserTx(ctx, user, inv); err != nil {
		return nil, err
	}

	token, _ := s.IssueToken(user.ID, 0)
	return &AuthPayload{
		Token:       token,
		User:        ToPublicUser(user),
		RecoveryKey: recKey,
	}, nil
}

// Login выполняет проверку логина и пароля
func (s *UserService) Login(ctx context.Context, name, password string) (*AuthPayload, error) {
	user, err := s.DB.GetUserByNameKey(ctx, name)
	if err != nil || user == nil {
		return nil, apierror.New(http.StatusUnauthorized, "badCredentials", "Неверный ник или пароль")
	}

	if user.Removed {
		return nil, apierror.New(http.StatusForbidden, "removed", "Вход на этот сервер для вас закрыт")
	}

	if !VerifyPassword(password, user.PasswordHash) {
		return nil, apierror.New(http.StatusUnauthorized, "badCredentials", "Неверный ник или пароль")
	}

	token, err := s.IssueToken(user.ID, user.TokenVersion)
	if err != nil {
		return nil, err
	}

	return &AuthPayload{
		Token: token,
		User:  ToPublicUser(user),
	}, nil
}

// RecoverPassword сбрасывает пароль по ключу восстановления (R04 fix)
func (s *UserService) RecoverPassword(ctx context.Context, name, key, newPassword string) (*AuthPayload, error) {
	user, err := s.DB.GetUserByNameKey(ctx, name)
	if err != nil || user == nil || !user.RecoveryHash.Valid {
		return nil, apierror.New(http.StatusUnauthorized, "badRecovery", "Неверный ник или ключ восстановления")
	}

	if user.Removed {
		return nil, apierror.New(http.StatusForbidden, "removed", "Доступ закрыт")
	}

	normalizedKey := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(key)), "-", "")
	if !VerifyPassword(normalizedKey, user.RecoveryHash.String) {
		return nil, apierror.New(http.StatusUnauthorized, "badRecovery", "Неверный ник или ключ восстановления")
	}

	newPassHash, err := HashPassword(newPassword)
	if err != nil {
		return nil, err
	}

	newRecKey, newRecHash, err := GenerateRecoveryKey()
	if err != nil {
		return nil, err
	}

	updated, err := s.DB.UpdateRecoveryKeyAtomically(ctx, user.ID, newPassHash, newRecHash, user.RecoveryHash.String, user.TokenVersion)
	if err != nil || !updated {
		return nil, apierror.New(http.StatusUnauthorized, "badRecovery", "Неверный ник или ключ восстановления")
	}

	token, _ := s.IssueToken(user.ID, user.TokenVersion+1)
	return &AuthPayload{
		Token:       token,
		User:        ToPublicUser(user),
		RecoveryKey: newRecKey,
	}, nil
}

// ChangePassword меняет пароль авторизованного пользователя (R03 & R09 fix)
func (s *UserService) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (string, error) {
	user, err := s.DB.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return "", apierror.New(http.StatusNotFound, "noUser", "Пользователь не найден")
	}

	if !VerifyPassword(currentPassword, user.PasswordHash) {
		return "", apierror.New(http.StatusForbidden, "badCurrentPassword", "Неверный текущий пароль")
	}

	newPassHash, err := HashPassword(newPassword)
	if err != nil {
		return "", err
	}

	updated, err := s.DB.UpdatePasswordAtomically(ctx, userID, newPassHash, user.PasswordHash, user.TokenVersion)
	if err != nil || !updated {
		return "", apierror.New(http.StatusForbidden, "badCurrentPassword", "Неверный текущий пароль")
	}

	return s.IssueToken(userID, user.TokenVersion+1)
}

// CreateResetLink генерирует одноразовую 24-часовую ссылку сброса (только суперадмин)
func (s *UserService) CreateResetLink(ctx context.Context, admin *domain.User, targetUserID string) (string, int64, error) {
	if !admin.IsSuperadmin {
		return "", 0, apierror.New(http.StatusForbidden, "adminOnly", "Только суперадмин может создавать ссылки сброса")
	}

	target, err := s.DB.GetUserByID(ctx, targetUserID)
	if err != nil || target == nil {
		return "", 0, apierror.New(http.StatusNotFound, "noUser", "Пользователь не найден")
	}
	if target.Removed {
		return "", 0, apierror.New(http.StatusConflict, "removedUser", "Сначала разблокируйте пользователя")
	}

	tokenBytes := make([]byte, 24)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	tokenHash := TokenDigest(token)
	now := database.NowMs()
	expiresAt := now + (24 * 3600 * 1000) // 24 часа

	if err := s.DB.CreatePasswordReset(ctx, targetUserID, tokenHash, expiresAt, now); err != nil {
		return "", 0, err
	}

	return token, expiresAt, nil
}

// CheckResetLink проверяет валидность ссылки сброса пароля
func (s *UserService) CheckResetLink(ctx context.Context, token string) (string, error) {
	tokenHash := TokenDigest(token)
	_, name, err := s.DB.FindPasswordReset(ctx, tokenHash, database.NowMs())
	if err != nil || name == "" {
		return "", apierror.New(http.StatusNotFound, "resetGone", "Ссылка недействительна или истекла")
	}
	return name, nil
}

// ResetPasswordWithToken сбрасывает пароль по одноразовой ссылке
func (s *UserService) ResetPasswordWithToken(ctx context.Context, token, newPassword string) (*AuthPayload, error) {
	tokenHash := TokenDigest(token)

	newPassHash, err := HashPassword(newPassword)
	if err != nil {
		return nil, err
	}

	recKey, recHash, err := GenerateRecoveryKey()
	if err != nil {
		return nil, err
	}

	userID, version, err := s.DB.ConsumePasswordResetTx(ctx, tokenHash, newPassHash, recHash)
	if err != nil || userID == "" {
		return nil, apierror.New(http.StatusNotFound, "resetGone", "Ссылка недействительна или уже использована")
	}

	user, _ := s.DB.GetUserByID(ctx, userID)
	jwtToken, _ := s.IssueToken(userID, version)

	return &AuthPayload{
		Token:       jwtToken,
		User:        ToPublicUser(user),
		RecoveryKey: recKey,
	}, nil
}

// UpdateBio обновляет текст профиля
func (s *UserService) UpdateBio(ctx context.Context, userID, bio string) error {
	clean := strings.TrimSpace(bio)
	if len(clean) > 190 {
		clean = clean[:190]
	}
	return s.DB.UpdateBio(ctx, userID, clean)
}

// ChangeName меняет никнейм (с обязательной проверкой пароля)
func (s *UserService) ChangeName(ctx context.Context, user *domain.User, newName, password string) (*PublicUser, error) {
	if !VerifyPassword(password, user.PasswordHash) {
		return nil, apierror.New(http.StatusForbidden, "badPassword", "Неверный пароль")
	}

	cleanName := strings.Join(strings.Fields(newName), " ")
	if err := CheckNick(cleanName); err != nil {
		return nil, err
	}

	existing, err := s.DB.GetUserByNameKey(ctx, cleanName)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.ID != user.ID {
		return nil, apierror.New(http.StatusConflict, "nickTaken", "Этот ник уже занят")
	}

	if err := s.DB.UpdateName(ctx, user.ID, cleanName); err != nil {
		return nil, err
	}

	user.Name = cleanName
	return ToPublicUser(user), nil
}

// UpdateAvatar сохраняет новую аватарку пользователя (256x256 WebP/PNG)
func (s *UserService) UpdateAvatar(ctx context.Context, user *domain.User, data []byte) (*PublicUser, error) {
	if len(data) == 0 || int64(len(data)) > config.MaxAvatarUploadBytes {
		return nil, apierror.New(http.StatusRequestEntityTooLarge, "imageOver8", "Картинка больше 8 МБ")
	}

	pngData, err := media.ProcessSquareImage(data, config.AvatarSize)
	if err != nil {
		return nil, err
	}

	token := make([]byte, 6)
	_, _ = rand.Read(token)
	fileName := fmt.Sprintf("%s-%s.png", user.ID, hex.EncodeToString(token))
	filePath := filepath.Join(s.Cfg.AvatarDir, fileName)

	if err := os.WriteFile(filePath, pngData, 0644); err != nil {
		return nil, err
	}

	oldFile, err := s.DB.UpdateAvatar(ctx, user.ID, &fileName)
	if err == nil && oldFile != "" {
		media.RemoveFileIfExists(filepath.Join(s.Cfg.AvatarDir, oldFile))
	}

	user.Avatar = sql.NullString{String: fileName, Valid: true}
	return ToPublicUser(user), nil
}

// DeleteAvatar удаляет аватарку пользователя
func (s *UserService) DeleteAvatar(ctx context.Context, user *domain.User) (*PublicUser, error) {
	oldFile, err := s.DB.UpdateAvatar(ctx, user.ID, nil)
	if err == nil && oldFile != "" {
		media.RemoveFileIfExists(filepath.Join(s.Cfg.AvatarDir, oldFile))
	}
	user.Avatar = sql.NullString{Valid: false}
	return ToPublicUser(user), nil
}

// SetUserRemoved блокирует/разблокирует пользователя (только суперадмин)
func (s *UserService) SetUserRemoved(ctx context.Context, admin *domain.User, targetUserID string, removed bool) error {
	if !admin.IsSuperadmin {
		return apierror.New(http.StatusForbidden, "adminOnly", "Только суперадмин может блокировать пользователей")
	}
	target, err := s.DB.GetUserByID(ctx, targetUserID)
	if err != nil || target == nil {
		return apierror.New(http.StatusNotFound, "noUser", "Пользователь не найден")
	}
	if target.IsSuperadmin {
		return apierror.New(http.StatusForbidden, "noAdminRemove", "Суперадмина нельзя заблокировать")
	}
	return s.DB.SetUserRemoved(ctx, targetUserID, removed)
}

// StartStatusExpiryLoop запускает фоновую очистку временных статусов
func (s *UserService) StartStatusExpiryLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case t := <-ticker.C:
				_, _ = s.DB.ExpireDueStatuses(ctx, t.UnixNano()/int64(time.Millisecond))
			}
		}
	}()
}

func ToPublicUser(u *domain.User) *PublicUser {
	var avatar *string
	if u.Avatar.Valid && u.Avatar.String != "" {
		avatar = &u.Avatar.String
	}
	return &PublicUser{
		ID:        u.ID,
		Name:      u.Name,
		Color:     u.Color,
		Bio:       u.Bio,
		Avatar:    avatar,
		Status:    u.Status,
		CreatedAt: u.CreatedAt,
	}
}
