package models

import (
	"math"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

// UserType represents the domain persona of an authenticated user.
type UserType string

const (
	UserTypeStudent  UserType = "student"
	UserTypeEmployee UserType = "employee"
	UserTypeLecturer UserType = "lecturer"
)

// IsValidUserType checks whether t is a supported UserType.
func IsValidUserType(t UserType) bool {
	switch t {
	case UserTypeStudent, UserTypeEmployee, UserTypeLecturer:
		return true
	default:
		return false
	}
}

// User corresponds to the "users" table
type User struct {
	ID           uuid.UUID     `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Email        string        `gorm:"type:text;not null;unique" json:"email"`
	Username     string        `gorm:"type:text;not null;unique" json:"username"`
	DisplayName  *string       `gorm:"type:text" json:"display_name"`
	UserType     UserType      `gorm:"type:text;not null;default:'student';index:users_user_type_idx" json:"user_type"`
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
		var transports []protocol.AuthenticatorTransport
		if cred.Transport != "" {
			for _, t := range strings.Split(cred.Transport, ",") {
				if trimmed := strings.TrimSpace(t); trimmed != "" {
					transports = append(transports, protocol.AuthenticatorTransport(trimmed))
				}
			}
		}

		webauthnCred := webauthn.Credential{
			ID:                cred.CredentialID,
			PublicKey:         cred.PublicKey,
			AttestationType:   cred.AttestationType,
			AttestationFormat: cred.AttestationFormat,
			Transport:         transports,
			Flags: webauthn.CredentialFlags{
				UserPresent:    cred.Presence,
				UserVerified:   cred.Verified,
				BackupEligible: cred.BackupEligible,
				BackupState:    cred.BackupState,
			},
			Authenticator: webauthn.Authenticator{
				AAGUID:       cred.AAGUID,
				CloneWarning: cred.CloneWarning,
				SignCount:    safeUint32SignCount(cred.SignCount),
				Attachment:   cred.Attachment,
			},
		}
		webauthnCreds = append(webauthnCreds, webauthnCred)
	}
	return webauthnCreds
}

func safeUint32SignCount(v int64) uint32 {
	if v <= 0 {
		return 0
	}
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

// EncodeCredentialFlags packs WebAuthn credential flags into a single byte bitmask.
func EncodeCredentialFlags(f webauthn.CredentialFlags) []byte {
	var flags uint8
	if f.UserPresent {
		flags |= 1
	}
	if f.UserVerified {
		flags |= 2
	}
	if f.BackupEligible {
		flags |= 4
	}
	if f.BackupState {
		flags |= 8
	}
	return []byte{flags}
}

// WebAuthnUser corresponds to the "webauthn_users" table
type WebAuthnUser struct {
	ID        uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	CreatedAt time.Time `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`
	RpID      string    `gorm:"column:rp_id;type:varchar(512);not null;uniqueIndex:webauthn_users_user_id_key;uniqueIndex:webauthn_users_handle_key" json:"rp_id"`
	UserID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:webauthn_users_user_id_key;index:webauthn_users_user_idx" json:"user_id"`
	User      User      `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	Handle    []byte    `gorm:"type:bytea;not null;uniqueIndex:webauthn_users_handle_key" json:"handle"`
}

func (WebAuthnUser) TableName() string { return "webauthn_users" }

// Credential corresponds to the "credentials" table
type Credential struct {
	ID                uuid.UUID                        `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID            uuid.UUID                        `gorm:"type:uuid;not null;uniqueIndex:credentials_user_credential_idx,priority:1" json:"user_id"`
	User              User                             `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	CredentialID      []byte                           `gorm:"type:bytea;not null;unique;uniqueIndex:credentials_user_credential_idx,priority:2" json:"credential_id"`
	AAGUID            []byte                           `gorm:"column:aaguid;type:bytea;not null" json:"aaguid"`
	PublicKey         []byte                           `gorm:"type:bytea;not null" json:"public_key"`
	AttestationType   string                           `gorm:"type:text;not null" json:"attestation_type"`
	AttestationFormat string                           `gorm:"type:text;not null" json:"attestation_format"`
	Attestation       []byte                           `gorm:"type:bytea" json:"attestation"`
	Transport         string                           `gorm:"type:text;not null" json:"transport"`
	SignCount         int64                            `gorm:"type:bigint;not null;default:0" json:"sign_count"`
	CloneWarning      bool                             `gorm:"type:boolean;not null;default:false" json:"clone_warning"`
	Attachment        protocol.AuthenticatorAttachment `gorm:"type:text;column:attachment;not null" json:"attachment"`
	Flags             []byte                           `gorm:"type:bytea" json:"flags"`
	Presence          bool                             `gorm:"type:boolean;not null;default:false" json:"presence"`
	Verified          bool                             `gorm:"type:boolean;not null;default:false" json:"verified"`
	BackupEligible    bool                             `gorm:"type:boolean;not null;default:false" json:"backup_eligible"`
	BackupState       bool                             `gorm:"type:boolean;not null;default:false" json:"backup_state"`
	Extensions        []byte                           `gorm:"type:bytea" json:"extensions"`
	CreatedAt         time.Time                        `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`
	LastUsedAt        *time.Time                       `gorm:"type:timestamptz" json:"last_used_at"`
}

func (Credential) TableName() string { return "credentials" }
