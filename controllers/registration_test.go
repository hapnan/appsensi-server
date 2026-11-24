package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"appsensi-server/models"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

type memoryRegistrationStore struct {
	mu          sync.Mutex
	joinRows    []registrationJoinRow
	usersByName map[string]*models.User
	credsByUser map[uuid.UUID]int
	studentLink map[uuid.UUID]uuid.UUID
	studentByID map[string]uuid.UUID
}

func newMemoryRegistrationStore() *memoryRegistrationStore {
	return &memoryRegistrationStore{
		usersByName: make(map[string]*models.User),
		credsByUser: make(map[uuid.UUID]int),
		studentLink: make(map[uuid.UUID]uuid.UUID),
		studentByID: make(map[string]uuid.UUID),
	}
}

func (m *memoryRegistrationStore) ListStudentsWithRegistration() ([]registrationJoinRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.joinRows), nil
}

func (m *memoryRegistrationStore) FindUserByUsername(username string) (*models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.usersByName[username]
	if !ok {
		return nil, nil
	}
	cp := *u
	return &cp, nil
}

func (m *memoryRegistrationStore) UpdateStudentUserID(studentUUID, userUUID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.studentLink[studentUUID] = userUUID
	return nil
}

func (m *memoryRegistrationStore) LinkStudentByUsernameIfUnlinked(username string, userUUID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stuUUID, ok := m.studentByID[username]; ok {
		if _, alreadyLinked := m.studentLink[stuUUID]; !alreadyLinked {
			m.studentLink[stuUUID] = userUUID
		}
	}
	return nil
}

func (m *memoryRegistrationStore) UserHasCredentials(userUUID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.credsByUser[userUUID] > 0, nil
}

func (m *memoryRegistrationStore) DeleteUser(userUUID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, u := range m.usersByName {
		if u.ID == userUUID {
			delete(m.usersByName, name)
		}
	}
	return nil
}

func setupRegistrationTestRouter(t *testing.T, store RegistrationStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	SetRegistrationStore(store)
	t.Cleanup(func() {
		SetRegistrationStore(nil)
	})

	r := gin.New()
	reg := r.Group("/registration")
	{
		reg.GET("/list", ListRegistrationStudents)
		reg.POST("/link-student", LinkStudentUser)
		reg.POST("/cleanup-incomplete", CleanupIncompleteUser)
	}
	return r
}

