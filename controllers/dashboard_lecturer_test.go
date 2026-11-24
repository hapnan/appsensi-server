package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"appsensi-server/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type lecturerMemoryDashboardStore struct {
	lecturers    map[uuid.UUID]models.Lecturer
	faculties    map[uuid.UUID]models.Faculty
	courses      map[uuid.UUID]models.Course
	rooms        map[uuid.UUID]models.Room
	schedules    map[uuid.UUID]models.ClassSchedule
	students     map[uuid.UUID]models.Student
	registered   map[uuid.UUID]bool
	enrollments  []models.StudentSchedule
	attendances  []models.Attendance
	currentClock func() time.Time
}

func newMemoryLecturerDashboardStore() *lecturerMemoryDashboardStore {
	return &lecturerMemoryDashboardStore{
		lecturers:   make(map[uuid.UUID]models.Lecturer),
		faculties:   make(map[uuid.UUID]models.Faculty),
		courses:     make(map[uuid.UUID]models.Course),
		rooms:       make(map[uuid.UUID]models.Room),
		schedules:   make(map[uuid.UUID]models.ClassSchedule),
		students:    make(map[uuid.UUID]models.Student),
		registered:  make(map[uuid.UUID]bool),
		enrollments: []models.StudentSchedule{},
		attendances: []models.Attendance{},
		currentClock: func() time.Time {
			return time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
		},
	}
}

func (m *lecturerMemoryDashboardStore) GetSummary(lecturerID uuid.UUID) (*LecturerSummaryResponse, error) {
	summary := &LecturerSummaryResponse{
		LecturerID: lecturerID,
	}
	if lec, ok := m.lecturers[lecturerID]; ok {
		summary.LecturerName = lec.Name
		summary.FacultyID = lec.FacultyID
		if lec.FacultyID != nil {
			if fac, ok := m.faculties[*lec.FacultyID]; ok {
				code := fac.Code
				name := fac.Name
				summary.FacultyCode = &code
				summary.FacultyName = &name
			}
		}
	}

	ownedSchedules := make(map[uuid.UUID]bool)
	for id, sched := range m.schedules {
		if sched.LecturerID != nil && *sched.LecturerID == lecturerID {
			ownedSchedules[id] = true
			summary.TotalSchedules++
		}
	}
	for _, enr := range m.enrollments {
		if ownedSchedules[enr.ClassScheduleID] && enr.Status == models.EnrollmentStatusEnrolled {
			summary.TotalEnrolled++
		}
	}
	for _, att := range m.attendances {
		if ownedSchedules[att.ClassScheduleID] {
			summary.TotalAttendances++
		}
	}
	return summary, nil
}

func (m *lecturerMemoryDashboardStore) ListSchedules(lecturerID uuid.UUID, filter LecturerScheduleFilter) ([]LecturerScheduleRow, error) {
	rows := []LecturerScheduleRow{}
	for _, sched := range m.schedules {
		if sched.LecturerID == nil || *sched.LecturerID != lecturerID {
			continue
		}
		if filter.AcademicTerm != "" {
			if sched.AcademicTerm == nil || !strings.EqualFold(strings.TrimSpace(*sched.AcademicTerm), strings.TrimSpace(filter.AcademicTerm)) {
				continue
			}
		}
		if filter.DayOfWeek != "" {
			if sched.DayOfWeek == nil || !strings.EqualFold(strings.TrimSpace(*sched.DayOfWeek), strings.TrimSpace(filter.DayOfWeek)) {
				continue
			}
		}

		var courseCode, courseName string
		var credits int
		if sched.CourseID != nil {
			if c, ok := m.courses[*sched.CourseID]; ok {
				courseCode = c.Code
				courseName = c.Name
				credits = c.Credits
			}
		}

		var roomCode *string
		if sched.RoomID != nil {
			if r, ok := m.rooms[*sched.RoomID]; ok {
				rc := r.Code
				roomCode = &rc
			}
		}

		enrolledCount := 0
		for _, enr := range m.enrollments {
			if enr.ClassScheduleID == sched.ID && enr.Status == models.EnrollmentStatusEnrolled {
				enrolledCount++
			}
		}

		rows = append(rows, LecturerScheduleRow{
			ID:            sched.ID,
			CourseID:      sched.CourseID,
			CourseCode:    courseCode,
			CourseName:    courseName,
			Credits:       credits,
			RoomID:        sched.RoomID,
			RoomCode:      roomCode,
			Section:       sched.Section,
			DayOfWeek:     sched.DayOfWeek,
			StartTime:     sched.StartTime,
			EndTime:       sched.EndTime,
			AcademicTerm:  sched.AcademicTerm,
			MaxCapacity:   sched.MaxCapacity,
			EnrolledCount: enrolledCount,
		})
	}
	slices.SortFunc(rows, func(a, b LecturerScheduleRow) int {
		return strings.Compare(a.CourseCode, b.CourseCode)
	})
	return rows, nil
}

