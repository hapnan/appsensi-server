package controllers

import (
	"cmp"
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
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type financeTestEnrollment struct {
	AcademicTerm string
	Credits      int
	Status       models.EnrollmentStatus
}

type financeTestStudentRecord struct {
	ID           uuid.UUID
	StudentID    string
	Name         string
	Department   *string
	FacultyID    *uuid.UUID
	IsRegistered bool
	CreatedAt    time.Time
	Enrollments  []financeTestEnrollment
}

type financeMemoryDashboardStore struct {
	mu        sync.Mutex
	faculties []models.Faculty
	students  []financeTestStudentRecord
}

func newMemoryFinanceDashboardStore() *financeMemoryDashboardStore {
	return &financeMemoryDashboardStore{}
}

func (m *financeMemoryDashboardStore) GetSummary(academicTerm string) (*FinanceSummaryResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	trimmedTerm := strings.TrimSpace(academicTerm)

	sortedFaculties := slices.Clone(m.faculties)
	slices.SortFunc(sortedFaculties, func(a, b models.Faculty) int {
		return cmp.Compare(a.Code, b.Code)
	})

	facultyIndex := make(map[uuid.UUID]int, len(sortedFaculties))
	byFaculty := make([]FinanceFacultyBreakdown, 0, len(sortedFaculties))
	for i, fac := range sortedFaculties {
		facultyIndex[fac.ID] = i
		byFaculty = append(byFaculty, FinanceFacultyBreakdown{
			FacultyID:   fac.ID,
			FacultyCode: fac.Code,
			FacultyName: fac.Name,
		})
	}

	var totalStudents, registeredStudents, enrolledStudents, totalCredits int
	for _, stu := range m.students {
		totalStudents++
		if stu.IsRegistered {
			registeredStudents++
		}

		stuCourses := 0
		stuCredits := 0
		for _, enr := range stu.Enrollments {
			status := enr.Status
			if status == "" {
				status = models.EnrollmentStatusEnrolled
			}
			if status != models.EnrollmentStatusEnrolled {
				continue
			}
			if trimmedTerm != "" && !strings.EqualFold(strings.TrimSpace(enr.AcademicTerm), trimmedTerm) {
				continue
			}
			stuCourses++
			stuCredits += enr.Credits
		}
		if stuCourses > 0 {
			enrolledStudents++
		}
		totalCredits += stuCredits

		if stu.FacultyID != nil {
			if idx, ok := facultyIndex[*stu.FacultyID]; ok {
				byFaculty[idx].TotalStudents++
				if stu.IsRegistered {
					byFaculty[idx].RegisteredStudents++
				}
				if stuCourses > 0 {
					byFaculty[idx].EnrolledStudents++
				}
				byFaculty[idx].TotalCredits += stuCredits
			}
		}
	}

	resp := &FinanceSummaryResponse{
		TotalStudents:      totalStudents,
		RegisteredStudents: registeredStudents,
		EnrolledStudents:   enrolledStudents,
		TotalCredits:       totalCredits,
		TotalFaculties:     len(byFaculty),
		ByFaculty:          byFaculty,
	}
	if trimmedTerm != "" {
		resp.AcademicTerm = &trimmedTerm
	}
	return resp, nil
}

func (m *financeMemoryDashboardStore) ListStudents(filter FinanceStudentsFilter) ([]FinanceStudentRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	facMap := make(map[uuid.UUID]models.Faculty, len(m.faculties))
	for _, fac := range m.faculties {
		facMap[fac.ID] = fac
	}

	trimmedTerm := strings.TrimSpace(filter.AcademicTerm)
	trimmedSearch := strings.ToLower(strings.TrimSpace(filter.Search))

	sortedStudents := slices.Clone(m.students)
	slices.SortFunc(sortedStudents, func(a, b financeTestStudentRecord) int {
		return cmp.Compare(a.StudentID, b.StudentID)
	})

	rows := make([]FinanceStudentRow, 0, len(sortedStudents))
	for _, stu := range sortedStudents {
		if filter.FacultyID != nil && *filter.FacultyID != uuid.Nil {
			if stu.FacultyID == nil || *stu.FacultyID != *filter.FacultyID {
				continue
			}
		}
		if trimmedSearch != "" {
			if !strings.Contains(strings.ToLower(stu.Name), trimmedSearch) &&
				!strings.Contains(strings.ToLower(stu.StudentID), trimmedSearch) {
				continue
			}
		}

		enrolledCourses := 0
		enrolledCredits := 0
		for _, enr := range stu.Enrollments {
			status := enr.Status
			if status == "" {
				status = models.EnrollmentStatusEnrolled
			}
			if status != models.EnrollmentStatusEnrolled {
				continue
			}
			if trimmedTerm != "" && !strings.EqualFold(strings.TrimSpace(enr.AcademicTerm), trimmedTerm) {
				continue
			}
			enrolledCourses++
			enrolledCredits += enr.Credits
		}

		var facCode, facName *string
		if stu.FacultyID != nil {
			if fac, ok := facMap[*stu.FacultyID]; ok {
				code := fac.Code
				name := fac.Name
				facCode = &code
				facName = &name
			}
		}

		rows = append(rows, FinanceStudentRow{
			ID:              stu.ID,
			StudentID:       stu.StudentID,
			Name:            stu.Name,
			Department:      stu.Department,
			FacultyID:       stu.FacultyID,
			FacultyCode:     facCode,
			FacultyName:     facName,
			EnrolledCourses: enrolledCourses,
			EnrolledCredits: enrolledCredits,
			IsRegistered:    stu.IsRegistered,
			CreatedAt:       stu.CreatedAt,
		})
	}

	return rows, nil
}

func setupFinanceDashboardTestRouter(t *testing.T, store FinanceDashboardStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	SetFinanceDashboardStore(store)
	ClearPasskeySessions()
	t.Cleanup(func() {
		SetFinanceDashboardStore(nil)
		ClearPasskeySessions()
	})

	r := gin.New()
	finance := r.Group("/dashboard/finance")
	{
		finance.GET("/summary", GetFinanceDashboardSummary)
		finance.GET("/students", ListFinanceDashboardStudents)
	}
	return r
}

func financeSeedTestStore() (*financeMemoryDashboardStore, uuid.UUID, uuid.UUID) {
	store := newMemoryFinanceDashboardStore()
	fcsID := uuid.New()
	fengID := uuid.New()

	store.faculties = []models.Faculty{
		{ID: fengID, Code: "FENG", Name: "Faculty of Engineering"},
		{ID: fcsID, Code: "FCS", Name: "Faculty of Computer Science"},
	}

	csDept := "Computer Science"
	eeDept := "Electrical Engineering"
	baseTime := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

	store.students = []financeTestStudentRecord{
		{
			ID:           uuid.New(),
			StudentID:    "STU-FCS-002",
			Name:         "Budi Santoso",
			Department:   &csDept,
			FacultyID:    &fcsID,
			IsRegistered: false,
			CreatedAt:    baseTime.Add(time.Hour),
			Enrollments: []financeTestEnrollment{
				{AcademicTerm: "2026/2027-Ganjil", Credits: 3, Status: models.EnrollmentStatusEnrolled},
			},
		},
		{
			ID:           uuid.New(),
			StudentID:    "STU-FCS-001",
			Name:         "Ayu Pratiwi",
			Department:   &csDept,
			FacultyID:    &fcsID,
			IsRegistered: true,
			CreatedAt:    baseTime,
			Enrollments: []financeTestEnrollment{
				{AcademicTerm: "2026/2027-Ganjil", Credits: 4, Status: models.EnrollmentStatusEnrolled},
				{AcademicTerm: "2026/2027-Ganjil", Credits: 3, Status: models.EnrollmentStatusEnrolled},
				{AcademicTerm: "2026/2027-Genap", Credits: 2, Status: models.EnrollmentStatusEnrolled},
			},
		},
		{
			ID:           uuid.New(),
			StudentID:    "STU-FENG-001",
			Name:         "Citra Lestari",
			Department:   &eeDept,
			FacultyID:    &fengID,
			IsRegistered: true,
			CreatedAt:    baseTime.Add(2 * time.Hour),
			Enrollments: []financeTestEnrollment{
				{AcademicTerm: "2026/2027-Genap", Credits: 3, Status: models.EnrollmentStatusEnrolled},
			},
		},
	}

	return store, fcsID, fengID
}

func financeCreateSessionToken(userType models.UserType, role *models.EmployeeJobRole, facultyID *uuid.UUID) string {
	profileID := uuid.New()
	return CreatePasskeySession(DashboardUserContext{
		UserID:      uuid.New(),
		Username:    "test-user",
		Email:       "test@campus.edu",
		DisplayName: "Test User",
		UserType:    userType,
		ProfileID:   &profileID,
		JobRole:     role,
		FacultyID:   facultyID,
	})
}

func TestFinanceDashboard_Authorization(t *testing.T) {
	store, fcsID, _ := financeSeedTestStore()
	r := setupFinanceDashboardTestRouter(t, store)

	endpoints := []string{
		"/dashboard/finance/summary",
		"/dashboard/finance/students",
	}

	// 1. Unauthenticated -> 401 Unauthorized
	for _, ep := range endpoints {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, ep, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s unauthenticated = %d, want 401", ep, w.Code)
		}
	}

	// 2. Forbidden personas: student, lecturer, academic_affairs, student_affairs -> 403 Forbidden
	acadRole := models.JobRoleAcademicAffairs
	saRole := models.JobRoleStudentAffairs
	forbiddenTokens := []struct {
		name  string
		token string
	}{
		{"student", financeCreateSessionToken(models.UserTypeStudent, nil, &fcsID)},
		{"lecturer", financeCreateSessionToken(models.UserTypeLecturer, nil, &fcsID)},
		{"academic_affairs", financeCreateSessionToken(models.UserTypeEmployee, &acadRole, nil)},
		{"student_affairs", financeCreateSessionToken(models.UserTypeEmployee, &saRole, &fcsID)},
	}

	for _, tc := range forbiddenTokens {
		for _, ep := range endpoints {
			req := httptest.NewRequest(http.MethodGet, ep, nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("GET %s as %s = %d, want 403 (%s)", ep, tc.name, w.Code, w.Body.String())
			}
		}
	}

	// 3. Finance employee -> 200 OK
	finRole := models.JobRoleFinance
	finToken := financeCreateSessionToken(models.UserTypeEmployee, &finRole, nil)
	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodGet, ep, nil)
		req.Header.Set("Authorization", "Bearer "+finToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s as finance = %d, want 200 (%s)", ep, w.Code, w.Body.String())
		}
	}
}

