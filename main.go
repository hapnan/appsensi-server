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

	// Student Passkey Registration Routes
	registrationGroup := r.Group("/registration")
	{
		registrationGroup.GET("/list", controllers.ListRegistrationStudents)
		registrationGroup.POST("/link-student", controllers.LinkStudentUser)
		registrationGroup.POST("/cleanup-incomplete", controllers.CleanupIncompleteUser)
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
