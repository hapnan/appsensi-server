package controllers

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"appsensi-server/config"
	"appsensi-server/models"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/google/uuid"
)

type memoryConformanceStore struct {
	mu          sync.Mutex
	usersByName map[string]*models.User
	usersByID   map[uuid.UUID]*models.User
	credsByID   map[string]*models.Credential
}

func newMemoryConformanceStore() *memoryConformanceStore {
	return &memoryConformanceStore{
		usersByName: make(map[string]*models.User),
		usersByID:   make(map[uuid.UUID]*models.User),
		credsByID:   make(map[string]*models.Credential),
	}
}

func (s *memoryConformanceStore) cloneUserLocked(u *models.User) *models.User {
	cp := *u
	if u.WebAuthnUser != nil {
		wu := *u.WebAuthnUser
		cp.WebAuthnUser = &wu
	}
	cp.Credentials = make([]models.Credential, len(u.Credentials))
	copy(cp.Credentials, u.Credentials)
	return &cp
}

func (s *memoryConformanceStore) GetOrCreateUser(username, displayName, rpid string) (*models.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.usersByName[username]
	if !ok {
		dn := displayName
		handle := make([]byte, 32)
		_, _ = rand.Read(handle)
		id := uuid.New()
		u = &models.User{
			ID:          id,
			Username:    username,
			Email:       username + "@conformance.test",
			DisplayName: &dn,
			WebAuthnUser: &models.WebAuthnUser{
				ID:     uuid.New(),
				RpID:   rpid,
				UserID: id,
				Handle: handle,
			},
		}
		s.usersByName[username] = u
		s.usersByID[id] = u
	} else {
		dn := displayName
		u.DisplayName = &dn
	}
	return s.cloneUserLocked(u), nil
}

func (s *memoryConformanceStore) GetExistingUser(username, rpid string) (*models.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.usersByName[username]
	if !ok || u.WebAuthnUser == nil || len(u.Credentials) == 0 {
		return nil, errors.New("user not found or has no credentials")
	}
	return s.cloneUserLocked(u), nil
}

func (s *memoryConformanceStore) GetUserByID(userID uuid.UUID, rpid string) (*models.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.usersByID[userID]
	if !ok || u.WebAuthnUser == nil {
		return nil, errors.New("user not found")
	}
	return s.cloneUserLocked(u), nil
}

func (s *memoryConformanceStore) CredentialExists(credentialID []byte) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.credsByID[string(credentialID)]
	return exists, nil
}

func (s *memoryConformanceStore) SaveCredential(cred *models.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := *cred
	if cp.ID == uuid.Nil {
		cp.ID = uuid.New()
	}
	s.credsByID[string(cp.CredentialID)] = &cp
	if u, ok := s.usersByID[cp.UserID]; ok {
		u.Credentials = append(u.Credentials, cp)
	}
	return nil
}

func (s *memoryConformanceStore) UpdateCredentialOnLogin(credentialID []byte, signCount int64, cloneWarning, verified, backupState bool, lastUsedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.credsByID[string(credentialID)]
	if !ok {
		return errors.New("credential not found")
	}
	c.SignCount = signCount
	c.CloneWarning = cloneWarning
	c.Verified = verified
	c.BackupState = backupState
	c.LastUsedAt = &lastUsedAt

	if u, ok := s.usersByID[c.UserID]; ok {
		for i := range u.Credentials {
			if bytes.Equal(u.Credentials[i].CredentialID, credentialID) {
				u.Credentials[i] = *c
			}
		}
	}
	return nil
}