func TestListRegistrationStudents(t *testing.T) {
	store := newMemoryRegistrationStore()
	r := setupRegistrationTestRouter(t, store)

	// Empty state returns []
	wEmpty := httptest.NewRecorder()
	r.ServeHTTP(wEmpty, httptest.NewRequest(http.MethodGet, "/registration/list", nil))
	if wEmpty.Code != http.StatusOK || strings.TrimSpace(wEmpty.Body.String()) != "[]" {
		t.Fatalf("empty list = %d %q, want 200 []", wEmpty.Code, wEmpty.Body.String())
	}

	stu1 := uuid.New()
	stu2 := uuid.New()
	stu3 := uuid.New()
	user2 := uuid.New()
	user3 := uuid.New()
	credA := uuid.New()
	credB := uuid.New()
	created := time.Date(2026, 11, 4, 8, 30, 0, 0, time.UTC)

	store.joinRows = []registrationJoinRow{
		{ID: stu1, Name: "Unlinked Student", Username: "STU-001", Department: strPtr("Computer Science")},
		{ID: stu2, Name: "Incomplete User", Username: "STU-002", UserID: &user2, Email: strPtr("stu2@campus.edu"), CreatedAt: &created, CredentialID: nil},
		{ID: stu3, Name: "Registered Student", Username: "STU-003", UserID: &user3, Email: strPtr("stu3@campus.edu"), CreatedAt: &created, CredentialID: nil},
		{ID: stu3, Name: "Registered Student", Username: "STU-003", UserID: &user3, Email: strPtr("stu3@campus.edu"), CreatedAt: &created, CredentialID: &credA},
		{ID: stu3, Name: "Registered Student", Username: "STU-003", UserID: &user3, Email: strPtr("stu3@campus.edu"), CreatedAt: &created, CredentialID: &credB},
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/registration/list", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got []RegistrationStudentRow
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if got[0].Username != "STU-001" || got[0].IsRegistered || got[0].UserID != nil {
		t.Errorf("row[0] = %+v", got[0])
	}
	if got[1].Username != "STU-002" || got[1].IsRegistered || got[1].UserID == nil || *got[1].UserID != user2 {
		t.Errorf("row[1] = %+v", got[1])
	}
	if got[2].Username != "STU-003" || !got[2].IsRegistered || got[2].Email == nil || *got[2].Email != "stu3@campus.edu" {
		t.Errorf("row[2] = %+v", got[2])
	}
}

func TestLinkStudentUserAndAutoLinkHelper(t *testing.T) {
	store := newMemoryRegistrationStore()
	r := setupRegistrationTestRouter(t, store)

	stuID := uuid.New()
	userID := uuid.New()
	created := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

	store.studentByID["STU-FCS-001"] = stuID
	store.usersByName["STU-FCS-001"] = &models.User{
		ID:        userID,
		Username:  "STU-FCS-001",
		Email:     "andi@campus.edu",
		CreatedAt: created,
	}

	// 1. Validation errors (400)
	if w := performPostJSON(r, "/registration/link-student", map[string]any{"studentId": "not-uuid", "username": "STU-FCS-001"}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad studentId = %d, want 400", w.Code)
	}
	if w := performPostJSON(r, "/registration/link-student", map[string]any{"studentId": stuID.String(), "username": ""}); w.Code != http.StatusBadRequest {
		t.Fatalf("empty username = %d, want 400", w.Code)
	}

	// 2. Missing user (404)
	wMissing := performPostJSON(r, "/registration/link-student", map[string]any{
		"studentId": stuID.String(),
		"username":  "STU-MISSING",
	})
	if wMissing.Code != http.StatusNotFound || !strings.Contains(wMissing.Body.String(), `Registered user \"STU-MISSING\" was not found.`) {
		t.Fatalf("missing user = %d %s", wMissing.Code, wMissing.Body.String())
	}

	// 3. Auto-link helper (called by FinishRegistration) links unlinked student
	if err := linkStudentAfterRegistration("STU-FCS-001", userID); err != nil {
		t.Fatalf("linkStudentAfterRegistration error: %v", err)
	}
	if store.studentLink[stuID] != userID {
		t.Fatalf("expected auto-linked userID %s, got %s", userID, store.studentLink[stuID])
	}

	// 4. Subsequent POST /registration/link-student is safely idempotent
	wOK := performPostJSON(r, "/registration/link-student", map[string]any{
		"studentId": stuID.String(),
		"username":  "STU-FCS-001",
	})
	if wOK.Code != http.StatusOK {
		t.Fatalf("link-student status = %d, want 200: %s", wOK.Code, wOK.Body.String())
	}
	var linkRes LinkStudentUserResponse
	if err := json.Unmarshal(wOK.Body.Bytes(), &linkRes); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if linkRes.UserID != userID || linkRes.Email != "andi@campus.edu" {
		t.Errorf("unexpected LinkStudentUserResponse: %+v", linkRes)
	}
}

func TestCleanupIncompleteUser(t *testing.T) {
	store := newMemoryRegistrationStore()
	r := setupRegistrationTestRouter(t, store)

	verifiedUserID := uuid.New()
	incompleteUserID := uuid.New()

	store.usersByName["STU-VERIFIED"] = &models.User{ID: verifiedUserID, Username: "STU-VERIFIED"}
	store.credsByUser[verifiedUserID] = 1

	store.usersByName["STU-INCOMPLETE"] = &models.User{ID: incompleteUserID, Username: "STU-INCOMPLETE"}
	saveSession(incompleteUserID, webauthn.SessionData{Challenge: "pending-challenge"})
	t.Cleanup(func() { deleteSession(incompleteUserID) })

	// 1. Empty username -> 400
	if w := performPostJSON(r, "/registration/cleanup-incomplete", map[string]any{"username": ""}); w.Code != http.StatusBadRequest {
		t.Fatalf("empty username = %d, want 400", w.Code)
	}

	// 2. Non-existent user -> cleaned: false
	wUnknown := performPostJSON(r, "/registration/cleanup-incomplete", map[string]any{"username": "STU-NONE"})
	if wUnknown.Code != http.StatusOK || !strings.Contains(wUnknown.Body.String(), `"cleaned":false`) {
		t.Fatalf("unknown user = %d %s", wUnknown.Code, wUnknown.Body.String())
	}

	// 3. Verified user with credentials -> cleaned: false (kept in store)
	wVerified := performPostJSON(r, "/registration/cleanup-incomplete", map[string]any{"username": "STU-VERIFIED"})
	if wVerified.Code != http.StatusOK || !strings.Contains(wVerified.Body.String(), `"cleaned":false`) {
		t.Fatalf("verified user = %d %s", wVerified.Code, wVerified.Body.String())
	}
	if _, stillExists := store.usersByName["STU-VERIFIED"]; !stillExists {
		t.Fatal("STU-VERIFIED should not have been deleted")
	}

	// 4. Incomplete user with 0 credentials -> cleaned: true, removed from store AND WebAuthn sessionStore
	wIncomplete := performPostJSON(r, "/registration/cleanup-incomplete", map[string]any{"username": "STU-INCOMPLETE"})
	if wIncomplete.Code != http.StatusOK || !strings.Contains(wIncomplete.Body.String(), `"cleaned":true`) {
		t.Fatalf("incomplete user = %d %s", wIncomplete.Code, wIncomplete.Body.String())
	}
	if _, exists := store.usersByName["STU-INCOMPLETE"]; exists {
		t.Fatal("STU-INCOMPLETE should have been deleted")
	}
	if _, hasSession := getSession(incompleteUserID); hasSession {
		t.Fatal("pending WebAuthn session for incomplete user should have been cleared")
	}
}

type memoryStaffRegistrationStore struct {
	mu          sync.Mutex
	empRows     []employeeRegistrationJoinRow
	lecRows     []lecturerRegistrationJoinRow
	usersByName map[string]*models.User
	empLink     map[uuid.UUID]uuid.UUID
	empByBadge  map[string]uuid.UUID
	lecLink     map[uuid.UUID]uuid.UUID
	lecByBadge  map[string]uuid.UUID
}

func newMemoryStaffRegistrationStore() *memoryStaffRegistrationStore {
	return &memoryStaffRegistrationStore{
		usersByName: make(map[string]*models.User),
		empLink:     make(map[uuid.UUID]uuid.UUID),
		empByBadge:  make(map[string]uuid.UUID),
		lecLink:     make(map[uuid.UUID]uuid.UUID),
		lecByBadge:  make(map[string]uuid.UUID),
	}
}

func (m *memoryStaffRegistrationStore) ListEmployeesWithRegistration() ([]employeeRegistrationJoinRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.empRows), nil
}

