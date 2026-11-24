package controllers

import (
	"errors"
	"fmt"
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

// AcademicSummaryResponse represents campus-wide academic infrastructure and staffing counts.
type AcademicSummaryResponse struct {
	TotalFaculties      int `json:"totalFaculties"`
	TotalBuildings      int `json:"totalBuildings"`
	TotalRooms          int `json:"totalRooms"`
	TotalCourses        int `json:"totalCourses"`
	TotalLecturers      int `json:"totalLecturers"`
	TotalEmployees      int `json:"totalEmployees"`
	TotalClassSchedules int `json:"totalClassSchedules"`
	TotalStudents       int `json:"totalStudents"`
}

// AcademicUpsertFacultyRequest represents the JSON payload for creating or updating a faculty.
type AcademicUpsertFacultyRequest struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	BuildingID *string `json:"buildingId"`
}

// AcademicUpsertCourseRequest represents the JSON payload for creating or updating a course.
type AcademicUpsertCourseRequest struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Credits     int     `json:"credits"`
	Description *string `json:"description"`
	FacultyID   *string `json:"facultyId"`
}

// AcademicUpsertLecturerRequest represents the JSON payload for creating or updating a lecturer.
type AcademicUpsertLecturerRequest struct {
	LecturerID string  `json:"lecturerId"`
	Name       string  `json:"name"`
	Department *string `json:"department"`
	FacultyID  *string `json:"facultyId"`
	PhotoURL   *string `json:"photoUrl"`
}

// AcademicUpsertEmployeeRequest represents the JSON payload for creating or updating an employee.
type AcademicUpsertEmployeeRequest struct {
	EmployeeID string                 `json:"employeeId"`
	Name       string                 `json:"name"`
	Department *string                `json:"department"`
	JobRole    models.EmployeeJobRole `json:"jobRole"`
	FacultyID  *string                `json:"facultyId"`
	PhotoURL   *string                `json:"photoUrl"`
}

// AcademicUpsertScheduleRequest represents the JSON payload for creating or updating a class schedule.
type AcademicUpsertScheduleRequest struct {
	CourseID     string  `json:"courseId"`
	RoomID       string  `json:"roomId"`
	LecturerID   *string `json:"lecturerId"`
	Section      string  `json:"section"`
	Instructor   *string `json:"instructor"`
	DayOfWeek    string  `json:"dayOfWeek"`
	StartTime    string  `json:"startTime"`
	EndTime      string  `json:"endTime"`
	AcademicTerm string  `json:"academicTerm"`
	MaxCapacity  *int    `json:"maxCapacity"`
}

// AcademicDashboardStore abstracts persistence operations for the Academic Affairs dashboard endpoints,
// backed by PostgreSQL (GORM) in production and overridable in unit tests.
type AcademicDashboardStore interface {
	GetSummary() (*AcademicSummaryResponse, error)

	ListFaculties() ([]models.Faculty, error)
	CreateFaculty(faculty *models.Faculty) error
	UpdateFaculty(faculty *models.Faculty) error

	ListCourses(facultyID *uuid.UUID) ([]models.Course, error)
	CreateCourse(course *models.Course) error
	UpdateCourse(course *models.Course) error
	DeleteCourse(id uuid.UUID) error

	ListLecturers(facultyID *uuid.UUID) ([]models.Lecturer, error)
	GetLecturerByID(id uuid.UUID) (*models.Lecturer, error)
	CreateLecturer(lecturer *models.Lecturer) error
	UpdateLecturer(lecturer *models.Lecturer) error
	DeleteLecturer(id uuid.UUID) error

	ListEmployees(jobRole *models.EmployeeJobRole, facultyID *uuid.UUID) ([]models.Employee, error)
	CreateEmployee(employee *models.Employee) error
	UpdateEmployee(employee *models.Employee) error
	DeleteEmployee(id uuid.UUID) error

	ListSchedules(courseID *uuid.UUID, lecturerID *uuid.UUID, academicTerm string) ([]models.ClassSchedule, error)
	CreateSchedule(schedule *models.ClassSchedule) error
	UpdateSchedule(schedule *models.ClassSchedule) error
	DeleteSchedule(id uuid.UUID) error
}

