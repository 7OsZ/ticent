package main

import (
	"log"
	"net/http"

	"ticent/config"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file: ", err)
	}

	config.ConnectDB()

	server := gin.Default()

	server.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	if err := server.Run(":8080"); err != nil {
		log.Fatal("Server berhenti: ", err)
	}
}
