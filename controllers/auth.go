package controllers

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"appsensi-server/config"
	"appsensi-server/database"
	"appsensi-server/models"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const authSessionTTL = 5 * time.Minute

type authSession struct {
	Ceremony    protocol.CeremonyType
	SessionData webauthn.SessionData
	CreatedAt   time.Time
}

// Thread-safe in-memory session store
var (
	sessionStore = make(map[uuid.UUID]authSession)
	sessionMu    sync.RWMutex
)

func saveCeremonySession(userID uuid.UUID, ceremony protocol.CeremonyType, data webauthn.SessionData) {
	now := time.Now()
	sessionMu.Lock()
	defer sessionMu.Unlock()
	for id, sess := range sessionStore {
		if now.Sub(sess.CreatedAt) > authSessionTTL {
			delete(sessionStore, id)
		}
	}
	sessionStore[userID] = authSession{
		Ceremony:    ceremony,
		SessionData: data,
		CreatedAt:   now,
	}
}

func saveSession(userID uuid.UUID, data webauthn.SessionData) {
	saveCeremonySession(userID, protocol.CreateCeremony, data)
}

func getCeremonySession(userID uuid.UUID, ceremony protocol.CeremonyType) (webauthn.SessionData, bool) {
	now := time.Now()
	sessionMu.RLock()
	sess, ok := sessionStore[userID]
	sessionMu.RUnlock()
	if !ok {
		return webauthn.SessionData{}, false
	}
	if now.Sub(sess.CreatedAt) > authSessionTTL {
		deleteSession(userID)
		return webauthn.SessionData{}, false
	}
	if sess.Ceremony != ceremony {
		return webauthn.SessionData{}, false
	}
	return sess.SessionData, true
}

func getSession(userID uuid.UUID) (webauthn.SessionData, bool) {
	now := time.Now()
	sessionMu.RLock()
	sess, ok := sessionStore[userID]
	sessionMu.RUnlock()
	if !ok {
		return webauthn.SessionData{}, false
	}
	if now.Sub(sess.CreatedAt) > authSessionTTL {
		deleteSession(userID)
		return webauthn.SessionData{}, false
	}
	return sess.SessionData, true
}

func deleteSession(userID uuid.UUID) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	delete(sessionStore, userID)
}

// AuthStore abstracts persistence operations for production WebAuthn registration and login endpoints,
// backed by PostgreSQL (GORM) in production and overridable in unit tests.
type AuthStore interface {
	GetOrCreateUserForRegistration(username, email, rpid string) (*models.User, error)
	GetExistingUserForLogin(username, rpid string) (*models.User, error)
	SaveCredential(cred *models.Credential) error
	UpdateCredentialOnLogin(credentialID []byte, signCount int64, cloneWarning bool, lastUsedAt time.Time) error
}

type gormAuthStore struct{}

var _ AuthStore = gormAuthStore{}

var credentialCeremonyOmitColumns = []string{"Attestation", "Extensions", "Flags"}

func omitUnusedCredentialBlobs(db *gorm.DB) *gorm.DB {
	return db.Omit(credentialCeremonyOmitColumns...)
}

func queryUserWithOptionalWebAuthn(db *gorm.DB, username, rpid string) *gorm.DB {
	return db.
		Joins("WebAuthnUser", db.Where(&models.WebAuthnUser{RpID: rpid}, "RpID")).
		Preload("Credentials", omitUnusedCredentialBlobs).
		Where("users.username = ?", username)
}

func queryUserForLogin(db *gorm.DB, username, rpid string) *gorm.DB {
	return db.
		InnerJoins("WebAuthnUser", db.Where(&models.WebAuthnUser{RpID: rpid}, "RpID")).
		Preload("Credentials", omitUnusedCredentialBlobs).
		Where("users.username = ?", username)
}

// GetOrCreateUserForRegistration gets an existing user or creates one from the Student table.
func (gormAuthStore) GetOrCreateUserForRegistration(username string, email string, rpid string) (*models.User, error) {
	var user models.User

	err := queryUserWithOptionalWebAuthn(database.DB, username, rpid).Take(&user).Error

	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}

		var student models.Student
		if err := database.DB.Where("student_id = ?", username).Take(&student).Error; err != nil {
			return nil, err
		}

		user = models.User{
			Username:    username,
			Email:       email,
			DisplayName: &student.Name,
		}
		if err := database.DB.Create(&user).Error; err != nil {
			return nil, err
		}
	}

	if user.WebAuthnUser == nil {
		handle := make([]byte, 32)
		if _, err := rand.Read(handle); err != nil {
			return nil, err
		}

		webAuthnUser := models.WebAuthnUser{
			RpID:   rpid,
			UserID: user.ID,
			Handle: handle,
		}
		if err := database.DB.Create(&webAuthnUser).Error; err != nil {
			return nil, err
		}

		user.WebAuthnUser = &webAuthnUser
	}

	return &user, nil
}

