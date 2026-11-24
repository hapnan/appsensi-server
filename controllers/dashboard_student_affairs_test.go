package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"appsensi-server/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type studentAffairsMemoryScheduleDetail struct {
	Schedule   models.ClassSchedule
	CourseCode string
	CourseName string
	Credits    int
	RoomCode   string
}

type studentAffairsMemoryDashboardStore struct {
	mu          sync.Mutex
	faculties   map[uuid.UUID]models.Faculty
	courses     map[uuid.UUID]models.Course
	students    map[uuid.UUID]models.Student
	schedules   map[uuid.UUID]studentAffairsMemoryScheduleDetail
	enrollments map[uuid.UUID]models.StudentSchedule
	attendances []StudentAffairsAttendanceRow
}

func newMemoryStudentAffairsDashboardStore() *studentAffairsMemoryDashboardStore {
	return &studentAffairsMemoryDashboardStore{
		faculties:   make(map[uuid.UUID]models.Faculty),
		courses:     make(map[uuid.UUID]models.Course),
		students:    make(map[uuid.UUID]models.Student),
		schedules:   make(map[uuid.UUID]studentAffairsMemoryScheduleDetail),
		enrollments: make(map[uuid.UUID]models.StudentSchedule),
		attendances: []StudentAffairsAttendanceRow{},
	}
}

func (m *studentAffairsMemoryDashboardStore) GetSummary(facultyID uuid.UUID) (*StudentAffairsSummaryResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var codePtr, namePtr *string
	if fac, ok := m.faculties[facultyID]; ok {
		c := fac.Code
		n := fac.Name
		codePtr = &c
		namePtr = &n
	}

	totalStudents := 0
	registeredStudents := 0
	facultyStudents := make(map[uuid.UUID]bool)
	for _, s := range m.students {
		if s.FacultyID != nil && *s.FacultyID == facultyID {
			totalStudents++
			facultyStudents[s.ID] = true
			if s.UserID != nil && *s.UserID != uuid.Nil {
				registeredStudents++
			}
		}
	}

	totalCourses := 0
	for _, c := range m.courses {
		if c.FacultyID != nil && *c.FacultyID == facultyID {
			totalCourses++
		}
	}

	totalEnrollments := 0
	for _, e := range m.enrollments {
		if facultyStudents[e.StudentID] && e.Status == models.EnrollmentStatusEnrolled {
			totalEnrollments++
		}
	}

	totalAttendances := 0
	for _, a := range m.attendances {
		if facultyStudents[a.StudentID] {
			totalAttendances++
		}
	}

	return &StudentAffairsSummaryResponse{
		FacultyID:          facultyID,
		FacultyCode:        codePtr,
		FacultyName:        namePtr,
		TotalStudents:      totalStudents,
		RegisteredStudents: registeredStudents,
		TotalCourses:       totalCourses,
		TotalEnrollments:   totalEnrollments,
		TotalAttendances:   totalAttendances,
	}, nil
}

func (m *studentAffairsMemoryDashboardStore) studentAffairsBuildRowLocked(s models.Student) StudentAffairsStudentRow {
	enrolled := 0
	for _, e := range m.enrollments {
		if e.StudentID == s.ID && e.Status == models.EnrollmentStatusEnrolled {
			enrolled++
		}
	}
	attCount := 0
	for _, a := range m.attendances {
		if a.StudentID == s.ID {
			attCount++
		}
	}
	return StudentAffairsStudentRow{
		ID:              s.ID,
		StudentID:       s.StudentID,
		Name:            s.Name,
		Department:      s.Department,
		FacultyID:       s.FacultyID,
		PhotoURL:        s.PhotoURL,
		UserID:          s.UserID,
		IsRegistered:    s.UserID != nil && *s.UserID != uuid.Nil,
		EnrolledCourses: enrolled,
		AttendanceCount: attCount,
		CreatedAt:       s.CreatedAt,
	}
}

