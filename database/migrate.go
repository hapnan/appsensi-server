package database

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"

	"appsensi-server/models"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const pgUndefinedTableCode = "42P01"

// supplementaryIndexStatements defines expression and covering indexes beyond GORM struct tags,
// applied idempotently whenever DB_AUTO_MIGRATE detects a schema or index change.
var supplementaryIndexStatements = []string{
	`CREATE INDEX IF NOT EXISTS class_schedules_room_norm_day_idx ON class_schedules (room_id, lower(trim(day_of_week)))`,
	`CREATE INDEX IF NOT EXISTS student_schedules_class_status_student_idx ON student_schedules (class_schedule_id, status, student_id)`,
	`CREATE INDEX IF NOT EXISTS attendance_schedule_date_student_idx ON attendance (class_schedule_id, session_date, student_id)`,
}

var analyzeTablesStatement = `ANALYZE users, webauthn_users, credentials, rooms, class_schedules, students, student_schedules, attendance`

func managedModels() []any {
	return []any{
		&models.User{},
		&models.WebAuthnUser{},
		&models.Credential{},
		&models.Building{},
		&models.Room{},
		&models.Faculty{},
		&models.Course{},
		&models.ClassSchedule{},
		&models.Student{},
		&models.StudentSchedule{},
		&models.Attendance{},
	}
}

// ComputeSchemaFingerprint computes a deterministic SHA-256 hex digest across all GORM models
// and supplementary PostgreSQL index statements so AutoMigrate only runs when schemas change.
func ComputeSchemaFingerprint() (string, error) {
	return computeFingerprintForModels(managedModels(), supplementaryIndexStatements)
}

