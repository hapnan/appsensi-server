package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"appsensi-server/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type memoryAttendanceStore struct {
	mu                     sync.Mutex
	catalogRows            []demoCatalogRow
	roomsByCode            map[string]*models.Room
	schedulesByRoom        map[uuid.UUID][]rawScheduleRow
	enrolledBySchedule     map[uuid.UUID][]enrolledStudentRow
	students               []*models.Student
	registeredUsersByID    map[uuid.UUID]bool
	registeredUsersByLogin map[string]uuid.UUID
	enrollments            map[string]bool // "<studentUUID>:<scheduleUUID>"
	attendanceKeys         map[string]time.Time
	forceInsertConflict    bool
}

func newMemoryAttendanceStore() *memoryAttendanceStore {
	return &memoryAttendanceStore{
		roomsByCode:            make(map[string]*models.Room),
		schedulesByRoom:        make(map[uuid.UUID][]rawScheduleRow),
		enrolledBySchedule:     make(map[uuid.UUID][]enrolledStudentRow),
		registeredUsersByID:    make(map[uuid.UUID]bool),
		registeredUsersByLogin: make(map[string]uuid.UUID),
		enrollments:            make(map[string]bool),
		attendanceKeys:         make(map[string]time.Time),
	}
}

func attKey(studentID, scheduleID uuid.UUID, sessionDate SessionDate) string {
	return studentID.String() + ":" + scheduleID.String() + ":" + sessionDate.String()
}

func (m *memoryAttendanceStore) ListDemoCatalogRows() ([]demoCatalogRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.catalogRows), nil
}

