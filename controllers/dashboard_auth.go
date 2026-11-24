package controllers

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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
)

const passkeySessionTTL = 24 * time.Hour

type passkeySessionEntry struct {
	Context   DashboardUserContext
	CreatedAt time.Time
}

var (
	passkeySessions   = make(map[string]passkeySessionEntry)
	passkeySessionsMu sync.RWMutex
)

func cloneDashboardUserContext(ctx DashboardUserContext) DashboardUserContext {
	cp := ctx
	if ctx.ProfileID != nil {
		v := *ctx.ProfileID
		cp.ProfileID = &v
	}
	if ctx.JobRole != nil {
		v := *ctx.JobRole
		cp.JobRole = &v
	}
	if ctx.Department != nil {
		v := *ctx.Department
		cp.Department = &v
	}
	if ctx.FacultyID != nil {
		v := *ctx.FacultyID
		cp.FacultyID = &v
	}
	if ctx.FacultyCode != nil {
		v := *ctx.FacultyCode
		cp.FacultyCode = &v
	}
	if ctx.FacultyName != nil {
		v := *ctx.FacultyName
		cp.FacultyName = &v
	}
	return cp
}

// CreatePasskeySession generates a random 32-byte hex session token and stores the user context for 24h.
func CreatePasskeySession(ctx DashboardUserContext) string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		copy(buf, []byte(strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")))
	}
	token := hex.EncodeToString(buf)
	now := time.Now()
	passkeySessionsMu.Lock()
	defer passkeySessionsMu.Unlock()
	for k, entry := range passkeySessions {
		if now.Sub(entry.CreatedAt) > passkeySessionTTL {
			delete(passkeySessions, k)
		}
	}
	passkeySessions[token] = passkeySessionEntry{
		Context:   cloneDashboardUserContext(ctx),
		CreatedAt: now,
	}
	return token
}

// GetPasskeySession looks up an active passkey session token.
func GetPasskeySession(token string) (*DashboardUserContext, bool) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return nil, false
	}
	now := time.Now()
	passkeySessionsMu.RLock()
	entry, ok := passkeySessions[trimmed]
	passkeySessionsMu.RUnlock()
	if !ok {
		return nil, false
	}
	if now.Sub(entry.CreatedAt) > passkeySessionTTL {
		passkeySessionsMu.Lock()
		delete(passkeySessions, trimmed)
		passkeySessionsMu.Unlock()
		return nil, false
	}
	cp := cloneDashboardUserContext(entry.Context)
	return &cp, true
}

// ClearPasskeySessions clears all active in-memory passkey sessions (used in tests).
func ClearPasskeySessions() {
	passkeySessionsMu.Lock()
	defer passkeySessionsMu.Unlock()
	passkeySessions = make(map[string]passkeySessionEntry)
}

// DashboardAuthStore resolves authenticated user identity, role, and faculty profile context.
type DashboardAuthStore interface {
	ResolveUserContextByUserID(userID uuid.UUID) (*DashboardUserContext, error)
	ResolveUserContextByUsername(username string) (*DashboardUserContext, error)
}

type gormDashboardAuthStore struct{}

var _ DashboardAuthStore = gormDashboardAuthStore{}

type dashboardProfileRow struct {
	ProfileID   uuid.UUID              `gorm:"column:profile_id"`
	Name        string                 `gorm:"column:name"`
	Department  *string                `gorm:"column:department"`
	JobRole     models.EmployeeJobRole `gorm:"column:job_role"`
	FacultyID   *uuid.UUID             `gorm:"column:faculty_id"`
	FacultyCode *string                `gorm:"column:faculty_code"`
	FacultyName *string                `gorm:"column:faculty_name"`
}

func buildEmployeeProfileContextQuery(db *gorm.DB, userID uuid.UUID, username string) *gorm.DB {
	return db.Raw(`SELECT employees.id AS profile_id, employees.name, employees.department, employees.job_role, employees.faculty_id, faculties.code AS faculty_code, faculties.name AS faculty_name FROM employees LEFT JOIN faculties ON faculties.id = employees.faculty_id WHERE employees.user_id = ? OR employees.employee_id = ? ORDER BY CASE WHEN employees.user_id = ? THEN 0 ELSE 1 END LIMIT 1`, userID, username, userID)
}

func buildLecturerProfileContextQuery(db *gorm.DB, userID uuid.UUID, username string) *gorm.DB {
	return db.Raw(`SELECT lecturers.id AS profile_id, lecturers.name, lecturers.department, lecturers.faculty_id, faculties.code AS faculty_code, faculties.name AS faculty_name FROM lecturers LEFT JOIN faculties ON faculties.id = lecturers.faculty_id WHERE lecturers.user_id = ? OR lecturers.lecturer_id = ? ORDER BY CASE WHEN lecturers.user_id = ? THEN 0 ELSE 1 END LIMIT 1`, userID, username, userID)
}

