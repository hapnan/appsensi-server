package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"appsensi-server/config"
	"appsensi-server/controllers"
	"appsensi-server/database"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	// Initialize configurations
	database.InitDB()
	config.InitWebAuthn()
	if config.IsConformanceEnabled() {
		config.InitConformanceWebAuthn()
	}

	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowOrigins:        []string{os.Getenv("ALLOWED_ORIGINS")},
		AllowMethods:        []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:        []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		ExposeHeaders:       []string{"Content-Length"},
		AllowCredentials:    true,
		MaxAge:              12 * time.Hour,
		AllowPrivateNetwork: os.Getenv("ALLOW_PRIVATE_NETWORK") == "true",
	}))
	// WebAuthn Routes
	r.POST("/register/begin", controllers.BeginRegistration)
	r.POST("/register/finish", controllers.FinishRegistration)

	r.POST("/login/begin", controllers.BeginLogin)
	r.POST("/login/finish", controllers.FinishLogin)

	// Attendance Routes
	attendanceGroup := r.Group("/attendance")
	{
		attendanceGroup.GET("/demo-catalog", controllers.GetDemoCatalog)
		attendanceGroup.GET("/roster", controllers.GetRoster)
		attendanceGroup.POST("/check-in", controllers.CheckIn)
	}

	// Passkey Registration Routes
	registrationGroup := r.Group("/registration")
	{
		registrationGroup.GET("/list", controllers.ListRegistrationStudents)
		registrationGroup.POST("/link-student", controllers.LinkStudentUser)
		registrationGroup.GET("/employees", controllers.ListRegistrationEmployees)
		registrationGroup.POST("/link-employee", controllers.LinkEmployeeUser)
		registrationGroup.GET("/lecturers", controllers.ListRegistrationLecturers)
		registrationGroup.POST("/link-lecturer", controllers.LinkLecturerUser)
		registrationGroup.POST("/cleanup-incomplete", controllers.CleanupIncompleteUser)
	}

	// Dashboard Routes
	dashboardGroup := r.Group("/dashboard")
	{
		dashboardGroup.GET("/me", controllers.GetDashboardMe)

		financeGroup := dashboardGroup.Group("/finance")
		{
			financeGroup.GET("/summary", controllers.GetFinanceDashboardSummary)
			financeGroup.GET("/students", controllers.ListFinanceDashboardStudents)
		}

		academicGroup := dashboardGroup.Group("/academic")
		{
			academicGroup.GET("/summary", controllers.GetAcademicDashboardSummary)
			academicGroup.GET("/faculties", controllers.ListAcademicFaculties)
			academicGroup.POST("/faculties", controllers.CreateAcademicFaculty)
			academicGroup.PUT("/faculties/:id", controllers.UpdateAcademicFaculty)
			academicGroup.GET("/courses", controllers.ListAcademicCourses)
			academicGroup.POST("/courses", controllers.CreateAcademicCourse)
			academicGroup.PUT("/courses/:id", controllers.UpdateAcademicCourse)
			academicGroup.DELETE("/courses/:id", controllers.DeleteAcademicCourse)
			academicGroup.GET("/lecturers", controllers.ListAcademicLecturers)
			academicGroup.POST("/lecturers", controllers.CreateAcademicLecturer)
			academicGroup.PUT("/lecturers/:id", controllers.UpdateAcademicLecturer)
			academicGroup.DELETE("/lecturers/:id", controllers.DeleteAcademicLecturer)
			academicGroup.GET("/employees", controllers.ListAcademicEmployees)
			academicGroup.POST("/employees", controllers.CreateAcademicEmployee)
			academicGroup.PUT("/employees/:id", controllers.UpdateAcademicEmployee)
			academicGroup.DELETE("/employees/:id", controllers.DeleteAcademicEmployee)
			academicGroup.GET("/schedules", controllers.ListAcademicSchedules)
			academicGroup.POST("/schedules", controllers.CreateAcademicSchedule)
			academicGroup.PUT("/schedules/:id", controllers.UpdateAcademicSchedule)
			academicGroup.DELETE("/schedules/:id", controllers.DeleteAcademicSchedule)
		}

		studentAffairsGroup := dashboardGroup.Group("/student-affairs")
		{
			studentAffairsGroup.GET("/summary", controllers.GetStudentAffairsDashboardSummary)
			studentAffairsGroup.GET("/students", controllers.ListStudentAffairsStudents)
			studentAffairsGroup.POST("/students", controllers.CreateStudentAffairsStudent)
			studentAffairsGroup.PUT("/students/:id", controllers.UpdateStudentAffairsStudent)
			studentAffairsGroup.DELETE("/students/:id", controllers.DeleteStudentAffairsStudent)
			studentAffairsGroup.GET("/students/:id/schedules", controllers.ListStudentAffairsStudentSchedules)
			studentAffairsGroup.POST("/students/:id/schedules", controllers.EnrollStudentAffairsStudentSchedule)
			studentAffairsGroup.DELETE("/students/:id/schedules/:scheduleId", controllers.DropStudentAffairsStudentSchedule)
			studentAffairsGroup.GET("/attendance", controllers.ListStudentAffairsAttendance)
		}

		lecturerGroup := dashboardGroup.Group("/lecturer")
		{
			lecturerGroup.GET("/summary", controllers.GetLecturerDashboardSummary)
			lecturerGroup.GET("/schedules", controllers.ListLecturerSchedules)
			lecturerGroup.GET("/schedules/:scheduleId/students", controllers.ListLecturerScheduleStudents)
			lecturerGroup.GET("/schedules/:scheduleId/attendance", controllers.GetLecturerScheduleAttendance)
			lecturerGroup.POST("/schedules/:scheduleId/attendance", controllers.RecordLecturerScheduleAttendance)
			lecturerGroup.DELETE("/schedules/:scheduleId/attendance/:studentId", controllers.DeleteLecturerScheduleAttendance)
		}
	}

	if config.IsConformanceEnabled() {
		r.POST("/attestation/options", controllers.ConformanceAttestationOptions)
		r.POST("/attestation/result", controllers.ConformanceAttestationResult)
		r.POST("/assertion/options", controllers.ConformanceAssertionOptions)
		r.POST("/assertion/result", controllers.ConformanceAssertionResult)
	}

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, controllers.HealthResponse{
			Message: "pong",
		})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting server on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to run server: %v", err)
	}
}