func computeFingerprintForModels(modelList []any, extraStatements []string) (string, error) {
	cache := &sync.Map{}
	namer := schema.NamingStrategy{IdentifierMaxLength: 63}

	tableEntries := make([]string, 0, len(modelList))
	for _, m := range modelList {
		s, err := schema.Parse(m, cache, namer)
		if err != nil {
			return "", fmt.Errorf("parse schema for %T: %w", m, err)
		}

		var b strings.Builder
		fmt.Fprintf(&b, "table:%s\n", s.Table)

		dbNames := slices.Clone(s.DBNames)
		slices.Sort(dbNames)
		for _, colName := range dbNames {
			f := s.FieldsByDBName[colName]
			if f == nil || f.IgnoreMigration {
				continue
			}
			fmt.Fprintf(
				&b,
				"col:%s|type:%s|gormType:%s|size:%d|prec:%d|scale:%d|nn:%t|uniq:%t|pk:%t|auto:%t|hasDef:%t|def:%s\n",
				f.DBName,
				f.DataType,
				f.GORMDataType,
				f.Size,
				f.Precision,
				f.Scale,
				f.NotNull,
				f.Unique,
				f.PrimaryKey,
				f.AutoIncrement,
				f.HasDefaultValue,
				f.DefaultValue,
			)
		}

		indexes := s.ParseIndexes()
		idxEntries := make([]string, 0, len(indexes))
		for _, idx := range indexes {
			cols := make([]string, 0, len(idx.Fields))
			for _, f := range idx.Fields {
				cols = append(cols, fmt.Sprintf("%s:%s:%d", f.DBName, f.Sort, f.Length))
			}
			idxEntries = append(
				idxEntries,
				fmt.Sprintf("idx:%s|class:%s|type:%s|opt:%s|where:%s|cols:%s",
					idx.Name, idx.Class, idx.Type, idx.Option, idx.Where, strings.Join(cols, ",")),
			)
		}
		slices.Sort(idxEntries)
		for _, entry := range idxEntries {
			b.WriteString(entry)
			b.WriteByte('\n')
		}

		checks := s.ParseCheckConstraints()
		chkNames := make([]string, 0, len(checks))
		for name := range checks {
			chkNames = append(chkNames, name)
		}
		slices.Sort(chkNames)
		for _, name := range chkNames {
			chk := checks[name]
			fmt.Fprintf(&b, "chk:%s|%s\n", chk.Name, chk.Constraint)
		}

		fkEntries := make([]string, 0, len(s.Relationships.Relations))
		for _, rel := range s.Relationships.Relations {
			if rel.Field.IgnoreMigration {
				continue
			}
			if c := rel.ParseConstraint(); c != nil && c.Schema == s {
				fks := make([]string, 0, len(c.ForeignKeys))
				for _, fk := range c.ForeignKeys {
					fks = append(fks, fk.DBName)
				}
				refs := make([]string, 0, len(c.References))
				for _, ref := range c.References {
					refs = append(refs, ref.DBName)
				}
				fkEntries = append(
					fkEntries,
					fmt.Sprintf("fk:%s|refTable:%s|fks:%s|refs:%s|del:%s|upd:%s",
						c.Name, c.ReferenceSchema.Table, strings.Join(fks, ","), strings.Join(refs, ","), c.OnDelete, c.OnUpdate),
				)
			}
		}
		slices.Sort(fkEntries)
		for _, entry := range fkEntries {
			b.WriteString(entry)
			b.WriteByte('\n')
		}

		tableEntries = append(tableEntries, b.String())
	}

	slices.Sort(tableEntries)

	h := sha256.New()
	for _, entry := range tableEntries {
		_, _ = h.Write([]byte(entry))
	}
	for _, stmt := range extraStatements {
		_, _ = h.Write([]byte("ddl:"))
		_, _ = h.Write([]byte(strings.TrimSpace(stmt)))
		_, _ = h.Write([]byte("\n"))
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func isUndefinedTableError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUndefinedTableCode
}

func readStoredSchemaFingerprint(db *gorm.DB) (string, error) {
	type metaRow struct {
		Fingerprint string `gorm:"column:fingerprint"`
	}
	var row metaRow
	res := db.Raw(`SELECT fingerprint FROM app_schema_meta WHERE id = 1`).Scan(&row)
	if res.Error != nil {
		if isUndefinedTableError(res.Error) {
			return "", nil
		}
		return "", res.Error
	}
	return row.Fingerprint, nil
}

func migrateIfSchemaChanged(db *gorm.DB) error {
	fingerprint, err := ComputeSchemaFingerprint()
	if err != nil {
		return err
	}

	stored, err := readStoredSchemaFingerprint(db)
	if err != nil {
		return fmt.Errorf("read schema fingerprint: %w", err)
	}

	shortHash := fingerprint[:12]
	if stored == fingerprint {
		log.Printf("Database schema unchanged (fingerprint=%s), skipping AutoMigrate.", shortHash)
		return nil
	}

	log.Printf("Database schema change detected (fingerprint=%s), running AutoMigrate and index updates...", shortHash)

	if err := db.Exec(`CREATE TABLE IF NOT EXISTS app_schema_meta (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		fingerprint TEXT NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`).Error; err != nil {
		return fmt.Errorf("create app_schema_meta table: %w", err)
	}

	if err := db.AutoMigrate(managedModels()...); err != nil {
		return fmt.Errorf("gorm auto-migrate: %w", err)
	}

	for _, stmt := range supplementaryIndexStatements {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("create supplementary index (%s): %w", stmt, err)
		}
	}

	if err := db.Exec(analyzeTablesStatement).Error; err != nil {
		log.Printf("Warning: ANALYZE after migration returned error: %v", err)
	}

	if err := db.Exec(`INSERT INTO app_schema_meta (id, fingerprint, updated_at)
		VALUES (1, ?, now())
		ON CONFLICT (id) DO UPDATE
		SET fingerprint = EXCLUDED.fingerprint, updated_at = now()`, fingerprint).Error; err != nil {
		return fmt.Errorf("record schema fingerprint: %w", err)
	}

	log.Printf("Database schema migration completed (fingerprint=%s).", shortHash)
	return nil
}