func buildStudentProfileContextQuery(db *gorm.DB, userID uuid.UUID, username string) *gorm.DB {
	return db.Raw(`SELECT students.id AS profile_id, students.name, students.department, students.faculty_id, faculties.code AS faculty_code, faculties.name AS faculty_name FROM students LEFT JOIN faculties ON faculties.id = students.faculty_id WHERE students.user_id = ? OR students.student_id = ? ORDER BY CASE WHEN students.user_id = ? THEN 0 ELSE 1 END LIMIT 1`, userID, username, userID)
}

func resolveDashboardUserContextFromUser(db *gorm.DB, user *models.User) (*DashboardUserContext, error) {
	userType := user.UserType
	if !models.IsValidUserType(userType) {
		userType = models.UserTypeStudent
	}
	ctx := &DashboardUserContext{UserID: user.ID, Username: user.Username, Email: user.Email, DisplayName: user.WebAuthnDisplayName(), UserType: userType}
	probeOrder := []models.UserType{models.UserTypeStudent, models.UserTypeEmployee, models.UserTypeLecturer}
	switch userType {
	case models.UserTypeEmployee:
		probeOrder = []models.UserType{models.UserTypeEmployee, models.UserTypeLecturer, models.UserTypeStudent}
	case models.UserTypeLecturer:
		probeOrder = []models.UserType{models.UserTypeLecturer, models.UserTypeEmployee, models.UserTypeStudent}
	}
	for _, candidate := range probeOrder {
		var row dashboardProfileRow
		var err error
		switch candidate {
		case models.UserTypeEmployee:
			err = buildEmployeeProfileContextQuery(db, user.ID, user.Username).Scan(&row).Error
		case models.UserTypeLecturer:
			err = buildLecturerProfileContextQuery(db, user.ID, user.Username).Scan(&row).Error
		case models.UserTypeStudent:
			err = buildStudentProfileContextQuery(db, user.ID, user.Username).Scan(&row).Error
		}
		if err != nil {
			return nil, err
		}
		if row.ProfileID != uuid.Nil {
			ctx.UserType = candidate
			profileID := row.ProfileID
			ctx.ProfileID = &profileID
			ctx.Department = row.Department
			ctx.FacultyID = row.FacultyID
			ctx.FacultyCode = row.FacultyCode
			ctx.FacultyName = row.FacultyName
			if candidate == models.UserTypeEmployee && row.JobRole != "" {
				role := row.JobRole
				ctx.JobRole = &role
			}
			if (user.DisplayName == nil || *user.DisplayName == "") && row.Name != "" {
				ctx.DisplayName = row.Name
			}
			break
		}
	}
	return ctx, nil
}

func (gormDashboardAuthStore) ResolveUserContextByUserID(userID uuid.UUID) (*DashboardUserContext, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	var user models.User
	if err := database.DB.Where("id = ?", userID).Take(&user).Error; err != nil {
		return nil, err
	}
	return resolveDashboardUserContextFromUser(database.DB, &user)
}

func (gormDashboardAuthStore) ResolveUserContextByUsername(username string) (*DashboardUserContext, error) {
	if database.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	var user models.User
	if err := database.DB.Where("username = ?", username).Take(&user).Error; err != nil {
		return nil, err
	}
	return resolveDashboardUserContextFromUser(database.DB, &user)
}

var (
	dashboardAuthStoreMu     sync.RWMutex
	dashboardAuthStore       DashboardAuthStore = gormDashboardAuthStore{}
	customDashboardAuthStore bool
)

func SetDashboardAuthStore(store DashboardAuthStore) {
	dashboardAuthStoreMu.Lock()
	defer dashboardAuthStoreMu.Unlock()
	if store == nil {
		dashboardAuthStore = gormDashboardAuthStore{}
		customDashboardAuthStore = false
		return
	}
	dashboardAuthStore = store
	customDashboardAuthStore = true
}

func getDashboardAuthStore() DashboardAuthStore {
	dashboardAuthStoreMu.RLock()
	defer dashboardAuthStoreMu.RUnlock()
	return dashboardAuthStore
}

func hasActiveDashboardAuthStore() bool {
	dashboardAuthStoreMu.RLock()
	defer dashboardAuthStoreMu.RUnlock()
	return customDashboardAuthStore || database.DB != nil
}

func mergeDashboardUserContext(resolved, fallback DashboardUserContext) DashboardUserContext {
	out := cloneDashboardUserContext(resolved)
	if out.UserID == uuid.Nil {
		out.UserID = fallback.UserID
	}
	if out.Username == "" {
		out.Username = fallback.Username
	}
	if out.Email == "" {
		out.Email = fallback.Email
	}
	if out.DisplayName == "" {
		out.DisplayName = fallback.DisplayName
	}
	if !models.IsValidUserType(out.UserType) {
		out.UserType = fallback.UserType
	}
	return out
}

