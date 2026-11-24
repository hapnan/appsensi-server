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
)

// FinanceFacultyBreakdown represents per-faculty enrollment and registration totals for the finance dashboard.
type FinanceFacultyBreakdown struct {
	FacultyID          uuid.UUID `json:"facultyId" gorm:"column:faculty_id"`
	FacultyCode        string    `json:"facultyCode" gorm:"column:faculty_code"`
	FacultyName        string    `json:"facultyName" gorm:"column:faculty_name"`
	TotalStudents      int       `json:"totalStudents" gorm:"column:total_students"`
	RegisteredStudents int       `json:"registeredStudents" gorm:"column:registered_students"`
	EnrolledStudents   int       `json:"enrolledStudents" gorm:"column:enrolled_students"`
	TotalCredits       int       `json:"totalCredits" gorm:"column:total_credits"`
}

// FinanceSummaryResponse represents campus-wide finance summary metrics and per-faculty breakdown.
type FinanceSummaryResponse struct {
	TotalStudents      int                       `json:"totalStudents"`
	RegisteredStudents int                       `json:"registeredStudents"`
	EnrolledStudents   int                       `json:"enrolledStudents"`
	TotalCredits       int                       `json:"totalCredits"`
	TotalFaculties     int                       `json:"totalFaculties"`
	AcademicTerm       *string                   `json:"academicTerm,omitempty"`
	ByFaculty          []FinanceFacultyBreakdown `json:"byFaculty"`
}

// FinanceStudentRow represents a student row in the finance enrolled-credits directory.
type FinanceStudentRow struct {
	ID              uuid.UUID  `json:"id" gorm:"column:id"`
	StudentID       string     `json:"studentId" gorm:"column:student_id"`
	Name            string     `json:"name" gorm:"column:name"`
	Department      *string    `json:"department" gorm:"column:department"`
	FacultyID       *uuid.UUID `json:"facultyId" gorm:"column:faculty_id"`
	FacultyCode     *string    `json:"facultyCode" gorm:"column:faculty_code"`
	FacultyName     *string    `json:"facultyName" gorm:"column:faculty_name"`
	EnrolledCourses int        `json:"enrolledCourses" gorm:"column:enrolled_courses"`
	EnrolledCredits int        `json:"enrolledCredits" gorm:"column:enrolled_credits"`
	IsRegistered    bool       `json:"isRegistered" gorm:"column:is_registered"`
	CreatedAt       time.Time  `json:"createdAt" gorm:"column:created_at"`
}

// FinanceStudentsFilter holds optional query filters for listing students in the finance dashboard.
type FinanceStudentsFilter struct {
	FacultyID    *uuid.UUID
	AcademicTerm string
	Search       string
}

// FinanceDashboardStore abstracts persistence operations for finance dashboard endpoints.
type FinanceDashboardStore interface {
	GetSummary(academicTerm string) (*FinanceSummaryResponse, error)
	ListStudents(filter FinanceStudentsFilter) ([]FinanceStudentRow, error)
}

type gormFinanceDashboardStore struct{}

var _ FinanceDashboardStore = gormFinanceDashboardStore{}

type financeSummaryTotalsRow struct {
	TotalStudents      int `gorm:"column:total_students"`
	RegisteredStudents int `gorm:"column:registered_students"`
	EnrolledStudents   int `gorm:"column:enrolled_students"`
	TotalCredits       int `gorm:"column:total_credits"`
}