type gormAcademicDashboardStore struct{}

var _ AcademicDashboardStore = gormAcademicDashboardStore{}

func (gormAcademicDashboardStore) GetSummary() (*AcademicSummaryResponse, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}

	var (
		totalFaculties      int64
		totalBuildings      int64
		totalRooms          int64
		totalCourses        int64
		totalLecturers      int64
		totalEmployees      int64
		totalClassSchedules int64
		totalStudents       int64
	)

	if err := database.DB.Model(&models.Faculty{}).Count(&totalFaculties).Error; err != nil {
		return nil, err
	}
	if err := database.DB.Model(&models.Building{}).Count(&totalBuildings).Error; err != nil {
		return nil, err
	}
	if err := database.DB.Model(&models.Room{}).Count(&totalRooms).Error; err != nil {
		return nil, err
	}
	if err := database.DB.Model(&models.Course{}).Count(&totalCourses).Error; err != nil {
		return nil, err
	}
	if err := database.DB.Model(&models.Lecturer{}).Count(&totalLecturers).Error; err != nil {
		return nil, err
	}
	if err := database.DB.Model(&models.Employee{}).Count(&totalEmployees).Error; err != nil {
		return nil, err
	}
	if err := database.DB.Model(&models.ClassSchedule{}).Count(&totalClassSchedules).Error; err != nil {
		return nil, err
	}
	if err := database.DB.Model(&models.Student{}).Count(&totalStudents).Error; err != nil {
		return nil, err
	}

	return &AcademicSummaryResponse{
		TotalFaculties:      int(totalFaculties),
		TotalBuildings:      int(totalBuildings),
		TotalRooms:          int(totalRooms),
		TotalCourses:        int(totalCourses),
		TotalLecturers:      int(totalLecturers),
		TotalEmployees:      int(totalEmployees),
		TotalClassSchedules: int(totalClassSchedules),
		TotalStudents:       int(totalStudents),
	}, nil
}

func (gormAcademicDashboardStore) ListFaculties() ([]models.Faculty, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	var faculties []models.Faculty
	if err := database.DB.Order("code ASC").Find(&faculties).Error; err != nil {
		return nil, err
	}
	return faculties, nil
}

func (gormAcademicDashboardStore) CreateFaculty(faculty *models.Faculty) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	if faculty.ID == uuid.Nil {
		faculty.ID = uuid.New()
	}
	return database.DB.Create(faculty).Error
}

func (gormAcademicDashboardStore) UpdateFaculty(faculty *models.Faculty) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	var existing models.Faculty
	if err := database.DB.Where("id = ?", faculty.ID).Take(&existing).Error; err != nil {
		return err
	}
	if err := database.DB.Model(&existing).
		Select("Code", "Name", "BuildingID").
		Updates(models.Faculty{
			Code:       faculty.Code,
			Name:       faculty.Name,
			BuildingID: faculty.BuildingID,
		}).Error; err != nil {
		return err
	}
	faculty.CreatedAt = existing.CreatedAt
	return nil
}

func (gormAcademicDashboardStore) ListCourses(facultyID *uuid.UUID) ([]models.Course, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	query := database.DB.Model(&models.Course{})
	if facultyID != nil && *facultyID != uuid.Nil {
		query = query.Where("faculty_id = ?", *facultyID)
	}
	var courses []models.Course
	if err := query.Order("code ASC").Find(&courses).Error; err != nil {
		return nil, err
	}
	return courses, nil
}

func (gormAcademicDashboardStore) CreateCourse(course *models.Course) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	if course.ID == uuid.Nil {
		course.ID = uuid.New()
	}
	return database.DB.Create(course).Error
}

