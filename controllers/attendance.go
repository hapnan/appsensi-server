package controllers

import (
	"cmp"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
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
	singleDigitHourTimeRegex = regexp.MustCompile(`^\d:\d{2}$`)

	dayOrder = map[string]int{
		"monday":    1,
		"tuesday":   2,
		"wednesday": 3,
		"thursday":  4,
		"friday":    5,
		"saturday":  6,
		"sunday":    7,
	}
)

// EnrolledStudent represents a student enrolled in an active class session awaiting check-in.
type EnrolledStudent struct {
	ID           uuid.UUID `json:"id"`
	StudentID    string    `json:"studentId"`
	Username     string    `json:"username"`
	Name         string    `json:"name"`
	Department   *string   `json:"department"`
	PhotoURL     *string   `json:"photoUrl"`
	IsRegistered bool      `json:"isRegistered"`
}

// ScheduleSummary represents a condensed class schedule slot.
type ScheduleSummary struct {
	ID         uuid.UUID `json:"id"`
	CourseCode string    `json:"courseCode"`
	CourseName string    `json:"courseName"`
	Section    string    `json:"section"`
	DayOfWeek  string    `json:"dayOfWeek"`
	StartTime  string    `json:"startTime"`
	EndTime    string    `json:"endTime"`
}

// ActiveScheduleDetails extends ScheduleSummary with full course/session metadata.
type ActiveScheduleDetails struct {
	ID           uuid.UUID `json:"id"`
	CourseCode   string    `json:"courseCode"`
	CourseName   string    `json:"courseName"`
	Credits      int       `json:"credits"`
	Section      string    `json:"section"`
	Instructor   string    `json:"instructor"`
	DayOfWeek    string    `json:"dayOfWeek"`
	StartTime    string    `json:"startTime"`
	EndTime      string    `json:"endTime"`
	AcademicTerm string    `json:"academicTerm"`
}

// RosterResult represents the active session and pending student roster for a room on a date.
type RosterResult struct {
	Room               RoomCode               `json:"room"`
	SessionDate        string                 `json:"sessionDate"`
	DayOfWeek          string                 `json:"dayOfWeek"`
	Schedule           *ActiveScheduleDetails `json:"schedule"`
	AvailableSchedules []ScheduleSummary      `json:"availableSchedules"`
	Total              int                    `json:"total"`
	Present            int                    `json:"present"`
	Pending            []EnrolledStudent      `json:"pending"`
}

// DemoCatalogRoom represents a classroom with scheduled weekly sessions for the home catalog.
type DemoCatalogRoom struct {
	Code         string            `json:"code"`
	Floor        int               `json:"floor"`
	RoomNumber   string            `json:"roomNumber"`
	BuildingCode string            `json:"buildingCode"`
	BuildingName string            `json:"buildingName"`
	FacultyCode  *string           `json:"facultyCode"`
	FacultyName  *string           `json:"facultyName"`
	Schedules    []ScheduleSummary `json:"schedules"`
}

// CheckInRequest represents the JSON payload for POST /attendance/check-in.
type CheckInRequest struct {
	Room       string  `json:"room"`
	StudentID  string  `json:"studentId"`
	ScheduleID *string `json:"scheduleId"`
	Date       *string `json:"date"`
}

// CheckInResponse represents the JSON payload returned upon successful check-in.
type CheckInResponse struct {
	ID          uuid.UUID `json:"id"`
	StudentID   string    `json:"studentId"`
	Name        string    `json:"name"`
	Room        RoomCode  `json:"room"`
	SessionDate string    `json:"sessionDate"`
	CheckedInAt time.Time `json:"checkedInAt"`
}

type demoCatalogRow struct {
	ScheduleID   uuid.UUID `gorm:"column:schedule_id"`
	Section      *string   `gorm:"column:section"`
	DayOfWeek    *string   `gorm:"column:day_of_week"`
	StartTime    *string   `gorm:"column:start_time"`
	EndTime      *string   `gorm:"column:end_time"`
	RoomCode     string    `gorm:"column:room_code"`
	Floor        int       `gorm:"column:floor"`
	RoomNumber   string    `gorm:"column:room_number"`
	BuildingCode string    `gorm:"column:building_code"`
	BuildingName string    `gorm:"column:building_name"`
	CourseCode   string    `gorm:"column:course_code"`
	CourseName   string    `gorm:"column:course_name"`
	FacultyCode  *string   `gorm:"column:faculty_code"`
	FacultyName  *string   `gorm:"column:faculty_name"`
}

