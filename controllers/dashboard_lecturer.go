package controllers

import (
	"errors"
	"net/http"
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

// LecturerSummaryResponse represents the summary metrics for a lecturer's dashboard.
type LecturerSummaryResponse struct {
	LecturerID       uuid.UUID  `json:"lecturerId"`
	LecturerName     string     `json:"lecturerName"`
	FacultyID        *uuid.UUID `json:"facultyId,omitempty"`
	FacultyCode      *string    `json:"facultyCode,omitempty"`
	FacultyName      *string    `json:"facultyName,omitempty"`
	TotalSchedules   int        `json:"totalSchedules"`
	TotalEnrolled    int        `json:"totalEnrolled"`
	TotalAttendances int        `json:"totalAttendances"`
}

// LecturerScheduleFilter holds optional query filters when listing lecturer schedules.
type LecturerScheduleFilter struct {
	AcademicTerm string
	DayOfWeek    string
}

// LecturerScheduleRow represents a class schedule assigned to the authenticated lecturer.
type LecturerScheduleRow struct {
	ID            uuid.UUID  `json:"id" gorm:"column:id"`
	CourseID      *uuid.UUID `json:"courseId" gorm:"column:course_id"`
	CourseCode    string     `json:"courseCode" gorm:"column:course_code"`
	CourseName    string     `json:"courseName" gorm:"column:course_name"`
	Credits       int        `json:"credits" gorm:"column:credits"`
	RoomID        *uuid.UUID `json:"roomId" gorm:"column:room_id"`
	RoomCode      *string    `json:"roomCode" gorm:"column:room_code"`
	Section       *string    `json:"section" gorm:"column:section"`
	DayOfWeek     *string    `json:"dayOfWeek" gorm:"column:day_of_week"`
	StartTime     *string    `json:"startTime" gorm:"column:start_time"`
	EndTime       *string    `json:"endTime" gorm:"column:end_time"`
	AcademicTerm  *string    `json:"academicTerm" gorm:"column:academic_term"`
	MaxCapacity   *int       `json:"maxCapacity" gorm:"column:max_capacity"`
	EnrolledCount int        `json:"enrolledCount" gorm:"column:enrolled_count"`
}

// LecturerRosterStudentRow represents an enrolled student in a lecturer's class schedule.
type LecturerRosterStudentRow struct {
	StudentID       uuid.UUID `json:"studentId" gorm:"column:student_id"`
	StudentBadgeID  string    `json:"studentBadgeId" gorm:"column:student_badge_id"`
	Name            string    `json:"name" gorm:"column:name"`
	Department      *string   `json:"department" gorm:"column:department"`
	PhotoURL        *string   `json:"photoUrl" gorm:"column:photo_url"`
	IsRegistered    bool      `json:"isRegistered" gorm:"column:is_registered"`
	EnrolledAt      time.Time `json:"enrolledAt" gorm:"column:enrolled_at"`
	AttendanceCount int       `json:"attendanceCount" gorm:"column:attendance_count"`
}

// LecturerSessionStudentStatus represents a single enrolled student's attendance state for a session date.
type LecturerSessionStudentStatus struct {
	StudentID      uuid.UUID  `json:"studentId"`
	StudentBadgeID string     `json:"studentBadgeId"`
	Name           string     `json:"name"`
	Department     *string    `json:"department"`
	Status         string     `json:"status"` // "present" or "absent"
	AttendanceID   *uuid.UUID `json:"attendanceId,omitempty"`
	CheckedInAt    *time.Time `json:"checkedInAt,omitempty"`
}

// LecturerSessionAttendanceResponse represents the attendance sheet for a class schedule on a session date.
type LecturerSessionAttendanceResponse struct {
	ScheduleID  uuid.UUID                      `json:"scheduleId"`
	SessionDate string                         `json:"sessionDate"`
	Total       int                            `json:"total"`
	Present     int                            `json:"present"`
	Absent      int                            `json:"absent"`
	Students    []LecturerSessionStudentStatus `json:"students"`
}

// LecturerRecordAttendanceRequest is the JSON payload for manual attendance check-in by a lecturer.
type LecturerRecordAttendanceRequest struct {
	StudentID string  `json:"studentId"`
	Date      *string `json:"date,omitempty"`
}

// LecturerAttendanceRecordResponse is returned when a lecturer records an attendance entry.
type LecturerAttendanceRecordResponse struct {
	ID          uuid.UUID `json:"id"`
	ScheduleID  uuid.UUID `json:"scheduleId"`
	StudentID   uuid.UUID `json:"studentId"`
	SessionDate string    `json:"sessionDate"`
	CheckedInAt time.Time `json:"checkedInAt"`
}

// LecturerDashboardStore abstracts persistence operations for the lecturer dashboard endpoints.
type LecturerDashboardStore interface {
	GetSummary(lecturerID uuid.UUID) (*LecturerSummaryResponse, error)
	ListSchedules(lecturerID uuid.UUID, filter LecturerScheduleFilter) ([]LecturerScheduleRow, error)
	FindOwnedSchedule(lecturerID, scheduleID uuid.UUID) (*models.ClassSchedule, error)
	ListScheduleStudents(scheduleID uuid.UUID) ([]LecturerRosterStudentRow, error)
	GetSessionAttendance(scheduleID uuid.UUID, sessionDate SessionDate) (*LecturerSessionAttendanceResponse, error)
	FindEnrolledStudent(scheduleID uuid.UUID, studentIdentifier string) (*models.Student, error)
	RecordAttendance(schedule *models.ClassSchedule, studentID uuid.UUID, sessionDate SessionDate) (*LecturerAttendanceRecordResponse, bool, error)
	DeleteAttendance(scheduleID, studentID uuid.UUID, sessionDate SessionDate) (bool, error)
}

type gormLecturerDashboardStore struct{}

var _ LecturerDashboardStore = gormLecturerDashboardStore{}

type lecturerProfileSummaryScan struct {
	ID          uuid.UUID  `gorm:"column:id"`
	Name        string     `gorm:"column:name"`
	FacultyID   *uuid.UUID `gorm:"column:faculty_id"`
	FacultyCode *string    `gorm:"column:faculty_code"`
	FacultyName *string    `gorm:"column:faculty_name"`
}

func (gormLecturerDashboardStore) GetSummary(lecturerID uuid.UUID) (*LecturerSummaryResponse, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}

	var profile lecturerProfileSummaryScan
	err := database.DB.Table("lecturers").
		Select("lecturers.id, lecturers.name, lecturers.faculty_id, faculties.code AS faculty_code, faculties.name AS faculty_name").
		Joins("LEFT JOIN faculties ON faculties.id = lecturers.faculty_id").
		Where("lecturers.id = ?", lecturerID).
		Scan(&profile).Error
	if err != nil {
		return nil, err
	}

	var totalSchedules int64
	if err := database.DB.Model(&models.ClassSchedule{}).
		Where("lecturer_id = ?", lecturerID).
		Count(&totalSchedules).Error; err != nil {
		return nil, err
	}

	var totalEnrolled int64
	if err := database.DB.Table("student_schedules").
		Joins("INNER JOIN class_schedules ON class_schedules.id = student_schedules.class_schedule_id").
		Where("class_schedules.lecturer_id = ? AND student_schedules.status = ?", lecturerID, models.EnrollmentStatusEnrolled).
		Count(&totalEnrolled).Error; err != nil {
		return nil, err
	}

	var totalAttendances int64
	if err := database.DB.Table("attendance").
		Joins("INNER JOIN class_schedules ON class_schedules.id = attendance.class_schedule_id").
		Where("class_schedules.lecturer_id = ?", lecturerID).
		Count(&totalAttendances).Error; err != nil {
		return nil, err
	}

	return &LecturerSummaryResponse{
		LecturerID:       lecturerID,
		LecturerName:     profile.Name,
		FacultyID:        profile.FacultyID,
		FacultyCode:      profile.FacultyCode,
		FacultyName:      profile.FacultyName,
		TotalSchedules:   int(totalSchedules),
		TotalEnrolled:    int(totalEnrolled),
		TotalAttendances: int(totalAttendances),
	}, nil
}