func (gormAcademicDashboardStore) UpdateCourse(course *models.Course) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	var existing models.Course
	if err := database.DB.Where("id = ?", course.ID).Take(&existing).Error; err != nil {
		return err
	}
	if err := database.DB.Model(&existing).
		Select("Code", "Name", "Credits", "Description", "FacultyID").
		Updates(models.Course{
			Code:        course.Code,
			Name:        course.Name,
			Credits:     course.Credits,
			Description: course.Description,
			FacultyID:   course.FacultyID,
		}).Error; err != nil {
		return err
	}
	course.CreatedAt = existing.CreatedAt
	return nil
}

func (gormAcademicDashboardStore) DeleteCourse(id uuid.UUID) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	res := database.DB.Delete(&models.Course{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (gormAcademicDashboardStore) ListLecturers(facultyID *uuid.UUID) ([]models.Lecturer, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	query := database.DB.Model(&models.Lecturer{})
	if facultyID != nil && *facultyID != uuid.Nil {
		query = query.Where("faculty_id = ?", *facultyID)
	}
	var lecturers []models.Lecturer
	if err := query.Order("lecturer_id ASC").Find(&lecturers).Error; err != nil {
		return nil, err
	}
	return lecturers, nil
}

func (gormAcademicDashboardStore) GetLecturerByID(id uuid.UUID) (*models.Lecturer, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	var lecturer models.Lecturer
	if err := database.DB.Where("id = ?", id).Take(&lecturer).Error; err != nil {
		return nil, err
	}
	return &lecturer, nil
}

func (gormAcademicDashboardStore) CreateLecturer(lecturer *models.Lecturer) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	if lecturer.ID == uuid.Nil {
		lecturer.ID = uuid.New()
	}
	return database.DB.Create(lecturer).Error
}

func (gormAcademicDashboardStore) UpdateLecturer(lecturer *models.Lecturer) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	var existing models.Lecturer
	if err := database.DB.Where("id = ?", lecturer.ID).Take(&existing).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := database.DB.Model(&existing).
		Select("LecturerID", "Name", "Department", "FacultyID", "PhotoURL", "UpdatedAt").
		Updates(models.Lecturer{
			LecturerID: lecturer.LecturerID,
			Name:       lecturer.Name,
			Department: lecturer.Department,
			FacultyID:  lecturer.FacultyID,
			PhotoURL:   lecturer.PhotoURL,
			UpdatedAt:  &now,
		}).Error; err != nil {
		return err
	}
	lecturer.UserID = existing.UserID
	lecturer.CreatedAt = existing.CreatedAt
	lecturer.UpdatedAt = &now
	return nil
}

func (gormAcademicDashboardStore) DeleteLecturer(id uuid.UUID) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	res := database.DB.Delete(&models.Lecturer{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (gormAcademicDashboardStore) ListEmployees(jobRole *models.EmployeeJobRole, facultyID *uuid.UUID) ([]models.Employee, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	query := database.DB.Model(&models.Employee{})
	if jobRole != nil && *jobRole != "" {
		query = query.Where("job_role = ?", *jobRole)
	}
	if facultyID != nil && *facultyID != uuid.Nil {
		query = query.Where("faculty_id = ?", *facultyID)
	}
	var employees []models.Employee
	if err := query.Order("employee_id ASC").Find(&employees).Error; err != nil {
		return nil, err
	}
	return employees, nil
}

func (gormAcademicDashboardStore) CreateEmployee(employee *models.Employee) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	if employee.ID == uuid.Nil {
		employee.ID = uuid.New()
	}
	return database.DB.Create(employee).Error
}

func (gormAcademicDashboardStore) UpdateEmployee(employee *models.Employee) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	var existing models.Employee
	if err := database.DB.Where("id = ?", employee.ID).Take(&existing).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := database.DB.Model(&existing).
		Select("EmployeeID", "Name", "Department", "JobRole", "FacultyID", "PhotoURL", "UpdatedAt").
		Updates(models.Employee{
			EmployeeID: employee.EmployeeID,
			Name:       employee.Name,
			Department: employee.Department,
			JobRole:    employee.JobRole,
			FacultyID:  employee.FacultyID,
			PhotoURL:   employee.PhotoURL,
			UpdatedAt:  &now,
		}).Error; err != nil {
		return err
	}
	employee.UserID = existing.UserID
	employee.CreatedAt = existing.CreatedAt
	employee.UpdatedAt = &now
	return nil
}

