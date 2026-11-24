package controllers

import (
	"errors"
	"fmt"
	"log"
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

// RegistrationStudentRow represents a student paired with any matching WebAuthn user account.
type RegistrationStudentRow struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Username     string     `json:"username"`
	Department   *string    `json:"department"`
	UserID       *uuid.UUID `json:"userId"`
	Email        *string    `json:"email"`
	CreatedAt    *time.Time `json:"createdAt"`
	IsRegistered bool       `json:"isRegistered"`
}

// LinkStudentUserRequest represents the JSON payload for POST /registration/link-student.
type LinkStudentUserRequest struct {
	StudentID string `json:"studentId"`
	Username  string `json:"username"`
}

// LinkStudentUserResponse represents the JSON payload returned after linking a student to a user.
type LinkStudentUserResponse struct {
	UserID    uuid.UUID `json:"userId"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"createdAt"`
}

// CleanupIncompleteUserRequest represents the JSON payload for POST /registration/cleanup-incomplete.
type CleanupIncompleteUserRequest struct {
	Username string `json:"username"`
}

// CleanupIncompleteUserResponse represents the JSON payload returned by cleanup-incomplete.
type CleanupIncompleteUserResponse struct {
	Cleaned bool `json:"cleaned"`
}

type registrationJoinRow struct {
	ID           uuid.UUID  `gorm:"column:id"`
	Name         string     `gorm:"column:name"`
	Username     string     `gorm:"column:username"`
	Department   *string    `gorm:"column:department"`
	UserID       *uuid.UUID `gorm:"column:user_id"`
	Email        *string    `gorm:"column:email"`
	CreatedAt    *time.Time `gorm:"column:created_at"`
	CredentialID *uuid.UUID `gorm:"column:credential_id"`
}

// RegistrationStore abstracts database operations for student passkey registration endpoints,
// backed by PostgreSQL (GORM) in production and overridable in unit tests.
type RegistrationStore interface {
	ListStudentsWithRegistration() ([]registrationJoinRow, error)
	FindUserByUsername(username string) (*models.User, error)
	UpdateStudentUserID(studentUUID, userUUID uuid.UUID) error
	LinkStudentByUsernameIfUnlinked(username string, userUUID uuid.UUID) error
	UserHasCredentials(userUUID uuid.UUID) (bool, error)
	DeleteUser(userUUID uuid.UUID) error
}

type gormRegistrationStore struct{}

var _ RegistrationStore = gormRegistrationStore{}

func buildStudentsWithRegistrationQuery(db *gorm.DB) *gorm.DB {
	return db.Raw(`WITH first_cred AS (
			SELECT DISTINCT ON (user_id) user_id, id
			FROM credentials
		)
		SELECT students.id,
			students.name,
			students.student_id AS username,
			students.department,
			CASE
				WHEN cred_id.id IS NOT NULL THEN u_id.id
				WHEN cred_name.id IS NOT NULL THEN u_name.id
				ELSE COALESCE(u_id.id, u_name.id)
			END AS user_id,
			CASE
				WHEN cred_id.id IS NOT NULL THEN u_id.email
				WHEN cred_name.id IS NOT NULL THEN u_name.email
				ELSE COALESCE(u_id.email, u_name.email)
			END AS email,
			CASE
				WHEN cred_id.id IS NOT NULL THEN u_id.created_at
				WHEN cred_name.id IS NOT NULL THEN u_name.created_at
				ELSE COALESCE(u_id.created_at, u_name.created_at)
			END AS created_at,
			COALESCE(cred_id.id, cred_name.id) AS credential_id
		FROM students
		LEFT JOIN users AS u_id ON u_id.id = students.user_id
		LEFT JOIN first_cred AS cred_id ON cred_id.user_id = u_id.id
		LEFT JOIN users AS u_name ON u_name.username = students.student_id
			AND (u_id.id IS NULL OR u_name.id <> u_id.id)
		LEFT JOIN first_cred AS cred_name ON cred_name.user_id = u_name.id
		ORDER BY students.student_id ASC`)
}

