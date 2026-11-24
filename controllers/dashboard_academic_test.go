package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"appsensi-server/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type memoryAcademicDashboardStore struct {
	mu             sync.RWMutex
	faculties      map[uuid.UUID]models.Faculty
	courses        map[uuid.UUID]models.Course
	lecturers      map[uuid.UUID]models.Lecturer
	employees      map[uuid.UUID]models.Employee
	schedules      map[uuid.UUID]models.ClassSchedule
	totalBuildings int
	totalRooms     int
	totalStudents  int
}

func newMemoryAcademicDashboardStore() *memoryAcademicDashboardStore {
	return &memoryAcademicDashboardStore{
		faculties:      make(map[uuid.UUID]models.Faculty),
		courses:        make(map[uuid.UUID]models.Course),
		lecturers:      make(map[uuid.UUID]models.Lecturer),
		employees:      make(map[uuid.UUID]models.Employee),
		schedules:      make(map[uuid.UUID]models.ClassSchedule),
		totalBuildings: 3,
		totalRooms:     12,
		totalStudents:  45,
	}
}

func (m *memoryAcademicDashboardStore) GetSummary() (*AcademicSummaryResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return &AcademicSummaryResponse{
		TotalFaculties:      len(m.faculties),
		TotalBuildings:      m.totalBuildings,
		TotalRooms:          m.totalRooms,
		TotalCourses:        len(m.courses),
		TotalLecturers:      len(m.lecturers),
		TotalEmployees:      len(m.employees),
		TotalClassSchedules: len(m.schedules),
		TotalStudents:       m.totalStudents,
	}, nil
}

func (m *memoryAcademicDashboardStore) ListFaculties() ([]models.Faculty, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.Faculty, 0, len(m.faculties))
	for _, f := range m.faculties {
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b models.Faculty) int {
		return strings.Compare(a.Code, b.Code)
	})
	return out, nil
}

func (m *memoryAcademicDashboardStore) CreateFaculty(faculty *models.Faculty) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if faculty.ID == uuid.Nil {
		faculty.ID = uuid.New()
	}
	m.faculties[faculty.ID] = *faculty
	return nil
}

func (m *memoryAcademicDashboardStore) UpdateFaculty(faculty *models.Faculty) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.faculties[faculty.ID]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	faculty.CreatedAt = existing.CreatedAt
	m.faculties[faculty.ID] = *faculty
	return nil
}

func (m *memoryAcademicDashboardStore) ListCourses(facultyID *uuid.UUID) ([]models.Course, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.Course, 0, len(m.courses))
	for _, c := range m.courses {
		if facultyID != nil && *facultyID != uuid.Nil {
			if c.FacultyID == nil || *c.FacultyID != *facultyID {
				continue
			}
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b models.Course) int {
		return strings.Compare(a.Code, b.Code)
	})
	return out, nil
}

func (m *memoryAcademicDashboardStore) CreateCourse(course *models.Course) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if course.ID == uuid.Nil {
		course.ID = uuid.New()
	}
	m.courses[course.ID] = *course
	return nil
}

func (m *memoryAcademicDashboardStore) UpdateCourse(course *models.Course) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.courses[course.ID]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	course.CreatedAt = existing.CreatedAt
	m.courses[course.ID] = *course
	return nil
}

func (m *memoryAcademicDashboardStore) DeleteCourse(id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.courses[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	delete(m.courses, id)
	return nil
}

func (m *memoryAcademicDashboardStore) ListLecturers(facultyID *uuid.UUID) ([]models.Lecturer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.Lecturer, 0, len(m.lecturers))
	for _, l := range m.lecturers {
		if facultyID != nil && *facultyID != uuid.Nil {
			if l.FacultyID == nil || *l.FacultyID != *facultyID {
				continue
			}
		}
		out = append(out, l)
	}
	slices.SortFunc(out, func(a, b models.Lecturer) int {
		return strings.Compare(a.LecturerID, b.LecturerID)
	})
	return out, nil
}

