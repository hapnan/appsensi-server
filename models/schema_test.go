package models

import (
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
	"gorm.io/gorm/schema"
)

func TestModelColumnNamesMatchJSONTags(t *testing.T) {
	modelsToCheck := []any{
		&User{},
		&WebAuthnUser{},
		&Credential{},
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
