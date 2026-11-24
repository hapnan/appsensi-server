package models

import (
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"gorm.io/gorm/schema"
)

func TestModelColumnNamesMatchJSONTags(t *testing.T) {
	modelsToCheck := []any{
		&User{},
		&WebAuthnUser{},
		&Credential{},
		&Employee{},
		&Lecturer{},
		&Student{},
		&StudentSchedule{},
		&Attendance{},
		&Building{},
		&Room{},
		&Faculty{},
		&Course{},
		&ClassSchedule{},
	}

	cache := &sync.Map{}
	namer := schema.NamingStrategy{}

	for _, model := range modelsToCheck {
		s, err := schema.Parse(model, cache, namer)
		if err != nil {
			t.Fatalf("failed to parse schema for %T: %v", model, err)
		}

		for _, field := range s.Fields {
			if field.DBName == "" {
				continue
			}

			jsonTag := strings.Split(field.Tag.Get("json"), ",")[0]
			if jsonTag == "" || jsonTag == "-" {
				continue
			}

			if field.DBName != jsonTag {
				t.Errorf("%s.%s mapped to DB column %q, expected %q", s.Name, field.Name, field.DBName, jsonTag)
			}
		}
	}
}

func TestCredentialAAGUIDColumnName(t *testing.T) {
	s, err := schema.Parse(&Credential{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("failed to parse Credential schema: %v", err)
	}

	field := s.LookUpField("AAGUID")
	if field == nil {
		t.Fatal("AAGUID field not found on Credential schema")
	}
	if field.DBName != "aaguid" {
		t.Fatalf("expected Credential.AAGUID DBName to be 'aaguid', got %q", field.DBName)
	}
}

func TestStudentAndAttendanceSchemaAlignment(t *testing.T) {
	cache := &sync.Map{}
	namer := schema.NamingStrategy{}

	ssSchema, err := schema.Parse(&StudentSchedule{}, cache, namer)
	if err != nil {
		t.Fatalf("failed to parse StudentSchedule schema: %v", err)
	}
	ssStudentID := ssSchema.LookUpField("StudentID")
	if ssStudentID == nil {
		t.Fatal("StudentSchedule.StudentID field not found")
	}
	if string(ssStudentID.DataType) != "uuid" {
		t.Fatalf("expected StudentSchedule.StudentID DataType 'uuid', got %q", ssStudentID.DataType)
	}

	attSchema, err := schema.Parse(&Attendance{}, cache, namer)
	if err != nil {
		t.Fatalf("failed to parse Attendance schema: %v", err)
	}
	for _, fieldName := range []string{"StudentID", "RoomID", "ClassScheduleID"} {
		f := attSchema.LookUpField(fieldName)
		if f == nil {
			t.Fatalf("Attendance.%s field not found", fieldName)
		}
		if string(f.DataType) != "uuid" {
			t.Fatalf("expected Attendance.%s DataType 'uuid', got %q", fieldName, f.DataType)
		}
		if !f.NotNull {
			t.Fatalf("expected Attendance.%s to be NOT NULL", fieldName)
		}
	}

	indexes := attSchema.ParseIndexes()
	var uniqueIdx *schema.Index
	for _, idx := range indexes {
		if idx.Name == "attendance_student_room_day_idx" {
			uniqueIdx = idx
			break
		}
	}
	if uniqueIdx == nil {
		t.Fatal("attendance_student_room_day_idx not found on Attendance schema")
	}
	if uniqueIdx.Class != "UNIQUE" {
		t.Fatalf("expected attendance_student_room_day_idx to be UNIQUE, got %q", uniqueIdx.Class)
	}
	wantCols := []string{"student_id", "class_schedule_id", "session_date"}
	if len(uniqueIdx.Fields) != len(wantCols) {
		t.Fatalf("expected %d fields in attendance_student_room_day_idx, got %d", len(wantCols), len(uniqueIdx.Fields))
	}
	for i, want := range wantCols {
		if uniqueIdx.Fields[i].DBName != want {
			t.Errorf("attendance_student_room_day_idx field[%d] = %q, want %q", i, uniqueIdx.Fields[i].DBName, want)
		}
	}

	assertSchemaIndexColumns(t, ssSchema, "student_schedules_class_status_idx", []string{"class_schedule_id", "status"})
	assertSchemaIndexColumns(t, attSchema, "attendance_schedule_date_idx", []string{"class_schedule_id", "session_date"})

	wuSchema, err := schema.Parse(&WebAuthnUser{}, cache, namer)
	if err != nil {
		t.Fatalf("failed to parse WebAuthnUser schema: %v", err)
	}
	assertSchemaIndexColumns(t, wuSchema, "webauthn_users_user_idx", []string{"user_id"})

	credSchema, err := schema.Parse(&Credential{}, cache, namer)
	if err != nil {
		t.Fatalf("failed to parse Credential schema: %v", err)
	}
	assertSchemaIndexColumns(t, credSchema, "credentials_user_credential_idx", []string{"user_id", "credential_id"})

	// Regression check: parsing StudentSchedule and Attendance in the same shared cache
	// must NOT treat Student as HasOne or overwrite Student.StudentID ("text" badge code) to "uuid".
	stuSchema, err := schema.Parse(&Student{}, cache, namer)
	if err != nil {
		t.Fatalf("failed to parse Student schema: %v", err)
	}
	stuBadgeField := stuSchema.LookUpField("StudentID")
	if stuBadgeField == nil {
		t.Fatal("Student.StudentID field not found")
	}
	if string(stuBadgeField.DataType) != "text" {
		t.Fatalf("Student.StudentID DataType = %q after parsing relations, want 'text'", stuBadgeField.DataType)
	}

	for name, rel := range map[string]*schema.Relationship{
		"StudentSchedule.Student": ssSchema.Relationships.Relations["Student"],
		"Attendance.Student":      attSchema.Relationships.Relations["Student"],
	} {
		if rel == nil {
			t.Fatalf("%s relationship not found", name)
		}
		if rel.Type != schema.BelongsTo {
			t.Fatalf("%s relationship Type = %q, want %q", name, rel.Type, schema.BelongsTo)
		}
		if len(rel.References) != 1 || rel.References[0].PrimaryKey.DBName != "id" || rel.References[0].ForeignKey.DBName != "student_id" {
			t.Fatalf("%s invalid reference mapping: %+v", name, rel.References)
		}
	}
}

func assertSchemaIndexColumns(t *testing.T, s *schema.Schema, idxName string, wantCols []string) {
	t.Helper()
	var found *schema.Index
	for _, idx := range s.ParseIndexes() {
		if idx.Name == idxName {
			found = idx
			break
		}
	}
	if found == nil {
		t.Fatalf("index %q not found on %s schema", idxName, s.Name)
	}
	if len(found.Fields) != len(wantCols) {
		t.Fatalf("index %q field count = %d, want %d", idxName, len(found.Fields), len(wantCols))
	}
	for i, want := range wantCols {
		if found.Fields[i].DBName != want {
			t.Errorf("index %q field[%d] = %q, want %q", idxName, i, found.Fields[i].DBName, want)
		}
	}
}

func TestSafeUint32SignCountAndCredentialFlags(t *testing.T) {
	if got := safeUint32SignCount(-42); got != 0 {
		t.Fatalf("safeUint32SignCount(-42) = %d, want 0", got)
	}
	if got := safeUint32SignCount(128); got != 128 {
		t.Fatalf("safeUint32SignCount(128) = %d, want 128", got)
	}
	if got := safeUint32SignCount(int64(math.MaxUint32) + 999); got != math.MaxUint32 {
		t.Fatalf("safeUint32SignCount(overflow) = %d, want %d", got, uint32(math.MaxUint32))
	}

	flags := EncodeCredentialFlags(webauthn.CredentialFlags{
		UserPresent:    true,
		UserVerified:   true,
		BackupEligible: false,
		BackupState:    true,
	})
	if len(flags) != 1 || flags[0] != (1|2|8) {
		t.Fatalf("EncodeCredentialFlags = %v, want [11]", flags)
	}
}

func assertBelongsToRelation(t *testing.T, s *schema.Schema, relName, wantPK, wantFK string) {
	t.Helper()
	rel := s.Relationships.Relations[relName]
	if rel == nil {
		t.Fatalf("%s.%s relationship not found", s.Name, relName)
	}
	if rel.Type != schema.BelongsTo {
		t.Fatalf("%s.%s relationship Type = %q, want %q", s.Name, relName, rel.Type, schema.BelongsTo)
	}
	if len(rel.References) != 1 || rel.References[0].PrimaryKey.DBName != wantPK || rel.References[0].ForeignKey.DBName != wantFK {
		t.Fatalf("%s.%s invalid reference mapping: %+v", s.Name, relName, rel.References)
	}
}

func TestEmployeeLecturerAndUpdatedSchemaAlignment(t *testing.T) {
	cache := &sync.Map{}
	namer := schema.NamingStrategy{}

	userSchema, err := schema.Parse(&User{}, cache, namer)
	if err != nil {
		t.Fatalf("failed to parse User schema: %v", err)
	}
	userTypeField := userSchema.LookUpField("UserType")
	if userTypeField == nil {
		t.Fatal("User.UserType field not found")
	}
	if userTypeField.DBName != "user_type" {
		t.Fatalf("User.UserType DBName = %q, want user_type", userTypeField.DBName)
	}
	if string(userTypeField.DataType) != "text" {
		t.Fatalf("User.UserType DataType = %q, want text", userTypeField.DataType)
	}
	if !userTypeField.NotNull {
		t.Fatal("expected User.UserType to be NOT NULL")
	}
	assertSchemaIndexColumns(t, userSchema, "users_user_type_idx", []string{"user_type"})

	empSchema, err := schema.Parse(&Employee{}, cache, namer)
	if err != nil {
		t.Fatalf("failed to parse Employee schema: %v", err)
	}
	for colName, wantType := range map[string]string{"employee_id": "text", "job_role": "text", "faculty_id": "uuid", "user_id": "uuid"} {
		f := empSchema.LookUpField(colName)
		if f == nil {
			t.Fatalf("Employee.%s field not found", colName)
		}
		if string(f.DataType) != wantType {
			t.Fatalf("Employee.%s DataType = %q, want %q", colName, f.DataType, wantType)
		}
	}
	assertBelongsToRelation(t, empSchema, "Faculty", "id", "faculty_id")
	assertBelongsToRelation(t, empSchema, "User", "id", "user_id")
	assertSchemaIndexColumns(t, empSchema, "employees_job_role_idx", []string{"job_role"})
	assertSchemaIndexColumns(t, empSchema, "employees_faculty_idx", []string{"faculty_id"})
	assertSchemaIndexColumns(t, empSchema, "employees_role_faculty_idx", []string{"job_role", "faculty_id"})
	assertSchemaIndexColumns(t, empSchema, "employees_user_idx", []string{"user_id"})
	lecSchema, err := schema.Parse(&Lecturer{}, cache, namer)
	if err != nil {
		t.Fatalf("failed to parse Lecturer schema: %v", err)
	}
	for colName, wantType := range map[string]string{"lecturer_id": "text", "faculty_id": "uuid", "user_id": "uuid"} {
		f := lecSchema.LookUpField(colName)
		if f == nil {
			t.Fatalf("Lecturer.%s field not found", colName)
		}
		if string(f.DataType) != wantType {
			t.Fatalf("Lecturer.%s DataType = %q, want %q", colName, f.DataType, wantType)
		}
	}
	assertBelongsToRelation(t, lecSchema, "Faculty", "id", "faculty_id")
	assertBelongsToRelation(t, lecSchema, "User", "id", "user_id")
	assertSchemaIndexColumns(t, lecSchema, "lecturers_faculty_idx", []string{"faculty_id"})
	assertSchemaIndexColumns(t, lecSchema, "lecturers_user_idx", []string{"user_id"})

	stuSchema, err := schema.Parse(&Student{}, cache, namer)
	if err != nil {
		t.Fatalf("failed to parse Student schema: %v", err)
	}
	stuFacultyField := stuSchema.LookUpField("faculty_id")
	if stuFacultyField == nil || string(stuFacultyField.DataType) != "uuid" {
		t.Fatalf("Student.faculty_id = %+v, want uuid", stuFacultyField)
	}
	assertSchemaIndexColumns(t, stuSchema, "students_faculty_idx", []string{"faculty_id"})
	assertBelongsToRelation(t, stuSchema, "Faculty", "id", "faculty_id")

	csSchema, err := schema.Parse(&ClassSchedule{}, cache, namer)
	if err != nil {
		t.Fatalf("failed to parse ClassSchedule schema: %v", err)
	}
	csLecturerField := csSchema.LookUpField("lecturer_id")
	if csLecturerField == nil || string(csLecturerField.DataType) != "uuid" {
		t.Fatalf("ClassSchedule.lecturer_id = %+v, want uuid", csLecturerField)
	}
	assertSchemaIndexColumns(t, csSchema, "class_schedules_lecturer_idx", []string{"lecturer_id"})
	assertBelongsToRelation(t, csSchema, "Lecturer", "id", "lecturer_id")

	lecBadgeField := lecSchema.LookUpField("LecturerID")
	if lecBadgeField == nil || string(lecBadgeField.DataType) != "text" {
		t.Fatalf("Lecturer.LecturerID DataType = %+v after parsing ClassSchedule, want text", lecBadgeField)
	}

	for _, ut := range []UserType{UserTypeStudent, UserTypeEmployee, UserTypeLecturer} {
		if !IsValidUserType(ut) {
			t.Errorf("expected IsValidUserType(%q) = true", ut)
		}
	}
	for _, badUT := range []UserType{"", "admin", "staff"} {
		if IsValidUserType(badUT) {
			t.Errorf("expected IsValidUserType(%q) = false", badUT)
		}
	}

	for _, role := range []EmployeeJobRole{JobRoleFinance, JobRoleAcademicAffairs, JobRoleStudentAffairs} {
		if !IsValidEmployeeJobRole(role) {
			t.Errorf("expected IsValidEmployeeJobRole(%q) = true", role)
		}
	}
	for _, badRole := range []EmployeeJobRole{"", "hr", "dean"} {
		if IsValidEmployeeJobRole(badRole) {
			t.Errorf("expected IsValidEmployeeJobRole(%q) = false", badRole)
		}
	}

	facID := uuid.New()
	zeroID := uuid.Nil
	if err := ValidateEmployeeRoleScope(JobRoleFinance, nil); err != nil {
		t.Errorf("ValidateEmployeeRoleScope(finance, nil) error: %v", err)
	}
	if err := ValidateEmployeeRoleScope(JobRoleFinance, &facID); err != nil {
		t.Errorf("ValidateEmployeeRoleScope(finance, &facID) error: %v", err)
	}
	if err := ValidateEmployeeRoleScope(JobRoleAcademicAffairs, nil); err != nil {
		t.Errorf("ValidateEmployeeRoleScope(academic_affairs, nil) error: %v", err)
	}
	if err := ValidateEmployeeRoleScope(JobRoleAcademicAffairs, &facID); err != nil {
		t.Errorf("ValidateEmployeeRoleScope(academic_affairs, &facID) error: %v", err)
	}
	if err := ValidateEmployeeRoleScope(JobRoleStudentAffairs, nil); err == nil {
		t.Error("expected ValidateEmployeeRoleScope(student_affairs, nil) to fail")
	}
	if err := ValidateEmployeeRoleScope(JobRoleStudentAffairs, &zeroID); err == nil {
		t.Error("expected ValidateEmployeeRoleScope(student_affairs, uuid.Nil) to fail")
	}
	if err := ValidateEmployeeRoleScope(JobRoleStudentAffairs, &facID); err != nil {
		t.Errorf("ValidateEmployeeRoleScope(student_affairs, &facID) error: %v", err)
	}
	if err := ValidateEmployeeRoleScope(EmployeeJobRole("invalid"), &facID); err == nil {
		t.Error("expected ValidateEmployeeRoleScope(invalid, &facID) to fail")
	}
}