func (gormLecturerDashboardStore) ListSchedules(lecturerID uuid.UUID, filter LecturerScheduleFilter) ([]LecturerScheduleRow, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}

	query := database.DB.Table("class_schedules").
		Select(`class_schedules.id,
			class_schedules.course_id,
			COALESCE(courses.code, '') AS course_code,
			COALESCE(courses.name, '') AS course_name,
			COALESCE(courses.credits, 0) AS credits,
			class_schedules.room_id,
			rooms.code AS room_code,
			class_schedules.section,
			class_schedules.day_of_week,
			class_schedules.start_time,
			class_schedules.end_time,
			class_schedules.academic_term,
			class_schedules.max_capacity,
			(SELECT COUNT(1) FROM student_schedules ss WHERE ss.class_schedule_id = class_schedules.id AND ss.status = ?) AS enrolled_count`,
			models.EnrollmentStatusEnrolled).
		Joins("LEFT JOIN courses ON courses.id = class_schedules.course_id").
		Joins("LEFT JOIN rooms ON rooms.id = class_schedules.room_id").
		Where("class_schedules.lecturer_id = ?", lecturerID)

	if term := strings.TrimSpace(filter.AcademicTerm); term != "" {
		query = query.Where("lower(trim(class_schedules.academic_term)) = lower(?)", term)
	}
	if day := strings.TrimSpace(filter.DayOfWeek); day != "" {
		query = query.Where("lower(trim(class_schedules.day_of_week)) = lower(?)", day)
	}

	var rows []LecturerScheduleRow
	if err := query.Order("courses.code ASC, class_schedules.section ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []LecturerScheduleRow{}
	}
	return rows, nil
}