func (m *studentAffairsMemoryDashboardStore) ListStudents(facultyID uuid.UUID, search, department string) ([]StudentAffairsStudentRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	searchLower := strings.ToLower(strings.TrimSpace(search))
	deptTrimmed := strings.TrimSpace(department)

	out := make([]StudentAffairsStudentRow, 0)
	for _, s := range m.students {
		if s.FacultyID == nil || *s.FacultyID != facultyID {
			continue
		}
		if deptTrimmed != "" {
			sDept := ""
			if s.Department != nil {
				sDept = strings.TrimSpace(*s.Department)
			}
			if !strings.EqualFold(sDept, deptTrimmed) {
				continue
			}
		}
		if searchLower != "" {
			sDept := ""
			if s.Department != nil {
				sDept = strings.ToLower(*s.Department)
			}
			if !strings.Contains(strings.ToLower(s.StudentID), searchLower) &&
				!strings.Contains(strings.ToLower(s.Name), searchLower) &&
				!strings.Contains(sDept, searchLower) {
				continue
			}
		}
		out = append(out, m.studentAffairsBuildRowLocked(s))
	}
	studentAffairsSortStudentRows(out)
	return out, nil
}

func (m *studentAffairsMemoryDashboardStore) CreateStudent(facultyID uuid.UUID, req StudentAffairsUpsertStudentRequest) (*StudentAffairsStudentRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, existing := range m.students {
		if strings.EqualFold(existing.StudentID, req.StudentID) {
			return nil, studentAffairsErrDuplicateStudentID
		}
	}

	facCopy := facultyID
	s := models.Student{
		ID:         uuid.New(),
		StudentID:  req.StudentID,
		Name:       req.Name,
		Department: req.Department,
		FacultyID:  &facCopy,
		PhotoURL:   req.PhotoURL,
		CreatedAt:  time.Now().UTC(),
	}
	m.students[s.ID] = s
	row := m.studentAffairsBuildRowLocked(s)
	return &row, nil
}

func (m *studentAffairsMemoryDashboardStore) UpdateStudent(facultyID, studentUUID uuid.UUID, req StudentAffairsUpsertStudentRequest) (*StudentAffairsStudentRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.students[studentUUID]
	if !ok || s.FacultyID == nil || *s.FacultyID != facultyID {
		return nil, nil
	}

	for id, existing := range m.students {
		if id != studentUUID && strings.EqualFold(existing.StudentID, req.StudentID) {
			return nil, studentAffairsErrDuplicateStudentID
		}
	}

	s.StudentID = req.StudentID
	s.Name = req.Name
	s.Department = req.Department
	s.PhotoURL = req.PhotoURL
	m.students[studentUUID] = s
	row := m.studentAffairsBuildRowLocked(s)
	return &row, nil
}

func (m *studentAffairsMemoryDashboardStore) DeleteStudent(facultyID, studentUUID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.students[studentUUID]
	if !ok || s.FacultyID == nil || *s.FacultyID != facultyID {
		return false, nil
	}
	delete(m.students, studentUUID)
	for eid, e := range m.enrollments {
		if e.StudentID == studentUUID {
			delete(m.enrollments, eid)
		}
	}
	return true, nil
}

func (m *studentAffairsMemoryDashboardStore) StudentExistsInFaculty(facultyID, studentUUID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.students[studentUUID]
	if !ok || s.FacultyID == nil || *s.FacultyID != facultyID {
		return false, nil
	}
	return true, nil
}

func (m *studentAffairsMemoryDashboardStore) studentAffairsBuildEnrollmentRowLocked(e models.StudentSchedule, d studentAffairsMemoryScheduleDetail) StudentAffairsEnrollmentRow {
	return StudentAffairsEnrollmentRow{
		ID:              e.ID,
		StudentID:       e.StudentID,
		ClassScheduleID: e.ClassScheduleID,
		CourseCode:      d.CourseCode,
		CourseName:      d.CourseName,
		Credits:         d.Credits,
		Section:         studentAffairsPtrOrDefault(d.Schedule.Section, ""),
		DayOfWeek:       studentAffairsPtrOrDefault(d.Schedule.DayOfWeek, ""),
		StartTime:       studentAffairsPtrOrDefault(d.Schedule.StartTime, ""),
		EndTime:         studentAffairsPtrOrDefault(d.Schedule.EndTime, ""),
		AcademicTerm:    studentAffairsPtrOrDefault(d.Schedule.AcademicTerm, ""),
		Status:          e.Status,
		EnrolledAt:      e.EnrolledAt,
	}
}