func ResolveUserContextForLogin(user *models.User) DashboardUserContext {
	if user == nil {
		return DashboardUserContext{UserType: models.UserTypeStudent}
	}
	userType := user.UserType
	if !models.IsValidUserType(userType) {
		userType = models.UserTypeStudent
	}
	fallback := DashboardUserContext{UserID: user.ID, Username: user.Username, Email: user.Email, DisplayName: user.WebAuthnDisplayName(), UserType: userType}
	if !hasActiveDashboardAuthStore() {
		return fallback
	}
	store := getDashboardAuthStore()
	if user.ID != uuid.Nil {
		if ctx, err := store.ResolveUserContextByUserID(user.ID); err == nil && ctx != nil {
			return mergeDashboardUserContext(*ctx, fallback)
		}
	}
	if user.Username != "" {
		if ctx, err := store.ResolveUserContextByUsername(user.Username); err == nil && ctx != nil {
			return mergeDashboardUserContext(*ctx, fallback)
		}
	}
	return fallback
}

func extractPasskeyToken(c *gin.Context) string {
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if len(authHeader) >= 7 && strings.EqualFold(authHeader[:7], "Bearer ") {
		if tok := strings.TrimSpace(authHeader[7:]); tok != "" {
			return tok
		}
	}
	return strings.TrimSpace(c.GetHeader("X-Session-Token"))
}

func RequireDashboardUser(c *gin.Context) (*DashboardUserContext, bool) {
	if token := extractPasskeyToken(c); token != "" {
		userCtx, ok := GetPasskeySession(token)
		if !ok {
			respondAPIError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid or expired session token")
			return nil, false
		}
		return userCtx, true
	}
	rawUserID := strings.TrimSpace(c.GetHeader("X-User-ID"))
	if rawUserID == "" {
		rawUserID = strings.TrimSpace(c.Query("userId"))
	}
	if rawUserID != "" {
		parsedID, err := uuid.Parse(rawUserID)
		if err != nil || parsedID == uuid.Nil {
			respondAPIError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid userId UUID")
			return nil, false
		}
		userCtx, err := getDashboardAuthStore().ResolveUserContextByUserID(parsedID)
		if err != nil || userCtx == nil {
			respondAPIError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "Authenticated user not found")
			return nil, false
		}
		cp := cloneDashboardUserContext(*userCtx)
		return &cp, true
	}
	username := strings.TrimSpace(c.GetHeader("X-Username"))
	if username == "" {
		username = strings.TrimSpace(c.Query("username"))
	}
	if username != "" {
		userCtx, err := getDashboardAuthStore().ResolveUserContextByUsername(username)
		if err != nil || userCtx == nil {
			respondAPIError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "Authenticated user not found")
			return nil, false
		}
		cp := cloneDashboardUserContext(*userCtx)
		return &cp, true
	}
	respondAPIError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "Authentication required")
	return nil, false
}

func RequireEmployeeRole(c *gin.Context, allowedRoles ...models.EmployeeJobRole) (*DashboardUserContext, bool) {
	userCtx, ok := RequireDashboardUser(c)
	if !ok {
		return nil, false
	}
	if userCtx.UserType != models.UserTypeEmployee || userCtx.JobRole == nil {
		respondAPIError(c, http.StatusForbidden, ErrCodeForbidden, "Employee role required")
		return nil, false
	}
	role := *userCtx.JobRole
	if len(allowedRoles) > 0 {
		if !slices.Contains(allowedRoles, role) {
			respondAPIError(c, http.StatusForbidden, ErrCodeForbidden, "Insufficient employee role permissions")
			return nil, false
		}
	} else if !models.IsValidEmployeeJobRole(role) {
		respondAPIError(c, http.StatusForbidden, ErrCodeForbidden, "Invalid employee role")
		return nil, false
	}
	if role == models.JobRoleStudentAffairs && (userCtx.FacultyID == nil || *userCtx.FacultyID == uuid.Nil) {
		respondAPIError(c, http.StatusForbidden, ErrCodeForbidden, "student_affairs employee must be assigned to a faculty")
		return nil, false
	}
	return userCtx, true
}

func RequireLecturerUser(c *gin.Context) (*DashboardUserContext, bool) {
	userCtx, ok := RequireDashboardUser(c)
	if !ok {
		return nil, false
	}
	if userCtx.UserType != models.UserTypeLecturer || userCtx.ProfileID == nil || *userCtx.ProfileID == uuid.Nil {
		respondAPIError(c, http.StatusForbidden, ErrCodeForbidden, "Lecturer account required")
		return nil, false
	}
	return userCtx, true
}

func GetDashboardMe(c *gin.Context) {
	userCtx, ok := RequireDashboardUser(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, userCtx)
}
