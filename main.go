package main

import (
	"deploytar/handler"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func main() {
	e := echo.New()

	// Middleware configuration
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	// Set up upload route
	e.POST("/", handler.UploadHandler)
	// Start the server
	e.Logger.Fatal(e.Start(":8080"))
}
