package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// EmployeeJobRole represents the functional role of an employee in the dashboard.
type EmployeeJobRole string

const (
	JobRoleFinance         EmployeeJobRole = "finance"
	JobRoleAcademicAffairs EmployeeJobRole = "academic_affairs"
	JobRoleStudentAffairs  EmployeeJobRole = "student_affairs"
)

// IsValidEmployeeJobRole checks whether role is a supported EmployeeJobRole.
func IsValidEmployeeJobRole(role EmployeeJobRole) bool {
	switch role {
	case JobRoleFinance, JobRoleAcademicAffairs, JobRoleStudentAffairs:
		return true
	default:
		return false
	}
}

// ValidateEmployeeRoleScope verifies that role is one of the valid employee job roles
// and that student_affairs employees have a non-nil, non-zero faculty_id.
func ValidateEmployeeRoleScope(role EmployeeJobRole, facultyID *uuid.UUID) error {
	if !IsValidEmployeeJobRole(role) {
		return errors.New("invalid employee job_role: must be finance, academic_affairs, or student_affairs")
	}
	if role == JobRoleStudentAffairs && (facultyID == nil || *facultyID == uuid.Nil) {
		return errors.New("faculty_id is required when job_role is student_affairs")
	}
	return nil
}

// Employee corresponds to the "employees" table
type Employee struct {
	ID         uuid.UUID       `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	EmployeeID string          `gorm:"type:text;not null;unique" json:"employee_id"`
	Name       string          `gorm:"type:text;not null" json:"name"`
	Department *string         `gorm:"type:text" json:"department"`
	JobRole    EmployeeJobRole `gorm:"type:text;not null;index:employees_job_role_idx;index:employees_role_faculty_idx,priority:1" json:"job_role"`
	FacultyID  *uuid.UUID      `gorm:"type:uuid;index:employees_faculty_idx;index:employees_role_faculty_idx,priority:2" json:"faculty_id"`
	Faculty    *Faculty        `gorm:"belongsTo;foreignKey:FacultyID;references:ID;constraint:OnDelete:SET NULL" json:"-"`
	PhotoURL   *string         `gorm:"type:text;column:photo_url" json:"photo_url"`
	UserID     *uuid.UUID      `gorm:"type:uuid;index:employees_user_idx" json:"user_id"`
	User       *User           `gorm:"belongsTo;foreignKey:UserID;references:ID;constraint:OnDelete:SET NULL" json:"-"`
	CreatedAt  time.Time       `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`
	UpdatedAt  *time.Time      `gorm:"type:timestamptz;autoUpdateTime" json:"updated_at"`
}

func (Employee) TableName() string { return "employees" }

// Lecturer corresponds to the "lecturers" table
type Lecturer struct {
	ID         uuid.UUID  `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	LecturerID string     `gorm:"type:text;not null;unique" json:"lecturer_id"`
	Name       string     `gorm:"type:text;not null" json:"name"`
	Department *string    `gorm:"type:text" json:"department"`
	FacultyID  *uuid.UUID `gorm:"type:uuid;index:lecturers_faculty_idx" json:"faculty_id"`
	Faculty    *Faculty   `gorm:"belongsTo;foreignKey:FacultyID;references:ID;constraint:OnDelete:SET NULL" json:"-"`
	PhotoURL   *string    `gorm:"type:text;column:photo_url" json:"photo_url"`
	UserID     *uuid.UUID `gorm:"type:uuid;index:lecturers_user_idx" json:"user_id"`
	User       *User      `gorm:"belongsTo;foreignKey:UserID;references:ID;constraint:OnDelete:SET NULL" json:"-"`
	CreatedAt  time.Time  `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`
	UpdatedAt  *time.Time `gorm:"type:timestamptz;autoUpdateTime" json:"updated_at"`
}

func (Lecturer) TableName() string { return "lecturers" }