func (m *lecturerMemoryDashboardStore) FindOwnedSchedule(lecturerID, scheduleID uuid.UUID) (*models.ClassSchedule, error) {
	sched, ok := m.schedules[scheduleID]
	if !ok || sched.LecturerID == nil || *sched.LecturerID != lecturerID {
		return nil, nil
	}
	cp := sched
	return &cp, nil
}

func (m *lecturerMemoryDashboardStore) ListScheduleStudents(scheduleID uuid.UUID) ([]LecturerRosterStudentRow, error) {
	rows := []LecturerRosterStudentRow{}
	for _, enr := range m.enrollments {
		if enr.ClassScheduleID != scheduleID || enr.Status != models.EnrollmentStatusEnrolled {
			continue
		}
		stu, ok := m.students[enr.StudentID]
		if !ok {
			continue
		}
		attCount := 0
		for _, att := range m.attendances {
			if att.ClassScheduleID == scheduleID && att.StudentID == stu.ID {
				attCount++
			}
		}
		rows = append(rows, LecturerRosterStudentRow{
			StudentID:       stu.ID,
			StudentBadgeID:  stu.StudentID,
			Name:            stu.Name,
			Department:      stu.Department,
			PhotoURL:        stu.PhotoURL,
			IsRegistered:    m.registered[stu.ID],
			EnrolledAt:      enr.EnrolledAt,
			AttendanceCount: attCount,
		})
	}
	slices.SortFunc(rows, func(a, b LecturerRosterStudentRow) int {
		return strings.Compare(a.Name, b.Name)
	})
	return rows, nil
}

func (m *lecturerMemoryDashboardStore) GetSessionAttendance(scheduleID uuid.UUID, sessionDate SessionDate) (*LecturerSessionAttendanceResponse, error) {
	students := []LecturerSessionStudentStatus{}
	present := 0
	for _, enr := range m.enrollments {
		if enr.ClassScheduleID != scheduleID || enr.Status != models.EnrollmentStatusEnrolled {
			continue
		}
		stu, ok := m.students[enr.StudentID]
		if !ok {
			continue
		}
		status := "absent"
		var attID *uuid.UUID
		var checkedInAt *time.Time
		for _, att := range m.attendances {
			if att.ClassScheduleID == scheduleID && att.StudentID == stu.ID && att.SessionDate.UTC().Format("2006-01-02") == sessionDate.String() {
				status = "present"
				idCopy := att.ID
				timeCopy := att.CheckedInAt
				attID = &idCopy
				checkedInAt = &timeCopy
				present++
				break
			}
		}
		students = append(students, LecturerSessionStudentStatus{
			StudentID:      stu.ID,
			StudentBadgeID: stu.StudentID,
			Name:           stu.Name,
			Department:     stu.Department,
			Status:         status,
			AttendanceID:   attID,
			CheckedInAt:    checkedInAt,
		})
	}
	slices.SortFunc(students, func(a, b LecturerSessionStudentStatus) int {
		return strings.Compare(a.Name, b.Name)
	})
	total := len(students)
	return &LecturerSessionAttendanceResponse{
		ScheduleID:  scheduleID,
		SessionDate: sessionDate.String(),
		Total:       total,
		Present:     present,
		Absent:      total - present,
		Students:    students,
	}, nil
}