func (m *studentAffairsMemoryDashboardStore) ListStudentSchedules(facultyID, studentUUID uuid.UUID) ([]StudentAffairsEnrollmentRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.students[studentUUID]
	if !ok || s.FacultyID == nil || *s.FacultyID != facultyID {
		return nil, studentAffairsErrStudentNotFound
	}

	out := make([]StudentAffairsEnrollmentRow, 0)
	for _, e := range m.enrollments {
		if e.StudentID == studentUUID && e.Status == models.EnrollmentStatusEnrolled {
			d := m.schedules[e.ClassScheduleID]
			out = append(out, m.studentAffairsBuildEnrollmentRowLocked(e, d))
		}
	}
	return out, nil
}

func (m *studentAffairsMemoryDashboardStore) EnrollStudentSchedule(facultyID, studentUUID, scheduleUUID uuid.UUID) (*StudentAffairsEnrollmentRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.students[studentUUID]
	if !ok || s.FacultyID == nil || *s.FacultyID != facultyID {
		return nil, studentAffairsErrStudentNotFound
	}
	d, ok := m.schedules[scheduleUUID]
	if !ok {
		return nil, studentAffairsErrScheduleNotFound
	}

	for id, existing := range m.enrollments {
		if existing.StudentID == studentUUID && existing.ClassScheduleID == scheduleUUID {
			existing.Status = models.EnrollmentStatusEnrolled
			m.enrollments[id] = existing
			row := m.studentAffairsBuildEnrollmentRowLocked(existing, d)
			return &row, nil
		}
	}

	e := models.StudentSchedule{
		ID:              uuid.New(),
		StudentID:       studentUUID,
		ClassScheduleID: scheduleUUID,
		Status:          models.EnrollmentStatusEnrolled,
		EnrolledAt:      time.Now().UTC(),
	}
	m.enrollments[e.ID] = e
	row := m.studentAffairsBuildEnrollmentRowLocked(e, d)
	return &row, nil
}

func (m *studentAffairsMemoryDashboardStore) DropStudentSchedule(facultyID, studentUUID, scheduleUUID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.students[studentUUID]
	if !ok || s.FacultyID == nil || *s.FacultyID != facultyID {
		return false, studentAffairsErrStudentNotFound
	}

	for id, existing := range m.enrollments {
		if existing.StudentID == studentUUID && existing.ClassScheduleID == scheduleUUID {
			delete(m.enrollments, id)
			return true, nil
		}
	}
	return false, nil
}

