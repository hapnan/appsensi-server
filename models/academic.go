package models

import (
	"time"
	"github.com/google/uuid"
)

// Building corresponds to the "buildings" table
type Building struct {
	ID          uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Code        string    `gorm:"type:text;not null;unique" json:"code"`
	Name        string    `gorm:"type:text;not null" json:"name"`
	Purpose     string    `gorm:"type:text;not null" json:"purpose"`
	HasClasses  bool      `gorm:"type:boolean;not null;default:true" json:"has_classes"`
	TotalFloors int       `gorm:"type:integer;not null;default:3" json:"total_floors"`
}

func (Building) TableName() string { return "buildings" }

// Room corresponds to the "rooms" table
type Room struct {
	ID         uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Code       string    `gorm:"type:text;not null;unique" json:"code"`
	BuildingID uuid.UUID `gorm:"type:uuid;not null;index:rooms_building_idx" json:"building_id"`
	Building   Building  `gorm:"foreignKey:BuildingID;constraint:OnDelete:CASCADE" json:"-"`
	Floor      int       `gorm:"type:integer;not null" json:"floor"`
	RoomNumber string    `gorm:"type:text;not null" json:"room_number"`
	Name       *string   `gorm:"type:text" json:"name"`
	Capacity   *int      `gorm:"type:integer" json:"capacity"`
}

func (Room) TableName() string { return "rooms" }

// Faculty corresponds to the "faculties" table
type Faculty struct {
	ID         uuid.UUID  `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Code       string     `gorm:"type:text;not null;unique" json:"code"`
	Name       string     `gorm:"type:text;not null" json:"name"`
	BuildingID *uuid.UUID `gorm:"type:uuid;index:faculties_building_idx" json:"building_id"`
	Building   *Building  `gorm:"foreignKey:BuildingID;constraint:OnDelete:SET NULL" json:"-"`
	CreatedAt  time.Time  `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`
}

func (Faculty) TableName() string { return "faculties" }

// Course corresponds to the "courses" table
type Course struct {
	ID          uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Code        string    `gorm:"type:text;not null;unique" json:"code"`
	Name        string    `gorm:"type:text;not null" json:"name"`
	Credits     int       `gorm:"type:integer;not null;default:3" json:"credits"`
	Description *string   `gorm:"type:text" json:"description"`
	FacultyID   uuid.UUID `gorm:"type:uuid;not null;index:courses_faculty_idx" json:"faculty_id"`
	Faculty     Faculty   `gorm:"foreignKey:FacultyID;constraint:OnDelete:CASCADE" json:"-"`
	CreatedAt   time.Time `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`
}

func (Course) TableName() string { return "courses" }

// ClassSchedule corresponds to the "class_schedules" table
type ClassSchedule struct {
	ID           uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	CourseID     uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:class_schedules_course_sec_term_idx;index:class_schedules_course_idx" json:"course_id"`
	Course       Course    `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE" json:"-"`
	RoomID       uuid.UUID `gorm:"type:uuid;not null;index:class_schedules_room_idx;index:class_schedules_day_room_idx" json:"room_id"`
	Room         Room      `gorm:"foreignKey:RoomID;constraint:OnDelete:CASCADE" json:"-"`
	Section      string    `gorm:"type:text;not null;default:'A';uniqueIndex:class_schedules_course_sec_term_idx" json:"section"`
	Instructor   string    `gorm:"type:text;not null" json:"instructor"`
	DayOfWeek    string    `gorm:"type:text;not null;index:class_schedules_day_room_idx" json:"day_of_week"`
	StartTime    string    `gorm:"type:text;not null" json:"start_time"`
	EndTime      string    `gorm:"type:text;not null" json:"end_time"`
	AcademicTerm string    `gorm:"type:text;not null;uniqueIndex:class_schedules_course_sec_term_idx" json:"academic_term"`
	MaxCapacity  *int      `gorm:"type:integer" json:"max_capacity"`
	CreatedAt    time.Time `gorm:"type:timestamptz;not null;default:now()" json:"created_at"`
}

func (ClassSchedule) TableName() string { return "class_schedules" }