func setupConformanceTestRouter(t *testing.T) (*gin.Engine, *memoryConformanceStore) {
	t.Helper()
	t.Setenv("FIDO_CONFORMANCE", "true")
	t.Setenv("FIDO_MDS_DIR", "../fido-conformance-mds")
	t.Setenv("FIDO_MDS3_ENABLED", "false")
	t.Setenv("PORT", "8080")
	t.Setenv("RP_ID", "localhost")

	config.InitConformanceWebAuthn()

	memStore := newMemoryConformanceStore()
	SetConformanceStore(memStore)
	t.Cleanup(func() {
		SetConformanceStore(nil)
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(config.CORSMiddleware())
	r.POST("/attestation/options", ConformanceAttestationOptions)
	r.POST("/attestation/result", ConformanceAttestationResult)
	r.POST("/assertion/options", ConformanceAssertionOptions)
	r.POST("/assertion/result", ConformanceAssertionResult)
	return r, memStore
}

func encodeCOSEES256PublicKey(t *testing.T, pub *ecdsa.PublicKey) []byte {
	t.Helper()
	xBytes := make([]byte, 32)
	yBytes := make([]byte, 32)
	pub.X.FillBytes(xBytes)
	pub.Y.FillBytes(yBytes)

	coseMap := map[int]any{
		1:  2,  // kty: EC2
		3:  -7, // alg: ES256
		-1: 1,  // crv: P-256
		-2: xBytes,
		-3: yBytes,
	}
	raw, err := webauthncbor.Marshal(coseMap)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func buildPackedSurrogateAttestation(t *testing.T, rpID string, signCount uint32, aaguid []byte, credID []byte, priv *ecdsa.PrivateKey, challenge string, origin string) (string, string) {
	t.Helper()

	clientData, err := json.Marshal(map[string]any{
		"type":        "webauthn.create",
		"challenge":   challenge,
		"origin":      origin,
		"crossOrigin": false,
	})
	if err != nil {
		t.Fatal(err)
	}

	cosePub := encodeCOSEES256PublicKey(t, &priv.PublicKey)

	rpHash := sha256.Sum256([]byte(rpID))
	var authData bytes.Buffer
	authData.Write(rpHash[:])
	// Flags: UP (0x01) | UV (0x04) | AT (0x40) = 0x45
	authData.WriteByte(0x45)
	var cntBuf [4]byte
	binary.BigEndian.PutUint32(cntBuf[:], signCount)
	authData.Write(cntBuf[:])

	// AttestedCredentialData: 16-byte AAGUID + 2-byte credID len + credID + COSE public key
	authData.Write(aaguid)
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(credID)))
	authData.Write(lenBuf[:])
	authData.Write(credID)
	authData.Write(cosePub)

	clientHash := sha256.Sum256(clientData)
	signedBytes := append(authData.Bytes(), clientHash[:]...)
	digest := sha256.Sum256(signedBytes)
	sigDER, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}

	attObj, err := webauthncbor.Marshal(map[string]any{
		"fmt":      "packed",
		"authData": authData.Bytes(),
		"attStmt": map[string]any{
			"alg": -7,
			"sig": sigDER,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return base64.RawURLEncoding.EncodeToString(attObj), base64.RawURLEncoding.EncodeToString(clientData)
}

func buildAssertionResponse(t *testing.T, rpID string, signCount uint32, priv *ecdsa.PrivateKey, challenge string, origin string) (string, string, string) {
	t.Helper()

	clientData, err := json.Marshal(map[string]any{
		"type":        "webauthn.get",
		"challenge":   challenge,
		"origin":      origin,
		"crossOrigin": false,
	})
	if err != nil {
		t.Fatal(err)
	}

	rpHash := sha256.Sum256([]byte(rpID))
	var authData bytes.Buffer
	authData.Write(rpHash[:])
	// Flags: UP (0x01) | UV (0x04) = 0x05
	authData.WriteByte(0x05)
	var cntBuf [4]byte
	binary.BigEndian.PutUint32(cntBuf[:], signCount)
	authData.Write(cntBuf[:])

	clientHash := sha256.Sum256(clientData)
	signedBytes := append(authData.Bytes(), clientHash[:]...)
	digest := sha256.Sum256(signedBytes)
	sigDER, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}

	return base64.RawURLEncoding.EncodeToString(authData.Bytes()),
		base64.RawURLEncoding.EncodeToString(clientData),
		base64.RawURLEncoding.EncodeToString(sigDER)
}

func postJSON(t *testing.T, r http.Handler, path string, body any, cookies []*http.Cookie) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8080")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode JSON response (%d): %v; body=%s", rec.Code, err, rec.Body.String())
	}
	return rec, resp
}

func TestConformanceOptionsValidationNegativeCases(t *testing.T) {
	r, _ := setupConformanceTestRouter(t)

	badAttestationReqs := []map[string]any{
		{"displayName": "Alice"},                                             // missing username
		{"username": "", "displayName": "Alice"},                             // empty username
		{"username": 123, "displayName": "Alice"},                            // non-string username
		{"username": "alice"},                                                // missing displayName
		{"username": "alice", "displayName": ""},                             // empty displayName
		{"username": "alice", "displayName": "Alice", "attestation": "fake"}, // invalid conveyance
		{
			"username":               "alice",
			"displayName":            "Alice",
			"authenticatorSelection": map[string]any{"authenticatorAttachment": "usb"},
		},
		{
			"username":               "alice",
			"displayName":            "Alice",
			"authenticatorSelection": map[string]any{"userVerification": "invalid"},
		},
		{
			"username":               "alice",
			"displayName":            "Alice",
			"authenticatorSelection": map[string]any{"residentKey": "invalid"},
		},
	}

	for i, tc := range badAttestationReqs {
		rec, resp := postJSON(t, r, "/attestation/options", tc, nil)
		if rec.Code != http.StatusBadRequest || resp["status"] != "failed" {
			t.Fatalf("case %d: expected 400 status=failed, got code=%d body=%v", i, rec.Code, resp)
		}
	}

	badAssertionReqs := []map[string]any{
		{},                                                            // missing username
		{"username": ""},                                              // empty username
		{"username": "unregistered-user"},                             // unregistered user
		{"username": "alice", "userVerification": "not-a-valid-enum"}, // invalid UV
	}

	for i, tc := range badAssertionReqs {
		rec, resp := postJSON(t, r, "/assertion/options", tc, nil)
		if rec.Code != http.StatusBadRequest || resp["status"] != "failed" {
			t.Fatalf("assertion case %d: expected 400 status=failed, got code=%d body=%v", i, rec.Code, resp)
		}
	}
}

