package controllers

import (
	"log"
	"net/http"

	"appsensi-server/config"
	"appsensi-server/database"
	"appsensi-server/models"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

// SessionData simulates an in-memory session store for authentication challenges.
// In a real application, you should use Redis, Memcached, or a database with TTL.
var sessionStore = make(map[uuid.UUID]webauthn.SessionData)

// Helper function to get or create a user by username
func getUserByUsername(username string) (*models.User, error) {
	var user models.User
	result := database.DB.Preload("Credentials").Where("username = ?", username).First(&user)
	if result.Error != nil {
		// If user doesn't exist, create one for demonstration purposes
		// In a real app, you might have a separate registration endpoint

		displayName := username
		user = models.User{
			Username:    username,
			Email:       username + "@example.com", // Dummy email required by unique constraint
			DisplayName: &displayName,
		}
		if err := database.DB.Create(&user).Error; err != nil {
			return nil, err
		}
	}
	return &user, nil
}

func BeginRegistration(c *gin.Context) {
	// Parse username from query parameter or body
	username := c.Query("username")
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	// Get or create user
	user, err := getUserByUsername(username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get or create user"})
		return
	}

	// Generate the WebAuthn registration challenge
	options, sessionData, err := config.WebAuthn.BeginRegistration(
		user,
		webauthn.WithPublicKeyCredentialHints([]protocol.PublicKeyCredentialHints{protocol.PublicKeyCredentialHintHybrid}),
	)
	if err != nil {
		log.Printf("BeginRegistration error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to begin registration"})
		return
	}

	// Store the session data for the finish registration step
	// We use the user's uint ID as the key in our simple in-memory map
	sessionStore[user.ID] = *sessionData

	// Send the options to the client
	c.JSON(http.StatusOK, options)
}

func FinishRegistration(c *gin.Context) {
	username := c.Query("username")
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	user, err := getUserByUsername(username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "user not found"})
		return
	}

	// Retrieve the session data
	sessionData, ok := sessionStore[user.ID]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no active registration session found"})
		return
	}

	// Parse the credential creation response
	parsedResponse, err := config.WebAuthn.FinishRegistration(user, sessionData, c.Request)
	if err != nil {
		log.Printf("FinishRegistration error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to finish registration"})
		return
	}

	// Convert bool flags to uint32 bitmask
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

	// Save the credential to the database
	newCred := models.Credential{
		UserID:            user.ID,
		CredentialID:      parsedResponse.ID,
		PublicKey:         parsedResponse.PublicKey,
		AttestationType:   parsedResponse.AttestationType,
		Flags:             []byte{byte(flags)}, // converting bitmask uint32 to []byte
		Attachment:        string(parsedResponse.Authenticator.Attachment),
		SignCount:         int64(parsedResponse.Authenticator.SignCount),
		CloneWarning:      parsedResponse.Authenticator.CloneWarning,
		AAGUID:            parsedResponse.Authenticator.AAGUID,
		Presence:          parsedResponse.Flags.UserPresent,
		Verified:          parsedResponse.Flags.UserVerified,
		BackupEligible:    parsedResponse.Flags.BackupEligible,
		BackupState:       parsedResponse.Flags.BackupState,
		AttestationFormat: "packed", // Defaulting as go-webauthn parses formats automatically
	}

	if err := database.DB.Create(&newCred).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save credential"})
		return
	}

	// Cleanup session
	delete(sessionStore, user.ID)

	c.JSON(http.StatusOK, gin.H{"status": "registration success"})
}

func BeginLogin(c *gin.Context) {
	username := c.Query("username")
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	user, err := getUserByUsername(username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "user not found"})
		return
	}

	// Generate WebAuthn login challenge
	options, sessionData, err := config.WebAuthn.BeginLogin(
		user,
		webauthn.WithAssertionPublicKeyCredentialHints([]protocol.PublicKeyCredentialHints{protocol.PublicKeyCredentialHintHybrid}),
	)
	if err != nil {
		log.Printf("BeginLogin error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to begin login"})
		return
	}

	// Store session
	sessionStore[user.ID] = *sessionData

	c.JSON(http.StatusOK, options)
}

func FinishLogin(c *gin.Context) {
	username := c.Query("username")
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	user, err := getUserByUsername(username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "user not found"})
		return
	}

	sessionData, ok := sessionStore[user.ID]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no active login session found"})
		return
	}

	// Parse login response
	credential, err := config.WebAuthn.FinishLogin(user, sessionData, c.Request)
	if err != nil {
		log.Printf("FinishLogin error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to verify login assertion"})
		return
	}

	// Update SignCount in the database to detect potential credential cloning
	var dbCred models.Credential
	if err := database.DB.Where("credential_id = ?", credential.ID).First(&dbCred).Error; err == nil {
		dbCred.SignCount = int64(credential.Authenticator.SignCount)
		database.DB.Save(&dbCred)
	}

	delete(sessionStore, user.ID)

	c.JSON(http.StatusOK, gin.H{
		"status":  "login success",
		"message": "User authenticated successfully",
		// You would typically generate and return a JWT or session cookie here
	})
}