func financeBuildStudentMetricsCTE(academicTerm string) (string, []any) {
	args := []any{models.EnrollmentStatusEnrolled}
	termFilter := ""
	if trimmed := strings.TrimSpace(academicTerm); trimmed != "" {
		termFilter = " AND lower(trim(cs.academic_term)) = lower(?)"
		args = append(args, trimmed)
	}

	cte := `WITH student_enrollments AS (
		SELECT
			ss.student_id,
			COUNT(1) AS enrolled_courses,
			COALESCE(SUM(COALESCE(c.credits, 0)), 0) AS enrolled_credits
		FROM student_schedules ss
		INNER JOIN class_schedules cs ON cs.id = ss.class_schedule_id
		INNER JOIN courses c ON c.id = cs.course_id
		WHERE ss.status = ?` + termFilter + `
		GROUP BY ss.student_id
	),
	student_metrics AS (
		SELECT
			s.id,
			s.faculty_id,
			CASE WHEN EXISTS (
				SELECT 1
				FROM credentials cred
				LEFT JOIN users u ON u.id = cred.user_id
				WHERE cred.user_id = s.user_id OR u.username = s.student_id
			) THEN 1 ELSE 0 END AS is_registered,
			CASE WHEN COALESCE(se.enrolled_courses, 0) > 0 THEN 1 ELSE 0 END AS is_enrolled,
			COALESCE(se.enrolled_credits, 0) AS enrolled_credits
		FROM students s
		LEFT JOIN student_enrollments se ON se.student_id = s.id
	)`
	return cte, args
}

func financeBuildSummaryFacultyQuery(db *gorm.DB, academicTerm string) *gorm.DB {
	cte, args := financeBuildStudentMetricsCTE(academicTerm)
	sql := cte + `
	SELECT
		f.id AS faculty_id,
		f.code AS faculty_code,
		f.name AS faculty_name,
		COUNT(sm.id) AS total_students,
		COALESCE(SUM(sm.is_registered), 0) AS registered_students,
		COALESCE(SUM(sm.is_enrolled), 0) AS enrolled_students,
		COALESCE(SUM(sm.enrolled_credits), 0) AS total_credits
	FROM faculties f
	LEFT JOIN student_metrics sm ON sm.faculty_id = f.id
	GROUP BY f.id, f.code, f.name
	ORDER BY f.code ASC`
	return db.Raw(sql, args...)
}

func financeBuildSummaryTotalsQuery(db *gorm.DB, academicTerm string) *gorm.DB {
	cte, args := financeBuildStudentMetricsCTE(academicTerm)
	sql := cte + `
	SELECT
		COUNT(id) AS total_students,
		COALESCE(SUM(is_registered), 0) AS registered_students,
		COALESCE(SUM(is_enrolled), 0) AS enrolled_students,
		COALESCE(SUM(enrolled_credits), 0) AS total_credits
	FROM student_metrics`
	return db.Raw(sql, args...)
}

func (gormFinanceDashboardStore) GetSummary(academicTerm string) (*FinanceSummaryResponse, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}

	trimmedTerm := strings.TrimSpace(academicTerm)

	var byFaculty []FinanceFacultyBreakdown
	if err := financeBuildSummaryFacultyQuery(database.DB, trimmedTerm).Scan(&byFaculty).Error; err != nil {
		return nil, err
	}
	if byFaculty == nil {
		byFaculty = []FinanceFacultyBreakdown{}
	}

	var totals financeSummaryTotalsRow
	if err := financeBuildSummaryTotalsQuery(database.DB, trimmedTerm).Scan(&totals).Error; err != nil {
		return nil, err
	}

	resp := &FinanceSummaryResponse{
		TotalStudents:      totals.TotalStudents,
		RegisteredStudents: totals.RegisteredStudents,
		EnrolledStudents:   totals.EnrolledStudents,
		TotalCredits:       totals.TotalCredits,
		TotalFaculties:     len(byFaculty),
		ByFaculty:          byFaculty,
	}
	if trimmedTerm != "" {
		resp.AcademicTerm = &trimmedTerm
	}
	return resp, nil
}

