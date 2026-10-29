package controllers

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

const (
	conformanceSessionCookie = "fido_conformance_session"
	conformanceSessionTTL    = 5 * time.Minute
)

// ConformanceStore abstracts user and credential persistence for FIDO Conformance endpoints,
// backed by PostgreSQL (GORM) in production/conformance runs and overridable in unit tests.
type ConformanceStore interface {
	GetOrCreateUser(username, displayName, rpid string) (*models.User, error)
	GetExistingUser(username, rpid string) (*models.User, error)
	GetUserByID(userID uuid.UUID, rpid string) (*models.User, error)
	CredentialExists(credentialID []byte) (bool, error)
	SaveCredential(cred *models.Credential) error
	UpdateCredentialOnLogin(credentialID []byte, signCount int64, cloneWarning, verified, backupState bool, lastUsedAt time.Time) error
}

type gormConformanceStore struct{}

func (gormConformanceStore) GetOrCreateUser(username, displayName, rpid string) (*models.User, error) {
	var user models.User
	err := database.DB.
		Preload("WebAuthnUser", "rp_id = ?", rpid).
		Preload("Credentials").
		Where("username = ?", username).
		First(&user).Error

	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}

		emailHash := sha256.Sum256([]byte(username))
		dn := displayName
		user = models.User{
			Username:    username,
			Email:       fmt.Sprintf("%x@conformance.appsensi.local", emailHash),
			DisplayName: &dn,
		}
		if err := database.DB.Create(&user).Error; err != nil {
			return nil, err
		}
	} else if user.DisplayName == nil || *user.DisplayName != displayName {
		dn := displayName
		if err := database.DB.Model(&user).Update("display_name", dn).Error; err != nil {
			return nil, err
		}
		user.DisplayName = &dn
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

func (gormConformanceStore) GetExistingUser(username, rpid string) (*models.User, error) {
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
		return nil, errors.New("user has no WebAuthn handle for this RP ID")
	}
	if len(user.Credentials) == 0 {
		return nil, errors.New("user has no registered credentials")
	}
	return &user, nil
}

func (gormConformanceStore) GetUserByID(userID uuid.UUID, rpid string) (*models.User, error) {
	var user models.User
	err := database.DB.
		Preload("WebAuthnUser", "rp_id = ?", rpid).
		Preload("Credentials").
		Where("id = ?", userID).
		First(&user).Error
	if err != nil {
		return nil, err
	}
	if user.WebAuthnUser == nil {
		return nil, errors.New("user has no WebAuthn handle for this RP ID")
	}
	return &user, nil
}