func (m *memoryAttendanceStore) FindRoomByCode(code RoomCode) (*models.Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.roomsByCode[code.String()]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (m *memoryAttendanceStore) ListRoomSchedulesForDay(roomID uuid.UUID, targetDay string) ([]rawScheduleRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []rawScheduleRow
	for _, s := range m.schedulesByRoom[roomID] {
		if s.DayOfWeek != nil && strings.EqualFold(strings.TrimSpace(*s.DayOfWeek), targetDay) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *memoryAttendanceStore) ListEnrolledStudentsForSchedule(scheduleID uuid.UUID) ([]enrolledStudentRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.enrolledBySchedule[scheduleID]), nil
}

func (m *memoryAttendanceStore) ListCheckedInStudentIDs(scheduleID uuid.UUID, sessionDate SessionDate) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []uuid.UUID
	suffix := ":" + scheduleID.String() + ":" + sessionDate.String()
	for k := range m.attendanceKeys {
		if strings.HasSuffix(k, suffix) {
			parts := strings.SplitN(k, ":", 2)
			if id, err := uuid.Parse(parts[0]); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

func (m *memoryAttendanceStore) FindStudentByIdentifier(identifier string) (*models.Student, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	parsedUUID, uuidErr := uuid.Parse(identifier)
	for _, s := range m.students {
		if (uuidErr == nil && s.ID == parsedUUID) || s.StudentID == identifier {
			cp := *s
			if s.UserID != nil {
				u := *s.UserID
				cp.UserID = &u
			}
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *memoryAttendanceStore) FindRegisteredAccountForStudent(student *models.Student) (*uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if student.UserID != nil && m.registeredUsersByID[*student.UserID] {
		u := *student.UserID
		return &u, nil
	}
	if u, ok := m.registeredUsersByLogin[student.StudentID]; ok && m.registeredUsersByID[u] {
		cp := u
		return &cp, nil
	}
	return nil, nil
}

func (m *memoryAttendanceStore) LinkStudentToUser(studentUUID, userUUID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.students {
		if s.ID == studentUUID {
			u := userUUID
			s.UserID = &u
			break
		}
	}
	return nil
}

func (m *memoryAttendanceStore) IsStudentEnrolled(studentUUID, scheduleID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enrollments[studentUUID.String()+":"+scheduleID.String()], nil
}

func (m *memoryAttendanceStore) HasAttendance(studentUUID, scheduleID uuid.UUID, sessionDate SessionDate) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.attendanceKeys[attKey(studentUUID, scheduleID, sessionDate)]
	return ok, nil
}

func (m *memoryAttendanceStore) InsertAttendance(studentUUID, roomID, scheduleID uuid.UUID, sessionDate SessionDate) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.forceInsertConflict {
		return time.Time{}, false, nil
	}
	key := attKey(studentUUID, scheduleID, sessionDate)
	if _, exists := m.attendanceKeys[key]; exists {
		return time.Time{}, false, nil
	}
	ts := time.Date(2026, 11, 4, 10, 16, 0, 0, time.UTC)
	m.attendanceKeys[key] = ts
	return ts, true, nil
}

func setupAttendanceTestRouter(t *testing.T, store AttendanceStore, fixedNow time.Time) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	SetAttendanceStore(store)
	SetAttendanceClock(func() time.Time { return fixedNow })
	t.Cleanup(func() {
		SetAttendanceStore(nil)
		SetAttendanceClock(nil)
	})

	r := gin.New()
	att := r.Group("/attendance")
	{
		att.GET("/demo-catalog", GetDemoCatalog)
		att.GET("/roster", GetRoster)
		att.POST("/check-in", CheckIn)
	}
	return r
}

func strPtr(s string) *string { return &s }

func TestGetDemoCatalog_Empty(t *testing.T) {
	store := newMemoryAttendanceStore()
	r := setupAttendanceTestRouter(t, store, time.Date(2026, 11, 4, 9, 0, 0, 0, time.Local))

	req := httptest.NewRequest(http.MethodGet, "/attendance/demo-catalog", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("body = %q, want []", w.Body.String())
	}
}

func TestGetDemoCatalog_GroupingSortingAndNullFallbacks(t *testing.T) {
	store := newMemoryAttendanceStore()
	fcsCode := "FCS"
	fcsName := "Fakultas Ilmu Komputer"

	schedFri := uuid.New()
	schedMonLate := uuid.New()
	schedMonEarly := uuid.New()
	schedRoom10 := uuid.New()

	store.catalogRows = []demoCatalogRow{
		{
			ScheduleID:   schedRoom10,
			Section:      nil, // should default to "A"
			DayOfWeek:    strPtr("Tuesday"),
			StartTime:    strPtr("13:00"),
			EndTime:      strPtr("15:00"),
			RoomCode:     "H.2.10",
			Floor:        2,
			RoomNumber:   "10",
			BuildingCode: "H",
			BuildingName: "Gedung H",
			CourseCode:   "FCS-201",
			CourseName:   "Jaringan",
			FacultyCode:  nil,
			FacultyName:  nil,
		},
		{
			ScheduleID:   schedFri,
			Section:      strPtr("B"),
			DayOfWeek:    strPtr("Friday"),
			StartTime:    strPtr("08:00"),
			EndTime:      strPtr("10:00"),
			RoomCode:     "H.2.4",
			Floor:        2,
			RoomNumber:   "4",
			BuildingCode: "H",
			BuildingName: "Gedung H",
			CourseCode:   "FCS-103",
			CourseName:   "PBO",
			FacultyCode:  &fcsCode,
			FacultyName:  &fcsName,
		},
		{
			ScheduleID:   schedMonLate,
			Section:      strPtr("A"),
			DayOfWeek:    strPtr("Monday"),
			StartTime:    strPtr("10:00"),
			EndTime:      strPtr("12:00"),
			RoomCode:     "h.2.4",
			Floor:        2,
			RoomNumber:   "4",
			BuildingCode: "H",
			BuildingName: "Gedung H",
			CourseCode:   "FCS-102",
			CourseName:   "SDA",
			FacultyCode:  &fcsCode,
			FacultyName:  &fcsName,
		},
		{
			ScheduleID:   schedMonEarly,
			Section:      strPtr("A"),
			DayOfWeek:    strPtr("Monday"),
			StartTime:    strPtr("9:00"), // single-digit hour must sort before 10:00
			EndTime:      strPtr("10:00"),
			RoomCode:     "h.2.4",
			Floor:        2,
			RoomNumber:   "4",
			BuildingCode: "H",
			BuildingName: "Gedung H",
			CourseCode:   "FCS-101",
			CourseName:   "Logika",
			FacultyCode:  &fcsCode,
			FacultyName:  &fcsName,
		},
	}

	r := setupAttendanceTestRouter(t, store, time.Date(2026, 11, 4, 9, 0, 0, 0, time.Local))
	req := httptest.NewRequest(http.MethodGet, "/attendance/demo-catalog", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got []DemoCatalogRoom
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(rooms) = %d, want 2", len(got))
	}

	// Numeric roomNumber sort: "4" (h.2.4) must come before "10" (h.2.10)
	if got[0].Code != "h.2.4" || got[1].Code != "h.2.10" {
		t.Fatalf("room order = [%s, %s], want [h.2.4, h.2.10]", got[0].Code, got[1].Code)
	}
	if got[1].Schedules[0].Section != "A" {
		t.Errorf("nil section fallback = %q, want 'A'", got[1].Schedules[0].Section)
	}
	if got[1].FacultyCode != nil {
		t.Errorf("expected nil FacultyCode on h.2.10, got %v", *got[1].FacultyCode)
	}

	// Schedule sort on h.2.4: Mon 9:00 -> Mon 10:00 -> Fri 08:00
	wantOrder := []uuid.UUID{schedMonEarly, schedMonLate, schedFri}
	if len(got[0].Schedules) != 3 {
		t.Fatalf("len(h.2.4 schedules) = %d, want 3", len(got[0].Schedules))
	}
	for i, wantID := range wantOrder {
		if got[0].Schedules[i].ID != wantID {
			t.Errorf("schedule[%d].ID = %s, want %s", i, got[0].Schedules[i].ID, wantID)
		}
	}
}

func TestGetRoster_ValidationErrors(t *testing.T) {
	store := newMemoryAttendanceStore()
	r := setupAttendanceTestRouter(t, store, time.Date(2026, 11, 4, 9, 0, 0, 0, time.Local))

	cases := []struct {
		name  string
		query string
	}{
		{"missing room", ""},
		{"bad room format", "?room=H24"},
		{"bad scheduleId", "?room=h.2.4&scheduleId=not-a-uuid"},
		{"bad date format", "?room=h.2.4&date=04-11-2026"},
		{"invalid calendar date", "?room=h.2.4&date=2026-02-31"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/attendance/roster"+tc.query, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
			}
		})
	}
}