func (m *memoryAcademicDashboardStore) GetLecturerByID(id uuid.UUID) (*models.Lecturer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.lecturers[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := l
	return &cp, nil
}

func (m *memoryAcademicDashboardStore) CreateLecturer(lecturer *models.Lecturer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lecturer.ID == uuid.Nil {
		lecturer.ID = uuid.New()
	}
	m.lecturers[lecturer.ID] = *lecturer
	return nil
}

func (m *memoryAcademicDashboardStore) UpdateLecturer(lecturer *models.Lecturer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.lecturers[lecturer.ID]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	lecturer.UserID = existing.UserID
	lecturer.CreatedAt = existing.CreatedAt
	m.lecturers[lecturer.ID] = *lecturer
	return nil
}

func (m *memoryAcademicDashboardStore) DeleteLecturer(id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.lecturers[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	delete(m.lecturers, id)
	return nil
}

func (m *memoryAcademicDashboardStore) ListEmployees(jobRole *models.EmployeeJobRole, facultyID *uuid.UUID) ([]models.Employee, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.Employee, 0, len(m.employees))
	for _, e := range m.employees {
		if jobRole != nil && *jobRole != "" && e.JobRole != *jobRole {
			continue
		}
		if facultyID != nil && *facultyID != uuid.Nil {
			if e.FacultyID == nil || *e.FacultyID != *facultyID {
				continue
			}
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b models.Employee) int {
		return strings.Compare(a.EmployeeID, b.EmployeeID)
	})
	return out, nil
}

func (m *memoryAcademicDashboardStore) CreateEmployee(employee *models.Employee) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if employee.ID == uuid.Nil {
		employee.ID = uuid.New()
	}
	m.employees[employee.ID] = *employee
	return nil
}

func (m *memoryAcademicDashboardStore) UpdateEmployee(employee *models.Employee) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.employees[employee.ID]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	employee.UserID = existing.UserID
	employee.CreatedAt = existing.CreatedAt
	m.employees[employee.ID] = *employee
	return nil
}

func (m *memoryAcademicDashboardStore) DeleteEmployee(id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.employees[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	delete(m.employees, id)
	return nil
}

func (m *memoryAcademicDashboardStore) ListSchedules(courseID *uuid.UUID, lecturerID *uuid.UUID, academicTerm string) ([]models.ClassSchedule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]models.ClassSchedule, 0, len(m.schedules))
	for _, s := range m.schedules {
		if courseID != nil && *courseID != uuid.Nil {
			if s.CourseID == nil || *s.CourseID != *courseID {
				continue
			}
		}
		if lecturerID != nil && *lecturerID != uuid.Nil {
			if s.LecturerID == nil || *s.LecturerID != *lecturerID {
				continue
			}
		}
		if academicTerm != "" {
			if s.AcademicTerm == nil || *s.AcademicTerm != academicTerm {
				continue
			}
		}
		out = append(out, s)
	}
	return out, nil
}

func (m *memoryAcademicDashboardStore) CreateSchedule(schedule *models.ClassSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if schedule.ID == uuid.Nil {
		schedule.ID = uuid.New()
	}
	m.schedules[schedule.ID] = *schedule
	return nil
}

func (m *memoryAcademicDashboardStore) UpdateSchedule(schedule *models.ClassSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.schedules[schedule.ID]
	if !ok {
		return gorm.ErrRecordNotFound
	}
	schedule.CreatedAt = existing.CreatedAt
	m.schedules[schedule.ID] = *schedule
	return nil
}

func (m *memoryAcademicDashboardStore) DeleteSchedule(id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.schedules[id]; !ok {
		return gorm.ErrRecordNotFound
	}
	delete(m.schedules, id)
	return nil
}

func setupAcademicDashboardTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/dashboard/academic/summary", GetAcademicDashboardSummary)

	r.GET("/dashboard/academic/faculties", ListAcademicFaculties)
	r.POST("/dashboard/academic/faculties", CreateAcademicFaculty)
	r.PUT("/dashboard/academic/faculties/:id", UpdateAcademicFaculty)

	r.GET("/dashboard/academic/courses", ListAcademicCourses)
	r.POST("/dashboard/academic/courses", CreateAcademicCourse)
	r.PUT("/dashboard/academic/courses/:id", UpdateAcademicCourse)
	r.DELETE("/dashboard/academic/courses/:id", DeleteAcademicCourse)

	r.GET("/dashboard/academic/lecturers", ListAcademicLecturers)
	r.POST("/dashboard/academic/lecturers", CreateAcademicLecturer)
	r.PUT("/dashboard/academic/lecturers/:id", UpdateAcademicLecturer)
	r.DELETE("/dashboard/academic/lecturers/:id", DeleteAcademicLecturer)

	r.GET("/dashboard/academic/employees", ListAcademicEmployees)
	r.POST("/dashboard/academic/employees", CreateAcademicEmployee)
	r.PUT("/dashboard/academic/employees/:id", UpdateAcademicEmployee)
	r.DELETE("/dashboard/academic/employees/:id", DeleteAcademicEmployee)

	r.GET("/dashboard/academic/schedules", ListAcademicSchedules)
	r.POST("/dashboard/academic/schedules", CreateAcademicSchedule)
	r.PUT("/dashboard/academic/schedules/:id", UpdateAcademicSchedule)
	r.DELETE("/dashboard/academic/schedules/:id", DeleteAcademicSchedule)
	return r
}

func academicCreateEmployeeSessionToken(role models.EmployeeJobRole, facultyID *uuid.UUID) string {
	profileID := uuid.New()
	r := role
	return CreatePasskeySession(DashboardUserContext{
		UserID:      uuid.New(),
		Username:    "EMP-TEST-" + string(role),
		Email:       string(role) + "@appsensi.local",
		DisplayName: "Employee " + string(role),
		UserType:    models.UserTypeEmployee,
		ProfileID:   &profileID,
		JobRole:     &r,
		FacultyID:   facultyID,
	})
}