// GetExistingUserForLogin strictly looks up an existing user without creating new DB rows.
func (gormAuthStore) GetExistingUserForLogin(username string, rpid string) (*models.User, error) {
	var user models.User
	err := queryUserForLogin(database.DB, username, rpid).Take(&user).Error
	if err != nil {
		return nil, err
	}
	if user.WebAuthnUser == nil {
		return nil, errors.New("user has no webauthn handle registered for this RPID")
	}
	return &user, nil
}

func (gormAuthStore) SaveCredential(cred *models.Credential) error {
	return database.DB.Create(cred).Error
}

func (gormAuthStore) UpdateCredentialOnLogin(credentialID []byte, signCount int64, cloneWarning bool, lastUsedAt time.Time) error {
	return database.DB.Model(&models.Credential{}).
		Where("credential_id = ?", credentialID).
		Select("SignCount", "CloneWarning", "LastUsedAt").
		Updates(models.Credential{
			SignCount:    signCount,
			CloneWarning: cloneWarning,
			LastUsedAt:   &lastUsedAt,
		}).Error
}

var (
	authStoreMu sync.RWMutex
	authStore   AuthStore = gormAuthStore{}
)

// SetAuthStore overrides the persistence store for auth endpoints (used by unit tests).
func SetAuthStore(store AuthStore) {
	authStoreMu.Lock()
	defer authStoreMu.Unlock()
	if store == nil {
		authStore = gormAuthStore{}
		return
	}
	authStore = store
}

func getAuthStore() AuthStore {
	authStoreMu.RLock()
	defer authStoreMu.RUnlock()
	return authStore
}

func restoreBufferedRequestBody(c *gin.Context) bool {
	bodyVal, ok := c.Get(gin.BodyBytesKey)
	if !ok {
		return false
	}
	rawBytes, ok := bodyVal.([]byte)
	if !ok {
		return false
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(rawBytes))
	return true
}

type RegistrationRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
}

func BeginRegistration(c *gin.Context) {
	if config.WebAuthn == nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "WebAuthn is not initialized")
		return
	}

	var req RegistrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "username and valid email are required")
		return
	}

	store := getAuthStore()
	rpid := config.WebAuthn.Config.GetRPID()
	user, err := store.GetOrCreateUserForRegistration(req.Username, req.Email, rpid)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "failed to get or create user")
		return
	}

	// Exclude credentials the user has already registered so they don't re-register the same device
	excludeList := make([]protocol.CredentialDescriptor, len(user.Credentials))
	for i, cred := range user.Credentials {
		excludeList[i] = protocol.CredentialDescriptor{
			Type:         protocol.PublicKeyCredentialType,
			CredentialID: cred.CredentialID,
		}
	}

	options, sessionData, err := config.WebAuthn.BeginRegistration(
		user,
		webauthn.WithExclusions(excludeList),
		webauthn.WithPublicKeyCredentialHints([]protocol.PublicKeyCredentialHints{protocol.PublicKeyCredentialHintHybrid}),
	)
	if err != nil {
		log.Printf("BeginRegistration error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "failed to begin registration")
		return
	}

	saveCeremonySession(user.ID, protocol.CreateCeremony, *sessionData)
	c.JSON(http.StatusOK, options)
}

