package controllers

import (
	"cmp"
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"appsensi-server/database"
	"appsensi-server/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	studentAffairsErrStudentNotFound    = errors.New("student not found in faculty")
	studentAffairsErrDuplicateStudentID = errors.New("studentId already exists")
	studentAffairsErrScheduleNotFound   = errors.New("class schedule not found")
)

// StudentAffairsSummaryResponse represents the JSON payload returned by GET /dashboard/student-affairs/summary.
type StudentAffairsSummaryResponse struct {
	FacultyID          uuid.UUID `json:"facultyId"`
	FacultyCode        *string   `json:"facultyCode,omitempty"`
	FacultyName        *string   `json:"facultyName,omitempty"`
	TotalStudents      int       `json:"totalStudents"`
	RegisteredStudents int       `json:"registeredStudents"`
	TotalCourses       int       `json:"totalCourses"`
	TotalEnrollments   int       `json:"totalEnrollments"`
	TotalAttendances   int       `json:"totalAttendances"`
}

// StudentAffairsStudentRow represents a faculty-scoped student directory entry.
type StudentAffairsStudentRow struct {
	ID              uuid.UUID  `json:"id"`
	StudentID       string     `json:"studentId"`
	Name            string     `json:"name"`
	Department      *string    `json:"department"`
	FacultyID       *uuid.UUID `json:"facultyId"`
	PhotoURL        *string    `json:"photoUrl"`
	UserID          *uuid.UUID `json:"userId"`
	IsRegistered    bool       `json:"isRegistered"`
	EnrolledCourses int        `json:"enrolledCourses"`
	AttendanceCount int        `json:"attendanceCount"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// StudentAffairsUpsertStudentRequest represents the JSON payload for creating or updating a student.
type StudentAffairsUpsertStudentRequest struct {
	StudentID  string  `json:"studentId"`
	Name       string  `json:"name"`
	Department *string `json:"department"`
	PhotoURL   *string `json:"photoUrl"`
}

// StudentAffairsEnrollmentRow represents a student's class schedule enrollment entry.
type StudentAffairsEnrollmentRow struct {
	ID              uuid.UUID               `json:"id"`
	StudentID       uuid.UUID               `json:"studentId"`
	ClassScheduleID uuid.UUID               `json:"classScheduleId"`
	CourseCode      string                  `json:"courseCode"`
	CourseName      string                  `json:"courseName"`
	Credits         int                     `json:"credits"`
	Section         string                  `json:"section"`
	DayOfWeek       string                  `json:"dayOfWeek"`
	StartTime       string                  `json:"startTime"`
	EndTime         string                  `json:"endTime"`
	AcademicTerm    string                  `json:"academicTerm"`
	Status          models.EnrollmentStatus `json:"status"`
	EnrolledAt      time.Time               `json:"enrolledAt"`
}

// StudentAffairsEnrollRequest represents the JSON payload for enrolling a student into a class schedule.
type StudentAffairsEnrollRequest struct {
	ClassScheduleID string `json:"classScheduleId"`
}

// StudentAffairsAttendanceRow represents a faculty-scoped attendance record.
type StudentAffairsAttendanceRow struct {
	ID              uuid.UUID `json:"id"`
	StudentID       uuid.UUID `json:"studentId"`
	StudentBadgeID  string    `json:"studentBadgeId"`
	StudentName     string    `json:"studentName"`
	Department      *string   `json:"department"`
	ClassScheduleID uuid.UUID `json:"classScheduleId"`
	CourseCode      string    `json:"courseCode"`
	CourseName      string    `json:"courseName"`
	Section         string    `json:"section"`
	RoomCode        string    `json:"roomCode"`
	SessionDate     string    `json:"sessionDate"`
	CheckedInAt     time.Time `json:"checkedInAt"`
}

// StudentAffairsDashboardStore abstracts database operations for student affairs dashboard endpoints.
type StudentAffairsDashboardStore interface {
	GetSummary(facultyID uuid.UUID) (*StudentAffairsSummaryResponse, error)
	ListStudents(facultyID uuid.UUID, search, department string) ([]StudentAffairsStudentRow, error)
	CreateStudent(facultyID uuid.UUID, req StudentAffairsUpsertStudentRequest) (*StudentAffairsStudentRow, error)
	UpdateStudent(facultyID, studentUUID uuid.UUID, req StudentAffairsUpsertStudentRequest) (*StudentAffairsStudentRow, error)
	DeleteStudent(facultyID, studentUUID uuid.UUID) (bool, error)
	StudentExistsInFaculty(facultyID, studentUUID uuid.UUID) (bool, error)
	ListStudentSchedules(facultyID, studentUUID uuid.UUID) ([]StudentAffairsEnrollmentRow, error)
	EnrollStudentSchedule(facultyID, studentUUID, scheduleUUID uuid.UUID) (*StudentAffairsEnrollmentRow, error)
	DropStudentSchedule(facultyID, studentUUID, scheduleUUID uuid.UUID) (bool, error)
	ListAttendance(facultyID uuid.UUID, scheduleID, studentID *uuid.UUID, sessionDate *SessionDate) ([]StudentAffairsAttendanceRow, error)
}

type gormStudentAffairsDashboardStore struct{}

var _ StudentAffairsDashboardStore = gormStudentAffairsDashboardStore{}

type studentAffairsSummaryScanRow struct {
	FacultyCode        *string `gorm:"column:faculty_code"`
	FacultyName        *string `gorm:"column:faculty_name"`
	TotalStudents      int     `gorm:"column:total_students"`
	RegisteredStudents int     `gorm:"column:registered_students"`
	TotalCourses       int     `gorm:"column:total_courses"`
	TotalEnrollments   int     `gorm:"column:total_enrollments"`
	TotalAttendances   int     `gorm:"column:total_attendances"`
}

type studentAffairsStudentScanRow struct {
	ID              uuid.UUID  `gorm:"column:id"`
	StudentID       string     `gorm:"column:student_id"`
	Name            string     `gorm:"column:name"`
	Department      *string    `gorm:"column:department"`
	FacultyID       *uuid.UUID `gorm:"column:faculty_id"`
	PhotoURL        *string    `gorm:"column:photo_url"`
	UserID          *uuid.UUID `gorm:"column:user_id"`
	IsRegistered    bool       `gorm:"column:is_registered"`
	EnrolledCourses int        `gorm:"column:enrolled_courses"`
	AttendanceCount int        `gorm:"column:attendance_count"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
}