func (gormRegistrationStore) ListStudentsWithRegistration() ([]registrationJoinRow, error) {
	var rows []registrationJoinRow
	if err := buildStudentsWithRegistrationQuery(database.DB).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (gormRegistrationStore) FindUserByUsername(username string) (*models.User, error) {
	var user models.User
	err := database.DB.Where("username = ?", username).Take(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (gormRegistrationStore) UpdateStudentUserID(studentUUID, userUUID uuid.UUID) error {
	return database.DB.Model(&models.Student{}).
		Where("id = ?", studentUUID).
		Update("user_id", userUUID).Error
}

func (gormRegistrationStore) LinkStudentByUsernameIfUnlinked(username string, userUUID uuid.UUID) error {
	return database.DB.Model(&models.Student{}).
		Where("student_id = ? AND user_id IS NULL", username).
		Update("user_id", userUUID).Error
}

func (gormRegistrationStore) UserHasCredentials(userUUID uuid.UUID) (bool, error) {
	var count int64
	err := database.DB.Model(&models.Credential{}).
		Where("user_id = ?", userUUID).
		Limit(1).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (gormRegistrationStore) DeleteUser(userUUID uuid.UUID) error {
	return database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userUUID).Delete(&models.WebAuthnUser{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", userUUID).Delete(&models.User{}).Error
	})
}

var (
	registrationStoreMu sync.RWMutex
	registrationStore   RegistrationStore = gormRegistrationStore{}
)

// SetRegistrationStore overrides the persistence store for registration endpoints (used by unit tests).
func SetRegistrationStore(store RegistrationStore) {
	registrationStoreMu.Lock()
	defer registrationStoreMu.Unlock()
	if store == nil {
		registrationStore = gormRegistrationStore{}
		return
	}
	registrationStore = store
}

func getRegistrationStore() RegistrationStore {
	registrationStoreMu.RLock()
	defer registrationStoreMu.RUnlock()
	return registrationStore
}

func linkStudentAfterRegistration(username string, userUUID uuid.UUID) error {
	return getRegistrationStore().LinkStudentByUsernameIfUnlinked(username, userUUID)
}

// ListRegistrationStudents handles GET /registration/list.
// Lists all students paired with any matching account in the users table.
func ListRegistrationStudents(c *gin.Context) {
	store := getRegistrationStore()

	rows, err := store.ListStudentsWithRegistration()
	if err != nil {
		log.Printf("ListRegistrationStudents error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load registration list")
		return
	}

	students := make([]RegistrationStudentRow, 0, len(rows))
	indexByID := make(map[uuid.UUID]int, len(rows))

	for _, row := range rows {
		isRegistered := row.UserID != nil && row.CredentialID != nil

		entry := RegistrationStudentRow{
			ID:           row.ID,
			Name:         row.Name,
			Username:     row.Username,
			Department:   row.Department,
			UserID:       row.UserID,
			Email:        row.Email,
			CreatedAt:    row.CreatedAt,
			IsRegistered: isRegistered,
		}

		if idx, exists := indexByID[row.ID]; exists {
			if students[idx].IsRegistered || !isRegistered {
				continue
			}
			students[idx] = entry
			continue
		}
		indexByID[row.ID] = len(students)
		students = append(students, entry)
	}

	c.JSON(http.StatusOK, students)
}

// LinkStudentUser handles POST /registration/link-student.
// Links students.user_id to users.id once WebAuthn registration finishes.
func LinkStudentUser(c *gin.Context) {
	store := getRegistrationStore()

	var req LinkStudentUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid JSON request body")
		return
	}

	studentUUID, err := uuid.Parse(strings.TrimSpace(req.StudentID))
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid studentId UUID")
		return
	}

	if len(req.Username) == 0 {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "username is required")
		return
	}

	user, err := store.FindUserByUsername(req.Username)
	if err != nil {
		log.Printf("LinkStudentUser FindUserByUsername error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to look up user")
		return
	}
	if user == nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, fmt.Sprintf("Registered user %q was not found.", req.Username))
		return
	}

	if err := store.UpdateStudentUserID(studentUUID, user.ID); err != nil {
		log.Printf("LinkStudentUser UpdateStudentUserID error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to link student user")
		return
	}

	c.JSON(http.StatusOK, LinkStudentUserResponse{
		UserID:    user.ID,
		Email:     user.Email,
		CreatedAt: user.CreatedAt,
	})
}

