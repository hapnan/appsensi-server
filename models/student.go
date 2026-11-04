package models

import (
	"time"

	"github.com/google/uuid"
)

// Student corresponds to the "students" table
type Student struct {
	ID         uuid.UUID  `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	StudentID  string     `gorm:"type:text;not null;unique" json:"student_id"`
	Name       string     `gorm:"type:text;not null" json:"name"`
	Department *string    `gorm:"type:text" json:"department"`
	PhotoURL   *string    `gorm:"type:text;column:photo_url" json:"photo_url"`
	UserID     *uuid.UUID `gorm:"type:uuid;index:students_user_idx" json:"user_id"`
	User       *User      `gorm:"foreignKey:UserID;constraint:OnDelete:SET NULL" json:"-"`
	CreatedAt  time.Time  `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`
}

func (Student) TableName() string { return "students" }

// EnrollmentStatus represents the enrollment state of a student in a class schedule.
type EnrollmentStatus string

const (
	EnrollmentStatusEnrolled EnrollmentStatus = "enrolled"
)

// StudentSchedule corresponds to the "student_schedules" table
type StudentSchedule struct {
	ID              uuid.UUID        `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	StudentID       uuid.UUID        `gorm:"type:uuid;not null;uniqueIndex:student_schedule_unique_idx,priority:1;index:student_schedules_student_idx" json:"student_id"`
	Student         Student          `gorm:"foreignKey:StudentID;constraint:OnDelete:CASCADE" json:"-"`
	ClassScheduleID uuid.UUID        `gorm:"type:uuid;not null;uniqueIndex:student_schedule_unique_idx,priority:2;index:student_schedules_class_idx" json:"class_schedule_id"`
	ClassSchedule   ClassSchedule    `gorm:"foreignKey:ClassScheduleID;constraint:OnDelete:CASCADE" json:"-"`
	Status          EnrollmentStatus `gorm:"type:text;not null;default:'enrolled'" json:"status"`
	EnrolledAt      time.Time        `gorm:"type:timestamptz;not null;default:now()" json:"enrolled_at"`
}

func (StudentSchedule) TableName() string { return "student_schedules" }

// Attendance corresponds to the "attendance" table
type Attendance struct {
	ID              uuid.UUID     `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	StudentID       uuid.UUID     `gorm:"type:uuid;not null;uniqueIndex:attendance_student_room_day_idx,priority:1" json:"student_id"`
	Student         Student       `gorm:"foreignKey:StudentID;constraint:OnDelete:CASCADE" json:"-"`
	RoomID          uuid.UUID     `gorm:"type:uuid;not null;index:attendance_room_day_idx,priority:1" json:"room_id"`
	Room            Room          `gorm:"foreignKey:RoomID;constraint:OnDelete:CASCADE" json:"-"`
	ClassScheduleID uuid.UUID     `gorm:"type:uuid;not null;uniqueIndex:attendance_student_room_day_idx,priority:2;index:attendance_class_schedule_idx" json:"class_schedule_id"`
	ClassSchedule   ClassSchedule `gorm:"foreignKey:ClassScheduleID;constraint:OnDelete:CASCADE" json:"-"`
	SessionDate     time.Time     `gorm:"type:date;not null;uniqueIndex:attendance_student_room_day_idx,priority:3;index:attendance_room_day_idx,priority:2" json:"session_date"`
	CheckedInAt     time.Time     `gorm:"type:timestamptz;not null;default:now()" json:"checked_in_at"`
}

func (Attendance) TableName() string { return "attendance" }