type studentAffairsEnrollmentScanRow struct {
	ID              uuid.UUID               `gorm:"column:id"`
	StudentID       uuid.UUID               `gorm:"column:student_id"`
	ClassScheduleID uuid.UUID               `gorm:"column:class_schedule_id"`
	CourseCode      string                  `gorm:"column:course_code"`
	CourseName      string                  `gorm:"column:course_name"`
	Credits         int                     `gorm:"column:credits"`
	Section         *string                 `gorm:"column:section"`
	DayOfWeek       *string                 `gorm:"column:day_of_week"`
	StartTime       *string                 `gorm:"column:start_time"`
	EndTime         *string                 `gorm:"column:end_time"`
	AcademicTerm    *string                 `gorm:"column:academic_term"`
	Status          models.EnrollmentStatus `gorm:"column:status"`
	EnrolledAt      time.Time               `gorm:"column:enrolled_at"`
}

type studentAffairsAttendanceScanRow struct {
	ID              uuid.UUID `gorm:"column:id"`
	StudentID       uuid.UUID `gorm:"column:student_id"`
	StudentBadgeID  string    `gorm:"column:student_badge_id"`
	StudentName     string    `gorm:"column:student_name"`
	Department      *string   `gorm:"column:department"`
	ClassScheduleID uuid.UUID `gorm:"column:class_schedule_id"`
	CourseCode      string    `gorm:"column:course_code"`
	CourseName      string    `gorm:"column:course_name"`
	Section         *string   `gorm:"column:section"`
	RoomCode        string    `gorm:"column:room_code"`
	SessionDate     time.Time `gorm:"column:session_date"`
	CheckedInAt     time.Time `gorm:"column:checked_in_at"`
}

