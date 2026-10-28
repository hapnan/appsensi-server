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

// Thread-safe in-memory session store
var (
	sessionStore = make(map[uuid.UUID]webauthn.SessionData)
	sessionMu    sync.RWMutex
)

func saveSession(userID uuid.UUID, data webauthn.SessionData) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	sessionStore[userID] = data
}

func getSession(userID uuid.UUID) (webauthn.SessionData, bool) {
	sessionMu.RLock()
	defer sessionMu.RUnlock()
	data, ok := sessionStore[userID]
	return data, ok
}

func deleteSession(userID uuid.UUID) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	delete(sessionStore, userID)
}

// Helper for REGISTRATION: gets existing user or creates one from Student table
func getOrCreateUserForRegistration(username string, email string, rpid string) (*models.User, error) {
	var user models.User

	// Note: Use "rpid = ?" if your DB column is rpid, or "rp_id = ?" if GORM auto-migrated it without column:rpid
	err := database.DB.
		Preload("WebAuthnUser", "rp_id = ?", rpid).
		Preload("Credentials").
		Where("username = ?", username).
		First(&user).Error

	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}

		var student models.Student
		if err := database.DB.Where("student_id = ?", username).First(&student).Error; err != nil {
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

// Helper for LOGIN: strictly looks up an existing user without creating new DB rows
func getExistingUserForLogin(username string, rpid string) (*models.User, error) {
	var user models.User
	err := database.DB.
		Preload("WebAuthnUser", "rp_id = ?", rpid).
		Preload("Credentials").
		Where("username = ?", username).
		First(&user).Error
	if err != nil {
		return nil, err
	}
	if user.WebAuthnUser == nil {
		return nil, errors.New("user has no webauthn handle registered for this RPID")
	}
	return &user, nil
}

type RegistrationRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
}

func BeginRegistration(c *gin.Context) {
	var req RegistrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and valid email are required"})
		return
	}

	rpid := config.WebAuthn.Config.GetRPID()
	user, err := getOrCreateUserForRegistration(req.Username, req.Email, rpid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get or create user"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to begin registration"})
		return
	}

	saveSession(user.ID, *sessionData)
	c.JSON(http.StatusOK, options)
}

func FinishRegistration(c *gin.Context) {
	var req RegistrationRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and valid email are required"})
		return
	}
	// Restore request body so go-webauthn can read it
	if bodyBytes, ok := c.Get(gin.BodyBytesKey); ok {
		c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes.([]byte)))
	}

	rpid := config.WebAuthn.Config.GetRPID()
	user, err := getOrCreateUserForRegistration(req.Username, req.Email, rpid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "user not found"})
		return
	}

	sessionData, ok := getSession(user.ID)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no active registration session found"})
		return
	}

	parsedResponse, err := config.WebAuthn.FinishRegistration(user, sessionData, c.Request)
	if err != nil {
		log.Printf("FinishRegistration error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to finish registration"})
		return
	}

	var flags uint32
	if parsedResponse.Flags.UserPresent {
		flags |= 1
	}
	if parsedResponse.Flags.UserVerified {
		flags |= 2
	}
	if parsedResponse.Flags.BackupEligible {
		flags |= 4
	}
	if parsedResponse.Flags.BackupState {
		flags |= 8
	}

	// Join transports (e.g. "internal,hybrid") for the non-null Transport column
	transports := make([]string, len(parsedResponse.Transport))
	for i, t := range parsedResponse.Transport {
		transports[i] = string(t)
	}

	newCred := models.Credential{
		UserID:            user.ID,
		CredentialID:      parsedResponse.ID,
		PublicKey:         parsedResponse.PublicKey,
		AttestationType:   parsedResponse.AttestationType,
		AttestationFormat: "packed",
		Transport:         strings.Join(transports, ","),
		Flags:             []byte{byte(flags)},
		Attachment:        string(parsedResponse.Authenticator.Attachment),
		SignCount:         int64(parsedResponse.Authenticator.SignCount),
		CloneWarning:      parsedResponse.Authenticator.CloneWarning,
		AAGUID:            parsedResponse.Authenticator.AAGUID,
		Presence:          parsedResponse.Flags.UserPresent,
		Verified:          parsedResponse.Flags.UserVerified,
		BackupEligible:    parsedResponse.Flags.BackupEligible,
		BackupState:       parsedResponse.Flags.BackupState,
	}

	if err := database.DB.Create(&newCred).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save credential"})
		return
	}

	deleteSession(user.ID)
	c.JSON(http.StatusOK, gin.H{"status": "registration success"})
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
}

func BeginLogin(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	rpid := config.WebAuthn.Config.GetRPID()
	user, err := getExistingUserForLogin(req.Username, rpid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found or not registered for WebAuthn"})
		return
	}

	options, sessionData, err := config.WebAuthn.BeginLogin(
		user,
		webauthn.WithAssertionPublicKeyCredentialHints([]protocol.PublicKeyCredentialHints{protocol.PublicKeyCredentialHintHybrid}),
	)
	if err != nil {
		log.Printf("BeginLogin error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to begin login"})
		return
	}

	saveSession(user.ID, *sessionData)
	c.JSON(http.StatusOK, options)
}

func FinishLogin(c *gin.Context) {
	var req LoginRequest
	// Must use ShouldBindBodyWithJSON and restore c.Request.Body so FinishLogin can read the body!
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}
	if bodyBytes, ok := c.Get(gin.BodyBytesKey); ok {
		c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes.([]byte)))
	}

	rpid := config.WebAuthn.Config.GetRPID()
	user, err := getExistingUserForLogin(req.Username, rpid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	sessionData, ok := getSession(user.ID)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no active login session found"})
		return
	}

	credential, err := config.WebAuthn.FinishLogin(user, sessionData, c.Request)
	if err != nil {
		log.Printf("FinishLogin error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to verify login assertion"})
		return
	}

	if credential.Authenticator.CloneWarning {
		log.Printf("SECURITY WARNING: Credential clone detected for user %s", user.Username)
	}

	// Update SignCount, CloneWarning, and LastUsedAt in the database
	now := time.Now()
	database.DB.Model(&models.Credential{}).
		Where("credential_id = ?", credential.ID).
		Updates(map[string]interface{}{
			"sign_count":    int64(credential.Authenticator.SignCount),
			"clone_warning": credential.Authenticator.CloneWarning,
			"last_used_at":  &now,
		})

	deleteSession(user.ID)

	c.JSON(http.StatusOK, gin.H{
		"status":  "login success",
		"message": "User authenticated successfully",
	})
}