func (gormLecturerDashboardStore) FindOwnedSchedule(lecturerID, scheduleID uuid.UUID) (*models.ClassSchedule, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}

	var schedule models.ClassSchedule
	err := database.DB.Where("id = ? AND lecturer_id = ?", scheduleID, lecturerID).Take(&schedule).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &schedule, nil
}

func (gormLecturerDashboardStore) ListScheduleStudents(scheduleID uuid.UUID) ([]LecturerRosterStudentRow, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}

	var rows []LecturerRosterStudentRow
	err := database.DB.Table("student_schedules").
		Select(`students.id AS student_id,
			students.student_id AS student_badge_id,
			students.name,
			students.department,
			students.photo_url,
			EXISTS (
				SELECT 1
				FROM users u
				INNER JOIN credentials cred ON cred.user_id = u.id
				WHERE u.id = students.user_id OR u.username = students.student_id
			) AS is_registered,
			student_schedules.enrolled_at,
			(
				SELECT COUNT(1)
				FROM attendance a
				WHERE a.student_id = students.id AND a.class_schedule_id = student_schedules.class_schedule_id
			) AS attendance_count`).
		Joins("INNER JOIN students ON students.id = student_schedules.student_id").
		Where("student_schedules.class_schedule_id = ? AND student_schedules.status = ?", scheduleID, models.EnrollmentStatusEnrolled).
		Order("students.name ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []LecturerRosterStudentRow{}
	}
	return rows, nil
}

type lecturerSessionStudentScan struct {
	StudentID      uuid.UUID  `gorm:"column:student_id"`
	StudentBadgeID string     `gorm:"column:student_badge_id"`
	Name           string     `gorm:"column:name"`
	Department     *string    `gorm:"column:department"`
	AttendanceID   *uuid.UUID `gorm:"column:attendance_id"`
	CheckedInAt    *time.Time `gorm:"column:checked_in_at"`
}

func (gormLecturerDashboardStore) GetSessionAttendance(scheduleID uuid.UUID, sessionDate SessionDate) (*LecturerSessionAttendanceResponse, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}

	var scans []lecturerSessionStudentScan
	err := database.DB.Table("student_schedules").
		Select(`students.id AS student_id,
			students.student_id AS student_badge_id,
			students.name,
			students.department,
			attendance.id AS attendance_id,
			attendance.checked_in_at`).
		Joins("INNER JOIN students ON students.id = student_schedules.student_id").
		Joins("LEFT JOIN attendance ON attendance.student_id = students.id AND attendance.class_schedule_id = student_schedules.class_schedule_id AND attendance.session_date = ?", sessionDate.UTC()).
		Where("student_schedules.class_schedule_id = ? AND student_schedules.status = ?", scheduleID, models.EnrollmentStatusEnrolled).
		Order("students.name ASC").
		Scan(&scans).Error
	if err != nil {
		return nil, err
	}

	students := make([]LecturerSessionStudentStatus, 0, len(scans))
	presentCount := 0
	for _, s := range scans {
		status := "absent"
		if s.AttendanceID != nil && *s.AttendanceID != uuid.Nil {
			status = "present"
			presentCount++
		}
		students = append(students, LecturerSessionStudentStatus{
			StudentID:      s.StudentID,
			StudentBadgeID: s.StudentBadgeID,
			Name:           s.Name,
			Department:     s.Department,
			Status:         status,
			AttendanceID:   s.AttendanceID,
			CheckedInAt:    s.CheckedInAt,
		})
	}

	total := len(students)
	return &LecturerSessionAttendanceResponse{
		ScheduleID:  scheduleID,
		SessionDate: sessionDate.String(),
		Total:       total,
		Present:     presentCount,
		Absent:      total - presentCount,
		Students:    students,
	}, nil
}