func (m *memoryStaffRegistrationStore) UpdateEmployeeUserID(employeeUUID, userUUID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.empLink[employeeUUID] = userUUID
	return nil
}

func (m *memoryStaffRegistrationStore) LinkEmployeeByUsernameIfUnlinked(username string, userUUID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if empUUID, ok := m.empByBadge[username]; ok {
		if _, alreadyLinked := m.empLink[empUUID]; !alreadyLinked {
			m.empLink[empUUID] = userUUID
		}
	}
	return nil
}

func (m *memoryStaffRegistrationStore) ListLecturersWithRegistration() ([]lecturerRegistrationJoinRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.lecRows), nil
}

func (m *memoryStaffRegistrationStore) UpdateLecturerUserID(lecturerUUID, userUUID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lecLink[lecturerUUID] = userUUID
	return nil
}

func (m *memoryStaffRegistrationStore) LinkLecturerByUsernameIfUnlinked(username string, userUUID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lecUUID, ok := m.lecByBadge[username]; ok {
		if _, alreadyLinked := m.lecLink[lecUUID]; !alreadyLinked {
			m.lecLink[lecUUID] = userUUID
		}
	}
	return nil
}

func (m *memoryStaffRegistrationStore) FindUserByUsername(username string) (*models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.usersByName[username]
	if !ok {
		return nil, nil
	}
	cp := *u
	return &cp, nil
}

