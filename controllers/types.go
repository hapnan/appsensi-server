package controllers

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"appsensi-server/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// APIErrorCode identifies the machine-readable category of an API error response.
type APIErrorCode string

const (
	ErrCodeBadRequest         APIErrorCode = "BAD_REQUEST"
	ErrCodeUnauthorized       APIErrorCode = "UNAUTHORIZED"
	ErrCodeForbidden          APIErrorCode = "FORBIDDEN"
	ErrCodeNotFound           APIErrorCode = "NOT_FOUND"
	ErrCodePreconditionFailed APIErrorCode = "PRECONDITION_FAILED"
	ErrCodeConflict           APIErrorCode = "CONFLICT"
	ErrCodeInternal           APIErrorCode = "INTERNAL_SERVER_ERROR"
)

// APIErrorResponse is the standard JSON error payload across production API routes.
type APIErrorResponse struct {
	Error string       `json:"error"`
	Code  APIErrorCode `json:"code"`
}

// HealthResponse represents the JSON payload returned by GET /ping.
type HealthResponse struct {
	Message string `json:"message"`
}

// AuthStatusResponse represents the JSON payload returned upon WebAuthn ceremony completion.
type DashboardUserContext struct {
	UserID      uuid.UUID               `json:"userId"`
	Username    string                  `json:"username"`
	Email       string                  `json:"email"`
	DisplayName string                  `json:"displayName"`
	UserType    models.UserType         `json:"userType"`
	ProfileID   *uuid.UUID              `json:"profileId,omitempty"`
	JobRole     *models.EmployeeJobRole `json:"jobRole,omitempty"`
	Department  *string                 `json:"department,omitempty"`
	FacultyID   *uuid.UUID              `json:"facultyId,omitempty"`
	FacultyCode *string                 `json:"facultyCode,omitempty"`
	FacultyName *string                 `json:"facultyName,omitempty"`
}

// AuthStatusResponse represents the JSON payload returned upon WebAuthn ceremony completion.
type AuthStatusResponse struct {
	Status       string                `json:"status"`
	Message      string                `json:"message,omitempty"`
	SessionToken string                `json:"sessionToken,omitempty"`
	User         *DashboardUserContext `json:"user,omitempty"`
}

func respondAPIError(c *gin.Context, status int, code APIErrorCode, message string) {
	c.JSON(status, APIErrorResponse{
		Error: message,
		Code:  code,
	})
}

var (
	roomCodeRegex    = regexp.MustCompile(`^[a-z]+\.\d+\.\d+$`)
	sessionDateRegex = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// RoomCode is a normalized, validated classroom code in "building.floor.room" lowercase format.
type RoomCode string

// ParseRoomCode trims, lowercases, and validates a raw room code string.
func ParseRoomCode(raw string) (RoomCode, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if !roomCodeRegex.MatchString(normalized) {
		return "", errors.New("Room must use the building.floor.room format")
	}
	return RoomCode(normalized), nil
}

// String returns the canonical lowercase "building.floor.room" code.
func (r RoomCode) String() string {
	return string(r)
}

// Upper returns the uppercase display representation (e.g. "H.2.4").
func (r RoomCode) Upper() string {
	return strings.ToUpper(string(r))
}

// SessionDate represents a validated calendar date paired with its normalized midnight UTC timestamp.
type SessionDate struct {
	raw string
	utc time.Time
}

// ParseSessionDate validates a YYYY-MM-DD calendar date string.
func ParseSessionDate(raw string) (SessionDate, error) {
	trimmed := strings.TrimSpace(raw)
	if !sessionDateRegex.MatchString(trimmed) {
		return SessionDate{}, errors.New("Date must use YYYY-MM-DD format")
	}
	parsed, err := time.Parse("2006-01-02", trimmed)
	if err != nil || parsed.Format("2006-01-02") != trimmed {
		return SessionDate{}, errors.New("Invalid calendar date")
	}
	return SessionDate{
		raw: trimmed,
		utc: parsed.UTC(),
	}, nil
}

// CurrentSessionDate derives today's validated SessionDate in the provided clock's timezone.
func CurrentSessionDate(now time.Time) SessionDate {
	formatted := now.Format("2006-01-02")
	parsed, _ := time.Parse("2006-01-02", formatted)
	return SessionDate{
		raw: formatted,
		utc: parsed.UTC(),
	}
}

// String returns the canonical YYYY-MM-DD calendar date string.
func (d SessionDate) String() string {
	return d.raw
}

// UTC returns the midnight UTC time.Time for database date comparisons.
func (d SessionDate) UTC() time.Time {
	return d.utc
}

// Weekday returns the day of the week for this calendar date.
func (d SessionDate) Weekday() time.Weekday {
	return d.utc.Weekday()
}

// Display formats the calendar date for user-facing messages (e.g. "Wed, Nov 4, 2026").
func (d SessionDate) Display() string {
	return d.utc.Format("Mon, Jan 2, 2006")
}

// Equal reports whether two SessionDate values refer to the same calendar day.
func (d SessionDate) Equal(other SessionDate) bool {
	return d.raw == other.raw
}
