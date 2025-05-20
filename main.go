package main

import (
	"context"
	"deploytar/handler"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
)

func main() {
	e := echo.New()
	ctx := context.Background()

	// Set up OpenTelemetry
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		exporter, err := otlptracegrpc.New(ctx)
		if err != nil {
			panic(err)
		}
		tracerProvider := trace.NewTracerProvider(
			trace.WithBatcher(exporter),
		)
		otel.SetTracerProvider(tracerProvider)
		defer func() {
			if err := tracerProvider.Shutdown(ctx); err != nil {
				panic(err)
			}
		}()
		otel.SetTextMapPropagator(propagation.TraceContext{})
		echoMiddlewareOptions := []otelecho.Option{
			otelecho.WithTracerProvider(tracerProvider),
			otelecho.WithPropagators(propagation.TraceContext{}),
			otelecho.WithSkipper(func(c echo.Context) bool {
				return c.Request().URL.Path == "/health"
			}),
		}
		serviceName := os.Getenv("OTEL_SERVICE_NAME")
		if serviceName == "" {
			serviceName = "deploy-tar"
		}
		e.Use(otelecho.Middleware(serviceName, echoMiddlewareOptions...))
	}

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	// Set up upload route
	e.POST("/", handler.UploadHandler)
	e.PUT("/", handler.UploadHandler)

	// Health check endpoint
	e.GET("/healthz", handler.Healthz)
	// Start the server
	e.Logger.Fatal(e.Start(":8080"))
}