type rawScheduleRow struct {
	ID           uuid.UUID `gorm:"column:id"`
	Section      *string   `gorm:"column:section"`
	Instructor   *string   `gorm:"column:instructor"`
	DayOfWeek    *string   `gorm:"column:day_of_week"`
	StartTime    *string   `gorm:"column:start_time"`
	EndTime      *string   `gorm:"column:end_time"`
	AcademicTerm *string   `gorm:"column:academic_term"`
	CourseCode   string    `gorm:"column:course_code"`
	CourseName   string    `gorm:"column:course_name"`
	Credits      int       `gorm:"column:credits"`
}

type enrolledStudentRow struct {
	ID           uuid.UUID  `gorm:"column:id"`
	StudentID    string     `gorm:"column:student_id"`
	Name         string     `gorm:"column:name"`
	Department   *string    `gorm:"column:department"`
	PhotoURL     *string    `gorm:"column:photo_url"`
	UserID       *uuid.UUID `gorm:"column:user_id"`
	Username     *string    `gorm:"column:username"`
	CredentialID *uuid.UUID `gorm:"column:credential_id"`
}

// AttendanceStore abstracts database operations for attendance endpoints,
// backed by PostgreSQL (GORM) in production and overridable in unit tests.
type AttendanceStore interface {
	ListDemoCatalogRows() ([]demoCatalogRow, error)
	FindRoomByCode(code RoomCode) (*models.Room, error)
	ListRoomSchedulesForDay(roomID uuid.UUID, targetDay string) ([]rawScheduleRow, error)
	ListEnrolledStudentsForSchedule(scheduleID uuid.UUID) ([]enrolledStudentRow, error)
	ListCheckedInStudentIDs(scheduleID uuid.UUID, sessionDate SessionDate) ([]uuid.UUID, error)
	FindStudentByIdentifier(identifier string) (*models.Student, error)
	FindRegisteredAccountForStudent(student *models.Student) (*uuid.UUID, error)
	LinkStudentToUser(studentUUID, userUUID uuid.UUID) error
	IsStudentEnrolled(studentUUID, scheduleID uuid.UUID) (bool, error)
	HasAttendance(studentUUID, scheduleID uuid.UUID, sessionDate SessionDate) (bool, error)
	InsertAttendance(studentUUID, roomID, scheduleID uuid.UUID, sessionDate SessionDate) (time.Time, bool, error)
}

type gormAttendanceStore struct{}

var _ AttendanceStore = gormAttendanceStore{}