func (m *studentAffairsMemoryDashboardStore) ListAttendance(facultyID uuid.UUID, scheduleID, studentID *uuid.UUID, sessionDate *SessionDate) ([]StudentAffairsAttendanceRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]StudentAffairsAttendanceRow, 0)
	for _, a := range m.attendances {
		s, ok := m.students[a.StudentID]
		if !ok || s.FacultyID == nil || *s.FacultyID != facultyID {
			continue
		}
		if scheduleID != nil && a.ClassScheduleID != *scheduleID {
			continue
		}
		if studentID != nil && a.StudentID != *studentID {
			continue
		}
		if sessionDate != nil && a.SessionDate != sessionDate.String() {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func setupStudentAffairsDashboardTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	grp := r.Group("/dashboard/student-affairs")
	{
		grp.GET("/summary", GetStudentAffairsDashboardSummary)
		grp.GET("/students", ListStudentAffairsStudents)
		grp.POST("/students", CreateStudentAffairsStudent)
		grp.PUT("/students/:id", UpdateStudentAffairsStudent)
		grp.DELETE("/students/:id", DeleteStudentAffairsStudent)
		grp.GET("/students/:id/schedules", ListStudentAffairsStudentSchedules)
		grp.POST("/students/:id/schedules", EnrollStudentAffairsStudentSchedule)
		grp.DELETE("/students/:id/schedules/:scheduleId", DropStudentAffairsStudentSchedule)
		grp.GET("/attendance", ListStudentAffairsAttendance)
	}
	return r
}

func studentAffairsCreateRoleSession(role models.EmployeeJobRole, facultyID *uuid.UUID, code, name string) string {
	r := role
	var codePtr, namePtr *string
	if code != "" {
		codePtr = &code
	}
	if name != "" {
		namePtr = &name
	}
	return CreatePasskeySession(DashboardUserContext{
		UserID:      uuid.New(),
		Username:    "EMP-TEST",
		Email:       "emp@example.edu",
		DisplayName: "Employee Test",
		UserType:    models.UserTypeEmployee,
		JobRole:     &r,
		FacultyID:   facultyID,
		FacultyCode: codePtr,
		FacultyName: namePtr,
	})
}

func TestStudentAffairsDashboard_AuthorizationAndFacultyIsolation(t *testing.T) {
	ClearPasskeySessions()
	store := newMemoryStudentAffairsDashboardStore()
	SetStudentAffairsDashboardStore(store)
	t.Cleanup(func() {
		SetStudentAffairsDashboardStore(nil)
		ClearPasskeySessions()
	})

	facA := uuid.New()
	facB := uuid.New()
	deptB := "Mechanical Engineering"
	studentB := models.Student{
		ID:         uuid.New(),
		StudentID:  "STU-FENG-001",
		Name:       "Budi Santoso",
		Department: &deptB,
		FacultyID:  &facB,
		CreatedAt:  time.Now().UTC(),
	}
	store.students[studentB.ID] = studentB

	schedB := uuid.New()
	secA := "A"
	store.schedules[schedB] = studentAffairsMemoryScheduleDetail{
		Schedule:   models.ClassSchedule{ID: schedB, Section: &secA},
		CourseCode: "ME101",
		CourseName: "Thermodynamics",
		Credits:    3,
		RoomCode:   "e.1.1",
	}

	router := setupStudentAffairsDashboardTestRouter()

	// 1. Unauthenticated -> 401
	reqUnauth := httptest.NewRequest(http.MethodGet, "/dashboard/student-affairs/summary", nil)
	wUnauth := httptest.NewRecorder()
	router.ServeHTTP(wUnauth, reqUnauth)
	if wUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d", wUnauth.Code)
	}

	// 2. Finance & Academic Affairs roles -> 403
	for _, disallowedRole := range []models.EmployeeJobRole{models.JobRoleFinance, models.JobRoleAcademicAffairs} {
		tok := studentAffairsCreateRoleSession(disallowedRole, &facA, "FCS", "Faculty of Computer Science")
		req := httptest.NewRequest(http.MethodGet, "/dashboard/student-affairs/summary", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for role %s, got %d", disallowedRole, w.Code)
		}
	}

	// 3. student_affairs without FacultyID -> 403
	tokNoFac := studentAffairsCreateRoleSession(models.JobRoleStudentAffairs, nil, "", "")
	reqNoFac := httptest.NewRequest(http.MethodGet, "/dashboard/student-affairs/summary", nil)
	reqNoFac.Header.Set("Authorization", "Bearer "+tokNoFac)
	wNoFac := httptest.NewRecorder()
	router.ServeHTTP(wNoFac, reqNoFac)
	if wNoFac.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for student_affairs without FacultyID, got %d", wNoFac.Code)
	}

	// 4. student_affairs for Faculty A cannot modify/delete/enroll/list schedules for Faculty B student -> 404
	tokFacA := studentAffairsCreateRoleSession(models.JobRoleStudentAffairs, &facA, "FCS", "Faculty of Computer Science")

	// 4a. Update Faculty B student -> 404
	updateBody, _ := json.Marshal(StudentAffairsUpsertStudentRequest{
		StudentID: "STU-FENG-001",
		Name:      "Hacked Name",
	})
	reqUpd := httptest.NewRequest(http.MethodPut, "/dashboard/student-affairs/students/"+studentB.ID.String(), bytes.NewReader(updateBody))
	reqUpd.Header.Set("Content-Type", "application/json")
	reqUpd.Header.Set("Authorization", "Bearer "+tokFacA)
	wUpd := httptest.NewRecorder()
	router.ServeHTTP(wUpd, reqUpd)
	if wUpd.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when updating Faculty B student from Faculty A, got %d", wUpd.Code)
	}

	// 4b. Delete Faculty B student -> 404
	reqDel := httptest.NewRequest(http.MethodDelete, "/dashboard/student-affairs/students/"+studentB.ID.String(), nil)
	reqDel.Header.Set("Authorization", "Bearer "+tokFacA)
	wDel := httptest.NewRecorder()
	router.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when deleting Faculty B student from Faculty A, got %d", wDel.Code)
	}

	// 4c. List schedules for Faculty B student -> 404
	reqListSched := httptest.NewRequest(http.MethodGet, "/dashboard/student-affairs/students/"+studentB.ID.String()+"/schedules", nil)
	reqListSched.Header.Set("Authorization", "Bearer "+tokFacA)
	wListSched := httptest.NewRecorder()
	router.ServeHTTP(wListSched, reqListSched)
	if wListSched.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when listing schedules of Faculty B student from Faculty A, got %d", wListSched.Code)
	}

	// 4d. Enroll Faculty B student -> 404
	enrollBody, _ := json.Marshal(StudentAffairsEnrollRequest{ClassScheduleID: schedB.String()})
	reqEnroll := httptest.NewRequest(http.MethodPost, "/dashboard/student-affairs/students/"+studentB.ID.String()+"/schedules", bytes.NewReader(enrollBody))
	reqEnroll.Header.Set("Content-Type", "application/json")
	reqEnroll.Header.Set("Authorization", "Bearer "+tokFacA)
	wEnroll := httptest.NewRecorder()
	router.ServeHTTP(wEnroll, reqEnroll)
	if wEnroll.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when enrolling Faculty B student from Faculty A, got %d", wEnroll.Code)
	}

	// 4e. Drop schedule for Faculty B student -> 404
	reqDrop := httptest.NewRequest(http.MethodDelete, "/dashboard/student-affairs/students/"+studentB.ID.String()+"/schedules/"+schedB.String(), nil)
	reqDrop.Header.Set("Authorization", "Bearer "+tokFacA)
	wDrop := httptest.NewRecorder()
	router.ServeHTTP(wDrop, reqDrop)
	if wDrop.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when dropping schedule of Faculty B student from Faculty A, got %d", wDrop.Code)
	}
}