func (gormLecturerDashboardStore) FindEnrolledStudent(scheduleID uuid.UUID, studentIdentifier string) (*models.Student, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}

	trimmed := strings.TrimSpace(studentIdentifier)
	if trimmed == "" {
		return nil, nil
	}

	var student models.Student
	baseQuery := database.DB.Table("students").
		Select("students.*").
		Joins("INNER JOIN student_schedules ON student_schedules.student_id = students.id").
		Where("student_schedules.class_schedule_id = ? AND student_schedules.status = ?", scheduleID, models.EnrollmentStatusEnrolled)

	if parsedUUID, err := uuid.Parse(trimmed); err == nil && parsedUUID != uuid.Nil {
		err := baseQuery.Where("students.id = ?", parsedUUID).Take(&student).Error
		if err == nil {
			return &student, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	err := database.DB.Table("students").
		Select("students.*").
		Joins("INNER JOIN student_schedules ON student_schedules.student_id = students.id").
		Where("student_schedules.class_schedule_id = ? AND student_schedules.status = ? AND students.student_id = ?", scheduleID, models.EnrollmentStatusEnrolled, trimmed).
		Take(&student).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &student, nil
}

func (gormLecturerDashboardStore) RecordAttendance(schedule *models.ClassSchedule, studentID uuid.UUID, sessionDate SessionDate) (*LecturerAttendanceRecordResponse, bool, error) {
	if database.DB == nil {
		return nil, false, errors.New("database is not initialized")
	}

	roomID := uuid.Nil
	if schedule.RoomID != nil {
		roomID = *schedule.RoomID
	}

	att := models.Attendance{
		StudentID:       studentID,
		RoomID:          roomID,
		ClassScheduleID: schedule.ID,
		SessionDate:     sessionDate.UTC(),
	}

	res := database.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&att)
	if res.Error != nil {
		return nil, false, res.Error
	}
	if res.RowsAffected > 0 {
		return &LecturerAttendanceRecordResponse{
			ID:          att.ID,
			ScheduleID:  schedule.ID,
			StudentID:   studentID,
			SessionDate: sessionDate.String(),
			CheckedInAt: att.CheckedInAt,
		}, true, nil
	}

	var existing models.Attendance
	if err := database.DB.Where("student_id = ? AND class_schedule_id = ? AND session_date = ?", studentID, schedule.ID, sessionDate.UTC()).Take(&existing).Error; err != nil {
		return nil, false, err
	}
	return &LecturerAttendanceRecordResponse{
		ID:          existing.ID,
		ScheduleID:  schedule.ID,
		StudentID:   studentID,
		SessionDate: sessionDate.String(),
		CheckedInAt: existing.CheckedInAt,
	}, false, nil
}