func TestFinanceDashboard_Summary(t *testing.T) {
	store, fcsID, fengID := financeSeedTestStore()
	r := setupFinanceDashboardTestRouter(t, store)

	finRole := models.JobRoleFinance
	finToken := financeCreateSessionToken(models.UserTypeEmployee, &finRole, nil)

	// 1. Summary without academicTerm filter
	reqAll := httptest.NewRequest(http.MethodGet, "/dashboard/finance/summary", nil)
	reqAll.Header.Set("Authorization", "Bearer "+finToken)
	wAll := httptest.NewRecorder()
	r.ServeHTTP(wAll, reqAll)
	if wAll.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/finance/summary status = %d, want 200", wAll.Code)
	}

	var gotAll FinanceSummaryResponse
	if err := json.Unmarshal(wAll.Body.Bytes(), &gotAll); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if gotAll.TotalStudents != 3 || gotAll.RegisteredStudents != 2 || gotAll.EnrolledStudents != 3 || gotAll.TotalCredits != 15 || gotAll.TotalFaculties != 2 {
		t.Fatalf("unexpected overall summary: %+v", gotAll)
	}
	if gotAll.AcademicTerm != nil {
		t.Errorf("expected nil AcademicTerm when omitted, got %v", *gotAll.AcademicTerm)
	}
	if len(gotAll.ByFaculty) != 2 {
		t.Fatalf("len(ByFaculty) = %d, want 2", len(gotAll.ByFaculty))
	}
	// Ordered by FacultyCode ASC: FCS then FENG
	if gotAll.ByFaculty[0].FacultyID != fcsID || gotAll.ByFaculty[0].FacultyCode != "FCS" ||
		gotAll.ByFaculty[0].TotalStudents != 2 || gotAll.ByFaculty[0].RegisteredStudents != 1 ||
		gotAll.ByFaculty[0].EnrolledStudents != 2 || gotAll.ByFaculty[0].TotalCredits != 12 {
		t.Errorf("ByFaculty[0] = %+v", gotAll.ByFaculty[0])
	}
	if gotAll.ByFaculty[1].FacultyID != fengID || gotAll.ByFaculty[1].FacultyCode != "FENG" ||
		gotAll.ByFaculty[1].TotalStudents != 1 || gotAll.ByFaculty[1].RegisteredStudents != 1 ||
		gotAll.ByFaculty[1].EnrolledStudents != 1 || gotAll.ByFaculty[1].TotalCredits != 3 {
		t.Errorf("ByFaculty[1] = %+v", gotAll.ByFaculty[1])
	}

	// 2. Summary with academicTerm=2026/2027-Ganjil
	reqTerm := httptest.NewRequest(http.MethodGet, "/dashboard/finance/summary?academicTerm=2026/2027-Ganjil", nil)
	reqTerm.Header.Set("Authorization", "Bearer "+finToken)
	wTerm := httptest.NewRecorder()
	r.ServeHTTP(wTerm, reqTerm)
	if wTerm.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/finance/summary?academicTerm status = %d, want 200", wTerm.Code)
	}

	var gotTerm FinanceSummaryResponse
	if err := json.Unmarshal(wTerm.Body.Bytes(), &gotTerm); err != nil {
		t.Fatalf("unmarshal term summary: %v", err)
	}
	if gotTerm.AcademicTerm == nil || *gotTerm.AcademicTerm != "2026/2027-Ganjil" {
		t.Errorf("AcademicTerm = %v, want 2026/2027-Ganjil", gotTerm.AcademicTerm)
	}
	if gotTerm.TotalStudents != 3 || gotTerm.RegisteredStudents != 2 || gotTerm.EnrolledStudents != 2 || gotTerm.TotalCredits != 10 {
		t.Errorf("unexpected term summary: %+v", gotTerm)
	}
	if gotTerm.ByFaculty[0].TotalCredits != 10 || gotTerm.ByFaculty[0].EnrolledStudents != 2 {
		t.Errorf("ByFaculty[0] term = %+v", gotTerm.ByFaculty[0])
	}
	if gotTerm.ByFaculty[1].TotalCredits != 0 || gotTerm.ByFaculty[1].EnrolledStudents != 0 {
		t.Errorf("ByFaculty[1] term = %+v", gotTerm.ByFaculty[1])
	}

	// 3. Empty store returns empty array [] for byFaculty
	emptyStore := newMemoryFinanceDashboardStore()
	rEmpty := setupFinanceDashboardTestRouter(t, emptyStore)
	finTokenEmpty := financeCreateSessionToken(models.UserTypeEmployee, &finRole, nil)
	reqEmpty := httptest.NewRequest(http.MethodGet, "/dashboard/finance/summary", nil)
	reqEmpty.Header.Set("Authorization", "Bearer "+finTokenEmpty)
	wEmpty := httptest.NewRecorder()
	rEmpty.ServeHTTP(wEmpty, reqEmpty)
	if !strings.Contains(wEmpty.Body.String(), `"byFaculty":[]`) {
		t.Errorf("expected byFaculty:[], got %s", wEmpty.Body.String())
	}
}

