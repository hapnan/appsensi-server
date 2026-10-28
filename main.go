package main

import (
	"log"
	"os"

	"appsensi-server/config"
	"appsensi-server/controllers"
	"appsensi-server/database"

	"github.com/gin-gonic/gin"
)

func main() {
	// Initialize configurations
	database.InitDB()
	config.InitWebAuthn()

	r := gin.Default()
	r.Use(config.CORSMiddleware())
	// WebAuthn Routes
	r.POST("/register/begin", controllers.BeginRegistration)
	r.POST("/register/finish", controllers.FinishRegistration)

	r.POST("/login/begin", controllers.BeginLogin)
	r.POST("/login/finish", controllers.FinishLogin)

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
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