func TestGetRoster_SelectionDeduplicationAndPending(t *testing.T) {
	store := newMemoryAttendanceStore()
	roomID := uuid.New()
	store.roomsByCode["h.2.4"] = &models.Room{ID: roomID, Code: "h.2.4"}

	schedMorning := uuid.New()
	schedMidday := uuid.New()
	store.schedulesByRoom[roomID] = []rawScheduleRow{
		{
			ID:           schedMidday,
			Section:      strPtr("B"),
			Instructor:   strPtr("Dr. Sari"),
			DayOfWeek:    strPtr("Wednesday"),
			StartTime:    strPtr("10:30"),
			EndTime:      strPtr("12:30"),
			AcademicTerm: strPtr("2026/2027-Odd"),
			CourseCode:   "FCS-102",
			CourseName:   "Struktur Data",
			Credits:      3,
		},
		{
			ID:           schedMorning,
			Section:      nil, // defaults to "A"
			Instructor:   nil, // defaults to "TBA"
			DayOfWeek:    strPtr(" Wednesday "),
			StartTime:    strPtr("08:00"),
			EndTime:      strPtr("10:00"),
			AcademicTerm: strPtr("2026/2027-Odd"),
			CourseCode:   "FCS-101",
			CourseName:   "Logika Informatika",
			Credits:      3,
		},
	}

	stuAlice := uuid.New()
	stuBudi := uuid.New()
	stuCitra := uuid.New()
	userAlice := uuid.New()
	userBudi := uuid.New()
	cred1 := uuid.New()
	cred2 := uuid.New()

	// Ordered by name ASC: Alice (2 join rows, 2nd has credential), Budi (user without cred), Citra (already checked in)
	store.enrolledBySchedule[schedMidday] = []enrolledStudentRow{
		{ID: stuAlice, StudentID: "STU-001", Name: "Alice", UserID: &userAlice, Username: strPtr("STU-001"), CredentialID: nil},
		{ID: stuAlice, StudentID: "STU-001", Name: "Alice", UserID: &userAlice, Username: strPtr("STU-001"), CredentialID: &cred1},
		{ID: stuAlice, StudentID: "STU-001", Name: "Alice", UserID: &userAlice, Username: strPtr("STU-001"), CredentialID: &cred2},
		{ID: stuBudi, StudentID: "STU-002", Name: "Budi", UserID: &userBudi, Username: nil, CredentialID: nil},
		{ID: stuCitra, StudentID: "STU-003", Name: "Citra", UserID: &userAlice, Username: strPtr("STU-003"), CredentialID: &cred1},
	}

	wedToday, err := ParseSessionDate("2026-11-04")
	if err != nil {
		t.Fatalf("ParseSessionDate: %v", err)
	}
	store.attendanceKeys[attKey(stuCitra, schedMidday, wedToday)] = time.Now()

	// Clock is Wednesday 2026-11-04 10:15 local -> 08:00-10:00 ended, so 10:30-12:30 is auto-selected!
	now := time.Date(2026, 11, 4, 10, 15, 0, 0, time.Local)
	r := setupAttendanceTestRouter(t, store, now)

	// 1. Auto-selected midday session today
	req := httptest.NewRequest(http.MethodGet, "/attendance/roster?room=H.2.4", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var roster RosterResult
	if err := json.Unmarshal(w.Body.Bytes(), &roster); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if roster.Room != "h.2.4" {
		t.Errorf("room = %q, want 'h.2.4'", roster.Room)
	}
	if roster.Schedule == nil || roster.Schedule.ID != schedMidday {
		t.Fatalf("active schedule = %+v, want %s", roster.Schedule, schedMidday)
	}
	if len(roster.AvailableSchedules) != 2 || roster.AvailableSchedules[0].ID != schedMorning {
		t.Fatalf("availableSchedules not sorted chronologically: %+v", roster.AvailableSchedules)
	}
	if roster.Total != 3 || roster.Present != 1 || len(roster.Pending) != 2 {
		t.Fatalf("counts: total=%d present=%d pending=%d, want 3/1/2", roster.Total, roster.Present, len(roster.Pending))
	}
	if roster.Pending[0].Name != "Alice" || !roster.Pending[0].IsRegistered {
		t.Errorf("pending[0] = %+v, want Alice (isRegistered=true)", roster.Pending[0])
	}
	if roster.Pending[1].Name != "Budi" || roster.Pending[1].IsRegistered || roster.Pending[1].Username != "STU-002" {
		t.Errorf("pending[1] = %+v, want Budi (isRegistered=false, username=STU-002)", roster.Pending[1])
	}

	// 2. Non-today Wednesday (2026-11-11) -> selects first chronological schedule (schedMorning)
	req2 := httptest.NewRequest(http.MethodGet, "/attendance/roster?room=h.2.4&date=2026-11-11", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	var rosterOtherDay RosterResult
	if err := json.Unmarshal(w2.Body.Bytes(), &rosterOtherDay); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rosterOtherDay.Schedule == nil || rosterOtherDay.Schedule.ID != schedMorning {
		t.Fatalf("other day schedule = %+v, want morning %s", rosterOtherDay.Schedule, schedMorning)
	}
	if rosterOtherDay.Schedule.Section != "A" || rosterOtherDay.Schedule.Instructor != "TBA" {
		t.Errorf("null fallbacks = section:%q instructor:%q, want A/TBA", rosterOtherDay.Schedule.Section, rosterOtherDay.Schedule.Instructor)
	}

	// 3. Explicit scheduleId overrides time-based selection today
	req3 := httptest.NewRequest(http.MethodGet, "/attendance/roster?room=h.2.4&scheduleId="+schedMorning.String(), nil)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	var rosterExplicit RosterResult
	if err := json.Unmarshal(w3.Body.Bytes(), &rosterExplicit); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rosterExplicit.Schedule == nil || rosterExplicit.Schedule.ID != schedMorning {
		t.Fatalf("explicit schedule = %+v, want %s", rosterExplicit.Schedule, schedMorning)
	}

	// 4. Unknown room returns emptyResult with 200 OK
	req4 := httptest.NewRequest(http.MethodGet, "/attendance/roster?room=z.9.9", nil)
	w4 := httptest.NewRecorder()
	r.ServeHTTP(w4, req4)
	var emptyRoster RosterResult
	if err := json.Unmarshal(w4.Body.Bytes(), &emptyRoster); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if emptyRoster.Schedule != nil || len(emptyRoster.Pending) != 0 || emptyRoster.Total != 0 {
		t.Fatalf("expected empty roster, got %+v", emptyRoster)
	}
}

func performPostJSON(r http.Handler, path string, payload any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCheckIn_EndToEndAndErrorPaths(t *testing.T) {
	store := newMemoryAttendanceStore()
	roomID := uuid.New()
	store.roomsByCode["h.2.4"] = &models.Room{ID: roomID, Code: "h.2.4"}

	schedWed1 := uuid.New()
	schedWed2 := uuid.New()
	store.schedulesByRoom[roomID] = []rawScheduleRow{
		{
			ID:         schedWed1,
			Section:    strPtr("A"),
			DayOfWeek:  strPtr("Wednesday"),
			StartTime:  strPtr("08:00"),
			EndTime:    strPtr("10:00"),
			CourseCode: "FCS-101",
			CourseName: "Logika",
			Credits:    3,
		},
		{
			ID:         schedWed2,
			Section:    strPtr("A"),
			DayOfWeek:  strPtr("Wednesday"),
			StartTime:  strPtr("10:30"),
			EndTime:    strPtr("12:30"),
			CourseCode: "FCS-102",
			CourseName: "SDA",
			Credits:    3,
		},
	}

	stuAndiID := uuid.New()
	stuUnregisteredID := uuid.New()
	stuUnenrolledID := uuid.New()
	userAndiID := uuid.New()
	userUnenrolledID := uuid.New()

	stuAndi := &models.Student{
		ID:        stuAndiID,
		StudentID: "STU-FCS-001",
		Name:      "Andi Pratama",
		UserID:    nil, // Unlinked initially; CheckIn should backfill UserID!
	}
	stuUnregistered := &models.Student{
		ID:        stuUnregisteredID,
		StudentID: "STU-FCS-002",
		Name:      "Budi Santoso",
	}
	stuUnenrolled := &models.Student{
		ID:        stuUnenrolledID,
		StudentID: "STU-FCS-003",
		Name:      "Citra Dewi",
		UserID:    &userUnenrolledID,
	}
	store.students = []*models.Student{stuAndi, stuUnregistered, stuUnenrolled}

	store.registeredUsersByID[userAndiID] = true
	store.registeredUsersByLogin["STU-FCS-001"] = userAndiID
	store.registeredUsersByID[userUnenrolledID] = true

	store.enrollments[stuAndiID.String()+":"+schedWed1.String()] = true
	store.enrollments[stuAndiID.String()+":"+schedWed2.String()] = true

	now := time.Date(2026, 11, 4, 10, 45, 0, 0, time.Local) // Wednesday -> schedWed2 is ongoing
	r := setupAttendanceTestRouter(t, store, now)

	// 1. Validation errors (400)
	if w := performPostJSON(r, "/attendance/check-in", map[string]any{"room": "bad", "studentId": "STU-FCS-001"}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad room status = %d, want 400", w.Code)
	}
	if w := performPostJSON(r, "/attendance/check-in", map[string]any{"room": "h.2.4", "studentId": ""}); w.Code != http.StatusBadRequest {
		t.Fatalf("empty studentId status = %d, want 400", w.Code)
	}
	if w := performPostJSON(r, "/attendance/check-in", map[string]any{"room": "h.2.4", "studentId": "STU-FCS-001", "scheduleId": "bad"}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad scheduleId status = %d, want 400", w.Code)
	}
	if w := performPostJSON(r, "/attendance/check-in", map[string]any{"room": "h.2.4", "studentId": "STU-FCS-001", "date": "2026-02-30"}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad date status = %d, want 400", w.Code)
	}

	// 2. Room not found (404)
	wRoom := performPostJSON(r, "/attendance/check-in", map[string]any{"room": "z.9.9", "studentId": "STU-FCS-001"})
	if wRoom.Code != http.StatusNotFound || !strings.Contains(wRoom.Body.String(), "Room Z.9.9 not found") {
		t.Fatalf("missing room = %d %s", wRoom.Code, wRoom.Body.String())
	}

	// 3. Student not found (404)
	wStu := performPostJSON(r, "/attendance/check-in", map[string]any{"room": "h.2.4", "studentId": "STU-MISSING"})
	if wStu.Code != http.StatusNotFound || !strings.Contains(wStu.Body.String(), "Student not found") {
		t.Fatalf("missing student = %d %s", wStu.Code, wStu.Body.String())
	}

	// 4. Student without passkey (412)
	wNoPasskey := performPostJSON(r, "/attendance/check-in", map[string]any{"room": "h.2.4", "studentId": "STU-FCS-002"})
	if wNoPasskey.Code != http.StatusPreconditionFailed || !strings.Contains(wNoPasskey.Body.String(), "Budi Santoso has not registered a passkey yet") {
		t.Fatalf("no passkey = %d %s", wNoPasskey.Code, wNoPasskey.Body.String())
	}

	// 5. Unscheduled custom date (412 with formatted date)
	wNoSched := performPostJSON(r, "/attendance/check-in", map[string]any{
		"room":      "h.2.4",
		"studentId": "STU-FCS-001",
		"date":      "2026-11-08", // Sunday
	})
	if wNoSched.Code != http.StatusPreconditionFailed || !strings.Contains(wNoSched.Body.String(), "No class sessions are scheduled in Room H.2.4 on Sun, Nov 8, 2026") {
		t.Fatalf("unscheduled day = %d %s", wNoSched.Code, wNoSched.Body.String())
	}

	// 6. Explicit scheduleId not matching room/day (400)
	wWrongSched := performPostJSON(r, "/attendance/check-in", map[string]any{
		"room":       "h.2.4",
		"studentId":  "STU-FCS-001",
		"scheduleId": uuid.New().String(),
	})
	if wWrongSched.Code != http.StatusBadRequest || !strings.Contains(wWrongSched.Body.String(), "Selected class session is not scheduled in Room H.2.4 today") {
		t.Fatalf("wrong schedule = %d %s", wWrongSched.Code, wWrongSched.Body.String())
	}

	// 7. Student not enrolled in active session (400)
	wUnenrolled := performPostJSON(r, "/attendance/check-in", map[string]any{
		"room":      "h.2.4",
		"studentId": stuUnenrolledID.String(),
	})
	if wUnenrolled.Code != http.StatusBadRequest || !strings.Contains(wUnenrolled.Body.String(), "Citra Dewi is not enrolled in FCS-102 in Room H.2.4") {
		t.Fatalf("unenrolled = %d %s", wUnenrolled.Code, wUnenrolled.Body.String())
	}

	// 8. Successful check-in by badge studentId -> backfills stuAndi.UserID and records attendance for schedWed2
	wOK := performPostJSON(r, "/attendance/check-in", map[string]any{
		"room":      "H.2.4",
		"studentId": "STU-FCS-001",
	})
	if wOK.Code != http.StatusOK {
		t.Fatalf("checkIn status = %d, want 200: %s", wOK.Code, wOK.Body.String())
	}
	var okRes CheckInResponse
	if err := json.Unmarshal(wOK.Body.Bytes(), &okRes); err != nil {
		t.Fatalf("unmarshal CheckInResponse: %v", err)
	}
	if okRes.ID != stuAndiID || okRes.StudentID != "STU-FCS-001" || okRes.Room != "h.2.4" || okRes.SessionDate != "2026-11-04" {
		t.Errorf("unexpected CheckInResponse: %+v", okRes)
	}
	if !strings.Contains(wOK.Body.String(), `"id":"`+stuAndiID.String()+`"`) {
		t.Errorf("expected hyphenated string UUID in wire JSON, got %s", wOK.Body.String())
	}
	if stuAndi.UserID == nil || *stuAndi.UserID != userAndiID {
		t.Errorf("expected stuAndi.UserID to be backfilled to %s, got %v", userAndiID, stuAndi.UserID)
	}

	// 9. Duplicate check-in on same schedule/day (409)
	wDup := performPostJSON(r, "/attendance/check-in", map[string]any{
		"room":      "h.2.4",
		"studentId": stuAndiID.String(),
	})
	if wDup.Code != http.StatusConflict || !strings.Contains(wDup.Body.String(), "Andi Pratama is already checked in") {
		t.Fatalf("duplicate checkIn = %d %s", wDup.Code, wDup.Body.String())
	}

	// 10. Different class session (schedWed1) in the same room on the same day succeeds (200)!
	wSecondSession := performPostJSON(r, "/attendance/check-in", map[string]any{
		"room":       "h.2.4",
		"studentId":  stuAndiID.String(),
		"scheduleId": schedWed1.String(),
	})
	if wSecondSession.Code != http.StatusOK {
		t.Fatalf("second session same room/day failed: %d %s", wSecondSession.Code, wSecondSession.Body.String())
	}
}
