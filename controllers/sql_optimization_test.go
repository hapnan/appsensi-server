package controllers

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"appsensi-server/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type noopConnPool struct{}

func (noopConnPool) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("noopConnPool: unexpected PrepareContext in DryRun")
}

func (noopConnPool) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("noopConnPool: unexpected ExecContext in DryRun")
}

func (noopConnPool) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("noopConnPool: unexpected QueryContext in DryRun")
}

func (noopConnPool) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

func newDryRunPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		postgres.New(postgres.Config{Conn: noopConnPool{}}),
		&gorm.Config{
			DryRun:                 true,
			SkipDefaultTransaction: true,
			Logger:                 logger.Discard,
		},
	)
	if err != nil {
		t.Fatalf("gorm.Open DryRun failed: %v", err)
	}
	return db
}

func TestAuthQueries_EliminateOrderByJoinWebAuthnUserAndOmitBlobs(t *testing.T) {
	db := newDryRunPostgresDB(t)

	// 1. Login query uses INNER JOIN on webauthn_users + Take (LIMIT 1 without ORDER BY id)
	var loginUser models.User
	loginStmt := queryUserForLogin(db, "STU-FCS-006", "localhost").Take(&loginUser).Statement
	loginSQL := loginStmt.SQL.String()

	if !strings.Contains(loginSQL, `INNER JOIN "webauthn_users" "WebAuthnUser"`) {
		t.Errorf("expected INNER JOIN on webauthn_users in login SQL, got: %s", loginSQL)
	}
	if !strings.Contains(loginSQL, `"WebAuthnUser"."rp_id" =`) {
		t.Errorf("expected rp_id filter in INNER JOIN ON clause, got: %s", loginSQL)
	}
	if strings.Contains(strings.ToUpper(loginSQL), "ORDER BY") {
		t.Errorf("expected no ORDER BY on unique username lookup, got: %s", loginSQL)
	}
	if !strings.Contains(strings.ToUpper(loginSQL), "LIMIT") {
		t.Errorf("expected LIMIT 1 on login lookup, got: %s", loginSQL)
	}

	// 2. Registration query uses LEFT JOIN on webauthn_users + Take (no ORDER BY)
	var regUser models.User
	regStmt := queryUserWithOptionalWebAuthn(db.Session(&gorm.Session{DryRun: true}), "STU-FCS-006", "localhost").Take(&regUser).Statement
	regSQL := regStmt.SQL.String()

	if !strings.Contains(regSQL, `LEFT JOIN "webauthn_users" "WebAuthnUser"`) {
		t.Errorf("expected LEFT JOIN on webauthn_users in registration SQL, got: %s", regSQL)
	}
	if strings.Contains(strings.ToUpper(regSQL), "ORDER BY") {
		t.Errorf("expected no ORDER BY on unique username lookup, got: %s", regSQL)
	}

	// 3. Credential preload scope omits heavy unused bytea columns (attestation, extensions, flags)
	var creds []models.Credential
	credStmt := omitUnusedCredentialBlobs(db.Session(&gorm.Session{DryRun: true})).
		Where("user_id = ?", uuid.New()).
		Find(&creds).Statement
	credSQL := credStmt.SQL.String()

	for _, omittedCol := range []string{`"attestation"`, `"extensions"`, `"flags"`} {
		// Note: "attestation_type" and "attestation_format" must still be selected, only raw "attestation" is omitted.
		if strings.Contains(credSQL, `"credentials".`+omittedCol) {
			t.Errorf("expected column %s to be omitted from credential preload SQL, got: %s", omittedCol, credSQL)
		}
	}
	for _, keptCol := range []string{`"credential_id"`, `"public_key"`, `"attestation_type"`, `"attestation_format"`, `"sign_count"`} {
		if !strings.Contains(credSQL, `"credentials".`+keptCol) {
			t.Errorf("expected column %s to remain in credential preload SQL, got: %s", keptCol, credSQL)
		}
	}
}

func TestAttendanceAndRegistrationQueries_EliminateCrossColumnORJoins(t *testing.T) {
	db := newDryRunPostgresDB(t)
	scheduleID := uuid.New()

	// 1. ListEnrolledStudentsForSchedule SQL
	var enrolledRows []enrolledStudentRow
	rosterStmt := buildEnrolledStudentsQuery(db.Session(&gorm.Session{DryRun: true}), scheduleID).Scan(&enrolledRows).Statement
	rosterSQL := rosterStmt.SQL.String()

	if strings.Contains(rosterSQL, "users.id = students.user_id OR users.username = students.student_id") {
		t.Fatalf("roster SQL still contains cross-column OR join: %s", rosterSQL)
	}
	for _, expectedFragment := range []string{
		`FROM "student_schedules"`,
		"LEFT JOIN users AS u_id ON u_id.id = students.user_id",
		"LEFT JOIN users AS u_name ON u_name.username = students.student_id",
		"LEFT JOIN LATERAL",
		"LIMIT 1",
		"ORDER BY students.name ASC",
	} {
		if !strings.Contains(rosterSQL, expectedFragment) {
			t.Errorf("expected fragment %q in roster SQL, got: %s", expectedFragment, rosterSQL)
		}
	}

	// 2. FindRegisteredAccountForStudent SQL (both linked and unlinked student)
	linkedUserID := uuid.New()
	var account struct {
		UserID uuid.UUID `gorm:"column:user_id"`
	}
	linkedStmt := buildRegisteredAccountQuery(db.Session(&gorm.Session{DryRun: true}), &models.Student{
		ID:        uuid.New(),
		StudentID: "STU-FCS-001",
		UserID:    &linkedUserID,
	}).Scan(&account).Statement
	linkedSQL := linkedStmt.SQL.String()
	if !strings.Contains(linkedSQL, "UNION ALL") || !strings.Contains(linkedSQL, "EXISTS") {
		t.Errorf("expected UNION ALL + EXISTS in linked student account SQL, got: %s", linkedSQL)
	}

	unlinkedStmt := buildRegisteredAccountQuery(db.Session(&gorm.Session{DryRun: true}), &models.Student{
		ID:        uuid.New(),
		StudentID: "STU-FCS-002",
		UserID:    nil,
	}).Scan(&account).Statement
	unlinkedSQL := unlinkedStmt.SQL.String()
	if strings.Contains(unlinkedSQL, "UNION ALL") || !strings.Contains(unlinkedSQL, "EXISTS") {
		t.Errorf("expected direct username + EXISTS in unlinked student account SQL, got: %s", unlinkedSQL)
	}

	// 3. ListStudentsWithRegistration SQL
	var regRows []registrationJoinRow
	regDirStmt := buildStudentsWithRegistrationQuery(db.Session(&gorm.Session{DryRun: true})).Scan(&regRows).Statement
	regDirSQL := regDirStmt.SQL.String()
	if strings.Contains(regDirSQL, "users.id = students.user_id OR users.username = students.student_id") {
		t.Fatalf("registration directory SQL still contains cross-column OR join: %s", regDirSQL)
	}
	for _, expectedFragment := range []string{
		"SELECT DISTINCT ON (user_id) user_id, id",
		"LEFT JOIN users AS u_id ON u_id.id = students.user_id",
		"LEFT JOIN users AS u_name ON u_name.username = students.student_id",
		"ORDER BY students.student_id ASC",
	} {
		if !strings.Contains(regDirSQL, expectedFragment) {
			t.Errorf("expected fragment %q in registration directory SQL, got: %s", expectedFragment, regDirSQL)
		}
	}
}