func (gormStudentAffairsDashboardStore) GetSummary(facultyID uuid.UUID) (*StudentAffairsSummaryResponse, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	var row studentAffairsSummaryScanRow
	err := database.DB.Raw(`
		SELECT
			(SELECT code FROM faculties WHERE id = ?) AS faculty_code,
			(SELECT name FROM faculties WHERE id = ?) AS faculty_name,
			(SELECT COUNT(*) FROM students WHERE faculty_id = ?) AS total_students,
			(SELECT COUNT(*) FROM students WHERE faculty_id = ? AND (
				students.user_id IS NOT NULL
				OR EXISTS (
					SELECT 1 FROM users
					INNER JOIN credentials ON credentials.user_id = users.id
					WHERE users.username = students.student_id
				)
			)) AS registered_students,
			(SELECT COUNT(*) FROM courses WHERE faculty_id = ?) AS total_courses,
			(SELECT COUNT(*) FROM student_schedules
				INNER JOIN students ON students.id = student_schedules.student_id
				WHERE students.faculty_id = ? AND student_schedules.status = ?) AS total_enrollments,
			(SELECT COUNT(*) FROM attendance
				INNER JOIN students ON students.id = attendance.student_id
				WHERE students.faculty_id = ?) AS total_attendances
	`,
		facultyID,
		facultyID,
		facultyID,
		facultyID,
		facultyID,
		facultyID, models.EnrollmentStatusEnrolled,
		facultyID,
	).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	return &StudentAffairsSummaryResponse{
		FacultyID:          facultyID,
		FacultyCode:        row.FacultyCode,
		FacultyName:        row.FacultyName,
		TotalStudents:      row.TotalStudents,
		RegisteredStudents: row.RegisteredStudents,
		TotalCourses:       row.TotalCourses,
		TotalEnrollments:   row.TotalEnrollments,
		TotalAttendances:   row.TotalAttendances,
	}, nil
}

func studentAffairsQueryStudentsBase(db *gorm.DB, facultyID uuid.UUID) *gorm.DB {
	return db.Table("students").
		Select(`students.id,
			students.student_id,
			students.name,
			students.department,
			students.faculty_id,
			students.photo_url,
			COALESCE(students.user_id, u_name.id) AS user_id,
			(students.user_id IS NOT NULL OR cred_name.id IS NOT NULL) AS is_registered,
			COALESCE(enr.cnt, 0) AS enrolled_courses,
			COALESCE(att.cnt, 0) AS attendance_count,
			students.created_at`).
		Joins(`LEFT JOIN users AS u_name ON u_name.username = students.student_id AND students.user_id IS NULL`).
		Joins(`LEFT JOIN LATERAL (
			SELECT credentials.id
			FROM credentials
			WHERE credentials.user_id = u_name.id
			LIMIT 1
		) AS cred_name ON u_name.id IS NOT NULL`).
		Joins(`LEFT JOIN (
			SELECT student_id, COUNT(*) AS cnt
			FROM student_schedules
			WHERE status = ?
			GROUP BY student_id
		) AS enr ON enr.student_id = students.id`, models.EnrollmentStatusEnrolled).
		Joins(`LEFT JOIN (
			SELECT student_id, COUNT(*) AS cnt
			FROM attendance
			GROUP BY student_id
		) AS att ON att.student_id = students.id`).
		Where("students.faculty_id = ?", facultyID)
}

func (gormStudentAffairsDashboardStore) ListStudents(facultyID uuid.UUID, search, department string) ([]StudentAffairsStudentRow, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	q := studentAffairsQueryStudentsBase(database.DB, facultyID)
	if trimmedDept := strings.TrimSpace(department); trimmedDept != "" {
		q = q.Where("LOWER(TRIM(COALESCE(students.department, ''))) = LOWER(?)", trimmedDept)
	}
	if trimmedSearch := strings.TrimSpace(search); trimmedSearch != "" {
		likePattern := "%" + trimmedSearch + "%"
		q = q.Where("(students.student_id ILIKE ? OR students.name ILIKE ? OR COALESCE(students.department, '') ILIKE ?)", likePattern, likePattern, likePattern)
	}
	var scanRows []studentAffairsStudentScanRow
	if err := q.Order("students.student_id ASC, students.name ASC").Scan(&scanRows).Error; err != nil {
		return nil, err
	}
	out := make([]StudentAffairsStudentRow, 0, len(scanRows))
	for _, r := range scanRows {
		out = append(out, StudentAffairsStudentRow(r))
	}
	return out, nil
}