func TestStudentAffairsDashboard_SummaryAndStudentCRUD(t *testing.T) {
	ClearPasskeySessions()
	store := newMemoryStudentAffairsDashboardStore()
	SetStudentAffairsDashboardStore(store)
	t.Cleanup(func() {
		SetStudentAffairsDashboardStore(nil)
		ClearPasskeySessions()
	})

	facA := uuid.New()
	facB := uuid.New()
	store.faculties[facA] = models.Faculty{ID: facA, Code: "FCS", Name: "Faculty of Computer Science"}
	store.faculties[facB] = models.Faculty{ID: facB, Code: "FEB", Name: "Faculty of Economics and Business"}

	courseA := models.Course{ID: uuid.New(), Code: "CS101", Name: "Algorithms", Credits: 4, FacultyID: &facA}
	courseB := models.Course{ID: uuid.New(), Code: "EC101", Name: "Microeconomics", Credits: 3, FacultyID: &facB}
	store.courses[courseA.ID] = courseA
	store.courses[courseB.ID] = courseB

	regUserID := uuid.New()
	deptCS := "Computer Science"
	deptIS := "Information Systems"
	stuA1 := models.Student{ID: uuid.New(), StudentID: "STU-FCS-001", Name: "Alice Putri", Department: &deptCS, FacultyID: &facA, UserID: &regUserID, CreatedAt: time.Now().UTC()}
	stuA2 := models.Student{ID: uuid.New(), StudentID: "STU-FCS-002", Name: "Bima Pratama", Department: &deptIS, FacultyID: &facA, CreatedAt: time.Now().UTC()}
	stuB1 := models.Student{ID: uuid.New(), StudentID: "STU-FEB-001", Name: "Citra Dewi", FacultyID: &facB, CreatedAt: time.Now().UTC()}
	store.students[stuA1.ID] = stuA1
	store.students[stuA2.ID] = stuA2
	store.students[stuB1.ID] = stuB1

	tokFacA := studentAffairsCreateRoleSession(models.JobRoleStudentAffairs, &facA, "FCS", "Faculty of Computer Science")
	router := setupStudentAffairsDashboardTestRouter()

	// 1. Summary only counts Faculty A
	reqSum := httptest.NewRequest(http.MethodGet, "/dashboard/student-affairs/summary", nil)
	reqSum.Header.Set("Authorization", "Bearer "+tokFacA)
	wSum := httptest.NewRecorder()
	router.ServeHTTP(wSum, reqSum)
	if wSum.Code != http.StatusOK {
		t.Fatalf("expected 200 on summary, got %d: %s", wSum.Code, wSum.Body.String())
	}
	var summary StudentAffairsSummaryResponse
	if err := json.Unmarshal(wSum.Body.Bytes(), &summary); err != nil {
		t.Fatalf("failed to unmarshal summary: %v", err)
	}
	if summary.FacultyID != facA || summary.TotalStudents != 2 || summary.RegisteredStudents != 1 || summary.TotalCourses != 1 {
		t.Fatalf("unexpected summary payload: %+v", summary)
	}

	// 2. List students with search and department filters
	reqList := httptest.NewRequest(http.MethodGet, "/dashboard/student-affairs/students?department=Information+Systems", nil)
	reqList.Header.Set("Authorization", "Bearer "+tokFacA)
	wList := httptest.NewRecorder()
	router.ServeHTTP(wList, reqList)
	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 on list students, got %d", wList.Code)
	}
	var listRows []StudentAffairsStudentRow
	if err := json.Unmarshal(wList.Body.Bytes(), &listRows); err != nil {
		t.Fatalf("unmarshal list students failed: %v", err)
	}
	if len(listRows) != 1 || listRows[0].StudentID != "STU-FCS-002" {
		t.Fatalf("expected 1 filtered student STU-FCS-002, got %+v", listRows)
	}

	// 3. Create new student automatically assigned to Faculty A
	createPayload, _ := json.Marshal(StudentAffairsUpsertStudentRequest{
		StudentID:  "STU-FCS-003",
		Name:       "Dina Kusuma",
		Department: &deptCS,
	})
	reqCreate := httptest.NewRequest(http.MethodPost, "/dashboard/student-affairs/students", bytes.NewReader(createPayload))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("Authorization", "Bearer "+tokFacA)
	wCreate := httptest.NewRecorder()
	router.ServeHTTP(wCreate, reqCreate)
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("expected 201 on create student, got %d: %s", wCreate.Code, wCreate.Body.String())
	}
	var createdRow StudentAffairsStudentRow
	if err := json.Unmarshal(wCreate.Body.Bytes(), &createdRow); err != nil {
		t.Fatalf("unmarshal created student failed: %v", err)
	}
	if createdRow.FacultyID == nil || *createdRow.FacultyID != facA || createdRow.StudentID != "STU-FCS-003" {
		t.Fatalf("expected created student to have facultyID %s, got %+v", facA, createdRow)
	}

	// 4. Update student in Faculty A
	updPayload, _ := json.Marshal(StudentAffairsUpsertStudentRequest{
		StudentID:  "STU-FCS-003",
		Name:       "Dina Kusuma Updated",
		Department: &deptIS,
	})
	reqUpd := httptest.NewRequest(http.MethodPut, "/dashboard/student-affairs/students/"+createdRow.ID.String(), bytes.NewReader(updPayload))
	reqUpd.Header.Set("Content-Type", "application/json")
	reqUpd.Header.Set("Authorization", "Bearer "+tokFacA)
	wUpd := httptest.NewRecorder()
	router.ServeHTTP(wUpd, reqUpd)
	if wUpd.Code != http.StatusOK {
		t.Fatalf("expected 200 on update student, got %d: %s", wUpd.Code, wUpd.Body.String())
	}
	var updatedRow StudentAffairsStudentRow
	if err := json.Unmarshal(wUpd.Body.Bytes(), &updatedRow); err != nil {
		t.Fatalf("unmarshal updated student failed: %v", err)
	}
	if updatedRow.Name != "Dina Kusuma Updated" || updatedRow.Department == nil || *updatedRow.Department != deptIS {
		t.Fatalf("unexpected updated student row: %+v", updatedRow)
	}

	// 5. Delete student in Faculty A
	reqDel := httptest.NewRequest(http.MethodDelete, "/dashboard/student-affairs/students/"+createdRow.ID.String(), nil)
	reqDel.Header.Set("Authorization", "Bearer "+tokFacA)
	wDel := httptest.NewRecorder()
	router.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete student, got %d", wDel.Code)
	}
}