func setupStaffRegistrationTestRouter(t *testing.T, staffStore StaffRegistrationStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	SetStaffRegistrationStore(staffStore)
	t.Cleanup(func() { SetStaffRegistrationStore(nil) })
	r := gin.New()
	reg := r.Group("/registration")
	{
		reg.GET("/employees", ListRegistrationEmployees)
		reg.POST("/link-employee", LinkEmployeeUser)
		reg.GET("/lecturers", ListRegistrationLecturers)
		reg.POST("/link-lecturer", LinkLecturerUser)
	}
	return r
}

func TestListRegistrationEmployeesAndLink(t *testing.T) {
	store := newMemoryStaffRegistrationStore()
	r := setupStaffRegistrationTestRouter(t, store)
	wEmpty := httptest.NewRecorder()
	r.ServeHTTP(wEmpty, httptest.NewRequest(http.MethodGet, "/registration/employees", nil))
	if wEmpty.Code != http.StatusOK || strings.TrimSpace(wEmpty.Body.String()) != "[]" {
		t.Fatalf("empty employees = %d %q, want 200 []", wEmpty.Code, wEmpty.Body.String())
	}
	emp1 := uuid.New()
	emp2 := uuid.New()
	user2 := uuid.New()
	credA := uuid.New()
	facID := uuid.New()
	created := time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)
	store.empRows = []employeeRegistrationJoinRow{
		{ID: emp1, Name: "Finance Staff", Username: "EMP-FIN-001", Department: strPtr("Finance"), JobRole: models.JobRoleFinance},
		{ID: emp2, Name: "Student Affairs FCS", Username: "EMP-SA-FCS-001", Department: strPtr("Student Affairs"), JobRole: models.JobRoleStudentAffairs, FacultyID: &facID, FacultyCode: strPtr("FCS"), FacultyName: strPtr("Faculty of Computer Science"), UserID: &user2, Email: strPtr("sa.fcs@campus.edu"), CreatedAt: &created, CredentialID: nil},
		{ID: emp2, Name: "Student Affairs FCS", Username: "EMP-SA-FCS-001", Department: strPtr("Student Affairs"), JobRole: models.JobRoleStudentAffairs, FacultyID: &facID, FacultyCode: strPtr("FCS"), FacultyName: strPtr("Faculty of Computer Science"), UserID: &user2, Email: strPtr("sa.fcs@campus.edu"), CreatedAt: &created, CredentialID: &credA},
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/registration/employees", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got []RegistrationEmployeeRow
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Username != "EMP-FIN-001" || got[0].IsRegistered || got[0].JobRole != models.JobRoleFinance {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Username != "EMP-SA-FCS-001" || !got[1].IsRegistered || got[1].FacultyCode == nil || *got[1].FacultyCode != "FCS" {
		t.Errorf("got[1] = %+v", got[1])
	}
	store.empByBadge["EMP-FIN-001"] = emp1
	finUserID := uuid.New()
	store.usersByName["EMP-FIN-001"] = &models.User{ID: finUserID, Username: "EMP-FIN-001", Email: "fin@campus.edu", CreatedAt: created, UserType: models.UserTypeEmployee}
	if wBad := performPostJSON(r, "/registration/link-employee", map[string]any{"employeeId": "bad-uuid", "username": "EMP-FIN-001"}); wBad.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", wBad.Code)
	}
	if wMiss := performPostJSON(r, "/registration/link-employee", map[string]any{"employeeId": emp1.String(), "username": "EMP-MISSING"}); wMiss.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", wMiss.Code)
	}
	if err := linkEmployeeAfterRegistration("EMP-FIN-001", finUserID); err != nil {
		t.Fatalf("linkEmployeeAfterRegistration: %v", err)
	}
	if store.empLink[emp1] != finUserID {
		t.Fatalf("expected auto-linked employee userID %s, got %s", finUserID, store.empLink[emp1])
	}
	wLink := performPostJSON(r, "/registration/link-employee", map[string]any{"employeeId": emp1.String(), "username": "EMP-FIN-001"})
	if wLink.Code != http.StatusOK {
		t.Fatalf("link-employee = %d: %s", wLink.Code, wLink.Body.String())
	}
}