func (gormLecturerDashboardStore) DeleteAttendance(scheduleID, studentID uuid.UUID, sessionDate SessionDate) (bool, error) {
	if database.DB == nil {
		return false, errors.New("database is not initialized")
	}

	res := database.DB.Where("student_id = ? AND class_schedule_id = ? AND session_date = ?", studentID, scheduleID, sessionDate.UTC()).
		Delete(&models.Attendance{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

var (
	lecturerDashboardStoreMu sync.RWMutex
	lecturerDashboardStore   LecturerDashboardStore = gormLecturerDashboardStore{}
	lecturerDashboardNow     func() time.Time       = time.Now
)

// SetLecturerDashboardStore overrides the persistence store for lecturer dashboard endpoints (used in tests).
func SetLecturerDashboardStore(store LecturerDashboardStore) {
	lecturerDashboardStoreMu.Lock()
	defer lecturerDashboardStoreMu.Unlock()
	if store == nil {
		lecturerDashboardStore = gormLecturerDashboardStore{}
		return
	}
	lecturerDashboardStore = store
}

// SetLecturerDashboardClock overrides the clock used by lecturer dashboard endpoints (used in tests).
func SetLecturerDashboardClock(clock func() time.Time) {
	lecturerDashboardStoreMu.Lock()
	defer lecturerDashboardStoreMu.Unlock()
	if clock == nil {
		lecturerDashboardNow = time.Now
		return
	}
	lecturerDashboardNow = clock
}

func lecturerGetDashboardDeps() (LecturerDashboardStore, func() time.Time) {
	lecturerDashboardStoreMu.RLock()
	defer lecturerDashboardStoreMu.RUnlock()
	return lecturerDashboardStore, lecturerDashboardNow
}

func lecturerParseScheduleIDParam(c *gin.Context) (uuid.UUID, bool) {
	rawID := strings.TrimSpace(c.Param("scheduleId"))
	parsed, err := uuid.Parse(rawID)
	if err != nil || parsed == uuid.Nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid scheduleId UUID")
		return uuid.Nil, false
	}
	return parsed, true
}

// GetLecturerDashboardSummary handles GET /dashboard/lecturer/summary.
func GetLecturerDashboardSummary(c *gin.Context) {
	userCtx, ok := RequireLecturerUser(c)
	if !ok {
		return
	}
	lecturerID := *userCtx.ProfileID

	store, _ := lecturerGetDashboardDeps()
	summary, err := store.GetSummary(lecturerID)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load lecturer dashboard summary")
		return
	}
	if summary == nil {
		summary = &LecturerSummaryResponse{
			LecturerID: lecturerID,
		}
	}
	if summary.LecturerID == uuid.Nil {
		summary.LecturerID = lecturerID
	}
	if summary.LecturerName == "" {
		summary.LecturerName = userCtx.DisplayName
	}
	if summary.FacultyID == nil && userCtx.FacultyID != nil {
		summary.FacultyID = userCtx.FacultyID
	}
	if summary.FacultyCode == nil && userCtx.FacultyCode != nil {
		summary.FacultyCode = userCtx.FacultyCode
	}
	if summary.FacultyName == nil && userCtx.FacultyName != nil {
		summary.FacultyName = userCtx.FacultyName
	}

	c.JSON(http.StatusOK, summary)
}

// ListLecturerSchedules handles GET /dashboard/lecturer/schedules.
func ListLecturerSchedules(c *gin.Context) {
	userCtx, ok := RequireLecturerUser(c)
	if !ok {
		return
	}
	lecturerID := *userCtx.ProfileID

	filter := LecturerScheduleFilter{
		AcademicTerm: strings.TrimSpace(c.Query("academicTerm")),
		DayOfWeek:    strings.TrimSpace(c.Query("dayOfWeek")),
	}

	store, _ := lecturerGetDashboardDeps()
	schedules, err := store.ListSchedules(lecturerID, filter)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to list lecturer schedules")
		return
	}
	if schedules == nil {
		schedules = []LecturerScheduleRow{}
	}
	c.JSON(http.StatusOK, schedules)
}

// ListLecturerScheduleStudents handles GET /dashboard/lecturer/schedules/:scheduleId/students.
func ListLecturerScheduleStudents(c *gin.Context) {
	userCtx, ok := RequireLecturerUser(c)
	if !ok {
		return
	}
	lecturerID := *userCtx.ProfileID

	scheduleID, ok := lecturerParseScheduleIDParam(c)
	if !ok {
		return
	}

	store, _ := lecturerGetDashboardDeps()
	schedule, err := store.FindOwnedSchedule(lecturerID, scheduleID)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to verify class schedule")
		return
	}
	if schedule == nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Class schedule not found")
		return
	}

	students, err := store.ListScheduleStudents(scheduleID)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to list enrolled students")
		return
	}
	if students == nil {
		students = []LecturerRosterStudentRow{}
	}
	c.JSON(http.StatusOK, students)
}

