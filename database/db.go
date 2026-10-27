package database

import (
	"log"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		// Fallback for local development
		dsn = "host=localhost user=hapnan password=Snaiper123 dbname=appsensi port=5432 sslmode=disable TimeZone=Asia/Jakarta"
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Auto Migrate the schemas
	// fmt.Println("Migrating database schemas...")
	// err = db.AutoMigrate(
	// 	&models.User{},
	// 	&models.WebAuthnUser{},
	// 	&models.Credential{},
	// 	&models.Building{},
	// 	&models.Room{},
	// 	&models.Faculty{},
	// 	&models.Course{},
	// 	&models.ClassSchedule{},
	// 	&models.Student{},
	// 	&models.StudentSchedule{},
	// 	&models.Attendance{},
	// )
	// if err != nil {
	// 	log.Fatalf("Failed to migrate database schemas: %v", err)
	// }
	// fmt.Println("Database schemas migrated successfully.")

	DB = db
}