func (gormAttendanceStore) ListDemoCatalogRows() ([]demoCatalogRow, error) {
	var rows []demoCatalogRow
	err := database.DB.Table("class_schedules").
		Select(`class_schedules.id AS schedule_id,
			class_schedules.section,
			class_schedules.day_of_week,
			class_schedules.start_time,
			class_schedules.end_time,
			rooms.code AS room_code,
			rooms.floor,
			rooms.room_number,
			buildings.code AS building_code,
			buildings.name AS building_name,
			courses.code AS course_code,
			courses.name AS course_name,
			faculties.code AS faculty_code,
			faculties.name AS faculty_name`).
		Joins("INNER JOIN rooms ON rooms.id = class_schedules.room_id").
		Joins("INNER JOIN buildings ON buildings.id = rooms.building_id").
		Joins("INNER JOIN courses ON courses.id = class_schedules.course_id").
		Joins("LEFT JOIN faculties ON faculties.id = courses.faculty_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (gormAttendanceStore) FindRoomByCode(code RoomCode) (*models.Room, error) {
	var room models.Room
	err := database.DB.Where("code = ?", code.String()).Take(&room).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &room, nil
}

func (gormAttendanceStore) ListRoomSchedulesForDay(roomID uuid.UUID, targetDay string) ([]rawScheduleRow, error) {
	var rows []rawScheduleRow
	err := database.DB.Table("class_schedules").
		Select(`class_schedules.id,
			class_schedules.section,
			class_schedules.instructor,
			class_schedules.day_of_week,
			class_schedules.start_time,
			class_schedules.end_time,
			class_schedules.academic_term,
			courses.code AS course_code,
			courses.name AS course_name,
			courses.credits`).
		Joins("INNER JOIN courses ON courses.id = class_schedules.course_id").
		Where("class_schedules.room_id = ? AND lower(trim(class_schedules.day_of_week)) = lower(?)", roomID, targetDay).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func buildEnrolledStudentsQuery(db *gorm.DB, scheduleID uuid.UUID) *gorm.DB {
	return db.Table("student_schedules").
		Select(`students.id,
			students.student_id,
			students.name,
			students.department,
			students.photo_url,
			CASE
				WHEN cred_id.id IS NOT NULL THEN u_id.id
				WHEN cred_name.id IS NOT NULL THEN u_name.id
				ELSE COALESCE(u_id.id, u_name.id)
			END AS user_id,
			CASE
				WHEN cred_id.id IS NOT NULL THEN u_id.username
				WHEN cred_name.id IS NOT NULL THEN u_name.username
				ELSE COALESCE(u_id.username, u_name.username)
			END AS username,
			COALESCE(cred_id.id, cred_name.id) AS credential_id`).
		Joins("INNER JOIN students ON students.id = student_schedules.student_id").
		Joins("LEFT JOIN users AS u_id ON u_id.id = students.user_id").
		Joins(`LEFT JOIN LATERAL (
			SELECT credentials.id
			FROM credentials
			WHERE credentials.user_id = u_id.id
			LIMIT 1
		) AS cred_id ON u_id.id IS NOT NULL`).
		Joins(`LEFT JOIN users AS u_name ON u_name.username = students.student_id
			AND (u_id.id IS NULL OR u_name.id <> u_id.id)`).
		Joins(`LEFT JOIN LATERAL (
			SELECT credentials.id
			FROM credentials
			WHERE credentials.user_id = u_name.id
			LIMIT 1
		) AS cred_name ON u_name.id IS NOT NULL`).
		Where("student_schedules.class_schedule_id = ? AND student_schedules.status = ?", scheduleID, models.EnrollmentStatusEnrolled).
		Order("students.name ASC")
}

func (gormAttendanceStore) ListEnrolledStudentsForSchedule(scheduleID uuid.UUID) ([]enrolledStudentRow, error) {
	var rows []enrolledStudentRow
	if err := buildEnrolledStudentsQuery(database.DB, scheduleID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (gormAttendanceStore) ListCheckedInStudentIDs(scheduleID uuid.UUID, sessionDate SessionDate) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := database.DB.Model(&models.Attendance{}).
		Where("class_schedule_id = ? AND session_date = ?", scheduleID, sessionDate.UTC()).
		Pluck("student_id", &ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (gormAttendanceStore) FindStudentByIdentifier(identifier string) (*models.Student, error) {
	var student models.Student
	if parsedUUID, err := uuid.Parse(identifier); err == nil {
		err := database.DB.Where("id = ?", parsedUUID).Take(&student).Error
		if err == nil {
			return &student, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if err := database.DB.Where("student_id = ?", identifier).Take(&student).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &student, nil
}

func buildRegisteredAccountQuery(db *gorm.DB, student *models.Student) *gorm.DB {
	if student.UserID != nil {
		return db.Raw(`SELECT u.id AS user_id
			FROM (
				SELECT id, 1 AS prio FROM users WHERE id = ?
				UNION ALL
				SELECT id, 2 AS prio FROM users WHERE username = ? AND id <> ?
			) AS u
			WHERE EXISTS (SELECT 1 FROM credentials WHERE credentials.user_id = u.id)
			ORDER BY u.prio ASC
			LIMIT 1`, *student.UserID, student.StudentID, *student.UserID)
	}
	return db.Raw(`SELECT users.id AS user_id
		FROM users
		WHERE users.username = ?
			AND EXISTS (SELECT 1 FROM credentials WHERE credentials.user_id = users.id)
		LIMIT 1`, student.StudentID)
}

func (gormAttendanceStore) FindRegisteredAccountForStudent(student *models.Student) (*uuid.UUID, error) {
	type accountRow struct {
		UserID uuid.UUID `gorm:"column:user_id"`
	}
	var row accountRow
	res := buildRegisteredAccountQuery(database.DB, student).Scan(&row)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 || row.UserID == uuid.Nil {
		return nil, nil
	}
	return &row.UserID, nil
}

func (gormAttendanceStore) LinkStudentToUser(studentUUID, userUUID uuid.UUID) error {
	return database.DB.Model(&models.Student{}).
		Where("id = ?", studentUUID).
		Update("user_id", userUUID).Error
}

func (gormAttendanceStore) IsStudentEnrolled(studentUUID, scheduleID uuid.UUID) (bool, error) {
	var count int64
	err := database.DB.Model(&models.StudentSchedule{}).
		Where("student_id = ? AND class_schedule_id = ? AND status = ?", studentUUID, scheduleID, models.EnrollmentStatusEnrolled).
		Limit(1).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (gormAttendanceStore) HasAttendance(studentUUID, scheduleID uuid.UUID, sessionDate SessionDate) (bool, error) {
	var count int64
	err := database.DB.Model(&models.Attendance{}).
		Where("student_id = ? AND class_schedule_id = ? AND session_date = ?", studentUUID, scheduleID, sessionDate.UTC()).
		Limit(1).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (gormAttendanceStore) InsertAttendance(studentUUID, roomID, scheduleID uuid.UUID, sessionDate SessionDate) (time.Time, bool, error) {
	att := models.Attendance{
		StudentID:       studentUUID,
		RoomID:          roomID,
		ClassScheduleID: scheduleID,
		SessionDate:     sessionDate.UTC(),
	}
	res := database.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&att)
	if res.Error != nil {
		return time.Time{}, false, res.Error
	}
	if res.RowsAffected == 0 {
		return time.Time{}, false, nil
	}
	return att.CheckedInAt, true, nil
}

var (
	attendanceStoreMu sync.RWMutex
	attendanceStore   AttendanceStore   = gormAttendanceStore{}
	attendanceNow     func() time.Time  = time.Now
)

// SetAttendanceStore overrides the persistence store for attendance endpoints (used by unit tests).
func SetAttendanceStore(store AttendanceStore) {
	attendanceStoreMu.Lock()
	defer attendanceStoreMu.Unlock()
	if store == nil {
		attendanceStore = gormAttendanceStore{}
		return
	}
	attendanceStore = store
}

// SetAttendanceClock overrides the clock function for attendance endpoints (used by unit tests).
func SetAttendanceClock(clock func() time.Time) {
	attendanceStoreMu.Lock()
	defer attendanceStoreMu.Unlock()
	if clock == nil {
		attendanceNow = time.Now
		return
	}
	attendanceNow = clock
}

func getAttendanceDeps() (AttendanceStore, func() time.Time) {
	attendanceStoreMu.RLock()
	defer attendanceStoreMu.RUnlock()
	return attendanceStore, attendanceNow
}

func currentSessionTime(now time.Time) string {
	return now.Format("15:04")
}

func normalizeTime(value string) string {
	trimmed := strings.TrimSpace(value)
	if singleDigitHourTimeRegex.MatchString(trimmed) {
		return "0" + trimmed
	}
	return trimmed
}

func sortTimeKey(value string) string {
	normalized := normalizeTime(value)
	if len(normalized) > 0 {
		return normalized
	}
	return "99:99"
}

func sortDayKey(dayOfWeek string) int {
	if order, ok := dayOrder[strings.ToLower(strings.TrimSpace(dayOfWeek))]; ok {
		return order
	}
	return 99
}

func stringOrDefault(ptr *string, fallback string) string {
	if ptr == nil {
		return fallback
	}
	return *ptr
}

func parseJSNumber(raw string) (float64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, true
	}
	v, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func fetchRoomSchedulesForDay(store AttendanceStore, roomID uuid.UUID, targetDay string) ([]ActiveScheduleDetails, error) {
	rawSchedules, err := store.ListRoomSchedulesForDay(roomID, targetDay)
	if err != nil {
		return nil, err
	}

	schedules := make([]ActiveScheduleDetails, len(rawSchedules))
	for i, s := range rawSchedules {
		schedules[i] = ActiveScheduleDetails{
			ID:           s.ID,
			CourseCode:   s.CourseCode,
			CourseName:   s.CourseName,
			Credits:      s.Credits,
			Section:      stringOrDefault(s.Section, "A"),
			Instructor:   stringOrDefault(s.Instructor, "TBA"),
			DayOfWeek:    stringOrDefault(s.DayOfWeek, ""),
			StartTime:    stringOrDefault(s.StartTime, ""),
			EndTime:      stringOrDefault(s.EndTime, ""),
			AcademicTerm: stringOrDefault(s.AcademicTerm, ""),
		}
	}

	slices.SortFunc(schedules, func(a, b ActiveScheduleDetails) int {
		if startCmp := strings.Compare(sortTimeKey(a.StartTime), sortTimeKey(b.StartTime)); startCmp != 0 {
			return startCmp
		}
		if endCmp := strings.Compare(sortTimeKey(a.EndTime), sortTimeKey(b.EndTime)); endCmp != 0 {
			return endCmp
		}
		if courseCmp := strings.Compare(a.CourseCode, b.CourseCode); courseCmp != 0 {
			return courseCmp
		}
		return strings.Compare(a.Section, b.Section)
	})

	return schedules, nil
}

// selectActiveScheduleForDay picks the active schedule from a day's sorted room schedules:
// 1. Explicit requestedScheduleID if it matches one of the day's schedules
// 2. When currentTime is provided (viewing today), ongoing or next upcoming session (endTime >= currentTime)
// 3. First chronological session of that day
func selectActiveScheduleForDay(schedules []ActiveScheduleDetails, requestedScheduleID *uuid.UUID, currentTime *string) *ActiveScheduleDetails {
	if len(schedules) == 0 {
		return nil
	}
	if requestedScheduleID != nil {
		for i := range schedules {
			if schedules[i].ID == *requestedScheduleID {
				cp := schedules[i]
				return &cp
			}
		}
	}
	if currentTime != nil {
		normalizedNow := normalizeTime(*currentTime)
		for i := range schedules {
			end := normalizeTime(schedules[i].EndTime)
			if len(end) > 0 && end >= normalizedNow {
				cp := schedules[i]
				return &cp
			}
		}
	}
	cp := schedules[0]
	return &cp
}

// GetDemoCatalog handles GET /attendance/demo-catalog.
// Lists all campus rooms that have scheduled courses along with their weekly class sessions.
func GetDemoCatalog(c *gin.Context) {
	store, _ := getAttendanceDeps()

	rows, err := store.ListDemoCatalogRows()
	if err != nil {
		log.Printf("GetDemoCatalog error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load demo catalog")
		return
	}

	byRoomCode := make(map[string]*DemoCatalogRoom)
	for _, row := range rows {
		normalizedCode := strings.ToLower(row.RoomCode)
		entry, exists := byRoomCode[normalizedCode]
		if !exists {
			entry = &DemoCatalogRoom{
				Code:         normalizedCode,
				Floor:        row.Floor,
				RoomNumber:   row.RoomNumber,
				BuildingCode: row.BuildingCode,
				BuildingName: row.BuildingName,
				FacultyCode:  row.FacultyCode,
				FacultyName:  row.FacultyName,
				Schedules:    []ScheduleSummary{},
			}
			byRoomCode[normalizedCode] = entry
		}
		entry.Schedules = append(entry.Schedules, ScheduleSummary{
			ID:         row.ScheduleID,
			CourseCode: row.CourseCode,
			CourseName: row.CourseName,
			Section:    stringOrDefault(row.Section, "A"),
			DayOfWeek:  stringOrDefault(row.DayOfWeek, ""),
			StartTime:  stringOrDefault(row.StartTime, ""),
			EndTime:    stringOrDefault(row.EndTime, ""),
		})
	}

	rooms := make([]DemoCatalogRoom, 0, len(byRoomCode))
	for _, room := range byRoomCode {
		slices.SortFunc(room.Schedules, func(a, b ScheduleSummary) int {
			if dayCmp := sortDayKey(a.DayOfWeek) - sortDayKey(b.DayOfWeek); dayCmp != 0 {
				return dayCmp
			}
			if startCmp := strings.Compare(sortTimeKey(a.StartTime), sortTimeKey(b.StartTime)); startCmp != 0 {
				return startCmp
			}
			return strings.Compare(a.CourseCode, b.CourseCode)
		})
		rooms = append(rooms, *room)
	}

	slices.SortFunc(rooms, func(a, b DemoCatalogRoom) int {
		if bldgCmp := strings.Compare(a.BuildingCode, b.BuildingCode); bldgCmp != 0 {
			return bldgCmp
		}
		if a.Floor != b.Floor {
			return cmp.Compare(a.Floor, b.Floor)
		}
		numA, okA := parseJSNumber(a.RoomNumber)
		numB, okB := parseJSNumber(b.RoomNumber)
		if okA && okB && numA != numB {
			return cmp.Compare(numA, numB)
		}
		return strings.Compare(a.Code, b.Code)
	})

	c.JSON(http.StatusOK, rooms)
}

// GetRoster handles GET /attendance/roster?room=...&scheduleId=...&date=...
// Returns the active class session and students enrolled in it who still need to check in.
func GetRoster(c *gin.Context) {
	store, nowFn := getAttendanceDeps()

	normalizedRoom, err := ParseRoomCode(c.Query("room"))
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	var requestedScheduleID *uuid.UUID
	if rawSched, hasSched := c.GetQuery("scheduleId"); hasSched && rawSched != "" {
		parsedSched, err := uuid.Parse(strings.TrimSpace(rawSched))
		if err != nil {
			respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid scheduleId UUID")
			return
		}
		requestedScheduleID = &parsedSched
	}

	now := nowFn()
	today := CurrentSessionDate(now)
	sessionDate := today

	if rawDate, hasDate := c.GetQuery("date"); hasDate && rawDate != "" {
		parsedDate, err := ParseSessionDate(rawDate)
		if err != nil {
			respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
			return
		}
		sessionDate = parsedDate
	}

	isToday := sessionDate.Equal(today)
	targetDay := sessionDate.Weekday().String()

	emptyResult := RosterResult{
		Room:               normalizedRoom,
		SessionDate:        sessionDate.String(),
		DayOfWeek:          targetDay,
		Schedule:           nil,
		AvailableSchedules: []ScheduleSummary{},
		Total:              0,
		Present:            0,
		Pending:            []EnrolledStudent{},
	}

	room, err := store.FindRoomByCode(normalizedRoom)
	if err != nil {
		log.Printf("GetRoster FindRoomByCode error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to look up room")
		return
	}
	if room == nil {
		c.JSON(http.StatusOK, emptyResult)
		return
	}

	schedules, err := fetchRoomSchedulesForDay(store, room.ID, targetDay)
	if err != nil {
		log.Printf("GetRoster fetchRoomSchedulesForDay error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load room schedules")
		return
	}
	if len(schedules) == 0 {
		c.JSON(http.StatusOK, emptyResult)
		return
	}

	var currentTime *string
	if isToday {
		ct := currentSessionTime(now)
		currentTime = &ct
	}

	activeSchedule := selectActiveScheduleForDay(schedules, requestedScheduleID, currentTime)
	if activeSchedule == nil {
		c.JSON(http.StatusOK, emptyResult)
		return
	}

	var (
		enrolledRows []enrolledStudentRow
		enrolledErr  error
		checkedInIDs []uuid.UUID
		checkedInErr error
		rosterWG     sync.WaitGroup
	)
	rosterWG.Add(2)
	go func() {
		defer rosterWG.Done()
		enrolledRows, enrolledErr = store.ListEnrolledStudentsForSchedule(activeSchedule.ID)
	}()
	go func() {
		defer rosterWG.Done()
		checkedInIDs, checkedInErr = store.ListCheckedInStudentIDs(activeSchedule.ID, sessionDate)
	}()
	rosterWG.Wait()

	if enrolledErr != nil {
		log.Printf("GetRoster ListEnrolledStudentsForSchedule error: %v", enrolledErr)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load enrolled students")
		return
	}
	if checkedInErr != nil {
		log.Printf("GetRoster ListCheckedInStudentIDs error: %v", checkedInErr)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load attendance records")
		return
	}

	enrolled := make([]EnrolledStudent, 0, len(enrolledRows))
	indexByID := make(map[uuid.UUID]int, len(enrolledRows))
	for _, row := range enrolledRows {
		isRegistered := row.UserID != nil && row.CredentialID != nil
		username := row.StudentID
		if row.Username != nil && *row.Username != "" {
			username = *row.Username
		}
		entry := EnrolledStudent{
			ID:           row.ID,
			StudentID:    row.StudentID,
			Username:     username,
			Name:         row.Name,
			Department:   row.Department,
			PhotoURL:     row.PhotoURL,
			IsRegistered: isRegistered,
		}
		if idx, exists := indexByID[row.ID]; exists {
			if enrolled[idx].IsRegistered || !isRegistered {
				continue
			}
			enrolled[idx] = entry
			continue
		}
		indexByID[row.ID] = len(enrolled)
		enrolled = append(enrolled, entry)
	}

	checkedInSet := make(map[uuid.UUID]struct{}, len(checkedInIDs))
	for _, id := range checkedInIDs {
		checkedInSet[id] = struct{}{}
	}

	pending := make([]EnrolledStudent, 0, len(enrolled))
	for _, s := range enrolled {
		if _, present := checkedInSet[s.ID]; !present {
			pending = append(pending, s)
		}
	}

	availableSchedules := make([]ScheduleSummary, len(schedules))
	for i, s := range schedules {
		availableSchedules[i] = ScheduleSummary{
			ID:         s.ID,
			CourseCode: s.CourseCode,
			CourseName: s.CourseName,
			Section:    s.Section,
			DayOfWeek:  s.DayOfWeek,
			StartTime:  s.StartTime,
			EndTime:    s.EndTime,
		}
	}

	total := len(enrolled)
	present := total - len(pending)

	c.JSON(http.StatusOK, RosterResult{
		Room:               normalizedRoom,
		SessionDate:        sessionDate.String(),
		DayOfWeek:          targetDay,
		Schedule:           activeSchedule,
		AvailableSchedules: availableSchedules,
		Total:              total,
		Present:            present,
		Pending:            pending,
	})
}

// CheckIn handles POST /attendance/check-in.
// Records attendance for a student in a room for the active class schedule.
func CheckIn(c *gin.Context) {
	store, nowFn := getAttendanceDeps()

	var req CheckInRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid JSON request body")
		return
	}

	normalizedRoom, err := ParseRoomCode(req.Room)
	if err != nil {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}

	if len(req.StudentID) == 0 {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "studentId is required")
		return
	}

	var explicitScheduleID *uuid.UUID
	if req.ScheduleID != nil {
		parsedSched, err := uuid.Parse(strings.TrimSpace(*req.ScheduleID))
		if err != nil {
			respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, "Invalid scheduleId UUID")
			return
		}
		explicitScheduleID = &parsedSched
	}

	now := nowFn()
	today := CurrentSessionDate(now)
	sessionDate := today

	if req.Date != nil {
		parsedDate, err := ParseSessionDate(*req.Date)
		if err != nil {
			respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
			return
		}
		sessionDate = parsedDate
	}

	roomUpper := normalizedRoom.Upper()
	isToday := sessionDate.Equal(today)
	targetDay := sessionDate.Weekday().String()
	whenPhrase := "today"
	if !isToday {
		whenPhrase = "on " + sessionDate.Display()
	}

	var (
		room       *models.Room
		roomErr    error
		student    *models.Student
		studentErr error
		lookupWG   sync.WaitGroup
	)
	lookupWG.Add(2)
	go func() {
		defer lookupWG.Done()
		room, roomErr = store.FindRoomByCode(normalizedRoom)
	}()
	go func() {
		defer lookupWG.Done()
		student, studentErr = store.FindStudentByIdentifier(req.StudentID)
	}()
	lookupWG.Wait()

	if roomErr != nil {
		log.Printf("CheckIn FindRoomByCode error: %v", roomErr)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to look up room")
		return
	}
	if room == nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, fmt.Sprintf("Room %s not found", roomUpper))
		return
	}

	if studentErr != nil {
		log.Printf("CheckIn FindStudentByIdentifier error: %v", studentErr)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to look up student")
		return
	}
	if student == nil {
		respondAPIError(c, http.StatusNotFound, ErrCodeNotFound, "Student not found")
		return
	}

	var (
		registeredUserID *uuid.UUID
		regErr           error
		daySchedules     []ActiveScheduleDetails
		schedErr         error
		verifyWG         sync.WaitGroup
	)
	verifyWG.Add(2)
	go func() {
		defer verifyWG.Done()
		registeredUserID, regErr = store.FindRegisteredAccountForStudent(student)
	}()
	go func() {
		defer verifyWG.Done()
		daySchedules, schedErr = fetchRoomSchedulesForDay(store, room.ID, targetDay)
	}()
	verifyWG.Wait()

	if regErr != nil {
		log.Printf("CheckIn FindRegisteredAccountForStudent error: %v", regErr)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to verify student passkey")
		return
	}
	if registeredUserID == nil {
		respondAPIError(c, http.StatusPreconditionFailed, ErrCodePreconditionFailed, fmt.Sprintf("%s has not registered a passkey yet", student.Name))
		return
	}

	if student.UserID == nil {
		if err := store.LinkStudentToUser(student.ID, *registeredUserID); err != nil {
			log.Printf("CheckIn LinkStudentToUser error: %v", err)
			respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to link student account")
			return
		}
	}

	if schedErr != nil {
		log.Printf("CheckIn fetchRoomSchedulesForDay error: %v", schedErr)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to load room schedules")
		return
	}
	if len(daySchedules) == 0 {
		respondAPIError(c, http.StatusPreconditionFailed, ErrCodePreconditionFailed, fmt.Sprintf("No class sessions are scheduled in Room %s %s", roomUpper, whenPhrase))
		return
	}

	var activeSchedule *ActiveScheduleDetails
	if explicitScheduleID != nil {
		for i := range daySchedules {
			if daySchedules[i].ID == *explicitScheduleID {
				cp := daySchedules[i]
				activeSchedule = &cp
				break
			}
		}
		if activeSchedule == nil {
			respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, fmt.Sprintf("Selected class session is not scheduled in Room %s %s", roomUpper, whenPhrase))
			return
		}
	} else {
		var currentTime *string
		if isToday {
			ct := currentSessionTime(now)
			currentTime = &ct
		}
		activeSchedule = selectActiveScheduleForDay(daySchedules, nil, currentTime)
		if activeSchedule == nil {
			respondAPIError(c, http.StatusPreconditionFailed, ErrCodePreconditionFailed, fmt.Sprintf("No class sessions are scheduled in Room %s %s", roomUpper, whenPhrase))
			return
		}
	}

	enrolled, err := store.IsStudentEnrolled(student.ID, activeSchedule.ID)
	if err != nil {
		log.Printf("CheckIn IsStudentEnrolled error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to verify enrollment")
		return
	}
	if !enrolled {
		respondAPIError(c, http.StatusBadRequest, ErrCodeBadRequest, fmt.Sprintf("%s is not enrolled in %s in Room %s", student.Name, activeSchedule.CourseCode, roomUpper))
		return
	}

	checkedInAt, inserted, err := store.InsertAttendance(student.ID, room.ID, activeSchedule.ID, sessionDate)
	if err != nil {
		log.Printf("CheckIn InsertAttendance error: %v", err)
		respondAPIError(c, http.StatusInternalServerError, ErrCodeInternal, "Failed to record attendance")
		return
	}
	if !inserted {
		respondAPIError(c, http.StatusConflict, ErrCodeConflict, fmt.Sprintf("%s is already checked in", student.Name))
		return
	}

	c.JSON(http.StatusOK, CheckInResponse{
		ID:          student.ID,
		StudentID:   student.StudentID,
		Name:        student.Name,
		Room:        normalizedRoom,
		SessionDate: sessionDate.String(),
		CheckedInAt: checkedInAt,
	})
}