func TestConformanceFullRegistrationAndAssertionCeremony(t *testing.T) {
	r, store := setupConformanceTestRouter(t)

	// Step 1: POST /attestation/options with custom extension & authenticatorSelection
	optRec, optResp := postJSON(t, r, "/attestation/options", map[string]any{
		"username":    "conformance_alice",
		"displayName": "Conformance Alice",
		"attestation": "direct",
		"authenticatorSelection": map[string]any{
			"authenticatorAttachment": "cross-platform",
			"requireResidentKey":      false,
			"userVerification":        "preferred",
		},
		"extensions": map[string]any{
			"example.extension": true,
		},
	}, nil)

	if optRec.Code != http.StatusOK || optResp["status"] != "ok" {
		t.Fatalf("expected 200 status=ok from /attestation/options, got %d %v", optRec.Code, optResp)
	}
	if _, nested := optResp["publicKey"]; nested {
		t.Fatal("expected PublicKeyCredentialCreationOptions to be flattened at top-level, not nested under publicKey")
	}
	challenge, ok := optResp["challenge"].(string)
	if !ok || challenge == "" {
		t.Fatalf("missing challenge in /attestation/options response: %v", optResp)
	}
	extMap, ok := optResp["extensions"].(map[string]any)
	if !ok || extMap["example.extension"] != true {
		t.Fatalf("expected echoed example.extension in options response, got %v", optResp["extensions"])
	}
	cookies := optRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected fido_conformance_session cookie on /attestation/options")
	}

	// Prepare virtual authenticator credential (Packed surrogate self-attestation, zero AAGUID)
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	credID := []byte("conformance-test-credential-0001")
	credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
	zeroAAGUID := make([]byte, 16)

	attObjB64, clientDataB64 := buildPackedSurrogateAttestation(
		t, "localhost", 5, zeroAAGUID, credID, privKey, challenge, "http://localhost:8080",
	)

	// Step 2a: Negative /attestation/result with padded base64url rawId ("=") must fail with 400 WITHOUT destroying session
	badRec, badResp := postJSON(t, r, "/attestation/result", map[string]any{
		"id":    credIDB64,
		"rawId": credIDB64 + "=",
		"type":  "public-key",
		"response": map[string]any{
			"attestationObject": attObjB64,
			"clientDataJSON":    clientDataB64,
		},
	}, cookies)
	if badRec.Code != http.StatusBadRequest || badResp["status"] != "failed" {
		t.Fatalf("expected padded rawId to fail 400, got %d %v", badRec.Code, badResp)
	}

	// Step 2b: Negative /attestation/result with unsolicited extension output must fail with 400 WITHOUT destroying session
	unsolRec, unsolResp := postJSON(t, r, "/attestation/result", map[string]any{
		"id":    credIDB64,
		"rawId": credIDB64,
		"type":  "public-key",
		"response": map[string]any{
			"attestationObject": attObjB64,
			"clientDataJSON":    clientDataB64,
		},
		"getClientExtensionResults": map[string]any{
			"unsolicited.extension": true,
		},
	}, cookies)
	if unsolRec.Code != http.StatusBadRequest || unsolResp["status"] != "failed" {
		t.Fatalf("expected unsolicited extension output to fail 400, got %d %v", unsolRec.Code, unsolResp)
	}

	// Step 2c: Positive /attestation/result on the SAME session using FIDO's getClientExtensionResults field
	regRec, regResp := postJSON(t, r, "/attestation/result", map[string]any{
		"id":    credIDB64,
		"rawId": credIDB64,
		"type":  "public-key",
		"response": map[string]any{
			"attestationObject": attObjB64,
			"clientDataJSON":    clientDataB64,
		},
		"getClientExtensionResults": map[string]any{
			"example.extension": true,
		},
	}, cookies)
	if regRec.Code != http.StatusOK || regResp["status"] != "ok" {
		t.Fatalf("expected 200 status=ok from /attestation/result, got %d %v", regRec.Code, regResp)
	}

	// Step 2d: Replaying /attestation/result with consumed session cookie must be rejected
	replayRec, _ := postJSON(t, r, "/attestation/result", map[string]any{
		"id":    credIDB64,
		"rawId": credIDB64,
		"type":  "public-key",
		"response": map[string]any{
			"attestationObject": attObjB64,
			"clientDataJSON":    clientDataB64,
		},
	}, cookies)
	if replayRec.Code != http.StatusBadRequest {
		t.Fatalf("expected session replay to fail 400, got %d", replayRec.Code)
	}

	// Step 2e: Duplicate credential ID registration under a second user must be rejected
	opt2Rec, opt2Resp := postJSON(t, r, "/attestation/options", map[string]any{
		"username":    "conformance_bob",
		"displayName": "Conformance Bob",
	}, nil)
	dupAttObj, dupClientData := buildPackedSurrogateAttestation(
		t, "localhost", 1, zeroAAGUID, credID, privKey, opt2Resp["challenge"].(string), "http://localhost:8080",
	)
	dupRec, dupResp := postJSON(t, r, "/attestation/result", map[string]any{
		"id":    credIDB64,
		"rawId": credIDB64,
		"type":  "public-key",
		"response": map[string]any{
			"attestationObject": dupAttObj,
			"clientDataJSON":    dupClientData,
		},
	}, opt2Rec.Result().Cookies())
	if dupRec.Code != http.StatusBadRequest || dupResp["status"] != "failed" {
		t.Fatalf("expected duplicate credential ID to fail 400, got %d %v", dupRec.Code, dupResp)
	}

	// Step 3: POST /assertion/options
	assertOptRec, assertOptResp := postJSON(t, r, "/assertion/options", map[string]any{
		"username":         "conformance_alice",
		"userVerification": "preferred",
		"extensions": map[string]any{
			"example.extension": true,
		},
	}, nil)
	if assertOptRec.Code != http.StatusOK || assertOptResp["status"] != "ok" {
		t.Fatalf("expected 200 status=ok from /assertion/options, got %d %v", assertOptRec.Code, assertOptResp)
	}
	assertChallenge := assertOptResp["challenge"].(string)
	assertCookies := assertOptRec.Result().Cookies()

	// Step 4a: Negative /assertion/result with non-increasing signCount (5 <= 5) -> CloneWarning -> must fail 400
	badAuthData, badAssertCD, badSig := buildAssertionResponse(
		t, "localhost", 5, privKey, assertChallenge, "http://localhost:8080",
	)
	cloneRec, cloneResp := postJSON(t, r, "/assertion/result", map[string]any{
		"id":    credIDB64,
		"rawId": credIDB64,
		"type":  "public-key",
		"response": map[string]any{
			"authenticatorData": badAuthData,
			"clientDataJSON":    badAssertCD,
			"signature":         badSig,
		},
	}, assertCookies)
	if cloneRec.Code != http.StatusBadRequest || cloneResp["status"] != "failed" {
		t.Fatalf("expected signCount regression (5 <= 5) to fail 400, got %d %v", cloneRec.Code, cloneResp)
	}

	// Step 4b: Positive /assertion/result on the SAME session with incremented signCount (6 > 5) must succeed 200 OK
	goodAuthData, goodAssertCD, goodSig := buildAssertionResponse(
		t, "localhost", 6, privKey, assertChallenge, "http://localhost:8080",
	)
	assertRec, assertResp := postJSON(t, r, "/assertion/result", map[string]any{
		"id":    credIDB64,
		"rawId": credIDB64,
		"type":  "public-key",
		"response": map[string]any{
			"authenticatorData": goodAuthData,
			"clientDataJSON":    goodAssertCD,
			"signature":         goodSig,
		},
		"getClientExtensionResults": map[string]any{
			"example.extension": true,
		},
	}, assertCookies)
	if assertRec.Code != http.StatusOK || assertResp["status"] != "ok" {
		t.Fatalf("expected 200 status=ok from /assertion/result, got %d %v", assertRec.Code, assertResp)
	}

	// Verify stored SignCount advanced to 6
	updatedUser, err := store.GetExistingUser("conformance_alice", "localhost")
	if err != nil {
		t.Fatal(err)
	}
	if updatedUser.Credentials[0].SignCount != 6 {
		t.Fatalf("expected stored SignCount=6, got %d", updatedUser.Credentials[0].SignCount)
	}
}