// GetLecturerScheduleAttendance handles GET /dashboard/lecturer/schedules/:scheduleId/attendance.
func GetLecturerScheduleAttendance(c *gin.Context) {
	userCtx, ok := RequireLecturerUser(c)
	if !ok {
		return
	}
	lecturerID := *userCtx.ProfileID

	scheduleID, ok := lecturerParseScheduleIDParam(c)
	if !ok {
		return
	}

	store, nowFn := lecturerGetDashboardDeps()
	var sessionDate SessionDate
	if rawDate := strings.TrimSpace(c.Query("date")); rawDate != "" {
		parsedDate, err := ParseSessionDate(rawDate)
		if err != nil {
			respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
			return
		}
		sessionDate = parsedDate
	} else {
		sessionDate = CurrentSessionDate(nowFn())
	}

	schedule, err := store.FindOwnedSchedule(lecturerID, scheduleID)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to verify class schedule")
		return
	}
	if schedule == nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Class schedule not found")
		return
	}

	sheet, err := store.GetSessionAttendance(scheduleID, sessionDate)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load session attendance")
		return
	}
	if sheet.Students == nil {
		sheet.Students = []LecturerSessionStudentStatus{}
	}
	c.JSON(http.StatusOK, sheet)
}

// RecordLecturerScheduleAttendance handles POST /dashboard/lecturer/schedules/:scheduleId/attendance.
func RecordLecturerScheduleAttendance(c *gin.Context) {
	userCtx, ok := RequireLecturerUser(c)
	if !ok {
		return
	}
	lecturerID := *userCtx.ProfileID

	scheduleID, ok := lecturerParseScheduleIDParam(c)
	if !ok {
		return
	}

	var req LecturerRecordAttendanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}
	studentIdentifier := strings.TrimSpace(req.StudentID)
	if studentIdentifier == "" {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "studentId is required")
		return
	}

	store, nowFn := lecturerGetDashboardDeps()
	var sessionDate SessionDate
	if req.Date != nil && strings.TrimSpace(*req.Date) != "" {
		parsedDate, err := ParseSessionDate(*req.Date)
		if err != nil {
			respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
			return
		}
		sessionDate = parsedDate
	} else {
		sessionDate = CurrentSessionDate(nowFn())
	}

	schedule, err := store.FindOwnedSchedule(lecturerID, scheduleID)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to verify class schedule")
		return
	}
	if schedule == nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Class schedule not found")
		return
	}

	student, err := store.FindEnrolledStudent(scheduleID, studentIdentifier)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to verify student enrollment")
		return
	}
	if student == nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Enrolled student not found in this schedule")
		return
	}

	record, created, err := store.RecordAttendance(schedule, student.ID, sessionDate)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to record attendance")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, record)
}

// DeleteLecturerScheduleAttendance handles DELETE /dashboard/lecturer/schedules/:scheduleId/attendance/:studentId.
func DeleteLecturerScheduleAttendance(c *gin.Context) {
	userCtx, ok := RequireLecturerUser(c)
	if !ok {
		return
	}
	lecturerID := *userCtx.ProfileID

	scheduleID, ok := lecturerParseScheduleIDParam(c)
	if !ok {
		return
	}

	rawStudentID := strings.TrimSpace(c.Param("studentId"))
	if rawStudentID == "" {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "studentId is required")
		return
	}

	rawDate := strings.TrimSpace(c.Query("date"))
	if rawDate == "" {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "date query parameter is required")
		return
	}
	sessionDate, err := ParseSessionDate(rawDate)
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	store, _ := lecturerGetDashboardDeps()
	schedule, err := store.FindOwnedSchedule(lecturerID, scheduleID)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to verify class schedule")
		return
	}
	if schedule == nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Class schedule not found")
		return
	}

	targetStudentUUID, err := uuid.Parse(rawStudentID)
	if err != nil || targetStudentUUID == uuid.Nil {
		student, findErr := store.FindEnrolledStudent(scheduleID, rawStudentID)
		if findErr != nil {
			respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to resolve student")
			return
		}
		if student == nil {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Attendance record not found")
			return
		}
		targetStudentUUID = student.ID
	}

	deleted, err := store.DeleteAttendance(scheduleID, targetStudentUUID, sessionDate)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete attendance")
		return
	}
	if !deleted {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Attendance record not found")
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": true})
}