func TestFinanceDashboard_Students(t *testing.T) {
	store, fcsID, fengID := financeSeedTestStore()
	r := setupFinanceDashboardTestRouter(t, store)

	finRole := models.JobRoleFinance
	finToken := financeCreateSessionToken(models.UserTypeEmployee, &finRole, nil)

	// 1. List all students (ordered by studentId ASC)
	reqAll := httptest.NewRequest(http.MethodGet, "/dashboard/finance/students", nil)
	reqAll.Header.Set("Authorization", "Bearer "+finToken)
	wAll := httptest.NewRecorder()
	r.ServeHTTP(wAll, reqAll)
	if wAll.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", wAll.Code)
	}

	var gotAll []FinanceStudentRow
	if err := json.Unmarshal(wAll.Body.Bytes(), &gotAll); err != nil {
		t.Fatalf("unmarshal students: %v", err)
	}
	if len(gotAll) != 3 {
		t.Fatalf("len(gotAll) = %d, want 3", len(gotAll))
	}
	if gotAll[0].StudentID != "STU-FCS-001" || gotAll[0].EnrolledCourses != 3 || gotAll[0].EnrolledCredits != 9 || !gotAll[0].IsRegistered {
		t.Errorf("gotAll[0] = %+v", gotAll[0])
	}
	if gotAll[1].StudentID != "STU-FCS-002" || gotAll[1].EnrolledCourses != 1 || gotAll[1].EnrolledCredits != 3 || gotAll[1].IsRegistered {
		t.Errorf("gotAll[1] = %+v", gotAll[1])
	}
	if gotAll[2].StudentID != "STU-FENG-001" || gotAll[2].EnrolledCourses != 1 || gotAll[2].EnrolledCredits != 3 || !gotAll[2].IsRegistered {
		t.Errorf("gotAll[2] = %+v", gotAll[2])
	}

	// 2. Invalid facultyId -> 400 Bad Request with ErrCodeBadRequest
	reqBadFac := httptest.NewRequest(http.MethodGet, "/dashboard/finance/students?facultyId=not-a-uuid", nil)
	reqBadFac.Header.Set("Authorization", "Bearer "+finToken)
	wBadFac := httptest.NewRecorder()
	r.ServeHTTP(wBadFac, reqBadFac)
	if wBadFac.Code != http.StatusBadRequest {
		t.Fatalf("invalid facultyId status = %d, want 400", wBadFac.Code)
	}
	var errResp APIErrorResponse
	if err := json.Unmarshal(wBadFac.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Code != ErrCodeBadRequest {
		t.Errorf("error code = %q, want %q", errResp.Code, ErrCodeBadRequest)
	}

	// 3. Filter by facultyId
	reqFac := httptest.NewRequest(http.MethodGet, "/dashboard/finance/students?facultyId="+fengID.String(), nil)
	reqFac.Header.Set("Authorization", "Bearer "+finToken)
	wFac := httptest.NewRecorder()
	r.ServeHTTP(wFac, reqFac)
	if wFac.Code != http.StatusOK {
		t.Fatalf("facultyId filter status = %d, want 200", wFac.Code)
	}
	var gotFac []FinanceStudentRow
	if err := json.Unmarshal(wFac.Body.Bytes(), &gotFac); err != nil {
		t.Fatalf("unmarshal fac students: %v", err)
	}
	if len(gotFac) != 1 || gotFac[0].StudentID != "STU-FENG-001" || gotFac[0].FacultyID == nil || *gotFac[0].FacultyID != fengID {
		t.Errorf("gotFac = %+v", gotFac)
	}

	// 4. Filter by academicTerm
	reqTerm := httptest.NewRequest(http.MethodGet, "/dashboard/finance/students?facultyId="+fcsID.String()+"&academicTerm=2026/2027-Ganjil", nil)
	reqTerm.Header.Set("Authorization", "Bearer "+finToken)
	wTerm := httptest.NewRecorder()
	r.ServeHTTP(wTerm, reqTerm)
	if wTerm.Code != http.StatusOK {
		t.Fatalf("academicTerm filter status = %d, want 200", wTerm.Code)
	}
	var gotTerm []FinanceStudentRow
	if err := json.Unmarshal(wTerm.Body.Bytes(), &gotTerm); err != nil {
		t.Fatalf("unmarshal term students: %v", err)
	}
	if len(gotTerm) != 2 || gotTerm[0].EnrolledCourses != 2 || gotTerm[0].EnrolledCredits != 7 {
		t.Errorf("gotTerm = %+v", gotTerm)
	}

	// 5. Filter by search (case-insensitive on name or student_id)
	reqSearch := httptest.NewRequest(http.MethodGet, "/dashboard/finance/students?search=budi", nil)
	reqSearch.Header.Set("Authorization", "Bearer "+finToken)
	wSearch := httptest.NewRecorder()
	r.ServeHTTP(wSearch, reqSearch)
	if wSearch.Code != http.StatusOK {
		t.Fatalf("search filter status = %d, want 200", wSearch.Code)
	}
	var gotSearch []FinanceStudentRow
	if err := json.Unmarshal(wSearch.Body.Bytes(), &gotSearch); err != nil {
		t.Fatalf("unmarshal search students: %v", err)
	}
	if len(gotSearch) != 1 || gotSearch[0].StudentID != "STU-FCS-002" {
		t.Errorf("gotSearch = %+v", gotSearch)
	}

	// 6. Search with no matches returns [] (not null)
	reqNone := httptest.NewRequest(http.MethodGet, "/dashboard/finance/students?search=nonexistent", nil)
	reqNone.Header.Set("Authorization", "Bearer "+finToken)
	wNone := httptest.NewRecorder()
	r.ServeHTTP(wNone, reqNone)
	if wNone.Code != http.StatusOK || strings.TrimSpace(wNone.Body.String()) != "[]" {
		t.Errorf("empty search = %d %q, want 200 []", wNone.Code, wNone.Body.String())
	}
}