func (m *lecturerMemoryDashboardStore) FindEnrolledStudent(scheduleID uuid.UUID, studentIdentifier string) (*models.Student, error) {
	trimmed := strings.TrimSpace(studentIdentifier)
	parsedUUID, uuidErr := uuid.Parse(trimmed)
	for _, enr := range m.enrollments {
		if enr.ClassScheduleID != scheduleID || enr.Status != models.EnrollmentStatusEnrolled {
			continue
		}
		stu, ok := m.students[enr.StudentID]
		if !ok {
			continue
		}
		if (uuidErr == nil && stu.ID == parsedUUID) || stu.StudentID == trimmed {
			cp := stu
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *lecturerMemoryDashboardStore) RecordAttendance(schedule *models.ClassSchedule, studentID uuid.UUID, sessionDate SessionDate) (*LecturerAttendanceRecordResponse, bool, error) {
	for _, att := range m.attendances {
		if att.ClassScheduleID == schedule.ID && att.StudentID == studentID && att.SessionDate.UTC().Format("2006-01-02") == sessionDate.String() {
			return &LecturerAttendanceRecordResponse{
				ID:          att.ID,
				ScheduleID:  schedule.ID,
				StudentID:   studentID,
				SessionDate: sessionDate.String(),
				CheckedInAt: att.CheckedInAt,
			}, false, nil
		}
	}

	roomID := uuid.Nil
	if schedule.RoomID != nil {
		roomID = *schedule.RoomID
	}
	now := m.currentClock()
	newAtt := models.Attendance{
		ID:              uuid.New(),
		StudentID:       studentID,
		RoomID:          roomID,
		ClassScheduleID: schedule.ID,
		SessionDate:     sessionDate.UTC(),
		CheckedInAt:     now,
	}
	m.attendances = append(m.attendances, newAtt)

	return &LecturerAttendanceRecordResponse{
		ID:          newAtt.ID,
		ScheduleID:  schedule.ID,
		StudentID:   studentID,
		SessionDate: sessionDate.String(),
		CheckedInAt: now,
	}, true, nil
}

func (m *lecturerMemoryDashboardStore) DeleteAttendance(scheduleID, studentID uuid.UUID, sessionDate SessionDate) (bool, error) {
	for i, att := range m.attendances {
		if att.ClassScheduleID == scheduleID && att.StudentID == studentID && att.SessionDate.UTC().Format("2006-01-02") == sessionDate.String() {
			m.attendances = append(m.attendances[:i], m.attendances[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func setupLecturerDashboardTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	lecturerGroup := r.Group("/dashboard/lecturer")
	{
		lecturerGroup.GET("/summary", GetLecturerDashboardSummary)
		lecturerGroup.GET("/schedules", ListLecturerSchedules)
		lecturerGroup.GET("/schedules/:scheduleId/students", ListLecturerScheduleStudents)
		lecturerGroup.GET("/schedules/:scheduleId/attendance", GetLecturerScheduleAttendance)
		lecturerGroup.POST("/schedules/:scheduleId/attendance", RecordLecturerScheduleAttendance)
		lecturerGroup.DELETE("/schedules/:scheduleId/attendance/:studentId", DeleteLecturerScheduleAttendance)
	}
	return r
}

func lecturerParseUUID(raw string) uuid.UUID {
	id, err := uuid.Parse(raw)
	if err != nil {
		panic(err)
	}
	return id
}

func lecturerStrPtr(v string) *string {
	return &v
}

func lecturerIntPtr(v int) *int {
	return &v
}

func TestLecturerDashboard(t *testing.T) {
	ClearPasskeySessions()
	t.Cleanup(func() {
		ClearPasskeySessions()
		SetLecturerDashboardStore(nil)
		SetLecturerDashboardClock(nil)
	})

	store := newMemoryLecturerDashboardStore()
	fixedNow := time.Date(2026, 11, 5, 9, 30, 0, 0, time.UTC)
	store.currentClock = func() time.Time { return fixedNow }
	SetLecturerDashboardStore(store)
	SetLecturerDashboardClock(func() time.Time { return fixedNow })

	facultyID := lecturerParseUUID("11111111-1111-1111-1111-111111111111")
	store.faculties[facultyID] = models.Faculty{
		ID:   facultyID,
		Code: "FTI",
		Name: "Faculty of Information Technology",
	}

	lecturerAID := lecturerParseUUID("22222222-2222-2222-2222-222222222221")
	lecturerBID := lecturerParseUUID("22222222-2222-2222-2222-222222222222")
	store.lecturers[lecturerAID] = models.Lecturer{
		ID:         lecturerAID,
		LecturerID: "LEC-001",
		Name:       "Dr. Budi Rahardjo",
		Department: lecturerStrPtr("Informatics"),
		FacultyID:  &facultyID,
	}
	store.lecturers[lecturerBID] = models.Lecturer{
		ID:         lecturerBID,
		LecturerID: "LEC-002",
		Name:       "Dr. Siti Aminah",
		Department: lecturerStrPtr("Information Systems"),
		FacultyID:  &facultyID,
	}

	course1ID := lecturerParseUUID("33333333-3333-3333-3333-333333333331")
	course2ID := lecturerParseUUID("33333333-3333-3333-3333-333333333332")
	course3ID := lecturerParseUUID("33333333-3333-3333-3333-333333333333")
	store.courses[course1ID] = models.Course{ID: course1ID, Code: "IF101", Name: "Algorithms", Credits: 4, FacultyID: &facultyID}
	store.courses[course2ID] = models.Course{ID: course2ID, Code: "IF202", Name: "Operating Systems", Credits: 3, FacultyID: &facultyID}
	store.courses[course3ID] = models.Course{ID: course3ID, Code: "IS301", Name: "Enterprise Architecture", Credits: 3, FacultyID: &facultyID}

	room1ID := lecturerParseUUID("44444444-4444-4444-4444-444444444441")
	store.rooms[room1ID] = models.Room{ID: room1ID, Code: "h.2.4", Floor: 2, RoomNumber: "4"}

	schedA1ID := lecturerParseUUID("55555555-5555-5555-5555-555555555551")
	schedA2ID := lecturerParseUUID("55555555-5555-5555-5555-555555555552")
	schedB1ID := lecturerParseUUID("55555555-5555-5555-5555-555555555553")

	store.schedules[schedA1ID] = models.ClassSchedule{
		ID:           schedA1ID,
		CourseID:     &course1ID,
		RoomID:       &room1ID,
		LecturerID:   &lecturerAID,
		Section:      lecturerStrPtr("A"),
		DayOfWeek:    lecturerStrPtr("Monday"),
		StartTime:    lecturerStrPtr("08:00"),
		EndTime:      lecturerStrPtr("10:00"),
		AcademicTerm: lecturerStrPtr("2026/2027-Ganjil"),
		MaxCapacity:  lecturerIntPtr(40),
	}
	store.schedules[schedA2ID] = models.ClassSchedule{
		ID:           schedA2ID,
		CourseID:     &course2ID,
		RoomID:       &room1ID,
		LecturerID:   &lecturerAID,
		Section:      lecturerStrPtr("B"),
		DayOfWeek:    lecturerStrPtr("Thursday"),
		StartTime:    lecturerStrPtr("13:00"),
		EndTime:      lecturerStrPtr("15:00"),
		AcademicTerm: lecturerStrPtr("2026/2027-Genap"),
		MaxCapacity:  lecturerIntPtr(35),
	}
	store.schedules[schedB1ID] = models.ClassSchedule{
		ID:           schedB1ID,
		CourseID:     &course3ID,
		RoomID:       &room1ID,
		LecturerID:   &lecturerBID,
		Section:      lecturerStrPtr("A"),
		DayOfWeek:    lecturerStrPtr("Tuesday"),
		StartTime:    lecturerStrPtr("10:00"),
		EndTime:      lecturerStrPtr("12:00"),
		AcademicTerm: lecturerStrPtr("2026/2027-Ganjil"),
		MaxCapacity:  lecturerIntPtr(30),
	}

	student1ID := lecturerParseUUID("66666666-6666-6666-6666-666666666661")
	student2ID := lecturerParseUUID("66666666-6666-6666-6666-666666666662")
	student3ID := lecturerParseUUID("66666666-6666-6666-6666-666666666663")

	store.students[student1ID] = models.Student{
		ID:         student1ID,
		StudentID:  "STU-001",
		Name:       "Andi Pratama",
		Department: lecturerStrPtr("Informatics"),
		FacultyID:  &facultyID,
	}
	store.students[student2ID] = models.Student{
		ID:         student2ID,
		StudentID:  "STU-002",
		Name:       "Citra Lestari",
		Department: lecturerStrPtr("Informatics"),
		FacultyID:  &facultyID,
	}
	store.students[student3ID] = models.Student{
		ID:         student3ID,
		StudentID:  "STU-003",
		Name:       "Dimas Anggara",
		Department: lecturerStrPtr("Information Systems"),
		FacultyID:  &facultyID,
	}
	store.registered[student1ID] = true
	store.registered[student2ID] = false

	enrolledTime := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	store.enrollments = []models.StudentSchedule{
		{ID: uuid.New(), StudentID: student1ID, ClassScheduleID: schedA1ID, Status: models.EnrollmentStatusEnrolled, EnrolledAt: enrolledTime},
		{ID: uuid.New(), StudentID: student2ID, ClassScheduleID: schedA1ID, Status: models.EnrollmentStatusEnrolled, EnrolledAt: enrolledTime},
		{ID: uuid.New(), StudentID: student1ID, ClassScheduleID: schedA2ID, Status: models.EnrollmentStatusEnrolled, EnrolledAt: enrolledTime},
		{ID: uuid.New(), StudentID: student3ID, ClassScheduleID: schedB1ID, Status: models.EnrollmentStatusEnrolled, EnrolledAt: enrolledTime},
	}

	initialSessionDate, _ := ParseSessionDate("2026-11-05")
	store.attendances = []models.Attendance{
		{
			ID:              uuid.New(),
			StudentID:       student1ID,
			RoomID:          room1ID,
			ClassScheduleID: schedA1ID,
			SessionDate:     initialSessionDate.UTC(),
			CheckedInAt:     fixedNow.Add(-15 * time.Minute),
		},
	}

	lecturerAToken := CreatePasskeySession(DashboardUserContext{
		UserID:      uuid.New(),
		Username:    "LEC-001",
		Email:       "budi@campus.ac.id",
		DisplayName: "Dr. Budi Rahardjo",
		UserType:    models.UserTypeLecturer,
		ProfileID:   &lecturerAID,
		FacultyID:   &facultyID,
		FacultyCode: lecturerStrPtr("FTI"),
		FacultyName: lecturerStrPtr("Faculty of Information Technology"),
	})

	studentProfileID := student1ID
	studentToken := CreatePasskeySession(DashboardUserContext{
		UserID:      uuid.New(),
		Username:    "STU-001",
		Email:       "andi@campus.ac.id",
		DisplayName: "Andi Pratama",
		UserType:    models.UserTypeStudent,
		ProfileID:   &studentProfileID,
	})

	empProfileID := uuid.New()
	empRole := models.JobRoleAcademicAffairs
	employeeToken := CreatePasskeySession(DashboardUserContext{
		UserID:      uuid.New(),
		Username:    "EMP-001",
		Email:       "staff@campus.ac.id",
		DisplayName: "Academic Staff",
		UserType:    models.UserTypeEmployee,
		ProfileID:   &empProfileID,
		JobRole:     &empRole,
	})

	router := setupLecturerDashboardTestRouter()

	t.Run("Authorization Enforcement", func(t *testing.T) {
		// Unauthenticated -> 401
		req := httptest.NewRequest(http.MethodGet, "/dashboard/lecturer/summary", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for unauthenticated request, got %d", w.Code)
		}

		// Student -> 403
		req = httptest.NewRequest(http.MethodGet, "/dashboard/lecturer/summary", nil)
		req.Header.Set("Authorization", "Bearer "+studentToken)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for student user, got %d", w.Code)
		}

		// Employee -> 403
		req = httptest.NewRequest(http.MethodGet, "/dashboard/lecturer/schedules", nil)
		req.Header.Set("Authorization", "Bearer "+employeeToken)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for employee user, got %d", w.Code)
		}
	})

	t.Run("Cross-Lecturer Ownership Enforcement Returns 404", func(t *testing.T) {
		// Roster for Lecturer B's schedule
		req := httptest.NewRequest(http.MethodGet, "/dashboard/lecturer/schedules/"+schedB1ID.String()+"/students", nil)
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when viewing another lecturer's schedule roster, got %d", w.Code)
		}

		// Attendance sheet for Lecturer B's schedule
		req = httptest.NewRequest(http.MethodGet, "/dashboard/lecturer/schedules/"+schedB1ID.String()+"/attendance?date=2026-11-05", nil)
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when viewing another lecturer's attendance sheet, got %d", w.Code)
		}

		// Manual check-in on Lecturer B's schedule
		body, _ := json.Marshal(LecturerRecordAttendanceRequest{StudentID: student3ID.String()})
		req = httptest.NewRequest(http.MethodPost, "/dashboard/lecturer/schedules/"+schedB1ID.String()+"/attendance", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when recording attendance on another lecturer's schedule, got %d", w.Code)
		}

		// Delete attendance on Lecturer B's schedule
		req = httptest.NewRequest(http.MethodDelete, "/dashboard/lecturer/schedules/"+schedB1ID.String()+"/attendance/"+student3ID.String()+"?date=2026-11-05", nil)
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when deleting attendance on another lecturer's schedule, got %d", w.Code)
		}
	})

	t.Run("GetLecturerDashboardSummary", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/lecturer/summary", nil)
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}

		var summary LecturerSummaryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil {
			t.Fatalf("failed to unmarshal summary: %v", err)
		}
		if summary.LecturerID != lecturerAID {
			t.Errorf("expected LecturerID %s, got %s", lecturerAID, summary.LecturerID)
		}
		if summary.LecturerName != "Dr. Budi Rahardjo" {
			t.Errorf("expected LecturerName 'Dr. Budi Rahardjo', got %q", summary.LecturerName)
		}
		if summary.FacultyCode == nil || *summary.FacultyCode != "FTI" {
			t.Errorf("expected FacultyCode 'FTI', got %v", summary.FacultyCode)
		}
		if summary.TotalSchedules != 2 {
			t.Errorf("expected TotalSchedules 2, got %d", summary.TotalSchedules)
		}
		if summary.TotalEnrolled != 3 {
			t.Errorf("expected TotalEnrolled 3, got %d", summary.TotalEnrolled)
		}
		if summary.TotalAttendances != 1 {
			t.Errorf("expected TotalAttendances 1, got %d", summary.TotalAttendances)
		}
	})

	t.Run("ListLecturerSchedules and Filters", func(t *testing.T) {
		// Unfiltered -> returns Lecturer A's 2 schedules
		req := httptest.NewRequest(http.MethodGet, "/dashboard/lecturer/schedules", nil)
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", w.Code)
		}
		var allSchedules []LecturerScheduleRow
		if err := json.Unmarshal(w.Body.Bytes(), &allSchedules); err != nil {
			t.Fatalf("failed to unmarshal schedules: %v", err)
		}
		if len(allSchedules) != 2 {
			t.Fatalf("expected 2 schedules for Lecturer A, got %d", len(allSchedules))
		}
		if allSchedules[0].CourseCode != "IF101" || allSchedules[0].EnrolledCount != 2 {
			t.Errorf("unexpected first schedule row: %+v", allSchedules[0])
		}

		// Filter by academicTerm and dayOfWeek
		req = httptest.NewRequest(http.MethodGet, "/dashboard/lecturer/schedules?academicTerm=2026/2027-Genap&dayOfWeek=thursday", nil)
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", w.Code)
		}
		var filtered []LecturerScheduleRow
		if err := json.Unmarshal(w.Body.Bytes(), &filtered); err != nil {
			t.Fatalf("failed to unmarshal filtered schedules: %v", err)
		}
		if len(filtered) != 1 || filtered[0].ID != schedA2ID {
			t.Fatalf("expected only schedule %s, got %+v", schedA2ID, filtered)
		}
	})

	t.Run("ListLecturerScheduleStudents", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/lecturer/schedules/"+schedA1ID.String()+"/students", nil)
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}

		var students []LecturerRosterStudentRow
		if err := json.Unmarshal(w.Body.Bytes(), &students); err != nil {
			t.Fatalf("failed to unmarshal students: %v", err)
		}
		if len(students) != 2 {
			t.Fatalf("expected 2 students in schedule A1, got %d", len(students))
		}
		if students[0].StudentID != student1ID || students[0].StudentBadgeID != "STU-001" || !students[0].IsRegistered || students[0].AttendanceCount != 1 {
			t.Errorf("unexpected student[0]: %+v", students[0])
		}
		if students[1].StudentID != student2ID || students[1].StudentBadgeID != "STU-002" || students[1].IsRegistered || students[1].AttendanceCount != 0 {
			t.Errorf("unexpected student[1]: %+v", students[1])
		}
	})

	t.Run("GetLecturerScheduleAttendance Present vs Absent", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/lecturer/schedules/"+schedA1ID.String()+"/attendance?date=2026-11-05", nil)
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}

		var sheet LecturerSessionAttendanceResponse
		if err := json.Unmarshal(w.Body.Bytes(), &sheet); err != nil {
			t.Fatalf("failed to unmarshal attendance sheet: %v", err)
		}
		if sheet.ScheduleID != schedA1ID || sheet.SessionDate != "2026-11-05" {
			t.Errorf("unexpected header fields: %+v", sheet)
		}
		if sheet.Total != 2 || sheet.Present != 1 || sheet.Absent != 1 {
			t.Errorf("expected Total=2, Present=1, Absent=1; got %+v", sheet)
		}
		if sheet.Students[0].StudentID != student1ID || sheet.Students[0].Status != "present" || sheet.Students[0].AttendanceID == nil {
			t.Errorf("expected student 1 to be present, got %+v", sheet.Students[0])
		}
		if sheet.Students[1].StudentID != student2ID || sheet.Students[1].Status != "absent" || sheet.Students[1].AttendanceID != nil {
			t.Errorf("expected student 2 to be absent, got %+v", sheet.Students[1])
		}
	})

	t.Run("RecordLecturerScheduleAttendance Manual Check-In and Non-Enrolled 404", func(t *testing.T) {
		// Non-enrolled student -> 404
		nonEnrolledPayload, _ := json.Marshal(LecturerRecordAttendanceRequest{
			StudentID: student3ID.String(),
			Date:      lecturerStrPtr("2026-11-05"),
		})
		req := httptest.NewRequest(http.MethodPost, "/dashboard/lecturer/schedules/"+schedA1ID.String()+"/attendance", bytes.NewReader(nonEnrolledPayload))
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for non-enrolled student, got %d: %s", w.Code, w.Body.String())
		}

		// Enrolled student2 check-in by badge ID (omitting date defaults to clock's 2026-11-05) -> 201 Created
		checkInPayload, _ := json.Marshal(LecturerRecordAttendanceRequest{
			StudentID: "STU-002",
		})
		req = httptest.NewRequest(http.MethodPost, "/dashboard/lecturer/schedules/"+schedA1ID.String()+"/attendance", bytes.NewReader(checkInPayload))
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for manual check-in, got %d: %s", w.Code, w.Body.String())
		}

		var rec LecturerAttendanceRecordResponse
		if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
			t.Fatalf("failed to unmarshal attendance record: %v", err)
		}
		if rec.StudentID != student2ID || rec.ScheduleID != schedA1ID || rec.SessionDate != "2026-11-05" {
			t.Errorf("unexpected attendance record response: %+v", rec)
		}

		// Idempotent repeat check-in -> 200 OK
		req = httptest.NewRequest(http.MethodPost, "/dashboard/lecturer/schedules/"+schedA1ID.String()+"/attendance", bytes.NewReader(checkInPayload))
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on idempotent manual check-in, got %d", w.Code)
		}
	})

	t.Run("DeleteLecturerScheduleAttendance Revoke", func(t *testing.T) {
		// Missing date query param -> 400
		req := httptest.NewRequest(http.MethodDelete, "/dashboard/lecturer/schedules/"+schedA1ID.String()+"/attendance/"+student2ID.String(), nil)
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 when date query param is missing, got %d", w.Code)
		}

		// Valid delete -> 200 OK {"deleted": true}
		req = httptest.NewRequest(http.MethodDelete, "/dashboard/lecturer/schedules/"+schedA1ID.String()+"/attendance/"+student2ID.String()+"?date=2026-11-05", nil)
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK when revoking attendance, got %d: %s", w.Code, w.Body.String())
		}
		var delResp map[string]bool
		if err := json.Unmarshal(w.Body.Bytes(), &delResp); err != nil || !delResp["deleted"] {
			t.Fatalf("expected {\"deleted\":true}, got %s", w.Body.String())
		}

		// Verify student2 is now absent again on the attendance sheet
		req = httptest.NewRequest(http.MethodGet, "/dashboard/lecturer/schedules/"+schedA1ID.String()+"/attendance?date=2026-11-05", nil)
		req.Header.Set("Authorization", "Bearer "+lecturerAToken)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var sheet LecturerSessionAttendanceResponse
		_ = json.Unmarshal(w.Body.Bytes(), &sheet)
		if sheet.Present != 1 || sheet.Absent != 1 {
			t.Fatalf("expected Present=1, Absent=1 after revoke, got Present=%d, Absent=%d", sheet.Present, sheet.Absent)
		}
	})
}