// CleanupIncompleteUser handles POST /registration/cleanup-incomplete.
// Removes an unverified users record (one with zero passkey credentials) if a WebAuthn ceremony is cancelled or fails.
func CleanupIncompleteUser(c *gin.Context) {
	store := getRegistrationStore()

	var req CleanupIncompleteUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid JSON request body")
		return
	}

	if len(req.Username) == 0 {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "username is required")
		return
	}

	user, err := store.FindUserByUsername(req.Username)
	if err != nil {
		log.Printf("CleanupIncompleteUser FindUserByUsername error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to look up user")
		return
	}
	if user == nil {
		c.JSON(http.StatusOK, CleanupIncompleteUserResponse{Cleaned: false})
		return
	}

	hasCreds, err := store.UserHasCredentials(user.ID)
	if err != nil {
		log.Printf("CleanupIncompleteUser UserHasCredentials error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to check user credentials")
		return
	}
	if hasCreds {
		c.JSON(http.StatusOK, CleanupIncompleteUserResponse{Cleaned: false})
		return
	}

	deleteSession(user.ID)

	if err := store.DeleteUser(user.ID); err != nil {
		log.Printf("CleanupIncompleteUser DeleteUser error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete incomplete user")
		return
	}

	c.JSON(http.StatusOK, CleanupIncompleteUserResponse{Cleaned: true})
}

// RegistrationEmployeeRow represents an employee paired with any matching WebAuthn user account.
type RegistrationEmployeeRow struct {
	ID           uuid.UUID              `json:"id"`
	Name         string                 `json:"name"`
	Username     string                 `json:"username"`
	Department   *string                `json:"department"`
	JobRole      models.EmployeeJobRole `json:"jobRole"`
	FacultyID    *uuid.UUID             `json:"facultyId"`
	FacultyCode  *string                `json:"facultyCode"`
	FacultyName  *string                `json:"facultyName"`
	UserID       *uuid.UUID             `json:"userId"`
	Email        *string                `json:"email"`
	CreatedAt    *time.Time             `json:"createdAt"`
	IsRegistered bool                   `json:"isRegistered"`
}

// LinkEmployeeUserRequest represents the JSON payload for POST /registration/link-employee.
type LinkEmployeeUserRequest struct {
	EmployeeID string `json:"employeeId"`
	Username   string `json:"username"`
}

// RegistrationLecturerRow represents a lecturer paired with any matching WebAuthn user account.
type RegistrationLecturerRow struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Username     string     `json:"username"`
	Department   *string    `json:"department"`
	FacultyID    *uuid.UUID `json:"facultyId"`
	FacultyCode  *string    `json:"facultyCode"`
	FacultyName  *string    `json:"facultyName"`
	UserID       *uuid.UUID `json:"userId"`
	Email        *string    `json:"email"`
	CreatedAt    *time.Time `json:"createdAt"`
	IsRegistered bool       `json:"isRegistered"`
}

// LinkLecturerUserRequest represents the JSON payload for POST /registration/link-lecturer.
type LinkLecturerUserRequest struct {
	LecturerID string `json:"lecturerId"`
	Username   string `json:"username"`
}

type employeeRegistrationJoinRow struct {
	ID           uuid.UUID              `gorm:"column:id"`
	Name         string                 `gorm:"column:name"`
	Username     string                 `gorm:"column:username"`
	Department   *string                `gorm:"column:department"`
	JobRole      models.EmployeeJobRole `gorm:"column:job_role"`
	FacultyID    *uuid.UUID             `gorm:"column:faculty_id"`
	FacultyCode  *string                `gorm:"column:faculty_code"`
	FacultyName  *string                `gorm:"column:faculty_name"`
	UserID       *uuid.UUID             `gorm:"column:user_id"`
	Email        *string                `gorm:"column:email"`
	CreatedAt    *time.Time             `gorm:"column:created_at"`
	CredentialID *uuid.UUID             `gorm:"column:credential_id"`
}

