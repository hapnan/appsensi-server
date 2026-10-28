package models

import (
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

// User corresponds to the "users" table
type User struct {
	ID           uuid.UUID     `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Email        string        `gorm:"type:text;not null;unique" json:"email"`
	Username     string        `gorm:"type:text;not null;unique" json:"username"`
	DisplayName  *string       `gorm:"type:text" json:"display_name"`
	WebAuthnUser *WebAuthnUser `gorm:"foreignKey:UserID" json:"-"`
	Credentials  []Credential  `gorm:"foreignKey:UserID" json:"-"`
	CreatedAt    time.Time     `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`
	UpdatedAt    *time.Time    `gorm:"type:timestamptz;autoUpdateTime" json:"updated_at"`
}

func (User) TableName() string { return "users" }

// Ensure User implements webauthn.User interface
var _ webauthn.User = &User{}

// WebAuthnID returns the opaque WebAuthn User Handle from webauthn_users
func (u *User) WebAuthnID() []byte {
	if u.WebAuthnUser != nil {
		return u.WebAuthnUser.Handle
	}
	return nil
}

// WebAuthnName returns the user's username
func (u *User) WebAuthnName() string {
	return u.Username
}

// WebAuthnDisplayName returns the user's display name, fallback to username
func (u *User) WebAuthnDisplayName() string {
	if u.DisplayName != nil && *u.DisplayName != "" {
		return *u.DisplayName
	}
	return u.Username
}

// WebAuthnIcon is deprecated in the spec, return empty string
func (u *User) WebAuthnIcon() string {
	return ""
}

// WebAuthnCredentials returns the user's WebAuthn credentials
func (u *User) WebAuthnCredentials() []webauthn.Credential {
	var webauthnCreds []webauthn.Credential
	for _, cred := range u.Credentials {
		webauthnCred := webauthn.Credential{
			ID:              cred.CredentialID,
			PublicKey:       cred.PublicKey,
			AttestationType: cred.AttestationType,
			Flags: webauthn.CredentialFlags{
				UserPresent:    cred.Presence,
				UserVerified:   cred.Verified,
				BackupEligible: cred.BackupEligible,
				BackupState:    cred.BackupState,
			},
			Authenticator: webauthn.Authenticator{
				AAGUID:       cred.AAGUID,
				CloneWarning: cred.CloneWarning,
				SignCount:    uint32(cred.SignCount),
			},
		}
		webauthnCreds = append(webauthnCreds, webauthnCred)
	}
	return webauthnCreds
}

// WebAuthnUser corresponds to the "webauthn_users" table
type WebAuthnUser struct {
	ID        uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	CreatedAt time.Time `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`
	RpID      string    `gorm:"column:rp_id;type:varchar(512);not null;uniqueIndex:webauthn_users_user_id_key;uniqueIndex:webauthn_users_handle_key" json:"rp_id"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:webauthn_users_user_id_key" json:"user_id"`
	User      User      `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	Handle    []byte    `gorm:"type:bytea;not null;uniqueIndex:webauthn_users_handle_key" json:"handle"`
}

func (WebAuthnUser) TableName() string { return "webauthn_users" }

// Credential corresponds to the "credentials" table
type Credential struct {
	ID                uuid.UUID  `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID            uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:credentials_user_credential_idx" json:"user_id"`
	User              User       `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	CredentialID      []byte     `gorm:"type:bytea;not null;unique;uniqueIndex:credentials_user_credential_idx" json:"credential_id"`
	AAGUID            []byte     `gorm:"column:aaguid;type:bytea;not null" json:"aaguid"`
	PublicKey         []byte     `gorm:"type:bytea;not null" json:"public_key"`
	AttestationType   string     `gorm:"type:text;not null" json:"attestation_type"`
	AttestationFormat string     `gorm:"type:text;not null" json:"attestation_format"`
	Attestation       []byte     `gorm:"type:bytea" json:"attestation"`
	Transport         string     `gorm:"type:text;not null" json:"transport"`
	SignCount         int64      `gorm:"type:bigint;not null;default:0" json:"sign_count"`
	CloneWarning      bool       `gorm:"type:boolean;not null;default:false" json:"clone_warning"`
	Attachment        string     `gorm:"type:text;column:attachment;not null" json:"attachment"`
	Flags             []byte     `gorm:"type:bytea" json:"flags"`
	Presence          bool       `gorm:"type:boolean;not null;default:false" json:"presence"`
	Verified          bool       `gorm:"type:boolean;not null;default:false" json:"verified"`
	BackupEligible    bool       `gorm:"type:boolean;not null;default:false" json:"backup_eligible"`
	BackupState       bool       `gorm:"type:boolean;not null;default:false" json:"backup_state"`
	Extensions        []byte     `gorm:"type:bytea" json:"extensions"`
	CreatedAt         time.Time  `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`
	LastUsedAt        *time.Time `gorm:"type:timestamptz" json:"last_used_at"`
}

func (Credential) TableName() string { return "credentials" }