func (gormAcademicDashboardStore) DeleteEmployee(id uuid.UUID) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	res := database.DB.Delete(&models.Employee{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (gormAcademicDashboardStore) ListSchedules(courseID *uuid.UUID, lecturerID *uuid.UUID, academicTerm string) ([]models.ClassSchedule, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	query := database.DB.Model(&models.ClassSchedule{})
	if courseID != nil && *courseID != uuid.Nil {
		query = query.Where("course_id = ?", *courseID)
	}
	if lecturerID != nil && *lecturerID != uuid.Nil {
		query = query.Where("lecturer_id = ?", *lecturerID)
	}
	if academicTerm != "" {
		query = query.Where("academic_term = ?", academicTerm)
	}
	var schedules []models.ClassSchedule
	if err := query.Order("academic_term DESC, day_of_week ASC, start_time ASC").Find(&schedules).Error; err != nil {
		return nil, err
	}
	return schedules, nil
}

func (gormAcademicDashboardStore) CreateSchedule(schedule *models.ClassSchedule) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	if schedule.ID == uuid.Nil {
		schedule.ID = uuid.New()
	}
	return database.DB.Create(schedule).Error
}

func (gormAcademicDashboardStore) UpdateSchedule(schedule *models.ClassSchedule) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	var existing models.ClassSchedule
	if err := database.DB.Where("id = ?", schedule.ID).Take(&existing).Error; err != nil {
		return err
	}
	if err := database.DB.Model(&existing).
		Select("CourseID", "RoomID", "LecturerID", "Section", "Instructor", "DayOfWeek", "StartTime", "EndTime", "AcademicTerm", "MaxCapacity").
		Updates(models.ClassSchedule{
			CourseID:     schedule.CourseID,
			RoomID:       schedule.RoomID,
			LecturerID:   schedule.LecturerID,
			Section:      schedule.Section,
			Instructor:   schedule.Instructor,
			DayOfWeek:    schedule.DayOfWeek,
			StartTime:    schedule.StartTime,
			EndTime:      schedule.EndTime,
			AcademicTerm: schedule.AcademicTerm,
			MaxCapacity:  schedule.MaxCapacity,
		}).Error; err != nil {
		return err
	}
	schedule.CreatedAt = existing.CreatedAt
	return nil
}

func (gormAcademicDashboardStore) DeleteSchedule(id uuid.UUID) error {
	if database.DB == nil {
		return errors.New("database is not initialized")
	}
	res := database.DB.Delete(&models.ClassSchedule{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

var (
	academicDashboardStoreMu sync.RWMutex
	academicDashboardStore   AcademicDashboardStore = gormAcademicDashboardStore{}
)

// SetAcademicDashboardStore overrides the AcademicDashboardStore (used in unit tests).
func SetAcademicDashboardStore(store AcademicDashboardStore) {
	academicDashboardStoreMu.Lock()
	defer academicDashboardStoreMu.Unlock()
	if store == nil {
		academicDashboardStore = gormAcademicDashboardStore{}
		return
	}
	academicDashboardStore = store
}

func academicActiveDashboardStore() AcademicDashboardStore {
	academicDashboardStoreMu.RLock()
	defer academicDashboardStoreMu.RUnlock()
	return academicDashboardStore
}

func academicParseUUIDParam(c *gin.Context, paramName string) (uuid.UUID, bool) {
	raw := strings.TrimSpace(c.Param(paramName))
	parsed, err := uuid.Parse(raw)
	if err != nil || parsed == uuid.Nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, fmt.Sprintf("Invalid %s UUID", paramName))
		return uuid.Nil, false
	}
	return parsed, true
}

func academicParseOptionalUUID(raw *string, fieldName string) (*uuid.UUID, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(trimmed)
	if err != nil || parsed == uuid.Nil {
		return nil, fmt.Errorf("invalid %s UUID", fieldName)
	}
	return &parsed, nil
}

func academicParseOptionalQueryUUID(c *gin.Context, queryName string) (*uuid.UUID, bool) {
	raw := strings.TrimSpace(c.Query(queryName))
	if raw == "" {
		return nil, true
	}
	parsed, err := uuid.Parse(raw)
	if err != nil || parsed == uuid.Nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, fmt.Sprintf("Invalid %s UUID", queryName))
		return nil, false
	}
	return &parsed, true
}