func TestStudentAffairsDashboard_EnrollmentsAndAttendanceReport(t *testing.T) {
	ClearPasskeySessions()
	store := newMemoryStudentAffairsDashboardStore()
	SetStudentAffairsDashboardStore(store)
	t.Cleanup(func() {
		SetStudentAffairsDashboardStore(nil)
		ClearPasskeySessions()
	})

	facA := uuid.New()
	facB := uuid.New()
	deptCS := "Computer Science"
	stuA1 := models.Student{ID: uuid.New(), StudentID: "STU-FCS-010", Name: "Eko Wijaya", Department: &deptCS, FacultyID: &facA, CreatedAt: time.Now().UTC()}
	stuA2 := models.Student{ID: uuid.New(), StudentID: "STU-FCS-011", Name: "Farah Nabila", Department: &deptCS, FacultyID: &facA, CreatedAt: time.Now().UTC()}
	stuB1 := models.Student{ID: uuid.New(), StudentID: "STU-FEB-010", Name: "Gilang Ramadhan", FacultyID: &facB, CreatedAt: time.Now().UTC()}
	store.students[stuA1.ID] = stuA1
	store.students[stuA2.ID] = stuA2
	store.students[stuB1.ID] = stuB1

	sched1 := uuid.New()
	sched2 := uuid.New()
	secA := "A"
	dayMon := "Monday"
	start08 := "08:00"
	end10 := "10:00"
	term := "2026/2027-1"
	store.schedules[sched1] = studentAffairsMemoryScheduleDetail{
		Schedule: models.ClassSchedule{
			ID:           sched1,
			Section:      &secA,
			DayOfWeek:    &dayMon,
			StartTime:    &start08,
			EndTime:      &end10,
			AcademicTerm: &term,
		},
		CourseCode: "CS201",
		CourseName: "Data Structures",
		Credits:    4,
		RoomCode:   "h.2.4",
	}
	store.schedules[sched2] = studentAffairsMemoryScheduleDetail{
		Schedule: models.ClassSchedule{
			ID:           sched2,
			Section:      &secA,
			DayOfWeek:    &dayMon,
			StartTime:    &start08,
			EndTime:      &end10,
			AcademicTerm: &term,
		},
		CourseCode: "CS202",
		CourseName: "Operating Systems",
		Credits:    3,
		RoomCode:   "h.3.1",
	}

	now := time.Now().UTC()
	store.attendances = []StudentAffairsAttendanceRow{
		{
			ID:              uuid.New(),
			StudentID:       stuA1.ID,
			StudentBadgeID:  stuA1.StudentID,
			StudentName:     stuA1.Name,
			Department:      stuA1.Department,
			ClassScheduleID: sched1,
			CourseCode:      "CS201",
			CourseName:      "Data Structures",
			Section:         "A",
			RoomCode:        "h.2.4",
			SessionDate:     "2026-11-02",
			CheckedInAt:     now,
		},
		{
			ID:              uuid.New(),
			StudentID:       stuA2.ID,
			StudentBadgeID:  stuA2.StudentID,
			StudentName:     stuA2.Name,
			Department:      stuA2.Department,
			ClassScheduleID: sched2,
			CourseCode:      "CS202",
			CourseName:      "Operating Systems",
			Section:         "A",
			RoomCode:        "h.3.1",
			SessionDate:     "2026-11-03",
			CheckedInAt:     now,
		},
		{
			ID:              uuid.New(),
			StudentID:       stuB1.ID,
			StudentBadgeID:  stuB1.StudentID,
			StudentName:     stuB1.Name,
			ClassScheduleID: sched1,
			CourseCode:      "CS201",
			CourseName:      "Data Structures",
			Section:         "A",
			RoomCode:        "h.2.4",
			SessionDate:     "2026-11-02",
			CheckedInAt:     now,
		},
	}

	tokFacA := studentAffairsCreateRoleSession(models.JobRoleStudentAffairs, &facA, "FCS", "Faculty of Computer Science")
	router := setupStudentAffairsDashboardTestRouter()

	// 1. Enroll stuA1 in sched1 -> 201 Created
	enrollBody, _ := json.Marshal(StudentAffairsEnrollRequest{ClassScheduleID: sched1.String()})
	reqEnroll := httptest.NewRequest(http.MethodPost, "/dashboard/student-affairs/students/"+stuA1.ID.String()+"/schedules", bytes.NewReader(enrollBody))
	reqEnroll.Header.Set("Content-Type", "application/json")
	reqEnroll.Header.Set("Authorization", "Bearer "+tokFacA)
	wEnroll := httptest.NewRecorder()
	router.ServeHTTP(wEnroll, reqEnroll)
	if wEnroll.Code != http.StatusCreated {
		t.Fatalf("expected 201 on enroll schedule, got %d: %s", wEnroll.Code, wEnroll.Body.String())
	}

	// 2. List schedules for stuA1 -> 1 item
	reqListSched := httptest.NewRequest(http.MethodGet, "/dashboard/student-affairs/students/"+stuA1.ID.String()+"/schedules", nil)
	reqListSched.Header.Set("Authorization", "Bearer "+tokFacA)
	wListSched := httptest.NewRecorder()
	router.ServeHTTP(wListSched, reqListSched)
	if wListSched.Code != http.StatusOK {
		t.Fatalf("expected 200 on list schedules, got %d", wListSched.Code)
	}
	var enrollments []StudentAffairsEnrollmentRow
	if err := json.Unmarshal(wListSched.Body.Bytes(), &enrollments); err != nil {
		t.Fatalf("unmarshal enrollments failed: %v", err)
	}
	if len(enrollments) != 1 || enrollments[0].ClassScheduleID != sched1 || enrollments[0].CourseCode != "CS201" {
		t.Fatalf("unexpected enrollments list: %+v", enrollments)
	}

	// 3. Drop schedule for stuA1 -> 200 OK
	reqDrop := httptest.NewRequest(http.MethodDelete, "/dashboard/student-affairs/students/"+stuA1.ID.String()+"/schedules/"+sched1.String(), nil)
	reqDrop.Header.Set("Authorization", "Bearer "+tokFacA)
	wDrop := httptest.NewRecorder()
	router.ServeHTTP(wDrop, reqDrop)
	if wDrop.Code != http.StatusOK {
		t.Fatalf("expected 200 on drop schedule, got %d", wDrop.Code)
	}

	// 4. Attendance report without filters returns only Faculty A attendances (2 rows, excluding stuB1)
	reqAttAll := httptest.NewRequest(http.MethodGet, "/dashboard/student-affairs/attendance", nil)
	reqAttAll.Header.Set("Authorization", "Bearer "+tokFacA)
	wAttAll := httptest.NewRecorder()
	router.ServeHTTP(wAttAll, reqAttAll)
	if wAttAll.Code != http.StatusOK {
		t.Fatalf("expected 200 on attendance report, got %d", wAttAll.Code)
	}
	var attRows []StudentAffairsAttendanceRow
	if err := json.Unmarshal(wAttAll.Body.Bytes(), &attRows); err != nil {
		t.Fatalf("unmarshal attendance rows failed: %v", err)
	}
	if len(attRows) != 2 {
		t.Fatalf("expected 2 faculty-scoped attendance rows, got %d", len(attRows))
	}

	// 5. Filter attendance by date, scheduleId, and studentId
	reqAttFiltered := httptest.NewRequest(
		http.MethodGet,
		"/dashboard/student-affairs/attendance?date=2026-11-02&scheduleId="+sched1.String()+"&studentId="+stuA1.ID.String(),
		nil,
	)
	reqAttFiltered.Header.Set("Authorization", "Bearer "+tokFacA)
	wAttFiltered := httptest.NewRecorder()
	router.ServeHTTP(wAttFiltered, reqAttFiltered)
	if wAttFiltered.Code != http.StatusOK {
		t.Fatalf("expected 200 on filtered attendance report, got %d", wAttFiltered.Code)
	}
	var filteredRows []StudentAffairsAttendanceRow
	if err := json.Unmarshal(wAttFiltered.Body.Bytes(), &filteredRows); err != nil {
		t.Fatalf("unmarshal filtered attendance rows failed: %v", err)
	}
	if len(filteredRows) != 1 || filteredRows[0].StudentID != stuA1.ID || filteredRows[0].SessionDate != "2026-11-02" {
		t.Fatalf("unexpected filtered attendance rows: %+v", filteredRows)
	}

	// 6. Invalid date format returns 400 Bad Request
	reqBadDate := httptest.NewRequest(http.MethodGet, "/dashboard/student-affairs/attendance?date=02-11-2026", nil)
	reqBadDate.Header.Set("Authorization", "Bearer "+tokFacA)
	wBadDate := httptest.NewRecorder()
	router.ServeHTTP(wBadDate, reqBadDate)
	if wBadDate.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid date format, got %d", wBadDate.Code)
	}
}
