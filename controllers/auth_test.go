package controllers

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
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
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/google/uuid"
)

type memoryAuthStore struct {
	mu                sync.Mutex
	studentsByID      map[string]string
	employeesByID     map[string]string
	lecturersByID     map[string]string
	usersByName       map[string]*models.User
	credsByID         map[string]*models.Credential
	failUpdateOnLogin bool
}

var _ AuthStore = (*memoryAuthStore)(nil)

func newMemoryAuthStore() *memoryAuthStore {
	return &memoryAuthStore{
		studentsByID:  make(map[string]string),
		employeesByID: make(map[string]string),
		lecturersByID: make(map[string]string),
		usersByName:   make(map[string]*models.User),
		credsByID:     make(map[string]*models.Credential),
	}
}

func (s *memoryAuthStore) cloneUserLocked(u *models.User) *models.User {
	cp := *u
	if u.WebAuthnUser != nil {
		wu := *u.WebAuthnUser
		cp.WebAuthnUser = &wu
	}
	cp.Credentials = make([]models.Credential, len(u.Credentials))
	copy(cp.Credentials, u.Credentials)
	return &cp
}

func (s *memoryAuthStore) GetOrCreateUserForRegistration(username, email, rpid string) (*models.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.usersByName[username]
	if !ok {
		var dn string
		userType := models.UserTypeStudent
		if name, ok := s.studentsByID[username]; ok {
			dn = name
			userType = models.UserTypeStudent
		} else if name, ok := s.employeesByID[username]; ok {
			dn = name
			userType = models.UserTypeEmployee
		} else if name, ok := s.lecturersByID[username]; ok {
			dn = name
			userType = models.UserTypeLecturer
		} else {
			return nil, errors.New("person not found")
		}
		handle := make([]byte, 32)
		_, _ = rand.Read(handle)
		id := uuid.New()
		u = &models.User{
			ID:          id,
			Username:    username,
			Email:       email,
			DisplayName: &dn,
			UserType:    userType,
			WebAuthnUser: &models.WebAuthnUser{
				ID:     uuid.New(),
				RpID:   rpid,
				UserID: id,
				Handle: handle,
			},
		}
		s.usersByName[username] = u
	}
	return s.cloneUserLocked(u), nil
}

func (s *memoryAuthStore) GetExistingUserForLogin(username, rpid string) (*models.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.usersByName[username]
	if !ok || u.WebAuthnUser == nil {
		return nil, errors.New("user not found")
	}
	return s.cloneUserLocked(u), nil
}

func (s *memoryAuthStore) SaveCredential(cred *models.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := *cred
	if cp.ID == uuid.Nil {
		cp.ID = uuid.New()
	}
	s.credsByID[string(cp.CredentialID)] = &cp
	for _, u := range s.usersByName {
		if u.ID == cp.UserID {
			u.Credentials = append(u.Credentials, cp)
		}
	}
	return nil
}

func (s *memoryAuthStore) UpdateCredentialOnLogin(credentialID []byte, signCount int64, cloneWarning bool, lastUsedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failUpdateOnLogin {
		return errors.New("simulated database write failure")
	}
	c, ok := s.credsByID[string(credentialID)]
	if !ok {
		return errors.New("credential not found")
	}
	c.SignCount = signCount
	c.CloneWarning = cloneWarning
	c.LastUsedAt = &lastUsedAt

	for _, u := range s.usersByName {
		if u.ID == c.UserID {
			for i := range u.Credentials {
				if bytes.Equal(u.Credentials[i].CredentialID, credentialID) {
					u.Credentials[i] = *c
				}
			}
		}
	}
	return nil
}