func (gormStudentAffairsDashboardStore) CreateStudent(facultyID uuid.UUID, req StudentAffairsUpsertStudentRequest) (*StudentAffairsStudentRow, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	var existingCount int64
	if err := database.DB.Model(&models.Student{}).Where("student_id = ?", req.StudentID).Limit(1).Count(&existingCount).Error; err != nil {
		return nil, err
	}
	if existingCount > 0 {
		return nil, studentAffairsErrDuplicateStudentID
	}
	facCopy := facultyID
	student := models.Student{
		StudentID:  req.StudentID,
		Name:       req.Name,
		Department: req.Department,
		FacultyID:  &facCopy,
		PhotoURL:   req.PhotoURL,
	}
	if err := database.DB.Create(&student).Error; err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, studentAffairsErrDuplicateStudentID
		}
		return nil, err
	}
	return &StudentAffairsStudentRow{
		ID:              student.ID,
		StudentID:       student.StudentID,
		Name:            student.Name,
		Department:      student.Department,
		FacultyID:       student.FacultyID,
		PhotoURL:        student.PhotoURL,
		UserID:          student.UserID,
		IsRegistered:    false,
		EnrolledCourses: 0,
		AttendanceCount: 0,
		CreatedAt:       student.CreatedAt,
	}, nil
}

func (s gormStudentAffairsDashboardStore) UpdateStudent(facultyID, studentUUID uuid.UUID, req StudentAffairsUpsertStudentRequest) (*StudentAffairsStudentRow, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	var student models.Student
	if err := database.DB.Where("id = ? AND faculty_id = ?", studentUUID, facultyID).Take(&student).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var dupCount int64
	if err := database.DB.Model(&models.Student{}).Where("student_id = ? AND id <> ?", req.StudentID, studentUUID).Limit(1).Count(&dupCount).Error; err != nil {
		return nil, err
	}
	if dupCount > 0 {
		return nil, studentAffairsErrDuplicateStudentID
	}
	updates := map[string]any{
		"student_id": req.StudentID,
		"name":       req.Name,
		"department": req.Department,
		"photo_url":  req.PhotoURL,
	}
	if err := database.DB.Model(&models.Student{}).Where("id = ? AND faculty_id = ?", studentUUID, facultyID).Updates(updates).Error; err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, studentAffairsErrDuplicateStudentID
		}
		return nil, err
	}
	var scanRow studentAffairsStudentScanRow
	if err := studentAffairsQueryStudentsBase(database.DB, facultyID).Where("students.id = ?", studentUUID).Take(&scanRow).Error; err != nil {
		return nil, err
	}
	out := StudentAffairsStudentRow(scanRow)
	return &out, nil
}