type lecturerRegistrationJoinRow struct {
	ID           uuid.UUID  `gorm:"column:id"`
	Name         string     `gorm:"column:name"`
	Username     string     `gorm:"column:username"`
	Department   *string    `gorm:"column:department"`
	FacultyID    *uuid.UUID `gorm:"column:faculty_id"`
	FacultyCode  *string    `gorm:"column:faculty_code"`
	FacultyName  *string    `gorm:"column:faculty_name"`
	UserID       *uuid.UUID `gorm:"column:user_id"`
	Email        *string    `gorm:"column:email"`
	CreatedAt    *time.Time `gorm:"column:created_at"`
	CredentialID *uuid.UUID `gorm:"column:credential_id"`
}

// StaffRegistrationStore abstracts database operations for employee and lecturer passkey registration endpoints.
type StaffRegistrationStore interface {
	ListEmployeesWithRegistration() ([]employeeRegistrationJoinRow, error)
	UpdateEmployeeUserID(employeeUUID, userUUID uuid.UUID) error
	LinkEmployeeByUsernameIfUnlinked(username string, userUUID uuid.UUID) error
	ListLecturersWithRegistration() ([]lecturerRegistrationJoinRow, error)
	UpdateLecturerUserID(lecturerUUID, userUUID uuid.UUID) error
	LinkLecturerByUsernameIfUnlinked(username string, userUUID uuid.UUID) error
	FindUserByUsername(username string) (*models.User, error)
}

type gormStaffRegistrationStore struct{}

var _ StaffRegistrationStore = gormStaffRegistrationStore{}

func buildEmployeesWithRegistrationQuery(db *gorm.DB) *gorm.DB {
	return db.Raw(`WITH first_cred AS ( SELECT DISTINCT ON (user_id) user_id, id FROM credentials ) SELECT employees.id, employees.name, employees.employee_id AS username, employees.department, employees.job_role, employees.faculty_id, faculties.code AS faculty_code, faculties.name AS faculty_name, CASE WHEN cred_id.id IS NOT NULL THEN u_id.id WHEN cred_name.id IS NOT NULL THEN u_name.id ELSE COALESCE(u_id.id, u_name.id) END AS user_id, CASE WHEN cred_id.id IS NOT NULL THEN u_id.email WHEN cred_name.id IS NOT NULL THEN u_name.email ELSE COALESCE(u_id.email, u_name.email) END AS email, CASE WHEN cred_id.id IS NOT NULL THEN u_id.created_at WHEN cred_name.id IS NOT NULL THEN u_name.created_at ELSE COALESCE(u_id.created_at, u_name.created_at) END AS created_at, COALESCE(cred_id.id, cred_name.id) AS credential_id FROM employees LEFT JOIN faculties ON faculties.id = employees.faculty_id LEFT JOIN users AS u_id ON u_id.id = employees.user_id LEFT JOIN first_cred AS cred_id ON cred_id.user_id = u_id.id LEFT JOIN users AS u_name ON u_name.username = employees.employee_id AND (u_id.id IS NULL OR u_name.id <> u_id.id) LEFT JOIN first_cred AS cred_name ON cred_name.user_id = u_name.id ORDER BY employees.employee_id ASC`)
}

func buildLecturersWithRegistrationQuery(db *gorm.DB) *gorm.DB {
	return db.Raw(`WITH first_cred AS ( SELECT DISTINCT ON (user_id) user_id, id FROM credentials ) SELECT lecturers.id, lecturers.name, lecturers.lecturer_id AS username, lecturers.department, lecturers.faculty_id, faculties.code AS faculty_code, faculties.name AS faculty_name, CASE WHEN cred_id.id IS NOT NULL THEN u_id.id WHEN cred_name.id IS NOT NULL THEN u_name.id ELSE COALESCE(u_id.id, u_name.id) END AS user_id, CASE WHEN cred_id.id IS NOT NULL THEN u_id.email WHEN cred_name.id IS NOT NULL THEN u_name.email ELSE COALESCE(u_id.email, u_name.email) END AS email, CASE WHEN cred_id.id IS NOT NULL THEN u_id.created_at WHEN cred_name.id IS NOT NULL THEN u_name.created_at ELSE COALESCE(u_id.created_at, u_name.created_at) END AS created_at, COALESCE(cred_id.id, cred_name.id) AS credential_id FROM lecturers LEFT JOIN faculties ON faculties.id = lecturers.faculty_id LEFT JOIN users AS u_id ON u_id.id = lecturers.user_id LEFT JOIN first_cred AS cred_id ON cred_id.user_id = u_id.id LEFT JOIN users AS u_name ON u_name.username = lecturers.lecturer_id AND (u_id.id IS NULL OR u_name.id <> u_id.id) LEFT JOIN first_cred AS cred_name ON cred_name.user_id = u_name.id ORDER BY lecturers.lecturer_id ASC`)
}

