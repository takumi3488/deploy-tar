package handler

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// Healthz responds with a 200 OK status and "OK" body for health checks.
func Healthz(c echo.Context) error {
	return c.String(http.StatusOK, "OK")
}