func (gormStudentAffairsDashboardStore) DeleteStudent(facultyID, studentUUID uuid.UUID) (bool, error) {
	if database.DB == nil {
		return false, errors.New("database is not initialized")
	}
	res := database.DB.Where("id = ? AND faculty_id = ?", studentUUID, facultyID).Delete(&models.Student{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (gormStudentAffairsDashboardStore) StudentExistsInFaculty(facultyID, studentUUID uuid.UUID) (bool, error) {
	if database.DB == nil {
		return false, errors.New("database is not initialized")
	}
	var count int64
	err := database.DB.Model(&models.Student{}).
		Where("id = ? AND faculty_id = ?", studentUUID, facultyID).
		Limit(1).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func studentAffairsConvertEnrollmentScanRow(r studentAffairsEnrollmentScanRow) StudentAffairsEnrollmentRow {
	return StudentAffairsEnrollmentRow{
		ID:              r.ID,
		StudentID:       r.StudentID,
		ClassScheduleID: r.ClassScheduleID,
		CourseCode:      r.CourseCode,
		CourseName:      r.CourseName,
		Credits:         r.Credits,
		Section:         studentAffairsPtrOrDefault(r.Section, ""),
		DayOfWeek:       studentAffairsPtrOrDefault(r.DayOfWeek, ""),
		StartTime:       studentAffairsPtrOrDefault(r.StartTime, ""),
		EndTime:         studentAffairsPtrOrDefault(r.EndTime, ""),
		AcademicTerm:    studentAffairsPtrOrDefault(r.AcademicTerm, ""),
		Status:          r.Status,
		EnrolledAt:      r.EnrolledAt,
	}
}

func (s gormStudentAffairsDashboardStore) ListStudentSchedules(facultyID, studentUUID uuid.UUID) ([]StudentAffairsEnrollmentRow, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	exists, err := s.StudentExistsInFaculty(facultyID, studentUUID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, studentAffairsErrStudentNotFound
	}
	var scanRows []studentAffairsEnrollmentScanRow
	err = database.DB.Table("student_schedules").
		Select(`student_schedules.id,
			student_schedules.student_id,
			student_schedules.class_schedule_id,
			courses.code AS course_code,
			courses.name AS course_name,
			courses.credits,
			class_schedules.section,
			class_schedules.day_of_week,
			class_schedules.start_time,
			class_schedules.end_time,
			class_schedules.academic_term,
			student_schedules.status,
			student_schedules.enrolled_at`).
		Joins("INNER JOIN students ON students.id = student_schedules.student_id").
		Joins("INNER JOIN class_schedules ON class_schedules.id = student_schedules.class_schedule_id").
		Joins("INNER JOIN courses ON courses.id = class_schedules.course_id").
		Where("students.faculty_id = ? AND student_schedules.student_id = ? AND student_schedules.status = ?", facultyID, studentUUID, models.EnrollmentStatusEnrolled).
		Order("courses.code ASC, class_schedules.section ASC").
		Scan(&scanRows).Error
	if err != nil {
		return nil, err
	}
	out := make([]StudentAffairsEnrollmentRow, 0, len(scanRows))
	for _, r := range scanRows {
		out = append(out, studentAffairsConvertEnrollmentScanRow(r))
	}
	return out, nil
}

func (s gormStudentAffairsDashboardStore) EnrollStudentSchedule(facultyID, studentUUID, scheduleUUID uuid.UUID) (*StudentAffairsEnrollmentRow, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	exists, err := s.StudentExistsInFaculty(facultyID, studentUUID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, studentAffairsErrStudentNotFound
	}
	var schedCount int64
	if err := database.DB.Model(&models.ClassSchedule{}).Where("id = ?", scheduleUUID).Limit(1).Count(&schedCount).Error; err != nil {
		return nil, err
	}
	if schedCount == 0 {
		return nil, studentAffairsErrScheduleNotFound
	}
	enrollment := models.StudentSchedule{
		StudentID:       studentUUID,
		ClassScheduleID: scheduleUUID,
		Status:          models.EnrollmentStatusEnrolled,
	}
	err = database.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "student_id"}, {Name: "class_schedule_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"status"}),
	}).Create(&enrollment).Error
	if err != nil {
		return nil, err
	}
	var scanRow studentAffairsEnrollmentScanRow
	err = database.DB.Table("student_schedules").
		Select(`student_schedules.id,
			student_schedules.student_id,
			student_schedules.class_schedule_id,
			courses.code AS course_code,
			courses.name AS course_name,
			courses.credits,
			class_schedules.section,
			class_schedules.day_of_week,
			class_schedules.start_time,
			class_schedules.end_time,
			class_schedules.academic_term,
			student_schedules.status,
			student_schedules.enrolled_at`).
		Joins("INNER JOIN class_schedules ON class_schedules.id = student_schedules.class_schedule_id").
		Joins("INNER JOIN courses ON courses.id = class_schedules.course_id").
		Where("student_schedules.student_id = ? AND student_schedules.class_schedule_id = ?", studentUUID, scheduleUUID).
		Take(&scanRow).Error
	if err != nil {
		return nil, err
	}
	out := studentAffairsConvertEnrollmentScanRow(scanRow)
	return &out, nil
}

func (s gormStudentAffairsDashboardStore) DropStudentSchedule(facultyID, studentUUID, scheduleUUID uuid.UUID) (bool, error) {
	if database.DB == nil {
		return false, errors.New("database is not initialized")
	}
	exists, err := s.StudentExistsInFaculty(facultyID, studentUUID)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, studentAffairsErrStudentNotFound
	}
	res := database.DB.Where("student_id = ? AND class_schedule_id = ?", studentUUID, scheduleUUID).Delete(&models.StudentSchedule{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (gormStudentAffairsDashboardStore) ListAttendance(facultyID uuid.UUID, scheduleID, studentID *uuid.UUID, sessionDate *SessionDate) ([]StudentAffairsAttendanceRow, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	q := database.DB.Table("attendance").
		Select(`attendance.id,
			attendance.student_id,
			students.student_id AS student_badge_id,
			students.name AS student_name,
			students.department,
			attendance.class_schedule_id,
			courses.code AS course_code,
			courses.name AS course_name,
			class_schedules.section,
			rooms.code AS room_code,
			attendance.session_date,
			attendance.checked_in_at`).
		Joins("INNER JOIN students ON students.id = attendance.student_id").
		Joins("INNER JOIN class_schedules ON class_schedules.id = attendance.class_schedule_id").
		Joins("INNER JOIN courses ON courses.id = class_schedules.course_id").
		Joins("INNER JOIN rooms ON rooms.id = attendance.room_id").
		Where("students.faculty_id = ?", facultyID)

	if scheduleID != nil {
		q = q.Where("attendance.class_schedule_id = ?", *scheduleID)
	}
	if studentID != nil {
		q = q.Where("attendance.student_id = ?", *studentID)
	}
	if sessionDate != nil {
		q = q.Where("attendance.session_date = ?", sessionDate.UTC())
	}

	var scanRows []studentAffairsAttendanceScanRow
	if err := q.Order("attendance.session_date DESC, attendance.checked_in_at DESC").Scan(&scanRows).Error; err != nil {
		return nil, err
	}

	out := make([]StudentAffairsAttendanceRow, 0, len(scanRows))
	for _, r := range scanRows {
		out = append(out, StudentAffairsAttendanceRow{
			ID:              r.ID,
			StudentID:       r.StudentID,
			StudentBadgeID:  r.StudentBadgeID,
			StudentName:     r.StudentName,
			Department:      r.Department,
			ClassScheduleID: r.ClassScheduleID,
			CourseCode:      r.CourseCode,
			CourseName:      r.CourseName,
			Section:         studentAffairsPtrOrDefault(r.Section, ""),
			RoomCode:        r.RoomCode,
			SessionDate:     r.SessionDate.UTC().Format("2006-01-02"),
			CheckedInAt:     r.CheckedInAt,
		})
	}
	return out, nil
}

var (
	studentAffairsDashboardStoreMu sync.RWMutex
	studentAffairsDashboardStore   StudentAffairsDashboardStore = gormStudentAffairsDashboardStore{}
)

// SetStudentAffairsDashboardStore overrides the persistence store for student affairs dashboard endpoints.
func SetStudentAffairsDashboardStore(store StudentAffairsDashboardStore) {
	studentAffairsDashboardStoreMu.Lock()
	defer studentAffairsDashboardStoreMu.Unlock()
	if store == nil {
		studentAffairsDashboardStore = gormStudentAffairsDashboardStore{}
		return
	}
	studentAffairsDashboardStore = store
}

func getStudentAffairsDashboardStore() StudentAffairsDashboardStore {
	studentAffairsDashboardStoreMu.RLock()
	defer studentAffairsDashboardStoreMu.RUnlock()
	return studentAffairsDashboardStore
}

func studentAffairsPtrOrDefault(ptr *string, fallback string) string {
	if ptr == nil {
		return fallback
	}
	return *ptr
}

func studentAffairsNormalizeOptionalString(ptr *string) *string {
	if ptr == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*ptr)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func studentAffairsParseUUID(raw string) (uuid.UUID, bool) {
	parsed, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == uuid.Nil {
		return uuid.Nil, false
	}
	return parsed, true
}

func studentAffairsSortStudentRows(rows []StudentAffairsStudentRow) {
	slices.SortFunc(rows, func(a, b StudentAffairsStudentRow) int {
		if c := cmp.Compare(a.StudentID, b.StudentID); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
}

// GetStudentAffairsDashboardSummary handles GET /dashboard/student-affairs/summary.
func GetStudentAffairsDashboardSummary(c *gin.Context) {
	userCtx, ok := RequireEmployeeRole(c, models.JobRoleStudentAffairs)
	if !ok {
		return
	}
	facultyID := *userCtx.FacultyID

	summary, err := getStudentAffairsDashboardStore().GetSummary(facultyID)
	if err != nil {
		log.Printf("GetStudentAffairsDashboardSummary error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load student affairs summary")
		return
	}
	summary.FacultyID = facultyID
	if summary.FacultyCode == nil && userCtx.FacultyCode != nil {
		code := *userCtx.FacultyCode
		summary.FacultyCode = &code
	}
	if summary.FacultyName == nil && userCtx.FacultyName != nil {
		name := *userCtx.FacultyName
		summary.FacultyName = &name
	}
	c.JSON(http.StatusOK, summary)
}

// ListStudentAffairsStudents handles GET /dashboard/student-affairs/students.
func ListStudentAffairsStudents(c *gin.Context) {
	userCtx, ok := RequireEmployeeRole(c, models.JobRoleStudentAffairs)
	if !ok {
		return
	}
	facultyID := *userCtx.FacultyID

	search := strings.TrimSpace(c.Query("search"))
	department := strings.TrimSpace(c.Query("department"))

	rows, err := getStudentAffairsDashboardStore().ListStudents(facultyID, search, department)
	if err != nil {
		log.Printf("ListStudentAffairsStudents error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load faculty students")
		return
	}
	if rows == nil {
		rows = []StudentAffairsStudentRow{}
	}
	c.JSON(http.StatusOK, rows)
}

// CreateStudentAffairsStudent handles POST /dashboard/student-affairs/students.
func CreateStudentAffairsStudent(c *gin.Context) {
	userCtx, ok := RequireEmployeeRole(c, models.JobRoleStudentAffairs)
	if !ok {
		return
	}
	facultyID := *userCtx.FacultyID

	var req StudentAffairsUpsertStudentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid JSON request body")
		return
	}
	req.StudentID = strings.TrimSpace(req.StudentID)
	req.Name = strings.TrimSpace(req.Name)
	req.Department = studentAffairsNormalizeOptionalString(req.Department)
	req.PhotoURL = studentAffairsNormalizeOptionalString(req.PhotoURL)

	if req.StudentID == "" || req.Name == "" {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "studentId and name are required")
		return
	}

	created, err := getStudentAffairsDashboardStore().CreateStudent(facultyID, req)
	if err != nil {
		if errors.Is(err, studentAffairsErrDuplicateStudentID) {
			respondAPIError(c, http.StatusConflict, ErrCodeConflict, "Student with this studentId already exists")
			return
		}
		log.Printf("CreateStudentAffairsStudent error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to create student")
		return
	}
	c.JSON(http.StatusCreated, created)
}

// UpdateStudentAffairsStudent handles PUT /dashboard/student-affairs/students/:id.
func UpdateStudentAffairsStudent(c *gin.Context) {
	userCtx, ok := RequireEmployeeRole(c, models.JobRoleStudentAffairs)
	if !ok {
		return
	}
	facultyID := *userCtx.FacultyID

	studentUUID, valid := studentAffairsParseUUID(c.Param("id"))
	if !valid {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid student id UUID")
		return
	}

	var req StudentAffairsUpsertStudentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid JSON request body")
		return
	}
	req.StudentID = strings.TrimSpace(req.StudentID)
	req.Name = strings.TrimSpace(req.Name)
	req.Department = studentAffairsNormalizeOptionalString(req.Department)
	req.PhotoURL = studentAffairsNormalizeOptionalString(req.PhotoURL)

	if req.StudentID == "" || req.Name == "" {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "studentId and name are required")
		return
	}

	updated, err := getStudentAffairsDashboardStore().UpdateStudent(facultyID, studentUUID, req)
	if err != nil {
		if errors.Is(err, studentAffairsErrDuplicateStudentID) {
			respondAPIError(c, http.StatusConflict, ErrCodeConflict, "Student with this studentId already exists")
			return
		}
		log.Printf("UpdateStudentAffairsStudent error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to update student")
		return
	}
	if updated == nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Student not found in your faculty")
		return
	}
	c.JSON(http.StatusOK, updated)
}

// DeleteStudentAffairsStudent handles DELETE /dashboard/student-affairs/students/:id.
func DeleteStudentAffairsStudent(c *gin.Context) {
	userCtx, ok := RequireEmployeeRole(c, models.JobRoleStudentAffairs)
	if !ok {
		return
	}
	facultyID := *userCtx.FacultyID

	studentUUID, valid := studentAffairsParseUUID(c.Param("id"))
	if !valid {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid student id UUID")
		return
	}

	deleted, err := getStudentAffairsDashboardStore().DeleteStudent(facultyID, studentUUID)
	if err != nil {
		log.Printf("DeleteStudentAffairsStudent error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete student")
		return
	}
	if !deleted {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Student not found in your faculty")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"deleted": true,
		"id":      studentUUID,
	})
}