func academicNormalizeOptionalString(raw *string) *string {
	if raw == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// GetAcademicDashboardSummary handles GET /dashboard/academic/summary.
func GetAcademicDashboardSummary(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	summary, err := academicActiveDashboardStore().GetSummary()
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load academic dashboard summary")
		return
	}
	c.JSON(http.StatusOK, summary)
}

// ListAcademicFaculties handles GET /dashboard/academic/faculties.
func ListAcademicFaculties(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	faculties, err := academicActiveDashboardStore().ListFaculties()
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to list faculties")
		return
	}
	if faculties == nil {
		faculties = []models.Faculty{}
	}
	c.JSON(http.StatusOK, faculties)
}

// CreateAcademicFaculty handles POST /dashboard/academic/faculties.
func CreateAcademicFaculty(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	var req AcademicUpsertFacultyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	if code == "" || name == "" {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "code and name are required")
		return
	}

	buildingUUID, err := academicParseOptionalUUID(req.BuildingID, "buildingId")
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	faculty := models.Faculty{
		ID:         uuid.New(),
		Code:       code,
		Name:       name,
		BuildingID: buildingUUID,
		CreatedAt:  time.Now().UTC(),
	}
	if err := academicActiveDashboardStore().CreateFaculty(&faculty); err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to create faculty")
		return
	}
	c.JSON(http.StatusCreated, faculty)
}

// UpdateAcademicFaculty handles PUT /dashboard/academic/faculties/:id.
func UpdateAcademicFaculty(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	id, ok := academicParseUUIDParam(c, "id")
	if !ok {
		return
	}

	var req AcademicUpsertFacultyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	if code == "" || name == "" {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "code and name are required")
		return
	}

	buildingUUID, err := academicParseOptionalUUID(req.BuildingID, "buildingId")
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	faculty := models.Faculty{
		ID:         id,
		Code:       code,
		Name:       name,
		BuildingID: buildingUUID,
	}
	if err := academicActiveDashboardStore().UpdateFaculty(&faculty); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Faculty not found")
			return
		}
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to update faculty")
		return
	}
	c.JSON(http.StatusOK, faculty)
}

// ListAcademicCourses handles GET /dashboard/academic/courses.
func ListAcademicCourses(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	facultyUUID, ok := academicParseOptionalQueryUUID(c, "facultyId")
	if !ok {
		return
	}

	courses, err := academicActiveDashboardStore().ListCourses(facultyUUID)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to list courses")
		return
	}
	if courses == nil {
		courses = []models.Course{}
	}
	c.JSON(http.StatusOK, courses)
}

// CreateAcademicCourse handles POST /dashboard/academic/courses.
func CreateAcademicCourse(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	var req AcademicUpsertCourseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	if code == "" || name == "" || req.Credits <= 0 {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "code, name, and credits > 0 are required")
		return
	}

	facultyUUID, err := academicParseOptionalUUID(req.FacultyID, "facultyId")
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	course := models.Course{
		ID:          uuid.New(),
		Code:        code,
		Name:        name,
		Credits:     req.Credits,
		Description: academicNormalizeOptionalString(req.Description),
		FacultyID:   facultyUUID,
		CreatedAt:   time.Now().UTC(),
	}
	if err := academicActiveDashboardStore().CreateCourse(&course); err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to create course")
		return
	}
	c.JSON(http.StatusCreated, course)
}