func (gormConformanceStore) CredentialExists(credentialID []byte) (bool, error) {
	var count int64
	if err := database.DB.Model(&models.Credential{}).Where("credential_id = ?", credentialID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (gormConformanceStore) SaveCredential(cred *models.Credential) error {
	return database.DB.Create(cred).Error
}

func (gormConformanceStore) UpdateCredentialOnLogin(credentialID []byte, signCount int64, cloneWarning, verified, backupState bool, lastUsedAt time.Time) error {
	return database.DB.Model(&models.Credential{}).
		Where("credential_id = ?", credentialID).
		Updates(map[string]any{
			"sign_count":    signCount,
			"clone_warning": cloneWarning,
			"verified":      verified,
			"backup_state":  backupState,
			"last_used_at":  &lastUsedAt,
		}).Error
}

var (
	conformanceStoreMu sync.RWMutex
	conformanceStore   ConformanceStore = gormConformanceStore{}
)

// SetConformanceStore overrides the persistence store (used by unit tests).
func SetConformanceStore(store ConformanceStore) {
	conformanceStoreMu.Lock()
	defer conformanceStoreMu.Unlock()
	if store == nil {
		conformanceStore = gormConformanceStore{}
		return
	}
	conformanceStore = store
}

func activeConformanceStore() ConformanceStore {
	conformanceStoreMu.RLock()
	defer conformanceStoreMu.RUnlock()
	return conformanceStore
}

type conformanceSession struct {
	ID          string
	UserID      uuid.UUID
	Ceremony    protocol.CeremonyType
	SessionData webauthn.SessionData
	CreatedAt   time.Time
}

var (
	conformanceSessions   = make(map[string]*conformanceSession)
	conformanceSessionsMu sync.RWMutex
)

func saveConformanceSession(userID uuid.UUID, ceremony protocol.CeremonyType, data webauthn.SessionData) (string, error) {
	rawID := make([]byte, 24)
	if _, err := rand.Read(rawID); err != nil {
		return "", err
	}
	sessID := hex.EncodeToString(rawID)
	now := time.Now()

	conformanceSessionsMu.Lock()
	defer conformanceSessionsMu.Unlock()

	for k, v := range conformanceSessions {
		if now.Sub(v.CreatedAt) > conformanceSessionTTL {
			delete(conformanceSessions, k)
		}
	}

	conformanceSessions[sessID] = &conformanceSession{
		ID:          sessID,
		UserID:      userID,
		Ceremony:    ceremony,
		SessionData: data,
		CreatedAt:   now,
	}
	return sessID, nil
}

func resolveConformanceSession(c *gin.Context, ceremony protocol.CeremonyType, challenge string) (*conformanceSession, error) {
	now := time.Now()

	conformanceSessionsMu.Lock()
	defer conformanceSessionsMu.Unlock()

	for k, v := range conformanceSessions {
		if now.Sub(v.CreatedAt) > conformanceSessionTTL {
			delete(conformanceSessions, k)
		}
	}

	if cookieID, err := c.Cookie(conformanceSessionCookie); err == nil && cookieID != "" {
		sess, ok := conformanceSessions[cookieID]
		if !ok {
			return nil, errors.New("unknown or expired conformance session")
		}
		if sess.Ceremony != ceremony {
			return nil, errors.New("conformance session ceremony mismatch")
		}
		cp := *sess
		return &cp, nil
	}

	// Only fall back to challenge lookup if no Cookie header was sent at all.
	if strings.TrimSpace(c.GetHeader("Cookie")) == "" && challenge != "" {
		for _, sess := range conformanceSessions {
			if sess.Ceremony == ceremony && sess.SessionData.Challenge == challenge {
				cp := *sess
				return &cp, nil
			}
		}
	}

	return nil, errors.New("no active conformance session found")
}

func deleteConformanceSession(sessID string) {
	conformanceSessionsMu.Lock()
	defer conformanceSessionsMu.Unlock()
	delete(conformanceSessions, sessID)
}

func conformanceFail(c *gin.Context, msg string) {
	log.Printf("Conformance failure [%s]: %s", c.Request.URL.Path, msg)
	c.JSON(http.StatusBadRequest, gin.H{
		"status":       "failed",
		"errorMessage": msg,
	})
}

func decodeStrictJSONMap(r io.Reader) (map[string]any, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("request body must be a JSON object")
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("invalid JSON object: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("request body contains trailing JSON data")
	}
	if m == nil {
		return nil, errors.New("request body must be a non-null JSON object")
	}
	return m, nil
}

func requireNonEmptyStringField(m map[string]any, key string) (string, error) {
	val, exists := m[key]
	if !exists || val == nil {
		return "", fmt.Errorf("missing required field %q", key)
	}
	str, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("field %q must be a string", key)
	}
	if str == "" {
		return "", fmt.Errorf("field %q must not be empty", key)
	}
	return str, nil
}

func requireUnpaddedBase64URLField(m map[string]any, key string) (string, error) {
	str, err := requireNonEmptyStringField(m, key)
	if err != nil {
		return "", err
	}
	if strings.Contains(str, "=") {
		return "", fmt.Errorf("field %q must be unpadded base64url", key)
	}
	return str, nil
}

func normalizeResultExtensions(m map[string]any) error {
	for _, alias := range []string{"getClientExtensionResults", "extensions"} {
		rawVal, exists := m[alias]
		if !exists {
			continue
		}
		delete(m, alias)
		if rawVal == nil {
			continue
		}
		extMap, ok := rawVal.(map[string]any)
		if !ok {
			return fmt.Errorf("field %q must be a JSON object", alias)
		}
		if _, hasCanonical := m["clientExtensionResults"]; !hasCanonical {
			m["clientExtensionResults"] = extMap
		}
	}
	if rawCanonical, exists := m["clientExtensionResults"]; exists && rawCanonical != nil {
		if _, ok := rawCanonical.(map[string]any); !ok {
			return errors.New(`field "clientExtensionResults" must be a JSON object`)
		}
	}
	return nil
}

func flattenOptionsWithStatus(options any) (map[string]any, error) {
	raw, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	out["status"] = "ok"
	out["errorMessage"] = ""
	return out, nil
}

// ConformanceAttestationOptions handles POST /attestation/options per FIDO2 Server Conformance API.
func ConformanceAttestationOptions(c *gin.Context) {
	if config.ConformanceWebAuthn == nil {
		conformanceFail(c, "conformance WebAuthn is not initialized")
		return
	}

	reqMap, err := decodeStrictJSONMap(c.Request.Body)
	if err != nil {
		conformanceFail(c, err.Error())
		return
	}

	username, err := requireNonEmptyStringField(reqMap, "username")
	if err != nil {
		conformanceFail(c, err.Error())
		return
	}
	displayName, err := requireNonEmptyStringField(reqMap, "displayName")
	if err != nil {
		conformanceFail(c, err.Error())
		return
	}

	conveyance := protocol.PreferNoAttestation
	if rawAtt, exists := reqMap["attestation"]; exists {
		attStr, ok := rawAtt.(string)
		if !ok {
			conformanceFail(c, `field "attestation" must be a string`)
			return
		}
		switch protocol.ConveyancePreference(attStr) {
		case protocol.PreferNoAttestation,
			protocol.PreferIndirectAttestation,
			protocol.PreferDirectAttestation,
			protocol.PreferEnterpriseAttestation:
			conveyance = protocol.ConveyancePreference(attStr)
		default:
			conformanceFail(c, fmt.Sprintf("unsupported attestation conveyance preference %q", attStr))
			return
		}
	}

	var authSel protocol.AuthenticatorSelection
	hasAuthSel := false
	if rawSel, exists := reqMap["authenticatorSelection"]; exists {
		selMap, ok := rawSel.(map[string]any)
		if !ok || selMap == nil {
			conformanceFail(c, `field "authenticatorSelection" must be a JSON object`)
			return
		}
		hasAuthSel = true
		authSel.UserVerification = protocol.VerificationPreferred

		if rawAttach, hasAttach := selMap["authenticatorAttachment"]; hasAttach && rawAttach != nil {
			attachStr, ok := rawAttach.(string)
			if !ok {
				conformanceFail(c, `authenticatorSelection.authenticatorAttachment must be a string`)
				return
			}
			switch protocol.AuthenticatorAttachment(attachStr) {
			case protocol.Platform, protocol.CrossPlatform:
				authSel.AuthenticatorAttachment = protocol.AuthenticatorAttachment(attachStr)
			default:
				conformanceFail(c, fmt.Sprintf("invalid authenticatorAttachment %q", attachStr))
				return
			}
		}

		if rawRRK, hasRRK := selMap["requireResidentKey"]; hasRRK && rawRRK != nil {
			rrkBool, ok := rawRRK.(bool)
			if !ok {
				conformanceFail(c, `authenticatorSelection.requireResidentKey must be a boolean`)
				return
			}
			authSel.RequireResidentKey = &rrkBool
			if rrkBool && authSel.ResidentKey == "" {
				authSel.ResidentKey = protocol.ResidentKeyRequirementRequired
			}
		}

		if rawRK, hasRK := selMap["residentKey"]; hasRK && rawRK != nil {
			rkStr, ok := rawRK.(string)
			if !ok {
				conformanceFail(c, `authenticatorSelection.residentKey must be a string`)
				return
			}
			switch protocol.ResidentKeyRequirement(rkStr) {
			case protocol.ResidentKeyRequirementDiscouraged,
				protocol.ResidentKeyRequirementPreferred,
				protocol.ResidentKeyRequirementRequired:
				authSel.ResidentKey = protocol.ResidentKeyRequirement(rkStr)
				if authSel.ResidentKey == protocol.ResidentKeyRequirementRequired && authSel.RequireResidentKey == nil {
					t := true
					authSel.RequireResidentKey = &t
				}
			default:
				conformanceFail(c, fmt.Sprintf("invalid residentKey requirement %q", rkStr))
				return
			}
		}

		if rawUV, hasUV := selMap["userVerification"]; hasUV && rawUV != nil {
			uvStr, ok := rawUV.(string)
			if !ok {
				conformanceFail(c, `authenticatorSelection.userVerification must be a string`)
				return
			}
			switch protocol.UserVerificationRequirement(uvStr) {
			case protocol.VerificationRequired,
				protocol.VerificationPreferred,
				protocol.VerificationDiscouraged:
				authSel.UserVerification = protocol.UserVerificationRequirement(uvStr)
			default:
				conformanceFail(c, fmt.Sprintf("invalid userVerification requirement %q", uvStr))
				return
			}
		}
	}

	var regOpts []webauthn.RegistrationOption
	regOpts = append(regOpts,
		webauthn.WithCredentialParameters(config.ConformanceCredentialParameters()),
		webauthn.WithConveyancePreference(conveyance),
	)
	if hasAuthSel {
		regOpts = append(regOpts, webauthn.WithAuthenticatorSelection(authSel))
	}

	if rawExt, exists := reqMap["extensions"]; exists && rawExt != nil {
		extMap, ok := rawExt.(map[string]any)
		if !ok {
			conformanceFail(c, `field "extensions" must be a JSON object`)
			return
		}
		parsedExts, err := protocol.ParseAuthenticationExtensions(extMap)
		if err != nil {
			conformanceFail(c, fmt.Sprintf("invalid extensions: %v", err))
			return
		}
		regOpts = append(regOpts, webauthn.WithExtensions(webauthn.WithExtensionInputs(parsedExts)))
	}

	rpid := config.ConformanceWebAuthn.Config.GetRPID()
	user, err := activeConformanceStore().GetOrCreateUser(username, displayName, rpid)
	if err != nil {
		conformanceFail(c, fmt.Sprintf("failed to load or create user: %v", err))
		return
	}

	existingCreds := user.WebAuthnCredentials()
	if len(existingCreds) > 0 {
		excludeList := make([]protocol.CredentialDescriptor, len(existingCreds))
		for i := range existingCreds {
			excludeList[i] = existingCreds[i].Descriptor()
		}
		regOpts = append(regOpts, webauthn.WithExclusions(excludeList))
	}

	creation, sessionData, err := config.ConformanceWebAuthn.BeginRegistration(user, regOpts...)
	if err != nil {
		conformanceFail(c, fmt.Sprintf("failed to generate registration options: %v", err))
		return
	}

	sessID, err := saveConformanceSession(user.ID, protocol.CreateCeremony, *sessionData)
	if err != nil {
		conformanceFail(c, "failed to create conformance session")
		return
	}

	respBody, err := flattenOptionsWithStatus(creation.Response)
	if err != nil {
		conformanceFail(c, "failed to encode registration options")
		return
	}

	c.SetCookie(conformanceSessionCookie, sessID, int(conformanceSessionTTL.Seconds()), "/", "", false, true)
	c.JSON(http.StatusOK, respBody)
}

// ConformanceAttestationResult handles POST /attestation/result per FIDO2 Server Conformance API.
func ConformanceAttestationResult(c *gin.Context) {
	if config.ConformanceWebAuthn == nil {
		conformanceFail(c, "conformance WebAuthn is not initialized")
		return
	}

	reqMap, err := decodeStrictJSONMap(c.Request.Body)
	if err != nil {
		conformanceFail(c, err.Error())
		return
	}

	if _, err := requireUnpaddedBase64URLField(reqMap, "id"); err != nil {
		conformanceFail(c, err.Error())
		return
	}
	if _, err := requireUnpaddedBase64URLField(reqMap, "rawId"); err != nil {
		conformanceFail(c, err.Error())
		return
	}
	if _, err := requireNonEmptyStringField(reqMap, "type"); err != nil {
		conformanceFail(c, err.Error())
		return
	}

	rawResp, hasResp := reqMap["response"]
	if !hasResp || rawResp == nil {
		conformanceFail(c, `missing required field "response"`)
		return
	}
	respMap, ok := rawResp.(map[string]any)
	if !ok {
		conformanceFail(c, `field "response" must be a JSON object`)
		return
	}
	if _, err := requireUnpaddedBase64URLField(respMap, "attestationObject"); err != nil {
		conformanceFail(c, err.Error())
		return
	}
	if _, err := requireUnpaddedBase64URLField(respMap, "clientDataJSON"); err != nil {
		conformanceFail(c, err.Error())
		return
	}

	if err := normalizeResultExtensions(reqMap); err != nil {
		conformanceFail(c, err.Error())
		return
	}

	normBytes, err := json.Marshal(reqMap)
	if err != nil {
		conformanceFail(c, "failed to normalize attestation payload")
		return
	}

	parsedResponse, err := protocol.ParseCredentialCreationResponseBytes(normBytes)
	if err != nil {
		conformanceFail(c, fmt.Sprintf("invalid attestation response: %v", err))
		return
	}

	sess, err := resolveConformanceSession(c, protocol.CreateCeremony, parsedResponse.Response.CollectedClientData.Challenge)
	if err != nil {
		conformanceFail(c, err.Error())
		return
	}

	store := activeConformanceStore()
	rpid := config.ConformanceWebAuthn.Config.GetRPID()
	user, err := store.GetUserByID(sess.UserID, rpid)
	if err != nil {
		conformanceFail(c, fmt.Sprintf("session user not found: %v", err))
		return
	}

	if config.ConformanceMDS != nil {
		if err := config.ConformanceMDS.ValidateZeroAAGUIDAttestationCert(
			c.Request.Context(),
			parsedResponse.Response.AttestationObject.AuthData.AttData.AAGUID,
			parsedResponse.Response.AttestationObject.AttStatement,
		); err != nil {
			conformanceFail(c, err.Error())
			return
		}
	}

	cred, err := config.ConformanceWebAuthn.CreateCredential(user, sess.SessionData, parsedResponse)
	if err != nil {
		conformanceFail(c, fmt.Sprintf("attestation verification failed: %v", err))
		return
	}

	exists, err := store.CredentialExists(cred.ID)
	if err != nil {
		conformanceFail(c, "failed to verify credential uniqueness")
		return
	}
	if exists {
		conformanceFail(c, "credential ID is already registered")
		return
	}

	var flags uint32
	if cred.Flags.UserPresent {
		flags |= 1
	}
	if cred.Flags.UserVerified {
		flags |= 2
	}
	if cred.Flags.BackupEligible {
		flags |= 4
	}
	if cred.Flags.BackupState {
		flags |= 8
	}

	transports := make([]string, len(cred.Transport))
	for i, t := range cred.Transport {
		transports[i] = string(t)
	}

	aaguid := cred.Authenticator.AAGUID
	if aaguid == nil {
		aaguid = []byte{}
	}
	attFormat := cred.AttestationFormat
	if attFormat == "" {
		attFormat = cred.AttestationType
	}

	newCred := models.Credential{
		UserID:            user.ID,
		CredentialID:      cred.ID,
		PublicKey:         cred.PublicKey,
		AttestationType:   cred.AttestationType,
		AttestationFormat: attFormat,
		Attestation:       cred.Attestation.Object,
		Transport:         strings.Join(transports, ","),
		Flags:             []byte{byte(flags)},
		Attachment:        string(cred.Authenticator.Attachment),
		SignCount:         int64(cred.Authenticator.SignCount),
		CloneWarning:      cred.Authenticator.CloneWarning,
		AAGUID:            aaguid,
		Presence:          cred.Flags.UserPresent,
		Verified:          cred.Flags.UserVerified,
		BackupEligible:    cred.Flags.BackupEligible,
		BackupState:       cred.Flags.BackupState,
	}

	if err := store.SaveCredential(&newCred); err != nil {
		conformanceFail(c, fmt.Sprintf("failed to store credential: %v", err))
		return
	}

	deleteConformanceSession(sess.ID)
	c.JSON(http.StatusOK, gin.H{
		"status":       "ok",
		"errorMessage": "",
	})
}

// ConformanceAssertionOptions handles POST /assertion/options per FIDO2 Server Conformance API.
func ConformanceAssertionOptions(c *gin.Context) {
	if config.ConformanceWebAuthn == nil {
		conformanceFail(c, "conformance WebAuthn is not initialized")
		return
	}

	reqMap, err := decodeStrictJSONMap(c.Request.Body)
	if err != nil {
		conformanceFail(c, err.Error())
		return
	}

	username, err := requireNonEmptyStringField(reqMap, "username")
	if err != nil {
		conformanceFail(c, err.Error())
		return
	}

	uv := protocol.VerificationPreferred
	if rawUV, exists := reqMap["userVerification"]; exists {
		uvStr, ok := rawUV.(string)
		if !ok {
			conformanceFail(c, `field "userVerification" must be a string`)
			return
		}
		switch protocol.UserVerificationRequirement(uvStr) {
		case protocol.VerificationRequired,
			protocol.VerificationPreferred,
			protocol.VerificationDiscouraged:
			uv = protocol.UserVerificationRequirement(uvStr)
		default:
			conformanceFail(c, fmt.Sprintf("invalid userVerification requirement %q", uvStr))
			return
		}
	}

	var loginOpts []webauthn.LoginOption
	loginOpts = append(loginOpts, webauthn.WithUserVerification(uv))

	if rawExt, exists := reqMap["extensions"]; exists && rawExt != nil {
		extMap, ok := rawExt.(map[string]any)
		if !ok {
			conformanceFail(c, `field "extensions" must be a JSON object`)
			return
		}
		parsedExts, err := protocol.ParseAuthenticationExtensions(extMap)
		if err != nil {
			conformanceFail(c, fmt.Sprintf("invalid extensions: %v", err))
			return
		}
		loginOpts = append(loginOpts, webauthn.WithAssertionExtensions(webauthn.WithExtensionInputs(parsedExts)))
	}

	rpid := config.ConformanceWebAuthn.Config.GetRPID()
	user, err := activeConformanceStore().GetExistingUser(username, rpid)
	if err != nil {
		conformanceFail(c, fmt.Sprintf("registered user not found: %v", err))
		return
	}

	assertion, sessionData, err := config.ConformanceWebAuthn.BeginLogin(user, loginOpts...)
	if err != nil {
		conformanceFail(c, fmt.Sprintf("failed to generate assertion options: %v", err))
		return
	}

	sessID, err := saveConformanceSession(user.ID, protocol.AssertCeremony, *sessionData)
	if err != nil {
		conformanceFail(c, "failed to create conformance session")
		return
	}

	respBody, err := flattenOptionsWithStatus(assertion.Response)
	if err != nil {
		conformanceFail(c, "failed to encode assertion options")
		return
	}

	c.SetCookie(conformanceSessionCookie, sessID, int(conformanceSessionTTL.Seconds()), "/", "", false, true)
	c.JSON(http.StatusOK, respBody)
}

// ConformanceAssertionResult handles POST /assertion/result per FIDO2 Server Conformance API.
func ConformanceAssertionResult(c *gin.Context) {
	if config.ConformanceWebAuthn == nil {
		conformanceFail(c, "conformance WebAuthn is not initialized")
		return
	}

	reqMap, err := decodeStrictJSONMap(c.Request.Body)
	if err != nil {
		conformanceFail(c, err.Error())
		return
	}

	if _, err := requireUnpaddedBase64URLField(reqMap, "id"); err != nil {
		conformanceFail(c, err.Error())
		return
	}
	if _, err := requireUnpaddedBase64URLField(reqMap, "rawId"); err != nil {
		conformanceFail(c, err.Error())
		return
	}
	if _, err := requireNonEmptyStringField(reqMap, "type"); err != nil {
		conformanceFail(c, err.Error())
		return
	}

	rawResp, hasResp := reqMap["response"]
	if !hasResp || rawResp == nil {
		conformanceFail(c, `missing required field "response"`)
		return
	}
	respMap, ok := rawResp.(map[string]any)
	if !ok {
		conformanceFail(c, `field "response" must be a JSON object`)
		return
	}
	if _, err := requireUnpaddedBase64URLField(respMap, "authenticatorData"); err != nil {
		conformanceFail(c, err.Error())
		return
	}
	if _, err := requireUnpaddedBase64URLField(respMap, "clientDataJSON"); err != nil {
		conformanceFail(c, err.Error())
		return
	}
	if _, err := requireUnpaddedBase64URLField(respMap, "signature"); err != nil {
		conformanceFail(c, err.Error())
		return
	}
	if rawUH, hasUH := respMap["userHandle"]; hasUH && rawUH != nil {
		uhStr, ok := rawUH.(string)
		if !ok {
			conformanceFail(c, `field "userHandle" must be a string or null`)
			return
		}
		if strings.Contains(uhStr, "=") {
			conformanceFail(c, `field "userHandle" must be unpadded base64url`)
			return
		}
	}

	if err := normalizeResultExtensions(reqMap); err != nil {
		conformanceFail(c, err.Error())
		return
	}

	normBytes, err := json.Marshal(reqMap)
	if err != nil {
		conformanceFail(c, "failed to normalize assertion payload")
		return
	}

	parsedResponse, err := protocol.ParseCredentialRequestResponseBytes(normBytes)
	if err != nil {
		conformanceFail(c, fmt.Sprintf("invalid assertion response: %v", err))
		return
	}

	sess, err := resolveConformanceSession(c, protocol.AssertCeremony, parsedResponse.Response.CollectedClientData.Challenge)
	if err != nil {
		conformanceFail(c, err.Error())
		return
	}

	store := activeConformanceStore()
	rpid := config.ConformanceWebAuthn.Config.GetRPID()
	user, err := store.GetUserByID(sess.UserID, rpid)
	if err != nil {
		conformanceFail(c, fmt.Sprintf("session user not found: %v", err))
		return
	}

	credential, err := config.ConformanceWebAuthn.ValidateLogin(user, sess.SessionData, parsedResponse)
	if err != nil {
		conformanceFail(c, fmt.Sprintf("assertion verification failed: %v", err))
		return
	}

	if credential.Authenticator.CloneWarning {
		conformanceFail(c, "authenticator signature counter did not increase (possible cloned credential)")
		return
	}

	if err := store.UpdateCredentialOnLogin(
		credential.ID,
		int64(credential.Authenticator.SignCount),
		credential.Authenticator.CloneWarning,
		credential.Flags.UserVerified,
		credential.Flags.BackupState,
		time.Now(),
	); err != nil {
		conformanceFail(c, fmt.Sprintf("failed to update credential state: %v", err))
		return
	}

	deleteConformanceSession(sess.ID)
	c.JSON(http.StatusOK, gin.H{
		"status":       "ok",
		"errorMessage": "",
	})
}
