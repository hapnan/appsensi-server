package models

import (
	"strings"
	"sync"
	"testing"

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