// UpdateAcademicCourse handles PUT /dashboard/academic/courses/:id.
func UpdateAcademicCourse(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	id, ok := academicParseUUIDParam(c, "id")
	if !ok {
		return
	}

	var req AcademicUpsertCourseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	if code == "" || name == "" || req.Credits <= 0 {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "code, name, and credits > 0 are required")
		return
	}

	facultyUUID, err := academicParseOptionalUUID(req.FacultyID, "facultyId")
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	course := models.Course{
		ID:          id,
		Code:        code,
		Name:        name,
		Credits:     req.Credits,
		Description: academicNormalizeOptionalString(req.Description),
		FacultyID:   facultyUUID,
	}
	if err := academicActiveDashboardStore().UpdateCourse(&course); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Course not found")
			return
		}
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to update course")
		return
	}
	c.JSON(http.StatusOK, course)
}

// DeleteAcademicCourse handles DELETE /dashboard/academic/courses/:id.
func DeleteAcademicCourse(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	id, ok := academicParseUUIDParam(c, "id")
	if !ok {
		return
	}

	if err := academicActiveDashboardStore().DeleteCourse(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Course not found")
			return
		}
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete course")
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true, "id": id})
}

// ListAcademicLecturers handles GET /dashboard/academic/lecturers.
func ListAcademicLecturers(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	facultyUUID, ok := academicParseOptionalQueryUUID(c, "facultyId")
	if !ok {
		return
	}

	lecturers, err := academicActiveDashboardStore().ListLecturers(facultyUUID)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to list lecturers")
		return
	}
	if lecturers == nil {
		lecturers = []models.Lecturer{}
	}
	c.JSON(http.StatusOK, lecturers)
}

// CreateAcademicLecturer handles POST /dashboard/academic/lecturers.
func CreateAcademicLecturer(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	var req AcademicUpsertLecturerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	lecturerID := strings.TrimSpace(req.LecturerID)
	name := strings.TrimSpace(req.Name)
	if lecturerID == "" || name == "" {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "lecturerId and name are required")
		return
	}

	facultyUUID, err := academicParseOptionalUUID(req.FacultyID, "facultyId")
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	lecturer := models.Lecturer{
		ID:         uuid.New(),
		LecturerID: lecturerID,
		Name:       name,
		Department: academicNormalizeOptionalString(req.Department),
		FacultyID:  facultyUUID,
		PhotoURL:   academicNormalizeOptionalString(req.PhotoURL),
		CreatedAt:  time.Now().UTC(),
	}
	if err := academicActiveDashboardStore().CreateLecturer(&lecturer); err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to create lecturer")
		return
	}
	c.JSON(http.StatusCreated, lecturer)
}

// UpdateAcademicLecturer handles PUT /dashboard/academic/lecturers/:id.
func UpdateAcademicLecturer(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	id, ok := academicParseUUIDParam(c, "id")
	if !ok {
		return
	}

	var req AcademicUpsertLecturerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	lecturerID := strings.TrimSpace(req.LecturerID)
	name := strings.TrimSpace(req.Name)
	if lecturerID == "" || name == "" {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "lecturerId and name are required")
		return
	}

	facultyUUID, err := academicParseOptionalUUID(req.FacultyID, "facultyId")
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	lecturer := models.Lecturer{
		ID:         id,
		LecturerID: lecturerID,
		Name:       name,
		Department: academicNormalizeOptionalString(req.Department),
		FacultyID:  facultyUUID,
		PhotoURL:   academicNormalizeOptionalString(req.PhotoURL),
	}
	if err := academicActiveDashboardStore().UpdateLecturer(&lecturer); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Lecturer not found")
			return
		}
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to update lecturer")
		return
	}
	c.JSON(http.StatusOK, lecturer)
}

// DeleteAcademicLecturer handles DELETE /dashboard/academic/lecturers/:id.
func DeleteAcademicLecturer(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	id, ok := academicParseUUIDParam(c, "id")
	if !ok {
		return
	}

	if err := academicActiveDashboardStore().DeleteLecturer(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Lecturer not found")
			return
		}
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete lecturer")
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true, "id": id})
}