func academicPerformRequest(r *gin.Engine, method, path, token string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAcademicDashboard_RoleAuthorization(t *testing.T) {
	store := newMemoryAcademicDashboardStore()
	SetAcademicDashboardStore(store)
	defer SetAcademicDashboardStore(nil)
	ClearPasskeySessions()
	defer ClearPasskeySessions()

	r := setupAcademicDashboardTestRouter()

	// 1. Unauthenticated -> 401
	w := academicPerformRequest(r, http.MethodGet, "/dashboard/academic/summary", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Finance employee -> 403
	financeToken := academicCreateEmployeeSessionToken(models.JobRoleFinance, nil)
	w = academicPerformRequest(r, http.MethodGet, "/dashboard/academic/summary", financeToken, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for finance employee, got %d: %s", w.Code, w.Body.String())
	}

	// 3. Student affairs employee -> 403
	facultyID := uuid.New()
	saToken := academicCreateEmployeeSessionToken(models.JobRoleStudentAffairs, &facultyID)
	w = academicPerformRequest(r, http.MethodGet, "/dashboard/academic/summary", saToken, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for student_affairs employee, got %d: %s", w.Code, w.Body.String())
	}

	// 4. Lecturer user -> 403
	lecturerProfileID := uuid.New()
	lecturerToken := CreatePasskeySession(DashboardUserContext{
		UserID:      uuid.New(),
		Username:    "LEC-001",
		Email:       "lec@appsensi.local",
		DisplayName: "Dr. Lecturer",
		UserType:    models.UserTypeLecturer,
		ProfileID:   &lecturerProfileID,
	})
	w = academicPerformRequest(r, http.MethodGet, "/dashboard/academic/summary", lecturerToken, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for lecturer user, got %d: %s", w.Code, w.Body.String())
	}

	// 5. Student user -> 403
	studentProfileID := uuid.New()
	studentToken := CreatePasskeySession(DashboardUserContext{
		UserID:      uuid.New(),
		Username:    "STU-001",
		Email:       "stu@appsensi.local",
		DisplayName: "Student One",
		UserType:    models.UserTypeStudent,
		ProfileID:   &studentProfileID,
	})
	w = academicPerformRequest(r, http.MethodGet, "/dashboard/academic/summary", studentToken, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for student user, got %d: %s", w.Code, w.Body.String())
	}

	// 6. Academic affairs employee -> 200 OK
	academicToken := academicCreateEmployeeSessionToken(models.JobRoleAcademicAffairs, nil)
	w = academicPerformRequest(r, http.MethodGet, "/dashboard/academic/summary", academicToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for academic_affairs employee, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAcademicDashboard_SummaryAndFacultiesCRUD(t *testing.T) {
	store := newMemoryAcademicDashboardStore()
	SetAcademicDashboardStore(store)
	defer SetAcademicDashboardStore(nil)
	ClearPasskeySessions()
	defer ClearPasskeySessions()

	r := setupAcademicDashboardTestRouter()
	token := academicCreateEmployeeSessionToken(models.JobRoleAcademicAffairs, nil)

	// Create faculty validation error
	w := academicPerformRequest(r, http.MethodPost, "/dashboard/academic/faculties", token, map[string]any{
		"code": "",
		"name": "Missing Code",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when faculty code is empty, got %d", w.Code)
	}

	// Create faculty success
	buildingID := uuid.New().String()
	w = academicPerformRequest(r, http.MethodPost, "/dashboard/academic/faculties", token, AcademicUpsertFacultyRequest{
		Code:       "FCS",
		Name:       "Faculty of Computer Science",
		BuildingID: &buildingID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 on create faculty, got %d: %s", w.Code, w.Body.String())
	}
	var createdFaculty models.Faculty
	if err := json.Unmarshal(w.Body.Bytes(), &createdFaculty); err != nil {
		t.Fatalf("unmarshal created faculty: %v", err)
	}
	if createdFaculty.Code != "FCS" || createdFaculty.Name != "Faculty of Computer Science" {
		t.Fatalf("unexpected created faculty: %+v", createdFaculty)
	}

	// List faculties
	w = academicPerformRequest(r, http.MethodGet, "/dashboard/academic/faculties", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on list faculties, got %d", w.Code)
	}
	var faculties []models.Faculty
	if err := json.Unmarshal(w.Body.Bytes(), &faculties); err != nil {
		t.Fatalf("unmarshal faculties: %v", err)
	}
	if len(faculties) != 1 {
		t.Fatalf("expected 1 faculty, got %d", len(faculties))
	}

	// Update faculty
	w = academicPerformRequest(r, http.MethodPut, "/dashboard/academic/faculties/"+createdFaculty.ID.String(), token, AcademicUpsertFacultyRequest{
		Code: "FCS",
		Name: "Faculty of Computer Science & AI",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on update faculty, got %d: %s", w.Code, w.Body.String())
	}
	var updatedFaculty models.Faculty
	if err := json.Unmarshal(w.Body.Bytes(), &updatedFaculty); err != nil {
		t.Fatalf("unmarshal updated faculty: %v", err)
	}
	if updatedFaculty.Name != "Faculty of Computer Science & AI" || updatedFaculty.BuildingID != nil {
		t.Fatalf("unexpected updated faculty: %+v", updatedFaculty)
	}

	// Update non-existent faculty -> 404
	w = academicPerformRequest(r, http.MethodPut, "/dashboard/academic/faculties/"+uuid.NewString(), token, AcademicUpsertFacultyRequest{
		Code: "FENG",
		Name: "Faculty of Engineering",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on updating non-existent faculty, got %d", w.Code)
	}

	// Verify summary counts reflect created faculty
	w = academicPerformRequest(r, http.MethodGet, "/dashboard/academic/summary", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on summary, got %d", w.Code)
	}
	var summary AcademicSummaryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if summary.TotalFaculties != 1 || summary.TotalBuildings != 3 || summary.TotalRooms != 12 || summary.TotalStudents != 45 {
		t.Fatalf("unexpected summary counts: %+v", summary)
	}
}

func TestAcademicDashboard_CoursesCRUD(t *testing.T) {
	store := newMemoryAcademicDashboardStore()
	SetAcademicDashboardStore(store)
	defer SetAcademicDashboardStore(nil)
	ClearPasskeySessions()
	defer ClearPasskeySessions()

	r := setupAcademicDashboardTestRouter()
	token := academicCreateEmployeeSessionToken(models.JobRoleAcademicAffairs, nil)
	facultyIDStr := uuid.NewString()

	// Invalid credits (<= 0) -> 400
	w := academicPerformRequest(r, http.MethodPost, "/dashboard/academic/courses", token, AcademicUpsertCourseRequest{
		Code:    "CS101",
		Name:    "Intro to CS",
		Credits: 0,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for credits <= 0, got %d", w.Code)
	}

	// Create course -> 201
	desc := "Foundational programming course"
	w = academicPerformRequest(r, http.MethodPost, "/dashboard/academic/courses", token, AcademicUpsertCourseRequest{
		Code:        "CS101",
		Name:        "Intro to CS",
		Credits:     4,
		Description: &desc,
		FacultyID:   &facultyIDStr,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 on create course, got %d: %s", w.Code, w.Body.String())
	}
	var createdCourse models.Course
	if err := json.Unmarshal(w.Body.Bytes(), &createdCourse); err != nil {
		t.Fatalf("unmarshal created course: %v", err)
	}
	if createdCourse.Code != "CS101" || createdCourse.Credits != 4 {
		t.Fatalf("unexpected created course: %+v", createdCourse)
	}

	// List courses filtered by facultyId
	w = academicPerformRequest(r, http.MethodGet, "/dashboard/academic/courses?facultyId="+facultyIDStr, token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on list courses, got %d", w.Code)
	}
	var courses []models.Course
	if err := json.Unmarshal(w.Body.Bytes(), &courses); err != nil {
		t.Fatalf("unmarshal courses: %v", err)
	}
	if len(courses) != 1 {
		t.Fatalf("expected 1 course for faculty filter, got %d", len(courses))
	}

	// Filter by another facultyId -> 0 courses
	w = academicPerformRequest(r, http.MethodGet, "/dashboard/academic/courses?facultyId="+uuid.NewString(), token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on list courses, got %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &courses); err != nil {
		t.Fatalf("unmarshal courses: %v", err)
	}
	if len(courses) != 0 {
		t.Fatalf("expected 0 courses for unmatched facultyId, got %d", len(courses))
	}

	// Update course -> 200
	w = academicPerformRequest(r, http.MethodPut, "/dashboard/academic/courses/"+createdCourse.ID.String(), token, AcademicUpsertCourseRequest{
		Code:      "CS101",
		Name:      "Introduction to Computer Science",
		Credits:   3,
		FacultyID: &facultyIDStr,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on update course, got %d: %s", w.Code, w.Body.String())
	}

	// Delete course -> 200
	w = academicPerformRequest(r, http.MethodDelete, "/dashboard/academic/courses/"+createdCourse.ID.String(), token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete course, got %d: %s", w.Code, w.Body.String())
	}

	// Delete again -> 404
	w = academicPerformRequest(r, http.MethodDelete, "/dashboard/academic/courses/"+createdCourse.ID.String(), token, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on deleting already deleted course, got %d", w.Code)
	}
}

func TestAcademicDashboard_LecturersCRUD(t *testing.T) {
	store := newMemoryAcademicDashboardStore()
	SetAcademicDashboardStore(store)
	defer SetAcademicDashboardStore(nil)
	ClearPasskeySessions()
	defer ClearPasskeySessions()

	r := setupAcademicDashboardTestRouter()
	token := academicCreateEmployeeSessionToken(models.JobRoleAcademicAffairs, nil)
	facultyIDStr := uuid.NewString()
	dept := "Computer Science"

	// Missing lecturerId -> 400
	w := academicPerformRequest(r, http.MethodPost, "/dashboard/academic/lecturers", token, AcademicUpsertLecturerRequest{
		Name: "Dr. Alan Turing",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when lecturerId is missing, got %d", w.Code)
	}

	// Create lecturer -> 201
	w = academicPerformRequest(r, http.MethodPost, "/dashboard/academic/lecturers", token, AcademicUpsertLecturerRequest{
		LecturerID: "LEC-FCS-001",
		Name:       "Dr. Alan Turing",
		Department: &dept,
		FacultyID:  &facultyIDStr,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 on create lecturer, got %d: %s", w.Code, w.Body.String())
	}
	var createdLecturer models.Lecturer
	if err := json.Unmarshal(w.Body.Bytes(), &createdLecturer); err != nil {
		t.Fatalf("unmarshal lecturer: %v", err)
	}

	// List lecturers with faculty filter
	w = academicPerformRequest(r, http.MethodGet, "/dashboard/academic/lecturers?facultyId="+facultyIDStr, token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on list lecturers, got %d", w.Code)
	}
	var lecturers []models.Lecturer
	if err := json.Unmarshal(w.Body.Bytes(), &lecturers); err != nil {
		t.Fatalf("unmarshal lecturers: %v", err)
	}
	if len(lecturers) != 1 || lecturers[0].LecturerID != "LEC-FCS-001" {
		t.Fatalf("unexpected lecturers list: %+v", lecturers)
	}

	// Update lecturer -> 200
	w = academicPerformRequest(r, http.MethodPut, "/dashboard/academic/lecturers/"+createdLecturer.ID.String(), token, AcademicUpsertLecturerRequest{
		LecturerID: "LEC-FCS-001",
		Name:       "Prof. Alan Turing",
		Department: &dept,
		FacultyID:  &facultyIDStr,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on update lecturer, got %d: %s", w.Code, w.Body.String())
	}

	// Delete lecturer -> 200, then 404
	w = academicPerformRequest(r, http.MethodDelete, "/dashboard/academic/lecturers/"+createdLecturer.ID.String(), token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete lecturer, got %d", w.Code)
	}
	w = academicPerformRequest(r, http.MethodDelete, "/dashboard/academic/lecturers/"+createdLecturer.ID.String(), token, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on deleting non-existent lecturer, got %d", w.Code)
	}
}

func TestAcademicDashboard_EmployeesCRUD_AndRoleScopeValidation(t *testing.T) {
	store := newMemoryAcademicDashboardStore()
	SetAcademicDashboardStore(store)
	defer SetAcademicDashboardStore(nil)
	ClearPasskeySessions()
	defer ClearPasskeySessions()

	r := setupAcademicDashboardTestRouter()
	token := academicCreateEmployeeSessionToken(models.JobRoleAcademicAffairs, nil)
	facultyIDStr := uuid.NewString()

	// 1. Creating student_affairs employee WITHOUT facultyId must fail with 400 Bad Request
	w := academicPerformRequest(r, http.MethodPost, "/dashboard/academic/employees", token, AcademicUpsertEmployeeRequest{
		EmployeeID: "EMP-SA-001",
		Name:       "Siti Aminah",
		JobRole:    models.JobRoleStudentAffairs,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when creating student_affairs employee without facultyId, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Creating student_affairs employee WITH facultyId succeeds (201 Created)
	w = academicPerformRequest(r, http.MethodPost, "/dashboard/academic/employees", token, AcademicUpsertEmployeeRequest{
		EmployeeID: "EMP-SA-001",
		Name:       "Siti Aminah",
		JobRole:    models.JobRoleStudentAffairs,
		FacultyID:  &facultyIDStr,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating student_affairs employee with facultyId, got %d: %s", w.Code, w.Body.String())
	}
	var saEmployee models.Employee
	if err := json.Unmarshal(w.Body.Bytes(), &saEmployee); err != nil {
		t.Fatalf("unmarshal employee: %v", err)
	}

	// 3. Creating finance employee without facultyId succeeds (201 Created)
	w = academicPerformRequest(r, http.MethodPost, "/dashboard/academic/employees", token, AcademicUpsertEmployeeRequest{
		EmployeeID: "EMP-FIN-001",
		Name:       "Budi Santoso",
		JobRole:    models.JobRoleFinance,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 when creating finance employee, got %d: %s", w.Code, w.Body.String())
	}

	// 4. List employees filtered by jobRole and facultyId
	w = academicPerformRequest(r, http.MethodGet, "/dashboard/academic/employees?jobRole=student_affairs&facultyId="+facultyIDStr, token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on list employees, got %d", w.Code)
	}
	var employees []models.Employee
	if err := json.Unmarshal(w.Body.Bytes(), &employees); err != nil {
		t.Fatalf("unmarshal employees: %v", err)
	}
	if len(employees) != 1 || employees[0].EmployeeID != "EMP-SA-001" {
		t.Fatalf("expected 1 student_affairs employee, got %+v", employees)
	}

	// 5. Updating employee to student_affairs without facultyId fails with 400
	w = academicPerformRequest(r, http.MethodPut, "/dashboard/academic/employees/"+saEmployee.ID.String(), token, AcademicUpsertEmployeeRequest{
		EmployeeID: "EMP-SA-001",
		Name:       "Siti Aminah Updated",
		JobRole:    models.JobRoleStudentAffairs,
		FacultyID:  nil,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when updating student_affairs employee without facultyId, got %d", w.Code)
	}

	// 6. Updating employee with valid facultyId succeeds (200 OK)
	w = academicPerformRequest(r, http.MethodPut, "/dashboard/academic/employees/"+saEmployee.ID.String(), token, AcademicUpsertEmployeeRequest{
		EmployeeID: "EMP-SA-001",
		Name:       "Siti Aminah Updated",
		JobRole:    models.JobRoleStudentAffairs,
		FacultyID:  &facultyIDStr,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on update employee, got %d: %s", w.Code, w.Body.String())
	}

	// 7. Delete employee -> 200, then 404
	w = academicPerformRequest(r, http.MethodDelete, "/dashboard/academic/employees/"+saEmployee.ID.String(), token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete employee, got %d", w.Code)
	}
	w = academicPerformRequest(r, http.MethodDelete, "/dashboard/academic/employees/"+saEmployee.ID.String(), token, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on deleting non-existent employee, got %d", w.Code)
	}
}

func TestAcademicDashboard_SchedulesCRUD(t *testing.T) {
	store := newMemoryAcademicDashboardStore()
	SetAcademicDashboardStore(store)
	defer SetAcademicDashboardStore(nil)
	ClearPasskeySessions()
	defer ClearPasskeySessions()

	r := setupAcademicDashboardTestRouter()
	token := academicCreateEmployeeSessionToken(models.JobRoleAcademicAffairs, nil)

	// Seed a lecturer so schedule creation can auto-populate Instructor from LecturerID
	lecturerID := uuid.New()
	_ = store.CreateLecturer(&models.Lecturer{
		ID:         lecturerID,
		LecturerID: "LEC-FCS-010",
		Name:       "Dr. Grace Hopper",
	})

	courseIDStr := uuid.NewString()
	roomIDStr := uuid.NewString()
	lecturerIDStr := lecturerID.String()
	maxCap := 40

	// Missing required fields -> 400
	w := academicPerformRequest(r, http.MethodPost, "/dashboard/academic/schedules", token, AcademicUpsertScheduleRequest{
		CourseID: courseIDStr,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when schedule fields are missing, got %d", w.Code)
	}

	// Create schedule without explicit Instructor -> populates from Lecturer.Name
	w = academicPerformRequest(r, http.MethodPost, "/dashboard/academic/schedules", token, AcademicUpsertScheduleRequest{
		CourseID:     courseIDStr,
		RoomID:       roomIDStr,
		LecturerID:   &lecturerIDStr,
		Section:      "A",
		DayOfWeek:    "Monday",
		StartTime:    "08:00",
		EndTime:      "10:00",
		AcademicTerm: "2026/2027-1",
		MaxCapacity:  &maxCap,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 on create schedule, got %d: %s", w.Code, w.Body.String())
	}
	var createdSchedule models.ClassSchedule
	if err := json.Unmarshal(w.Body.Bytes(), &createdSchedule); err != nil {
		t.Fatalf("unmarshal schedule: %v", err)
	}
	if createdSchedule.Instructor == nil || *createdSchedule.Instructor != "Dr. Grace Hopper" {
		t.Fatalf("expected Instructor to be auto-populated to 'Dr. Grace Hopper', got %v", createdSchedule.Instructor)
	}

	// List schedules with courseId, lecturerId, and academicTerm filters
	w = academicPerformRequest(r, http.MethodGet, "/dashboard/academic/schedules?courseId="+courseIDStr+"&lecturerId="+lecturerIDStr+"&academicTerm=2026/2027-1", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on list schedules, got %d", w.Code)
	}
	var schedules []models.ClassSchedule
	if err := json.Unmarshal(w.Body.Bytes(), &schedules); err != nil {
		t.Fatalf("unmarshal schedules: %v", err)
	}
	if len(schedules) != 1 || schedules[0].ID != createdSchedule.ID {
		t.Fatalf("expected 1 matching schedule, got %+v", schedules)
	}

	// Update schedule -> 200
	customInstructor := "Prof. Grace Hopper"
	w = academicPerformRequest(r, http.MethodPut, "/dashboard/academic/schedules/"+createdSchedule.ID.String(), token, AcademicUpsertScheduleRequest{
		CourseID:     courseIDStr,
		RoomID:       roomIDStr,
		LecturerID:   &lecturerIDStr,
		Section:      "B",
		Instructor:   &customInstructor,
		DayOfWeek:    "Wednesday",
		StartTime:    "10:00",
		EndTime:      "12:00",
		AcademicTerm: "2026/2027-1",
		MaxCapacity:  &maxCap,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on update schedule, got %d: %s", w.Code, w.Body.String())
	}
	var updatedSchedule models.ClassSchedule
	if err := json.Unmarshal(w.Body.Bytes(), &updatedSchedule); err != nil {
		t.Fatalf("unmarshal updated schedule: %v", err)
	}
	if updatedSchedule.Section == nil || *updatedSchedule.Section != "B" || updatedSchedule.Instructor == nil || *updatedSchedule.Instructor != "Prof. Grace Hopper" {
		t.Fatalf("unexpected updated schedule: %+v", updatedSchedule)
	}

	// Delete schedule -> 200, then 404
	w = academicPerformRequest(r, http.MethodDelete, "/dashboard/academic/schedules/"+createdSchedule.ID.String(), token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete schedule, got %d", w.Code)
	}
	w = academicPerformRequest(r, http.MethodDelete, "/dashboard/academic/schedules/"+createdSchedule.ID.String(), token, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on deleting non-existent schedule, got %d", w.Code)
	}
}