// ListStudentAffairsStudentSchedules handles GET /dashboard/student-affairs/students/:id/schedules.
func ListStudentAffairsStudentSchedules(c *gin.Context) {
	userCtx, ok := RequireEmployeeRole(c, models.JobRoleStudentAffairs)
	if !ok {
		return
	}
	facultyID := *userCtx.FacultyID

	studentUUID, valid := studentAffairsParseUUID(c.Param("id"))
	if !valid {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid student id UUID")
		return
	}

	rows, err := getStudentAffairsDashboardStore().ListStudentSchedules(facultyID, studentUUID)
	if err != nil {
		if errors.Is(err, studentAffairsErrStudentNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Student not found in your faculty")
			return
		}
		log.Printf("ListStudentAffairsStudentSchedules error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load student schedule enrollments")
		return
	}
	if rows == nil {
		rows = []StudentAffairsEnrollmentRow{}
	}
	c.JSON(http.StatusOK, rows)
}

// EnrollStudentAffairsStudentSchedule handles POST /dashboard/student-affairs/students/:id/schedules.
func EnrollStudentAffairsStudentSchedule(c *gin.Context) {
	userCtx, ok := RequireEmployeeRole(c, models.JobRoleStudentAffairs)
	if !ok {
		return
	}
	facultyID := *userCtx.FacultyID

	studentUUID, valid := studentAffairsParseUUID(c.Param("id"))
	if !valid {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid student id UUID")
		return
	}

	var req StudentAffairsEnrollRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid JSON request body")
		return
	}
	scheduleUUID, valid := studentAffairsParseUUID(req.ClassScheduleID)
	if !valid {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid classScheduleId UUID")
		return
	}

	enrolled, err := getStudentAffairsDashboardStore().EnrollStudentSchedule(facultyID, studentUUID, scheduleUUID)
	if err != nil {
		if errors.Is(err, studentAffairsErrStudentNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Student not found in your faculty")
			return
		}
		if errors.Is(err, studentAffairsErrScheduleNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Class schedule not found")
			return
		}
		log.Printf("EnrollStudentAffairsStudentSchedule error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to enroll student in schedule")
		return
	}
	c.JSON(http.StatusCreated, enrolled)
}