func financeBuildStudentsQuery(db *gorm.DB, filter FinanceStudentsFilter) *gorm.DB {
	joinArgs := []any{models.EnrollmentStatusEnrolled}
	termClause := ""
	if term := strings.TrimSpace(filter.AcademicTerm); term != "" {
		termClause = " AND lower(trim(cs.academic_term)) = lower(?)"
		joinArgs = append(joinArgs, term)
	}

	enrollmentJoin := `LEFT JOIN (
		SELECT
			ss.student_id,
			COUNT(1) AS enrolled_courses,
			COALESCE(SUM(COALESCE(c.credits, 0)), 0) AS enrolled_credits
		FROM student_schedules ss
		INNER JOIN class_schedules cs ON cs.id = ss.class_schedule_id
		INNER JOIN courses c ON c.id = cs.course_id
		WHERE ss.status = ?` + termClause + `
		GROUP BY ss.student_id
	) AS enroll_stats ON enroll_stats.student_id = students.id`

	query := db.Table("students").
		Select(`students.id,
			students.student_id,
			students.name,
			students.department,
			students.faculty_id,
			faculties.code AS faculty_code,
			faculties.name AS faculty_name,
			COALESCE(enroll_stats.enrolled_courses, 0) AS enrolled_courses,
			COALESCE(enroll_stats.enrolled_credits, 0) AS enrolled_credits,
			EXISTS (
				SELECT 1
				FROM credentials cred
				LEFT JOIN users u ON u.id = cred.user_id
				WHERE cred.user_id = students.user_id OR u.username = students.student_id
			) AS is_registered,
			students.created_at`).
		Joins("LEFT JOIN faculties ON faculties.id = students.faculty_id").
		Joins(enrollmentJoin, joinArgs...)

	if filter.FacultyID != nil && *filter.FacultyID != uuid.Nil {
		query = query.Where("students.faculty_id = ?", *filter.FacultyID)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		pattern := "%" + strings.ToLower(search) + "%"
		query = query.Where("(LOWER(students.name) LIKE ? OR LOWER(students.student_id) LIKE ?)", pattern, pattern)
	}

	return query.Order("students.student_id ASC")
}

func (gormFinanceDashboardStore) ListStudents(filter FinanceStudentsFilter) ([]FinanceStudentRow, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}

	var rows []FinanceStudentRow
	if err := financeBuildStudentsQuery(database.DB, filter).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []FinanceStudentRow{}
	}
	return rows, nil
}

var (
	financeDashboardStoreMu sync.RWMutex
	financeDashboardStore   FinanceDashboardStore = gormFinanceDashboardStore{}
)

// SetFinanceDashboardStore overrides the persistence store for finance dashboard endpoints (used in tests).
func SetFinanceDashboardStore(store FinanceDashboardStore) {
	financeDashboardStoreMu.Lock()
	defer financeDashboardStoreMu.Unlock()
	if store == nil {
		financeDashboardStore = gormFinanceDashboardStore{}
		return
	}
	financeDashboardStore = store
}

func getFinanceDashboardStore() FinanceDashboardStore {
	financeDashboardStoreMu.RLock()
	defer financeDashboardStoreMu.RUnlock()
	return financeDashboardStore
}

// GetFinanceDashboardSummary handles GET /dashboard/finance/summary.
func GetFinanceDashboardSummary(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleFinance); !ok {
		return
	}

	academicTerm := strings.TrimSpace(c.Query("academicTerm"))
	summary, err := getFinanceDashboardStore().GetSummary(academicTerm)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load finance summary")
		return
	}
	if summary.ByFaculty == nil {
		summary.ByFaculty = []FinanceFacultyBreakdown{}
	}
	c.JSON(http.StatusOK, summary)
}

// ListFinanceDashboardStudents handles GET /dashboard/finance/students.
func ListFinanceDashboardStudents(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleFinance); !ok {
		return
	}

	var facultyID *uuid.UUID
	if rawFacultyID := strings.TrimSpace(c.Query("facultyId")); rawFacultyID != "" {
		parsed, err := uuid.Parse(rawFacultyID)
		if err != nil || parsed == uuid.Nil {
			respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid facultyId UUID")
			return
		}
		facultyID = &parsed
	}

	filter := FinanceStudentsFilter{
		FacultyID:    facultyID,
		AcademicTerm: strings.TrimSpace(c.Query("academicTerm")),
		Search:       strings.TrimSpace(c.Query("search")),
	}

	students, err := getFinanceDashboardStore().ListStudents(filter)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load finance students")
		return
	}
	if students == nil {
		students = []FinanceStudentRow{}
	}
	c.JSON(http.StatusOK, students)
}