func FinishRegistration(c *gin.Context) {
	if config.WebAuthn == nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "WebAuthn is not initialized")
		return
	}

	var req RegistrationRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "username and valid email are required")
		return
	}
	// Restore request body safely so go-webauthn can read it
	if !restoreBufferedRequestBody(c) {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid buffered request body")
		return
	}

	store := getAuthStore()
	rpid := config.WebAuthn.Config.GetRPID()
	user, err := store.GetOrCreateUserForRegistration(req.Username, req.Email, rpid)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "user not found")
		return
	}

	sessionData, ok := getCeremonySession(user.ID, protocol.CreateCeremony)
	if !ok {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "no active registration session found")
		return
	}

	parsedResponse, err := config.WebAuthn.FinishRegistration(user, sessionData, c.Request)
	if err != nil {
		log.Printf("FinishRegistration error: %v", err)
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "failed to finish registration")
		return
	}

	// Join transports (e.g. "internal,hybrid") for the non-null Transport column
	transports := make([]string, len(parsedResponse.Transport))
	for i, t := range parsedResponse.Transport {
		transports[i] = string(t)
	}

	aaguid := parsedResponse.Authenticator.AAGUID
	if aaguid == nil {
		aaguid = []byte{}
	}
	attFormat := parsedResponse.AttestationFormat
	if attFormat == "" {
		attFormat = parsedResponse.AttestationType
	}

	newCred := models.Credential{
		UserID:            user.ID,
		CredentialID:      parsedResponse.ID,
		PublicKey:         parsedResponse.PublicKey,
		AttestationType:   parsedResponse.AttestationType,
		AttestationFormat: attFormat,
		Attestation:       parsedResponse.Attestation.Object,
		Transport:         strings.Join(transports, ","),
		Flags:             models.EncodeCredentialFlags(parsedResponse.Flags),
		Attachment:        parsedResponse.Authenticator.Attachment,
		SignCount:         int64(parsedResponse.Authenticator.SignCount),
		CloneWarning:      parsedResponse.Authenticator.CloneWarning,
		AAGUID:            aaguid,
		Presence:          parsedResponse.Flags.UserPresent,
		Verified:          parsedResponse.Flags.UserVerified,
		BackupEligible:    parsedResponse.Flags.BackupEligible,
		BackupState:       parsedResponse.Flags.BackupState,
	}

	if err := store.SaveCredential(&newCred); err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "failed to save credential")
		return
	}

	if err := linkStudentAfterRegistration(user.Username, user.ID); err != nil {
		log.Printf("FinishRegistration warning: failed to link student %q to user %s: %v", user.Username, user.ID, err)
	}

	deleteSession(user.ID)
	c.JSON(http.StatusOK, AuthStatusResponse{Status: "registration success"})
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
}

func BeginLogin(c *gin.Context) {
	if config.WebAuthn == nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "WebAuthn is not initialized")
		return
	}

	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "username is required")
		return
	}

	store := getAuthStore()
	rpid := config.WebAuthn.Config.GetRPID()
	user, err := store.GetExistingUserForLogin(req.Username, rpid)
	if err != nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "user not found or not registered for WebAuthn")
		return
	}

	options, sessionData, err := config.WebAuthn.BeginLogin(
		user,
		webauthn.WithAssertionPublicKeyCredentialHints([]protocol.PublicKeyCredentialHints{protocol.PublicKeyCredentialHintHybrid}),
	)
	if err != nil {
		log.Printf("BeginLogin error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "failed to begin login")
		return
	}

	saveCeremonySession(user.ID, protocol.AssertCeremony, *sessionData)
	c.JSON(http.StatusOK, options)
}

func FinishLogin(c *gin.Context) {
	if config.WebAuthn == nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "WebAuthn is not initialized")
		return
	}

	var req LoginRequest
	// Must use ShouldBindBodyWithJSON and restore c.Request.Body so FinishLogin can read the body!
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "username is required")
		return
	}
	if !restoreBufferedRequestBody(c) {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid buffered request body")
		return
	}

	store := getAuthStore()
	rpid := config.WebAuthn.Config.GetRPID()
	user, err := store.GetExistingUserForLogin(req.Username, rpid)
	if err != nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "user not found")
		return
	}

	sessionData, ok := getCeremonySession(user.ID, protocol.AssertCeremony)
	if !ok {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "no active login session found")
		return
	}

	credential, err := config.WebAuthn.FinishLogin(user, sessionData, c.Request)
	if err != nil {
		log.Printf("FinishLogin error: %v", err)
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "failed to verify login assertion")
		return
	}

	if credential.Authenticator.CloneWarning {
		log.Printf("SECURITY WARNING: Credential clone detected for user %s", user.Username)
	}

	// Update SignCount, CloneWarning, and LastUsedAt in the database
	now := time.Now()
	if err := store.UpdateCredentialOnLogin(
		credential.ID,
		int64(credential.Authenticator.SignCount),
		credential.Authenticator.CloneWarning,
		now,
	); err != nil {
		log.Printf("FinishLogin UpdateCredentialOnLogin error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "failed to update credential state")
		return
	}

	deleteSession(user.ID)

	c.JSON(http.StatusOK, AuthStatusResponse{
		Status:  "login success",
		Message: "User authenticated successfully",
	})
}