func setupAuthTestRouter(t *testing.T) (*gin.Engine, *memoryAuthStore, *memoryRegistrationStore) {
	t.Helper()
	t.Setenv("RP_ID", "localhost")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000")
	config.InitWebAuthn()

	aStore := newMemoryAuthStore()
	rStore := newMemoryRegistrationStore()
	SetAuthStore(aStore)
	SetRegistrationStore(rStore)
	t.Cleanup(func() {
		SetAuthStore(nil)
		SetRegistrationStore(nil)
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/register/begin", BeginRegistration)
	r.POST("/register/finish", FinishRegistration)
	r.POST("/login/begin", BeginLogin)
	r.POST("/login/finish", FinishLogin)
	return r, aStore, rStore
}

func TestAuth_EndToEndCeremonyAndUnifiedErrors(t *testing.T) {
	r, aStore, rStore := setupAuthTestRouter(t)

	stuUUID := uuid.New()
	aStore.studentsByID["STU-2026-01"] = "Dewi Lestari"
	rStore.studentByID["STU-2026-01"] = stuUUID

	// 1. Validation error returns unified APIErrorResponse with BAD_REQUEST code
	wBad := performPostJSON(r, "/register/begin", map[string]any{
		"username": "STU-2026-01",
		"email":    "not-an-email",
	})
	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", wBad.Code)
	}
	var apiErr APIErrorResponse
	if err := json.Unmarshal(wBad.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	if apiErr.Code != ErrCodeBadRequest || apiErr.Error == "" {
		t.Fatalf("expected BAD_REQUEST code and message, got %+v", apiErr)
	}

	// 2. BeginRegistration succeeds
	wRegBegin := performPostJSON(r, "/register/begin", map[string]any{
		"username": "STU-2026-01",
		"email":    "dewi@campus.edu",
	})
	if wRegBegin.Code != http.StatusOK {
		t.Fatalf("BeginRegistration = %d: %s", wRegBegin.Code, wRegBegin.Body.String())
	}
	var creation protocol.CredentialCreation
	if err := json.Unmarshal(wRegBegin.Body.Bytes(), &creation); err != nil {
		t.Fatalf("unmarshal CredentialCreation: %v", err)
	}
	regChallenge := creation.Response.Challenge.String()

	// 3. Ceremony isolation: pending registration session cannot be consumed by /login/finish
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	credID := []byte("auth-test-cred-001")
	credIDB64 := base64.RawURLEncoding.EncodeToString(credID)

	authDataB64, loginClientDataB64, sigB64 := buildAssertionResponse(t, "localhost", 1, priv, regChallenge, "http://localhost:3000")
	wWrongCeremony := performPostJSON(r, "/login/finish", map[string]any{
		"username": "STU-2026-01",
		"id":       credIDB64,
		"rawId":    credIDB64,
		"type":     "public-key",
		"response": map[string]any{
			"authenticatorData": authDataB64,
			"clientDataJSON":    loginClientDataB64,
			"signature":         sigB64,
		},
	})
	if wWrongCeremony.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on ceremony mismatch, got %d: %s", wWrongCeremony.Code, wWrongCeremony.Body.String())
	}

	// 4. FinishRegistration succeeds and links student automatically
	aaguid := make([]byte, 16)
	attObjB64, regClientDataB64 := buildPackedSurrogateAttestation(t, "localhost", 5, aaguid, credID, priv, regChallenge, "http://localhost:3000")
	wRegFinish := performPostJSON(r, "/register/finish", map[string]any{
		"username": "STU-2026-01",
		"email":    "dewi@campus.edu",
		"id":       credIDB64,
		"rawId":    credIDB64,
		"type":     "public-key",
		"response": map[string]any{
			"attestationObject": attObjB64,
			"clientDataJSON":    regClientDataB64,
			"transports":        []string{"hybrid"},
		},
	})
	if wRegFinish.Code != http.StatusOK {
		t.Fatalf("FinishRegistration = %d: %s", wRegFinish.Code, wRegFinish.Body.String())
	}
	var regStatus AuthStatusResponse
	if err := json.Unmarshal(wRegFinish.Body.Bytes(), &regStatus); err != nil {
		t.Fatalf("unmarshal AuthStatusResponse: %v", err)
	}
	if regStatus.Status != "registration success" {
		t.Fatalf("status = %q, want 'registration success'", regStatus.Status)
	}
	if _, linked := rStore.studentLink[stuUUID]; !linked {
		t.Fatal("expected student to be auto-linked after FinishRegistration")
	}

	// 5. BeginLogin + FinishLogin updates SignCount and propagates DB update errors cleanly
	wLoginBegin := performPostJSON(r, "/login/begin", map[string]any{
		"username": "STU-2026-01",
	})
	if wLoginBegin.Code != http.StatusOK {
		t.Fatalf("BeginLogin = %d: %s", wLoginBegin.Code, wLoginBegin.Body.String())
	}
	var assertion protocol.CredentialAssertion
	if err := json.Unmarshal(wLoginBegin.Body.Bytes(), &assertion); err != nil {
		t.Fatalf("unmarshal CredentialAssertion: %v", err)
	}
	loginChallenge := assertion.Response.Challenge.String()

	authDataB64, loginClientDataB64, sigB64 = buildAssertionResponse(t, "localhost", 6, priv, loginChallenge, "http://localhost:3000")
	aStore.failUpdateOnLogin = true
	wUpdateFail := performPostJSON(r, "/login/finish", map[string]any{
		"username": "STU-2026-01",
		"id":       credIDB64,
		"rawId":    credIDB64,
		"type":     "public-key",
		"response": map[string]any{
			"authenticatorData": authDataB64,
			"clientDataJSON":    loginClientDataB64,
			"signature":         sigB64,
		},
	})
	if wUpdateFail.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when UpdateCredentialOnLogin fails, got %d: %s", wUpdateFail.Code, wUpdateFail.Body.String())
	}

	// Retry login with working DB store -> 200 OK and SignCount updated to 7
	aStore.failUpdateOnLogin = false
	wLoginBegin2 := performPostJSON(r, "/login/begin", map[string]any{"username": "STU-2026-01"})
	var assertion2 protocol.CredentialAssertion
	_ = json.Unmarshal(wLoginBegin2.Body.Bytes(), &assertion2)
	authData2, clientData2, sig2 := buildAssertionResponse(t, "localhost", 7, priv, assertion2.Response.Challenge.String(), "http://localhost:3000")

	wLoginOK := performPostJSON(r, "/login/finish", map[string]any{
		"username": "STU-2026-01",
		"id":       credIDB64,
		"rawId":    credIDB64,
		"type":     "public-key",
		"response": map[string]any{
			"authenticatorData": authData2,
			"clientDataJSON":    clientData2,
			"signature":         sig2,
		},
	})
	if wLoginOK.Code != http.StatusOK {
		t.Fatalf("FinishLogin = %d: %s", wLoginOK.Code, wLoginOK.Body.String())
	}
	if gotCount := aStore.credsByID[string(credID)].SignCount; gotCount != 7 {
		t.Fatalf("expected updated SignCount = 7, got %d", gotCount)
	}
}

