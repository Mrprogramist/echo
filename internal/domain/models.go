package domain

import (
	"database/sql"
)

type ServerRole string

const (
	RoleOwner     ServerRole = "owner"
	RoleAdmin     ServerRole = "admin"
	RoleModerator ServerRole = "moderator"
	RoleMember    ServerRole = "member"
)

type ChannelKind string

const (
	ChannelKindText  ChannelKind = "text"
	ChannelKindVoice ChannelKind = "voice"
)

type DMKind string

const (
	DMKindDirect DMKind = "dm"
	DMKindGroup  DMKind = "group"
)

type Attachment struct {
	URL    string `json:"url"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Name   string `json:"name,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

type User struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	NameKey             string         `json:"-"`
	PasswordHash        string         `json:"-"`
	Color               string         `json:"color"`
	Bio                 string         `json:"bio"`
	Avatar              sql.NullString `json:"avatar"`
	TokenVersion        int            `json:"-"`
	RecoveryHash        sql.NullString `json:"-"`
	IsSuperadmin        bool           `json:"is_superadmin"`
	CanCreateServers    bool           `json:"can_create_servers"`
	InvitedBy           sql.NullString `json:"invited_by,omitempty"`
	RegisteredViaInvite sql.NullString `json:"registered_via_invite,omitempty"`
	Status              string         `json:"status"`
	StatusText          string         `json:"status_text"`
	StatusUntil         sql.NullInt64  `json:"status_until,omitempty"`
	Removed             bool           `json:"removed"`
	CreatedAt           int64          `json:"created_at"`
}

func (u *User) MayCreateServer() bool {
	return u.IsSuperadmin || u.CanCreateServers
}

type Server struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Icon        sql.NullString `json:"icon"`
	OwnerID     string         `json:"owner_id"`
	CreatedAt   int64          `json:"created_at"`
}

type ServerMember struct {
	ServerID string     `json:"server_id"`
	UserID   string     `json:"user_id"`
	Role     ServerRole `json:"role"`
	JoinedAt int64      `json:"joined_at"`
}

type ServerInvite struct {
	Code               string        `json:"code"`
	ServerID           string        `json:"server_id"`
	CreatedBy          string        `json:"created_by"`
	AllowsRegistration bool          `json:"allows_registration"`
	MaxUses            int           `json:"max_uses"`
	Uses               int           `json:"uses"`
	ExpiresAt          sql.NullInt64 `json:"expires_at"`
	CreatedAt          int64         `json:"created_at"`
}

type Category struct {
	ID        string `json:"id"`
	ServerID  string `json:"server_id"`
	Name      string `json:"name"`
	Position  int    `json:"position"`
	CreatedAt int64  `json:"created_at"`
}

type Channel struct {
	ID         string         `json:"id"`
	ServerID   string         `json:"server_id"`
	CategoryID sql.NullString `json:"category_id,omitempty"`
	Name       string         `json:"name"`
	Kind       ChannelKind    `json:"kind"`
	Position   int            `json:"position"`
	DeletedAt  sql.NullInt64  `json:"deleted_at,omitempty"`
	CreatedAt  int64          `json:"created_at"`
}

type Message struct {
	ID          string         `json:"id"`
	ChannelID   string         `json:"channel_id"`
	UserID      string         `json:"user_id"`
	Content     string         `json:"content"`
	Attachments []Attachment   `json:"attachments"`
	ReplyTo     sql.NullString `json:"reply_to,omitempty"`
	EditedAt    sql.NullInt64  `json:"edited_at,omitempty"`
	CreatedAt   int64          `json:"created_at"`
}

type Block struct {
	UserID    string `json:"user_id"`
	BlockedID string `json:"blocked_id"`
	CreatedAt int64  `json:"created_at"`
}

type DM struct {
	ID        string         `json:"id"`
	Kind      DMKind         `json:"kind"`
	Name      string         `json:"name"`
	OwnerID   sql.NullString `json:"owner_id,omitempty"`
	PairKey   sql.NullString `json:"pair_key,omitempty"`
	CreatedAt int64          `json:"created_at"`
}

type DMMember struct {
	DMID     string `json:"dm_id"`
	UserID   string `json:"user_id"`
	JoinedAt int64  `json:"joined_at"`
}

type DMMessage struct {
	ID          string         `json:"id"`
	DMID        string         `json:"dm_id"`
	UserID      string         `json:"user_id"`
	Content     string         `json:"content"`
	Attachments []Attachment   `json:"attachments"`
	ReplyTo     sql.NullString `json:"reply_to,omitempty"`
	EditedAt    sql.NullInt64  `json:"edited_at,omitempty"`
	CreatedAt   int64          `json:"created_at"`
}

type Reaction struct {
	MessageID string `json:"message_id"`
	UserID    string `json:"user_id"`
	Emoji     string `json:"emoji"`
	CreatedAt int64  `json:"created_at"`
}

type ReadState struct {
	UserID         string `json:"user_id"`
	ConversationID string `json:"conversation_id"`
	ReadAt         int64  `json:"read_at"`
}

type ChannelMute struct {
	UserID    string `json:"user_id"`
	ChannelID string `json:"channel_id"`
}

type VoiceGrant struct {
	UserID string `json:"user_id"`
	Room   string `json:"room"`
}

type Upload struct {
	ID        string         `json:"id"`
	UserID    string         `json:"user_id"`
	FileName  sql.NullString `json:"file_name"`
	FileSize  sql.NullInt64  `json:"file_size"`
	FileType  sql.NullString `json:"file_type"`
	Width     int            `json:"width"`
	Height    int            `json:"height"`
	Scope     sql.NullString `json:"scope"`
	CreatedAt int64          `json:"created_at"`
}

type PasswordReset struct {
	UserID    string `json:"user_id"`
	TokenHash string `json:"-"`
	ExpiresAt int64  `json:"expires_at"`
	CreatedAt int64  `json:"created_at"`
}

type InstanceSetting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