// DropStudentAffairsStudentSchedule handles DELETE /dashboard/student-affairs/students/:id/schedules/:scheduleId.
func DropStudentAffairsStudentSchedule(c *gin.Context) {
	userCtx, ok := RequireEmployeeRole(c, models.JobRoleStudentAffairs)
	if !ok {
		return
	}
	facultyID := *userCtx.FacultyID

	studentUUID, valid := studentAffairsParseUUID(c.Param("id"))
	if !valid {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid student id UUID")
		return
	}
	scheduleUUID, valid := studentAffairsParseUUID(c.Param("scheduleId"))
	if !valid {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid scheduleId UUID")
		return
	}

	dropped, err := getStudentAffairsDashboardStore().DropStudentSchedule(facultyID, studentUUID, scheduleUUID)
	if err != nil {
		if errors.Is(err, studentAffairsErrStudentNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Student not found in your faculty")
			return
		}
		log.Printf("DropStudentAffairsStudentSchedule error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to drop student schedule")
		return
	}
	if !dropped {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Student schedule enrollment not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"dropped":         true,
		"studentId":       studentUUID,
		"classScheduleId": scheduleUUID,
	})
}

// ListStudentAffairsAttendance handles GET /dashboard/student-affairs/attendance.
func ListStudentAffairsAttendance(c *gin.Context) {
	userCtx, ok := RequireEmployeeRole(c, models.JobRoleStudentAffairs)
	if !ok {
		return
	}
	facultyID := *userCtx.FacultyID

	var scheduleID *uuid.UUID
	if rawSched := strings.TrimSpace(c.Query("scheduleId")); rawSched != "" {
		parsed, valid := studentAffairsParseUUID(rawSched)
		if !valid {
			respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid scheduleId UUID")
			return
		}
		scheduleID = &parsed
	}

	var studentID *uuid.UUID
	if rawStud := strings.TrimSpace(c.Query("studentId")); rawStud != "" {
		parsed, valid := studentAffairsParseUUID(rawStud)
		if !valid {
			respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid studentId UUID")
			return
		}
		studentID = &parsed
	}

	var sessionDate *SessionDate
	if rawDate := strings.TrimSpace(c.Query("date")); rawDate != "" {
		parsedDate, err := ParseSessionDate(rawDate)
		if err != nil {
			respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
			return
		}
		sessionDate = &parsedDate
	}

	rows, err := getStudentAffairsDashboardStore().ListAttendance(facultyID, scheduleID, studentID, sessionDate)
	if err != nil {
		log.Printf("ListStudentAffairsAttendance error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load faculty attendance report")
		return
	}
	if rows == nil {
		rows = []StudentAffairsAttendanceRow{}
	}
	c.JSON(http.StatusOK, rows)
}
