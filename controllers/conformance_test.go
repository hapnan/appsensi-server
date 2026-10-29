package controllers

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
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

func TestConformanceFourRegressionCases(t *testing.T) {
	r, _ := setupConformanceTestRouter(t)

	// -------------------------------------------------------------------------
	// Case 1: F-10 (x5cFullChain in "packed" full attestation must be rejected)
	//         and P-3 (Self Batch Referencing single non-CA cert in x5c must succeed)
	// -------------------------------------------------------------------------
	t.Run("Case1_PackedX5CFullChainRejectedAndSelfBatchAccepted", func(t *testing.T) {
		selfBatchAAGUID := uuid.MustParse("5b65dac1-7af4-46e6-8a4f-8701fcc4f3b4")
		entry, err := config.ConformanceMDS.GetEntry(t.Context(), selfBatchAAGUID)
		if err != nil || entry == nil {
			t.Fatalf("failed to load self-batch metadata entry: %v", err)
		}
		batchCert := entry.MetadataStatement.AttestationRootCertificates[0]

		// P-3: Single-element x5c equal to self-referenced non-CA batch certificate must pass trust anchor check
		if err := config.ConformanceMDS.ValidateAttestationTrustAnchor(
			t.Context(),
			"packed",
			selfBatchAAGUID[:],
			map[string]any{"x5c": []any{batchCert.Raw}},
		); err != nil {
			t.Fatalf("expected P-3 self-batch referencing to pass, got: %v", err)
		}

		// F-10: Multi-element x5c containing root certificate in x5c[1:] must fail
		secpAAGUID := uuid.MustParse("326adcf0-0cef-46d0-9392-98d6c4a84a72")
		secpEntry, err := config.ConformanceMDS.GetEntry(t.Context(), secpAAGUID)
		if err != nil || secpEntry == nil {
			t.Fatalf("failed to load secp256r1 metadata entry: %v", err)
		}
		rootCert := secpEntry.MetadataStatement.AttestationRootCertificates[0]
		if err := config.ConformanceMDS.ValidateAttestationTrustAnchor(
			t.Context(),
			"packed",
			secpAAGUID[:],
			map[string]any{"x5c": []any{batchCert.Raw, rootCert.Raw}},
		); err == nil {
			t.Fatal("expected F-10 x5cFullChain (root cert in x5c) to be rejected, but it succeeded")
		}

		// End-to-end HTTP check for F-10 (packed full attestation with root cert appended to x5c)
		optRec, optResp := postJSON(t, r, "/attestation/options", map[string]any{
			"username":    "packed_f10_user",
			"displayName": "Packed F10 User",
			"attestation": "direct",
		}, nil)
		if optRec.Code != http.StatusOK {
			t.Fatalf("options failed: %d %v", optRec.Code, optResp)
		}
		challenge := optResp["challenge"].(string)
		cookies := optRec.Result().Cookies()

		batchPrivBytes, _ := hex.DecodeString("28857aaed6dd6efc6e64762f53f7c3da3fa34e45b55bb040612d4f611f49f834")
		batchPriv := &ecdsa.PrivateKey{
			PublicKey: *batchCert.PublicKey.(*ecdsa.PublicKey),
			D:         new(big.Int).SetBytes(batchPrivBytes),
		}
		userKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		cosePub := encodeCOSEES256PublicKey(t, &userKey.PublicKey)
		credID := []byte("packed-f10-cred-id-0000000000001")

		rpHash := sha256.Sum256([]byte("localhost"))
		var authData bytes.Buffer
		authData.Write(rpHash[:])
		authData.WriteByte(0x41) // UP | AT
		authData.Write([]byte{0, 0, 0, 1})
		authData.Write(secpAAGUID[:])
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(credID)))
		authData.Write(lenBuf[:])
		authData.Write(credID)
		authData.Write(cosePub)

		clientData, _ := json.Marshal(map[string]any{
			"type":      "webauthn.create",
			"challenge": challenge,
			"origin":    "http://localhost:8080",
		})
		clientHash := sha256.Sum256(clientData)
		signedBytes := append(authData.Bytes(), clientHash[:]...)
		digest := sha256.Sum256(signedBytes)
		sigDER, _ := ecdsa.SignASN1(rand.Reader, batchPriv, digest[:])

		attObjFullChain, _ := webauthncbor.Marshal(map[string]any{
			"fmt":      "packed",
			"authData": authData.Bytes(),
			"attStmt": map[string]any{
				"alg": -7,
				"sig": sigDER,
				"x5c": []any{batchCert.Raw, rootCert.Raw},
			},
		})
		credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
		f10Rec, f10Resp := postJSON(t, r, "/attestation/result", map[string]any{
			"id":    credIDB64,
			"rawId": credIDB64,
			"type":  "public-key",
			"response": map[string]any{
				"attestationObject": base64.RawURLEncoding.EncodeToString(attObjFullChain),
				"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			},
		}, cookies)
		if f10Rec.Code != http.StatusBadRequest || f10Resp["status"] != "failed" {
			t.Fatalf("expected F-10 HTTP /attestation/result to fail 400, got %d %v", f10Rec.Code, f10Resp)
		}

		// Without rootCert in x5c, the exact same attestation succeeds on the same session
		attObjValid, _ := webauthncbor.Marshal(map[string]any{
			"fmt":      "packed",
			"authData": authData.Bytes(),
			"attStmt": map[string]any{
				"alg": -7,
				"sig": sigDER,
				"x5c": []any{batchCert.Raw},
			},
		})
		okRec, okResp := postJSON(t, r, "/attestation/result", map[string]any{
			"id":    credIDB64,
			"rawId": credIDB64,
			"type":  "public-key",
			"response": map[string]any{
				"attestationObject": base64.RawURLEncoding.EncodeToString(attObjValid),
				"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			},
		}, cookies)
		if okRec.Code != http.StatusOK || okResp["status"] != "ok" {
			t.Fatalf("expected valid full packed attestation to succeed 200, got %d %v", okRec.Code, okResp)
		}
	})

	// -------------------------------------------------------------------------
	// Case 2: Resp-8 P-1 (Valid "fido-u2f" attestation with SHA1WithRSA batch cert)
	// -------------------------------------------------------------------------
	t.Run("Case2_FIDOU2FAttestationSucceeds", func(t *testing.T) {
		optRec, optResp := postJSON(t, r, "/attestation/options", map[string]any{
			"username":    "u2f_user",
			"displayName": "U2F User",
			"attestation": "direct",
		}, nil)
		if optRec.Code != http.StatusOK {
			t.Fatalf("options failed: %d %v", optRec.Code, optResp)
		}
		challenge := optResp["challenge"].(string)
		cookies := optRec.Result().Cookies()

		// Exact U2F batch certificate and private key from FIDO Conformance u2fAuthenticator.js
		u2fBatchCertB64 := "MIIEKzCCAhOgAwIBAgIBATANBgkqhkiG9w0BAQUFADCBoTEYMBYGA1UEAwwPRklETzIgVEVTVCBST09UMTEwLwYJKoZIhvcNAQkBFiJjb25mb3JtYW5jZS10b29sc0BmaWRvYWxsaWFuY2Uub3JnMRYwFAYDVQQKDA1GSURPIEFsbGlhbmNlMQwwCgYDVQQLDANDV0cxCzAJBgNVBAYTAlVTMQswCQYDVQQIDAJNWTESMBAGA1UEBwwJV2FrZWZpZWxkMB4XDTE4MDMxNjE0MzUyN1oXDTI4MDMxMzE0MzUyN1owgawxIzAhBgNVBAMMGkZJRE8yIEJBVENIIEtFWSBwcmltZTI1NnYxMTEwLwYJKoZIhvcNAQkBFiJjb25mb3JtYW5jZS10b29sc0BmaWRvYWxsaWFuY2Uub3JnMRYwFAYDVQQKDA1GSURPIEFsbGlhbmNlMQwwCgYDVQQLDANDV0cxCzAJBgNVBAYTAlVTMQswCQYDVQQIDAJNWTESMBAGA1UEBwwJV2FrZWZpZWxkMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAETzpeXqtsH7yul/bfZEmWdix773IAQCp2xvIw9lVvF6qZm1l/xL9Qiq+OnvDNAT9aub0nkUvwgEN4y8yxG4m1RqMsMCowCQYDVR0TBAIwADAdBgNVHQ4EFgQUVk33wPjGVbahH2xNGfO/QeL9AXkwDQYJKoZIhvcNAQEFBQADggIBAI+/jI31FB+8J2XxzBXMuI4Yg+vAtq07ABHJqnQpUmt8lpOzmvJ0COKcwtq/7bpsgSVBJ26zhnyWcm1q8V0ZbxUvN2kH8N7nteIGn+CJOJkHDII+IbiH4+TUQCJjuCB52duUWL0fGVw2R13J6V+K7U5r0OWBzmtmwwiRVTggVbjDpbx2oqGAwzupG3RmBFDX1M92s3tgywnLr+e6NZal5yZdS8VblJGjswDZbdY+Qobo2DCN6vxvn5TVkukAHiArjpBBpAmuQfKa52vqSCYRpTCm57fQUZ1c1n29OsvDw1x9ckyH8j/9Xgk0AG+MlQ9Rdg3hCb7LkSPvC/zYDeS2Cj/yFw6OWahnnIRwO6t4UtLuRAkLrjP1T7nk0zu1whwj7YEwtva45niWWh6rdyg/SZlfsph3o/MZN5DwKaSrUaEO6b+numELH5GWjjiPgfgPKkIof+D40xaKUFBpNJzorQkAZCJWuHvXRpBZWFVh/UhNlGhX0mhz2yFlBrujYa9BgvIkdJ8Keok6qfAn+r5EEFXcSI8vGY7OEF01QKXVpu8+FW0uSxtQ991AcFD6KjvR51l7e61visUgduhZRIq9bYzeCIxnK5Jhm3o/NJE2bOp2NmVwVe4kjuJX87wo3Ba41bXgwIpdiLWyWJhSHPmJI/1ibRTZ5XO92xbPPSnnkXrF"
		u2fBatchCertDER, err := base64.StdEncoding.DecodeString(u2fBatchCertB64)
		if err != nil {
			t.Fatal(err)
		}
		u2fBatchCert, err := x509.ParseCertificate(u2fBatchCertDER)
		if err != nil {
			t.Fatal(err)
		}
		batchPrivBytes, err := hex.DecodeString("28857aaed6dd6efc6e64762f53f7c3da3fa34e45b55bb040612d4f611f49f834")
		if err != nil {
			t.Fatal(err)
		}
		batchPriv := &ecdsa.PrivateKey{
			PublicKey: *u2fBatchCert.PublicKey.(*ecdsa.PublicKey),
			D:         new(big.Int).SetBytes(batchPrivBytes),
		}

		userKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		cosePub := encodeCOSEES256PublicKey(t, &userKey.PublicKey)
		credID := []byte("u2f-conformance-cred-id-00000001")
		zeroAAGUID := make([]byte, 16)

		rpHash := sha256.Sum256([]byte("localhost"))
		var authData bytes.Buffer
		authData.Write(rpHash[:])
		authData.WriteByte(0x41) // UP (0x01) | AT (0x40)
		authData.Write([]byte{0, 0, 0, 1})
		authData.Write(zeroAAGUID)
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(credID)))
		authData.Write(lenBuf[:])
		authData.Write(credID)
		authData.Write(cosePub)

		clientData, err := json.Marshal(map[string]any{
			"type":      "webauthn.create",
			"challenge": challenge,
			"origin":    "http://localhost:8080",
		})
		if err != nil {
			t.Fatal(err)
		}
		clientHash := sha256.Sum256(clientData)

		// U2F verificationData: 0x00 || rpIdHash || clientDataHash || credentialId || 0x04 || x || y
		var xBytes, yBytes [32]byte
		userKey.PublicKey.X.FillBytes(xBytes[:])
		userKey.PublicKey.Y.FillBytes(yBytes[:])
		var verifData bytes.Buffer
		verifData.WriteByte(0x00)
		verifData.Write(rpHash[:])
		verifData.Write(clientHash[:])
		verifData.Write(credID)
		verifData.WriteByte(0x04)
		verifData.Write(xBytes[:])
		verifData.Write(yBytes[:])

		u2fDigest := sha256.Sum256(verifData.Bytes())
		u2fSig, err := ecdsa.SignASN1(rand.Reader, batchPriv, u2fDigest[:])
		if err != nil {
			t.Fatal(err)
		}

		attObj, err := webauthncbor.Marshal(map[string]any{
			"fmt":      "fido-u2f",
			"authData": authData.Bytes(),
			"attStmt": map[string]any{
				"sig": u2fSig,
				"x5c": []any{u2fBatchCertDER},
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
		resRec, resResp := postJSON(t, r, "/attestation/result", map[string]any{
			"id":    credIDB64,
			"rawId": credIDB64,
			"type":  "public-key",
			"response": map[string]any{
				"attestationObject": base64.RawURLEncoding.EncodeToString(attObj),
				"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			},
		}, cookies)
		if resRec.Code != http.StatusOK || resResp["status"] != "ok" {
			t.Fatalf("expected 200 status=ok for fido-u2f attestation, got %d %v", resRec.Code, resResp)
		}
	})

	// -------------------------------------------------------------------------
	// Case 3: Resp-9 P-2 (Valid "tpm" attestation for SHA-1 / RS1 with SHA1WithRSA AIK cert)
	// -------------------------------------------------------------------------
	t.Run("Case3_TPMSHA1AttestationSucceeds", func(t *testing.T) {
		optRec, optResp := postJSON(t, r, "/attestation/options", map[string]any{
			"username":    "tpm_sha1_user",
			"displayName": "TPM SHA1 User",
			"attestation": "direct",
		}, nil)
		if optRec.Code != http.StatusOK {
			t.Fatalf("options failed: %d %v", optRec.Code, optResp)
		}
		challenge := optResp["challenge"].(string)
		cookies := optRec.Result().Cookies()

		tpmAAGUID := uuid.MustParse("f244b67e-5364-4fd5-9f90-c396227317db")

		// Exact tpmBatch intermediate cert & private key parameters from ctap2Authenticator.js
		tpmBatchCertB64 := "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"
		tpmBatchDER, err := base64.StdEncoding.DecodeString(tpmBatchCertB64)
		if err != nil {
			t.Fatal(err)
		}
		tpmBatchCert, err := x509.ParseCertificate(tpmBatchDER)
		if err != nil {
			t.Fatal(err)
		}
		dBytes, _ := base64.RawURLEncoding.DecodeString("mzJmZymPyoXfgSkj1yZW1_qAvQadFCS5CZZOSQ8-DNBeNMVUw9ZPVQeVy2Ih4wVeHvInZ7I0BGm1Id6msH5WulqRukVmXpS7S2zXvVEeCTDdOCXhG9W3Vt8X9-zS7DPqT4q79sakdBI_sXTtdSsMh0iyhjNAxC9doZElFejEG1xFX3kHVPqQNG_kzFuCy02p_LY2o6qQKbMsaTISxmD3EFgh6-xG5JFxPOu9Akgf5sW0KxIqdhsUKbTSKPPQBoRsfn7kqnNAZswhexKCvJHDpbNlD2zWSOuKx_IHIuvErT5UHkq-1FwMX3YGwBrSaL-eRXk872m4ejU67h_7Xj9S2Q")
		pBytes, _ := base64.RawURLEncoding.DecodeString("69LFfEo93snYQ4VezOjacIu0W2EN_Gic8lI6XYe-Xd-8FhmsUv4I1NFG5zweXv-Ns6-QbW15T3ZXb839U_91-FV-1x_F3344C9CSiZ5CzXPh-b4bfxHxClXbA5CoIL93oTeSSvX10F4Ut_4O5OkH7UzGNMDmE8WNOvm3czQFbuc")
		qBytes, _ := base64.RawURLEncoding.DecodeString("6ajuM3L7B1Fw03i7WN-ICXBzby1egMexRlmdUTFWqKT-YCGZpk7tww8R0ofz0mF2uZOdg5Cc9AQzQTAFqWCHsinjhQqE8lx6nSnt3eORrM8HxDVgWHFAtHhxu1Zwp-D2Z8aHnWbG_Aw7GSkTe-phpLImvp-idsmgxyj_wjslcZc")
		tpmBatchPriv := &rsa.PrivateKey{
			PublicKey: *tpmBatchCert.PublicKey.(*rsa.PublicKey),
			D:         new(big.Int).SetBytes(dBytes),
			Primes: []*big.Int{
				new(big.Int).SetBytes(pBytes),
				new(big.Int).SetBytes(qBytes),
			},
		}
		tpmBatchPriv.Precompute()

		userRSA, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		aikRSA, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}

		// Build AIK certificate signed with SHA1WithRSA (matching generateTPMAIK('SHA-1', ...) in conformance tool)
		sanRDN, err := asn1.Marshal(pkix.RDNSequence{
			{
				{Type: asn1.ObjectIdentifier{2, 23, 133, 2, 1}, Value: "id:4E544300"},
				{Type: asn1.ObjectIdentifier{2, 23, 133, 2, 2}, Value: "NPCT6xx"},
				{Type: asn1.ObjectIdentifier{2, 23, 133, 2, 3}, Value: "id:000D0001"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		sanValue, err := asn1.Marshal([]asn1.RawValue{{
			Class:      asn1.ClassContextSpecific,
			Tag:        4,
			IsCompound: true,
			Bytes:      sanRDN,
		}})
		if err != nil {
			t.Fatal(err)
		}
		ekuValue, err := asn1.Marshal([]asn1.ObjectIdentifier{{2, 23, 133, 8, 3}})
		if err != nil {
			t.Fatal(err)
		}
		bcValue, err := asn1.Marshal(struct {
			IsCA bool `asn1:"optional"`
		}{IsCA: false})
		if err != nil {
			t.Fatal(err)
		}

		aikTmpl := &x509.Certificate{
			SerialNumber:       big.NewInt(12345),
			NotBefore:          time.Now().Add(-time.Hour),
			NotAfter:           time.Now().Add(time.Hour),
			SignatureAlgorithm: x509.SHA1WithRSA,
			ExtraExtensions: []pkix.Extension{
				{Id: asn1.ObjectIdentifier{2, 5, 29, 17}, Critical: true, Value: sanValue},
				{Id: asn1.ObjectIdentifier{2, 5, 29, 37}, Critical: false, Value: ekuValue},
				{Id: asn1.ObjectIdentifier{2, 5, 29, 19}, Critical: true, Value: bcValue},
			},
		}
		aikDER, err := x509.CreateCertificate(rand.Reader, aikTmpl, tpmBatchCert, &aikRSA.PublicKey, tpmBatchPriv)
		if err != nil {
			t.Fatal(err)
		}

		// COSE RS1 (-65535) credential public key
		expBytes := []byte{0x01, 0x00, 0x01}
		coseRS1, err := webauthncbor.Marshal(map[int]any{
			1:  3,      // kty: RSA
			3:  -65535, // alg: RS1
			-1: userRSA.PublicKey.N.Bytes(),
			-2: expBytes,
		})
		if err != nil {
			t.Fatal(err)
		}

		credID := []byte("tpm-sha1-conformance-cred-000001")
		rpHash := sha256.Sum256([]byte("localhost"))
		var authData bytes.Buffer
		authData.Write(rpHash[:])
		authData.WriteByte(0x41) // UP (0x01) | AT (0x40)
		authData.Write([]byte{0, 0, 0, 1})
		authData.Write(tpmAAGUID[:])
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(credID)))
		authData.Write(lenBuf[:])
		authData.Write(credID)
		authData.Write(coseRS1)

		clientData, err := json.Marshal(map[string]any{
			"type":      "webauthn.create",
			"challenge": challenge,
			"origin":    "http://localhost:8080",
		})
		if err != nil {
			t.Fatal(err)
		}
		clientHash := sha256.Sum256(clientData)
		attToBeSigned := append(authData.Bytes(), clientHash[:]...)
		extraDataSHA1 := sha1.Sum(attToBeSigned)

		// Build pubArea & certInfo using the exact binary layout from FIDO Conformance ctap2Authenticator.js
		pubAreaPrefix, _ := hex.DecodeString("0001000B0006047200209dffcbf36c383ae699fb9868dc6dcb89d7153884be2803922c124158bfad22ae001000100800000000000100")
		pubAreaBytes := append(pubAreaPrefix, userRSA.PublicKey.N.Bytes()...)
		pubAreaDigest := sha256.Sum256(pubAreaBytes)

		var certInfoBuf bytes.Buffer
		headerAndQualifiedSigner, _ := hex.DecodeString("ff54434780170022000b11e68ed44db5372e14296e2f84acc78398751881921e30fc22a06f8b73c79453")
		certInfoBuf.Write(headerAndQualifiedSigner)
		_ = binary.Write(&certInfoBuf, binary.BigEndian, uint16(len(extraDataSHA1)))
		certInfoBuf.Write(extraDataSHA1[:])
		clockAndFw, _ := hex.DecodeString("000000014770174855697b4bc68560a501737ef98d345655fb")
		certInfoBuf.Write(clockAndFw)
		// name: uint16(34) || 0x000B (TPM_ALG_SHA256) || sha256(pubArea)
		_ = binary.Write(&certInfoBuf, binary.BigEndian, uint16(2+len(pubAreaDigest)))
		certInfoBuf.Write([]byte{0x00, 0x0B})
		certInfoBuf.Write(pubAreaDigest[:])
		// qualifiedName: uint16(34) || 0x000B || 32-byte digest
		_ = binary.Write(&certInfoBuf, binary.BigEndian, uint16(2+len(pubAreaDigest)))
		certInfoBuf.Write([]byte{0x00, 0x0B})
		certInfoBuf.Write(pubAreaDigest[:])

		certInfoBytes := certInfoBuf.Bytes()
		certInfoSHA1 := sha1.Sum(certInfoBytes)
		tpmSig, err := rsa.SignPKCS1v15(rand.Reader, aikRSA, crypto.SHA1, certInfoSHA1[:])
		if err != nil {
			t.Fatal(err)
		}

		attObj, err := webauthncbor.Marshal(map[string]any{
			"fmt":      "tpm",
			"authData": authData.Bytes(),
			"attStmt": map[string]any{
				"ver":      "2.0",
				"alg":      -65535,
				"sig":      tpmSig,
				"x5c":      []any{aikDER, tpmBatchDER},
				"certInfo": certInfoBytes,
				"pubArea":  pubAreaBytes,
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
		resRec, resResp := postJSON(t, r, "/attestation/result", map[string]any{
			"id":    credIDB64,
			"rawId": credIDB64,
			"type":  "public-key",
			"response": map[string]any{
				"attestationObject": base64.RawURLEncoding.EncodeToString(attObj),
				"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			},
		}, cookies)
		if resRec.Code != http.StatusOK || resResp["status"] != "ok" {
			t.Fatalf("expected 200 status=ok for tpm SHA-1 attestation, got %d %v", resRec.Code, resResp)
		}
	})

	// -------------------------------------------------------------------------
	// Case 4: Server-MDS3 P-1 (fido2_good dynamic AAGUID d7eaec5b-31cf-4aff-a45f-86a15c26d80f
	//         with stale template authenticatorGetInfo.aaguid 326adcf00cef46d0939298d6c4a84a72)
	// -------------------------------------------------------------------------
	t.Run("Case4_MDS3Fido2GoodMismatchedGetInfoAAGUIDSucceeds", func(t *testing.T) {
		secpAAGUID := uuid.MustParse("326adcf0-0cef-46d0-9392-98d6c4a84a72")
		secpEntry, err := config.ConformanceMDS.GetEntry(t.Context(), secpAAGUID)
		if err != nil || secpEntry == nil {
			t.Fatalf("failed to load template secp256r1 entry: %v", err)
		}
		selfBatchEntry, err := config.ConformanceMDS.GetEntry(t.Context(), uuid.MustParse("5b65dac1-7af4-46e6-8a4f-8701fcc4f3b4"))
		if err != nil || selfBatchEntry == nil {
			t.Fatalf("failed to load self-batch entry: %v", err)
		}
		batchCert := selfBatchEntry.MetadataStatement.AttestationRootCertificates[0]
		rootCert := secpEntry.MetadataStatement.AttestationRootCertificates[0]

		// Build a minimal MDS3 test server serving a fido2_good blob with dynamic AAGUID d7eaec5b-31cf-4aff-a45f-86a15c26d80f
		// while authenticatorGetInfo.aaguid still contains the template 326adcf00cef46d0939298d6c4a84a72.
		mdsRootKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		mdsRootTmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: "MDS3 Test Root"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(time.Hour),
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
			BasicConstraintsValid: true,
			IsCA:                  true,
		}
		mdsRootDER, err := x509.CreateCertificate(rand.Reader, mdsRootTmpl, mdsRootTmpl, &mdsRootKey.PublicKey, mdsRootKey)
		if err != nil {
			t.Fatal(err)
		}

		fido2GoodAAGUID := uuid.MustParse("d7eaec5b-31cf-4aff-a45f-86a15c26d80f")
		headerBytes, _ := json.Marshal(map[string]any{
			"alg": "ES256",
			"typ": "JWT",
			"x5c": []string{base64.StdEncoding.EncodeToString(mdsRootDER)},
		})
		payloadBytes, _ := json.Marshal(map[string]any{
			"legalHeader": "https://fidoalliance.org/metadata/metadata-statement-legal-header/",
			"no":          1,
			"nextUpdate":  "2099-01-01",
			"entries": []any{
				map[string]any{
					"aaguid": fido2GoodAAGUID.String(),
					"statusReports": []any{
						map[string]any{
							"status":        "FIDO_CERTIFIED",
							"effectiveDate": "2025-01-01",
						},
					},
					"timeOfLastStatusChange": "2025-01-01",
					"metadataStatement": map[string]any{
						"legalHeader":                  "https://fidoalliance.org/metadata/metadata-statement-legal-header/",
						"aaguid":                       fido2GoodAAGUID.String(),
						"description":                  "Virtual Secp256R1 FIDO2 Conformance Testing CTAP2 Authenticator (fido2_good)",
						"authenticatorVersion":         2,
						"protocolFamily":               "fido2",
						"schema":                       3,
						"upv":                          []any{map[string]any{"major": 1, "minor": 0}},
						"authenticationAlgorithms":     []any{"secp256r1_ecdsa_sha256_raw"},
						"publicKeyAlgAndEncodings":     []any{"cose"},
						"attestationTypes":             []any{"basic_full", "basic_surrogate"},
						"userVerificationDetails":      []any{[]any{map[string]any{"userVerificationMethod": "none"}}},
						"keyProtection":                []any{"hardware"},
						"matcherProtection":            []any{"on_chip"},
						"cryptoStrength":               128,
						"attachmentHint":               []any{"external"},
						"tcDisplay":                    []any{},
						"attestationRootCertificates":  []any{base64.StdEncoding.EncodeToString(rootCert.Raw)},
						"authenticatorGetInfo": map[string]any{
							"versions": []any{"U2F_V2", "FIDO_2_0"},
							"aaguid":   "326adcf00cef46d0939298d6c4a84a72",
							"algorithms": []any{
								map[string]any{"type": "public-key", "alg": -7},
							},
						},
					},
				},
			},
		})
		signingInput := base64.RawURLEncoding.EncodeToString(headerBytes) + "." + base64.RawURLEncoding.EncodeToString(payloadBytes)
		jwsDigest := sha256.Sum256([]byte(signingInput))
		jr, js, _ := ecdsa.Sign(rand.Reader, mdsRootKey, jwsDigest[:])
		var jwsSig [64]byte
		jr.FillBytes(jwsSig[:32])
		js.FillBytes(jwsSig[32:])
		blobJWS := signingInput + "." + base64.RawURLEncoding.EncodeToString(jwsSig[:])

		mux := http.NewServeMux()
		mux.HandleFunc("/pki/MDS3ROOT.crt", func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write(mdsRootDER)
		})
		mux.HandleFunc("/getEndpoints", func(w http.ResponseWriter, req *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"result": []string{"https://mds3.fido.tools/blobs/fido2_good"},
			})
		})
		mux.HandleFunc("/blobs/fido2_good", func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte(blobJWS))
		})

		mdsProv, err := config.NewConformanceMDSProvider("../fido-conformance-mds", config.MDS3Config{
			Enabled:         true,
			EndpointsURL:    "https://mds3.fido.tools/getEndpoints",
			ConformanceURL:  "http://localhost:8080",
			RootCertURL:     "https://mds3.fido.tools/pki/MDS3ROOT.crt",
			CustomTransport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				return rec.Result(), nil
			}),
		})
		if err != nil {
			t.Fatalf("NewConformanceMDSProvider failed: %v", err)
		}
		prevMDS := config.ConformanceMDS
		config.ConformanceMDS = mdsProv
		config.ConformanceWebAuthn.Config.MDS = mdsProv
		defer func() {
			config.ConformanceMDS = prevMDS
			config.ConformanceWebAuthn.Config.MDS = prevMDS
		}()

		optRec, optResp := postJSON(t, r, "/attestation/options", map[string]any{
			"username":    "mds_fido2_good_user",
			"displayName": "MDS FIDO2 Good User",
			"attestation": "direct",
		}, nil)
		if optRec.Code != http.StatusOK {
			t.Fatalf("options failed: %d %v", optRec.Code, optResp)
		}
		challenge := optResp["challenge"].(string)
		cookies := optRec.Result().Cookies()

		batchPrivBytes, _ := hex.DecodeString("28857aaed6dd6efc6e64762f53f7c3da3fa34e45b55bb040612d4f611f49f834")
		batchPriv := &ecdsa.PrivateKey{
			PublicKey: *batchCert.PublicKey.(*ecdsa.PublicKey),
			D:         new(big.Int).SetBytes(batchPrivBytes),
		}
		userKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		cosePub := encodeCOSEES256PublicKey(t, &userKey.PublicKey)
		credID := []byte("mds-fido2-good-cred-id-000000001")

		rpHash := sha256.Sum256([]byte("localhost"))
		var authData bytes.Buffer
		authData.Write(rpHash[:])
		authData.WriteByte(0x41) // UP | AT
		authData.Write([]byte{0, 0, 0, 1})
		authData.Write(fido2GoodAAGUID[:])
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(credID)))
		authData.Write(lenBuf[:])
		authData.Write(credID)
		authData.Write(cosePub)

		clientData, _ := json.Marshal(map[string]any{
			"type":      "webauthn.create",
			"challenge": challenge,
			"origin":    "http://localhost:8080",
		})
		clientHash := sha256.Sum256(clientData)
		signedBytes := append(authData.Bytes(), clientHash[:]...)
		digest := sha256.Sum256(signedBytes)
		sigDER, _ := ecdsa.SignASN1(rand.Reader, batchPriv, digest[:])

		attObj, _ := webauthncbor.Marshal(map[string]any{
			"fmt":      "packed",
			"authData": authData.Bytes(),
			"attStmt": map[string]any{
				"alg": -7,
				"sig": sigDER,
				"x5c": []any{batchCert.Raw},
			},
		})
		credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
		resRec, resResp := postJSON(t, r, "/attestation/result", map[string]any{
			"id":    credIDB64,
			"rawId": credIDB64,
			"type":  "public-key",
			"response": map[string]any{
				"attestationObject": base64.RawURLEncoding.EncodeToString(attObj),
				"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			},
		}, cookies)
		if resRec.Code != http.StatusOK || resResp["status"] != "ok" {
			t.Fatalf("expected 200 status=ok for fido2_good MDS3 attestation, got %d %v", resRec.Code, resResp)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