func (gormStaffRegistrationStore) ListEmployeesWithRegistration() ([]employeeRegistrationJoinRow, error) {
	var rows []employeeRegistrationJoinRow
	if err := buildEmployeesWithRegistrationQuery(database.DB).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (gormStaffRegistrationStore) UpdateEmployeeUserID(employeeUUID, userUUID uuid.UUID) error {
	return database.DB.Model(&models.Employee{}).Where("id = ?", employeeUUID).Update("user_id", userUUID).Error
}

func (gormStaffRegistrationStore) LinkEmployeeByUsernameIfUnlinked(username string, userUUID uuid.UUID) error {
	if database.DB == nil {
		return nil
	}
	return database.DB.Model(&models.Employee{}).Where("employee_id = ? AND user_id IS NULL", username).Update("user_id", userUUID).Error
}

func (gormStaffRegistrationStore) ListLecturersWithRegistration() ([]lecturerRegistrationJoinRow, error) {
	var rows []lecturerRegistrationJoinRow
	if err := buildLecturersWithRegistrationQuery(database.DB).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (gormStaffRegistrationStore) UpdateLecturerUserID(lecturerUUID, userUUID uuid.UUID) error {
	return database.DB.Model(&models.Lecturer{}).Where("id = ?", lecturerUUID).Update("user_id", userUUID).Error
}

func (gormStaffRegistrationStore) LinkLecturerByUsernameIfUnlinked(username string, userUUID uuid.UUID) error {
	if database.DB == nil {
		return nil
	}
	return database.DB.Model(&models.Lecturer{}).Where("lecturer_id = ? AND user_id IS NULL", username).Update("user_id", userUUID).Error
}

func (gormStaffRegistrationStore) FindUserByUsername(username string) (*models.User, error) {
	return gormRegistrationStore{}.FindUserByUsername(username)
}

var (
	staffRegistrationStoreMu sync.RWMutex
	staffRegistrationStore   StaffRegistrationStore = gormStaffRegistrationStore{}
)

func SetStaffRegistrationStore(store StaffRegistrationStore) {
	staffRegistrationStoreMu.Lock()
	defer staffRegistrationStoreMu.Unlock()
	if store == nil {
		staffRegistrationStore = gormStaffRegistrationStore{}
		return
	}
	staffRegistrationStore = store
}

func getStaffRegistrationStore() StaffRegistrationStore {
	staffRegistrationStoreMu.RLock()
	defer staffRegistrationStoreMu.RUnlock()
	return staffRegistrationStore
}

func linkEmployeeAfterRegistration(username string, userUUID uuid.UUID) error {
	return getStaffRegistrationStore().LinkEmployeeByUsernameIfUnlinked(username, userUUID)
}

func linkLecturerAfterRegistration(username string, userUUID uuid.UUID) error {
	return getStaffRegistrationStore().LinkLecturerByUsernameIfUnlinked(username, userUUID)
}

func linkAccountAfterRegistration(user *models.User) error {
	if user == nil {
		return nil
	}
	switch user.UserType {
	case models.UserTypeEmployee:
		return linkEmployeeAfterRegistration(user.Username, user.ID)
	case models.UserTypeLecturer:
		return linkLecturerAfterRegistration(user.Username, user.ID)
	default:
		return linkStudentAfterRegistration(user.Username, user.ID)
	}
}

func ListRegistrationEmployees(c *gin.Context) {
	store := getStaffRegistrationStore()
	rows, err := store.ListEmployeesWithRegistration()
	if err != nil {
		log.Printf("ListRegistrationEmployees error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load employee registration list")
		return
	}
	employees := make([]RegistrationEmployeeRow, 0, len(rows))
	indexByID := make(map[uuid.UUID]int, len(rows))
	for _, row := range rows {
		isRegistered := row.UserID != nil && row.CredentialID != nil
		entry := RegistrationEmployeeRow{ID: row.ID, Name: row.Name, Username: row.Username, Department: row.Department, JobRole: row.JobRole, FacultyID: row.FacultyID, FacultyCode: row.FacultyCode, FacultyName: row.FacultyName, UserID: row.UserID, Email: row.Email, CreatedAt: row.CreatedAt, IsRegistered: isRegistered}
		if idx, exists := indexByID[row.ID]; exists {
			if employees[idx].IsRegistered || !isRegistered {
				continue
			}
			employees[idx] = entry
			continue
		}
		indexByID[row.ID] = len(employees)
		employees = append(employees, entry)
	}
	c.JSON(http.StatusOK, employees)
}

func LinkEmployeeUser(c *gin.Context) {
	store := getStaffRegistrationStore()
	var req LinkEmployeeUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid JSON request body")
		return
	}
	employeeUUID, err := uuid.Parse(strings.TrimSpace(req.EmployeeID))
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid employeeId UUID")
		return
	}
	if len(req.Username) == 0 {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "username is required")
		return
	}
	user, err := store.FindUserByUsername(req.Username)
	if err != nil {
		log.Printf("LinkEmployeeUser FindUserByUsername error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to look up user")
		return
	}
	if user == nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, fmt.Sprintf("Registered user %q was not found.", req.Username))
		return
	}
	if err := store.UpdateEmployeeUserID(employeeUUID, user.ID); err != nil {
		log.Printf("LinkEmployeeUser UpdateEmployeeUserID error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to link employee user")
		return
	}
	c.JSON(http.StatusOK, LinkStudentUserResponse{UserID: user.ID, Email: user.Email, CreatedAt: user.CreatedAt})
}