// ListAcademicEmployees handles GET /dashboard/academic/employees.
func ListAcademicEmployees(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	var jobRolePtr *models.EmployeeJobRole
	if rawRole := strings.TrimSpace(c.Query("jobRole")); rawRole != "" {
		role := models.EmployeeJobRole(rawRole)
		if !models.IsValidEmployeeJobRole(role) {
			respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid jobRole filter")
			return
		}
		jobRolePtr = &role
	}

	facultyUUID, ok := academicParseOptionalQueryUUID(c, "facultyId")
	if !ok {
		return
	}

	employees, err := academicActiveDashboardStore().ListEmployees(jobRolePtr, facultyUUID)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to list employees")
		return
	}
	if employees == nil {
		employees = []models.Employee{}
	}
	c.JSON(http.StatusOK, employees)
}

// CreateAcademicEmployee handles POST /dashboard/academic/employees.
func CreateAcademicEmployee(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	var req AcademicUpsertEmployeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	employeeID := strings.TrimSpace(req.EmployeeID)
	name := strings.TrimSpace(req.Name)
	if employeeID == "" || name == "" {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "employeeId and name are required")
		return
	}

	facultyUUID, err := academicParseOptionalUUID(req.FacultyID, "facultyId")
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	if err := models.ValidateEmployeeRoleScope(req.JobRole, facultyUUID); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	employee := models.Employee{
		ID:         uuid.New(),
		EmployeeID: employeeID,
		Name:       name,
		Department: academicNormalizeOptionalString(req.Department),
		JobRole:    req.JobRole,
		FacultyID:  facultyUUID,
		PhotoURL:   academicNormalizeOptionalString(req.PhotoURL),
		CreatedAt:  time.Now().UTC(),
	}
	if err := academicActiveDashboardStore().CreateEmployee(&employee); err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to create employee")
		return
	}
	c.JSON(http.StatusCreated, employee)
}

// UpdateAcademicEmployee handles PUT /dashboard/academic/employees/:id.
func UpdateAcademicEmployee(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	id, ok := academicParseUUIDParam(c, "id")
	if !ok {
		return
	}

	var req AcademicUpsertEmployeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	employeeID := strings.TrimSpace(req.EmployeeID)
	name := strings.TrimSpace(req.Name)
	if employeeID == "" || name == "" {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "employeeId and name are required")
		return
	}

	facultyUUID, err := academicParseOptionalUUID(req.FacultyID, "facultyId")
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	if err := models.ValidateEmployeeRoleScope(req.JobRole, facultyUUID); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	employee := models.Employee{
		ID:         id,
		EmployeeID: employeeID,
		Name:       name,
		Department: academicNormalizeOptionalString(req.Department),
		JobRole:    req.JobRole,
		FacultyID:  facultyUUID,
		PhotoURL:   academicNormalizeOptionalString(req.PhotoURL),
	}
	if err := academicActiveDashboardStore().UpdateEmployee(&employee); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Employee not found")
			return
		}
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to update employee")
		return
	}
	c.JSON(http.StatusOK, employee)
}

// DeleteAcademicEmployee handles DELETE /dashboard/academic/employees/:id.
func DeleteAcademicEmployee(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	id, ok := academicParseUUIDParam(c, "id")
	if !ok {
		return
	}

	if err := academicActiveDashboardStore().DeleteEmployee(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Employee not found")
			return
		}
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete employee")
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true, "id": id})
}

// ListAcademicSchedules handles GET /dashboard/academic/schedules.
func ListAcademicSchedules(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	courseUUID, ok := academicParseOptionalQueryUUID(c, "courseId")
	if !ok {
		return
	}
	lecturerUUID, ok := academicParseOptionalQueryUUID(c, "lecturerId")
	if !ok {
		return
	}
	academicTerm := strings.TrimSpace(c.Query("academicTerm"))

	schedules, err := academicActiveDashboardStore().ListSchedules(courseUUID, lecturerUUID, academicTerm)
	if err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to list class schedules")
		return
	}
	if schedules == nil {
		schedules = []models.ClassSchedule{}
	}
	c.JSON(http.StatusOK, schedules)
}