func TestFinanceDashboard_SQLQueries(t *testing.T) {
	db := newDryRunPostgresDB(t)
	facID := uuid.New()

	var byFac []FinanceFacultyBreakdown
	facStmt := financeBuildSummaryFacultyQuery(db.Session(&gorm.Session{DryRun: true}), "2026/2027-Ganjil").Scan(&byFac).Statement
	facSQL := facStmt.SQL.String()
	for _, fragment := range []string{
		"EXISTS (",
		"FROM credentials",
		"ORDER BY f.code ASC",
	} {
		if !strings.Contains(facSQL, fragment) {
			t.Errorf("expected fragment %q in summary faculty SQL, got: %s", fragment, facSQL)
		}
	}

	var rows []FinanceStudentRow
	stuStmt := financeBuildStudentsQuery(db.Session(&gorm.Session{DryRun: true}), FinanceStudentsFilter{
		FacultyID:    &facID,
		AcademicTerm: "2026/2027-Ganjil",
		Search:       "ayu",
	}).Scan(&rows).Statement
	stuSQL := stuStmt.SQL.String()
	for _, fragment := range []string{
		"EXISTS (",
		"FROM credentials",
		"students.faculty_id = $",
		"ORDER BY students.student_id ASC",
	} {
		if !strings.Contains(stuSQL, fragment) {
			t.Errorf("expected fragment %q in students SQL, got: %s", fragment, stuSQL)
		}
	}
}