func TestRestoreBufferedRequestBody_NonByteSliceDoesNotPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/register/finish", nil)

	// Missing BodyBytesKey returns false without panicking
	if restoreBufferedRequestBody(c) {
		t.Fatal("expected false when BodyBytesKey is absent")
	}

	// Non-[]byte value stored under BodyBytesKey returns false without panicking
	c.Set(gin.BodyBytesKey, "unexpected-string-value")
	if restoreBufferedRequestBody(c) {
		t.Fatal("expected false when BodyBytesKey holds non-[]byte value")
	}

	// Valid []byte restores c.Request.Body cleanly
	c.Set(gin.BodyBytesKey, []byte(`{"ok":true}`))
	if !restoreBufferedRequestBody(c) {
		t.Fatal("expected true when BodyBytesKey holds []byte")
	}
}

type memoryDashboardAuthStore struct {
 mu sync.Mutex
 byUserID map[uuid.UUID]DashboardUserContext
 byUsername map[string]DashboardUserContext
}

func newMemoryDashboardAuthStore() *memoryDashboardAuthStore {
 return &memoryDashboardAuthStore{
 byUserID: make(map[uuid.UUID]DashboardUserContext),
 byUsername: make(map[string]DashboardUserContext),
 }
}

func (m *memoryDashboardAuthStore) put(ctx DashboardUserContext) {
 m.mu.Lock()
 defer m.mu.Unlock()
 if ctx.UserID != uuid.Nil { m.byUserID[ctx.UserID] = cloneDashboardUserContext(ctx) }
 if ctx.Username != "" { m.byUsername[ctx.Username] = cloneDashboardUserContext(ctx) }
}