func academicBuildScheduleFromRequest(store AcademicDashboardStore, id uuid.UUID, req AcademicUpsertScheduleRequest) (*models.ClassSchedule, error) {
	courseIDRaw := strings.TrimSpace(req.CourseID)
	roomIDRaw := strings.TrimSpace(req.RoomID)
	section := strings.TrimSpace(req.Section)
	dayOfWeek := strings.TrimSpace(req.DayOfWeek)
	startTime := strings.TrimSpace(req.StartTime)
	endTime := strings.TrimSpace(req.EndTime)
	academicTerm := strings.TrimSpace(req.AcademicTerm)

	if courseIDRaw == "" || roomIDRaw == "" || section == "" || dayOfWeek == "" || startTime == "" || endTime == "" || academicTerm == "" {
		return nil, errors.New("courseId, roomId, section, dayOfWeek, startTime, endTime, and academicTerm are required")
	}

	courseUUID, err := uuid.Parse(courseIDRaw)
	if err != nil || courseUUID == uuid.Nil {
		return nil, errors.New("invalid courseId UUID")
	}
	roomUUID, err := uuid.Parse(roomIDRaw)
	if err != nil || roomUUID == uuid.Nil {
		return nil, errors.New("invalid roomId UUID")
	}

	lecturerUUID, err := academicParseOptionalUUID(req.LecturerID, "lecturerId")
	if err != nil {
		return nil, err
	}

	if req.MaxCapacity != nil && *req.MaxCapacity <= 0 {
		return nil, errors.New("maxCapacity must be greater than 0")
	}

	instructor := academicNormalizeOptionalString(req.Instructor)
	if lecturerUUID != nil && instructor == nil {
		if lecturer, err := store.GetLecturerByID(*lecturerUUID); err == nil && lecturer != nil {
			name := strings.TrimSpace(lecturer.Name)
			if name != "" {
				instructor = &name
			}
		}
	}

	return &models.ClassSchedule{
		ID:           id,
		CourseID:     &courseUUID,
		RoomID:       &roomUUID,
		LecturerID:   lecturerUUID,
		Section:      &section,
		Instructor:   instructor,
		DayOfWeek:    &dayOfWeek,
		StartTime:    &startTime,
		EndTime:      &endTime,
		AcademicTerm: &academicTerm,
		MaxCapacity:  req.MaxCapacity,
	}, nil
}

// CreateAcademicSchedule handles POST /dashboard/academic/schedules.
func CreateAcademicSchedule(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	var req AcademicUpsertScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	store := academicActiveDashboardStore()
	schedule, err := academicBuildScheduleFromRequest(store, uuid.New(), req)
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	schedule.CreatedAt = time.Now().UTC()

	if err := store.CreateSchedule(schedule); err != nil {
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to create class schedule")
		return
	}
	c.JSON(http.StatusCreated, schedule)
}

// UpdateAcademicSchedule handles PUT /dashboard/academic/schedules/:id.
func UpdateAcademicSchedule(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	id, ok := academicParseUUIDParam(c, "id")
	if !ok {
		return
	}

	var req AcademicUpsertScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	store := academicActiveDashboardStore()
	schedule, err := academicBuildScheduleFromRequest(store, id, req)
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	if err := store.UpdateSchedule(schedule); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Class schedule not found")
			return
		}
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to update class schedule")
		return
	}
	c.JSON(http.StatusOK, schedule)
}

// DeleteAcademicSchedule handles DELETE /dashboard/academic/schedules/:id.
func DeleteAcademicSchedule(c *gin.Context) {
	if _, ok := RequireEmployeeRole(c, models.JobRoleAcademicAffairs); !ok {
		return
	}

	id, ok := academicParseUUIDParam(c, "id")
	if !ok {
		return
	}

	if err := academicActiveDashboardStore().DeleteSchedule(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Class schedule not found")
			return
		}
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete class schedule")
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true, "id": id})
}