func TestListRegistrationLecturersAndLink(t *testing.T) {
	store := newMemoryStaffRegistrationStore()
	r := setupStaffRegistrationTestRouter(t, store)
	wEmpty := httptest.NewRecorder()
	r.ServeHTTP(wEmpty, httptest.NewRequest(http.MethodGet, "/registration/lecturers", nil))
	if wEmpty.Code != http.StatusOK || strings.TrimSpace(wEmpty.Body.String()) != "[]" {
		t.Fatalf("empty lecturers = %d %q, want 200 []", wEmpty.Code, wEmpty.Body.String())
	}
	lec1 := uuid.New()
	lec2 := uuid.New()
	user2 := uuid.New()
	credA := uuid.New()
	facID := uuid.New()
	created := time.Date(2026, 11, 5, 8, 30, 0, 0, time.UTC)
	store.lecRows = []lecturerRegistrationJoinRow{
		{ID: lec1, Name: "Dr. Hendra", Username: "LEC-FCS-001", Department: strPtr("Computer Science"), FacultyID: &facID, FacultyCode: strPtr("FCS"), FacultyName: strPtr("Faculty of Computer Science")},
		{ID: lec2, Name: "Prof. Sari", Username: "LEC-ENG-001", Department: strPtr("Civil Engineering"), UserID: &user2, Email: strPtr("sari@campus.edu"), CreatedAt: &created, CredentialID: &credA},
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/registration/lecturers", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got []RegistrationLecturerRow
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Username != "LEC-FCS-001" || got[0].IsRegistered || got[0].FacultyCode == nil || *got[0].FacultyCode != "FCS" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Username != "LEC-ENG-001" || !got[1].IsRegistered {
		t.Errorf("got[1] = %+v", got[1])
	}
	store.lecByBadge["LEC-FCS-001"] = lec1
	lecUserID := uuid.New()
	store.usersByName["LEC-FCS-001"] = &models.User{ID: lecUserID, Username: "LEC-FCS-001", Email: "hendra@campus.edu", CreatedAt: created, UserType: models.UserTypeLecturer}
	if wBad := performPostJSON(r, "/registration/link-lecturer", map[string]any{"lecturerId": "bad-uuid", "username": "LEC-FCS-001"}); wBad.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", wBad.Code)
	}
	if wMiss := performPostJSON(r, "/registration/link-lecturer", map[string]any{"lecturerId": lec1.String(), "username": "LEC-MISSING"}); wMiss.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", wMiss.Code)
	}
	if err := linkLecturerAfterRegistration("LEC-FCS-001", lecUserID); err != nil {
		t.Fatalf("linkLecturerAfterRegistration: %v", err)
	}
	if store.lecLink[lec1] != lecUserID {
		t.Fatalf("expected auto-linked lecturer userID %s, got %s", lecUserID, store.lecLink[lec1])
	}
	wLink := performPostJSON(r, "/registration/link-lecturer", map[string]any{"lecturerId": lec1.String(), "username": "LEC-FCS-001"})
	if wLink.Code != http.StatusOK {
		t.Fatalf("link-lecturer = %d: %s", wLink.Code, wLink.Body.String())
	}
}