func ListRegistrationLecturers(c *gin.Context) {
	store := getStaffRegistrationStore()
	rows, err := store.ListLecturersWithRegistration()
	if err != nil {
		log.Printf("ListRegistrationLecturers error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load lecturer registration list")
		return
	}
	lecturers := make([]RegistrationLecturerRow, 0, len(rows))
	indexByID := make(map[uuid.UUID]int, len(rows))
	for _, row := range rows {
		isRegistered := row.UserID != nil && row.CredentialID != nil
		entry := RegistrationLecturerRow{ID: row.ID, Name: row.Name, Username: row.Username, Department: row.Department, FacultyID: row.FacultyID, FacultyCode: row.FacultyCode, FacultyName: row.FacultyName, UserID: row.UserID, Email: row.Email, CreatedAt: row.CreatedAt, IsRegistered: isRegistered}
		if idx, exists := indexByID[row.ID]; exists {
			if lecturers[idx].IsRegistered || !isRegistered {
				continue
			}
			lecturers[idx] = entry
			continue
		}
		indexByID[row.ID] = len(lecturers)
		lecturers = append(lecturers, entry)
	}
	c.JSON(http.StatusOK, lecturers)
}

func LinkLecturerUser(c *gin.Context) {
	store := getStaffRegistrationStore()
	var req LinkLecturerUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid JSON request body")
		return
	}
	lecturerUUID, err := uuid.Parse(strings.TrimSpace(req.LecturerID))
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid lecturerId UUID")
		return
	}
	if len(req.Username) == 0 {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "username is required")
		return
	}
	user, err := store.FindUserByUsername(req.Username)
	if err != nil {
		log.Printf("LinkLecturerUser FindUserByUsername error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to look up user")
		return
	}
	if user == nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, fmt.Sprintf("Registered user %q was not found.", req.Username))
		return
	}
	if err := store.UpdateLecturerUserID(lecturerUUID, user.ID); err != nil {
		log.Printf("LinkLecturerUser UpdateLecturerUserID error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to link lecturer user")
		return
	}
	c.JSON(http.StatusOK, LinkStudentUserResponse{UserID: user.ID, Email: user.Email, CreatedAt: user.CreatedAt})
}