func (m *memoryDashboardAuthStore) ResolveUserContextByUserID(userID uuid.UUID) (*DashboardUserContext, error) {
 m.mu.Lock()
 defer m.mu.Unlock()
 ctx, ok := m.byUserID[userID]
 if !ok { return nil, errors.New("not found") }
 cp := cloneDashboardUserContext(ctx)
 return &cp, nil
}

func (m *memoryDashboardAuthStore) ResolveUserContextByUsername(username string) (*DashboardUserContext, error) {
 m.mu.Lock()
 defer m.mu.Unlock()
 ctx, ok := m.byUsername[username]
 if !ok { return nil, errors.New("not found") }
 cp := cloneDashboardUserContext(ctx)
 return &cp, nil
}

func TestAuth_EmployeeAndLecturerCeremonyAndLoginContext(t *testing.T) {
 r, aStore, _ := setupAuthTestRouter(t)
 staffStore := newMemoryStaffRegistrationStore()
 dashStore := newMemoryDashboardAuthStore()
 SetStaffRegistrationStore(staffStore)
 SetDashboardAuthStore(dashStore)
 ClearPasskeySessions()
 t.Cleanup(func() {
 SetStaffRegistrationStore(nil)
 SetDashboardAuthStore(nil)
 ClearPasskeySessions()
 })
 r.GET("/dashboard/me", GetDashboardMe)
 fcsID := uuid.New()
 cases := []struct {
 username string
 email string
 displayName string
 userType models.UserType
 profileID uuid.UUID
 jobRole *models.EmployeeJobRole
 facultyID *uuid.UUID
 facultyCode *string
 facultyName *string
 }{
 {username: "EMP-FIN-001", email: "fin@campus.edu", displayName: "Rina Finance", userType: models.UserTypeEmployee, profileID: uuid.New(), jobRole: rolePtr(models.JobRoleFinance)},
 {username: "EMP-ACA-001", email: "aca@campus.edu", displayName: "Budi Academic", userType: models.UserTypeEmployee, profileID: uuid.New(), jobRole: rolePtr(models.JobRoleAcademicAffairs)},
 {username: "EMP-SA-FCS-001", email: "sa.fcs@campus.edu", displayName: "Siti Student Affairs", userType: models.UserTypeEmployee, profileID: uuid.New(), jobRole: rolePtr(models.JobRoleStudentAffairs), facultyID: &fcsID, facultyCode: strPtr("FCS"), facultyName: strPtr("Faculty of Computer Science")},
 {username: "LEC-FCS-001", email: "lec.fcs@campus.edu", displayName: "Dr. Hendra", userType: models.UserTypeLecturer, profileID: uuid.New(), facultyID: &fcsID, facultyCode: strPtr("FCS"), facultyName: strPtr("Faculty of Computer Science")},
 }
 for i, tc := range cases {
 if tc.userType == models.UserTypeEmployee {
 aStore.employeesByID[tc.username] = tc.displayName
 staffStore.empByBadge[tc.username] = tc.profileID
 } else {
 aStore.lecturersByID[tc.username] = tc.displayName
 staffStore.lecByBadge[tc.username] = tc.profileID
 }
 pID := tc.profileID
 dashStore.put(DashboardUserContext{Username: tc.username, Email: tc.email, DisplayName: tc.displayName, UserType: tc.userType, ProfileID: &pID, JobRole: tc.jobRole, FacultyID: tc.facultyID, FacultyCode: tc.facultyCode, FacultyName: tc.facultyName})
 wBegin := performPostJSON(r, "/register/begin", map[string]any{"username": tc.username, "email": tc.email})
 if wBegin.Code != http.StatusOK { t.Fatalf("[%s] BeginRegistration = %d: %s", tc.username, wBegin.Code, wBegin.Body.String()) }
 var creation protocol.CredentialCreation
 _ = json.Unmarshal(wBegin.Body.Bytes(), &creation)
 priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
 credID := []byte("staff-cred-00" + string(rune(49+i)))
 credIDB64 := base64.RawURLEncoding.EncodeToString(credID)
 attObjB64, regClientDataB64 := buildPackedSurrogateAttestation(t, "localhost", 1, make([]byte, 16), credID, priv, creation.Response.Challenge.String(), "http://localhost:3000")
 wFin := performPostJSON(r, "/register/finish", map[string]any{"username": tc.username, "email": tc.email, "id": credIDB64, "rawId": credIDB64, "type": "public-key", "response": map[string]any{"attestationObject": attObjB64, "clientDataJSON": regClientDataB64, "transports": []string{"hybrid"}}})
 if wFin.Code != http.StatusOK { t.Fatalf("[%s] FinishRegistration = %d: %s", tc.username, wFin.Code, wFin.Body.String()) }
 if tc.userType == models.UserTypeEmployee {
 if _, ok := staffStore.empLink[tc.profileID]; !ok { t.Fatalf("[%s] expected employee to be auto-linked", tc.username) }
 } else {
 if _, ok := staffStore.lecLink[tc.profileID]; !ok { t.Fatalf("[%s] expected lecturer to be auto-linked", tc.username) }
 }
 wLoginBegin := performPostJSON(r, "/login/begin", map[string]any{"username": tc.username})
 var assertion protocol.CredentialAssertion
 _ = json.Unmarshal(wLoginBegin.Body.Bytes(), &assertion)
 authData, clientData, sig := buildAssertionResponse(t, "localhost", 2, priv, assertion.Response.Challenge.String(), "http://localhost:3000")
 wLoginFin := performPostJSON(r, "/login/finish", map[string]any{"username": tc.username, "id": credIDB64, "rawId": credIDB64, "type": "public-key", "response": map[string]any{"authenticatorData": authData, "clientDataJSON": clientData, "signature": sig}})
 if wLoginFin.Code != http.StatusOK { t.Fatalf("[%s] FinishLogin = %d: %s", tc.username, wLoginFin.Code, wLoginFin.Body.String()) }
 var loginRes AuthStatusResponse
 if err := json.Unmarshal(wLoginFin.Body.Bytes(), &loginRes); err != nil { t.Fatalf("unmarshal loginRes: %v", err) }
 if loginRes.SessionToken == "" || loginRes.User == nil { t.Fatalf("[%s] expected SessionToken and User in login response, got %+v", tc.username, loginRes) }
 if loginRes.User.UserType != tc.userType || loginRes.User.ProfileID == nil || *loginRes.User.ProfileID != tc.profileID { t.Fatalf("[%s] unexpected User context: %+v", tc.username, loginRes.User) }
 reqMe := httptest.NewRequest(http.MethodGet, "/dashboard/me", nil)
 reqMe.Header.Set("Authorization", "Bearer "+loginRes.SessionToken)
 wMe := httptest.NewRecorder()
 r.ServeHTTP(wMe, reqMe)
 if wMe.Code != http.StatusOK { t.Fatalf("[%s] GET /dashboard/me = %d: %s", tc.username, wMe.Code, wMe.Body.String()) }
 }
}

func rolePtr(r models.EmployeeJobRole) *models.EmployeeJobRole { return &r }
