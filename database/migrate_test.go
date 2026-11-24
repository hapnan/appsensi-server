package database

import (
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm/schema"
)

type sampleModelV1 struct {
	ID   string `gorm:"type:uuid;primaryKey"`
	Code string `gorm:"type:text;not null;index:sample_code_idx"`
}

func (sampleModelV1) TableName() string { return "samples" }

type sampleModelV2AddedIndex struct {
	ID     string `gorm:"type:uuid;primaryKey"`
	Code   string `gorm:"type:text;not null;index:sample_code_idx;index:sample_code_status_idx,priority:1"`
	Status string `gorm:"type:text;not null;index:sample_code_status_idx,priority:2"`
}

func (sampleModelV2AddedIndex) TableName() string { return "samples" }

func TestComputeSchemaFingerprint_DeterministicAndSensitiveToChanges(t *testing.T) {
	fp1, err := ComputeSchemaFingerprint()
	if err != nil {
		t.Fatalf("ComputeSchemaFingerprint() error: %v", err)
	}
	if len(fp1) != 64 {
		t.Fatalf("fingerprint length = %d, want 64 hex chars", len(fp1))
	}

	fp2, err := ComputeSchemaFingerprint()
	if err != nil {
		t.Fatalf("second ComputeSchemaFingerprint() error: %v", err)
	}
	if fp1 != fp2 {
		t.Fatalf("fingerprint not deterministic: %q != %q", fp1, fp2)
	}

	v1Hash, err := computeFingerprintForModels([]any{&sampleModelV1{}}, []string{"CREATE INDEX IF NOT EXISTS idx_a ON samples (code)"})
	if err != nil {
		t.Fatalf("computeFingerprintForModels v1 error: %v", err)
	}
	v2Hash, err := computeFingerprintForModels([]any{&sampleModelV2AddedIndex{}}, []string{"CREATE INDEX IF NOT EXISTS idx_a ON samples (code)"})
	if err != nil {
		t.Fatalf("computeFingerprintForModels v2 error: %v", err)
	}
	if v1Hash == v2Hash {
		t.Fatal("expected schema fingerprint to change when columns/indexes change")
	}

	ddlChangedHash, err := computeFingerprintForModels([]any{&sampleModelV1{}}, []string{"CREATE INDEX IF NOT EXISTS idx_b ON samples (lower(code))"})
	if err != nil {
		t.Fatalf("computeFingerprintForModels ddl change error: %v", err)
	}
	if v1Hash == ddlChangedHash {
		t.Fatal("expected schema fingerprint to change when supplementary index DDL changes")
	}
}

func TestResolvePoolLimitsAndEnvHelpers(t *testing.T) {
	open, idle := resolvePoolLimits(20, 10)
	if open != 20 || idle != 10 {
		t.Fatalf("resolvePoolLimits(20, 10) = (%d, %d), want (20, 10)", open, idle)
	}

	// Idle connections must never exceed max open connections
	open, idle = resolvePoolLimits(5, 12)
	if open != 5 || idle != 5 {
		t.Fatalf("resolvePoolLimits(5, 12) = (%d, %d), want (5, 5)", open, idle)
	}

	open, idle = resolvePoolLimits(0, 0)
	if open != defaultMaxOpenConns || idle != defaultMaxIdleConns {
		t.Fatalf("resolvePoolLimits(0, 0) = (%d, %d), want (%d, %d)", open, idle, defaultMaxOpenConns, defaultMaxIdleConns)
	}

	t.Setenv("DB_AUTO_MIGRATE", "true")
	if !envBool("DB_AUTO_MIGRATE") {
		t.Fatal("expected envBool(DB_AUTO_MIGRATE=true) to return true")
	}
	t.Setenv("DB_AUTO_MIGRATE", "false")
	if envBool("DB_AUTO_MIGRATE") {
		t.Fatal("expected envBool(DB_AUTO_MIGRATE=false) to return false")
	}

	if !isUndefinedTableError(&pgconn.PgError{Code: pgUndefinedTableCode}) {
		t.Fatal("expected 42P01 PgError to be recognized as undefined_table")
	}
	if isUndefinedTableError(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("did not expect 23505 PgError to be recognized as undefined_table")
	}
}

func TestManagedModels_SharedSchemaCachePreservesColumnTypes(t *testing.T) {
	namer := schema.NamingStrategy{IdentifierMaxLength: 63}
	models := managedModels()

	// Parse each model in an isolated cache first to capture its declared column types.
	declaredTypes := make(map[string]map[string]schema.DataType, len(models))
	for _, m := range models {
		isolated, err := schema.Parse(m, &sync.Map{}, namer)
		if err != nil {
			t.Fatalf("isolated schema.Parse(%T): %v", m, err)
		}
		colMap := make(map[string]schema.DataType, len(isolated.Fields))
		for _, f := range isolated.Fields {
			if f.DBName != "" {
				colMap[f.DBName] = f.DataType
			}
		}
		declaredTypes[isolated.Table] = colMap
	}

	// Parse all models together into a single shared cache (exactly as db.AutoMigrate does)
	// and verify no foreign-key relationship guess mutates a column's DataType.
	for _, table := range []string{"employees", "lecturers", "students", "class_schedules", "users"} {
		if _, ok := declaredTypes[table]; !ok {
			t.Fatalf("expected table %q in managedModels()", table)
		}
	}

	sharedCache := &sync.Map{}
	parsed := make([]*schema.Schema, 0, len(models))
	for _, m := range models {
		s, err := schema.Parse(m, sharedCache, namer)
		if err != nil {
			t.Fatalf("shared schema.Parse(%T): %v", m, err)
		}
		parsed = append(parsed, s)
	}

	for _, s := range parsed {
		for _, f := range s.Fields {
			if f.DBName == "" {
				continue
			}
			want := declaredTypes[s.Table][f.DBName]
			if f.DataType != want {
				t.Errorf("shared schema cache mutated %s.%s DataType from %q to %q", s.Table, f.DBName, want, f.DataType)
			}
		}
	}
}
