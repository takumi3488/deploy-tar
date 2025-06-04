package main

import (
	"context"
	"deploytar/handler"
	"log"
	"net"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"

	pb "deploytar/proto/deploytar/proto/fileservice/v1"
)

func main() {
	e := echo.New()
	ctx := context.Background()

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
				return c.Request().URL.Path == "/healthz"
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

	e.POST("/", handler.UploadHandler)
	e.PUT("/", handler.UploadHandler)

	e.GET("/list", handler.ListDirectoryHandler)

	e.GET("/healthz", handler.Healthz)

	go startGRPCServer()

	e.Logger.Fatal(e.Start(":8080"))
}

func startGRPCServer() {
	lis, err := net.Listen("tcp", ":9090")
	if err != nil {
		log.Fatalf("Failed to listen on port 9090: %v", err)
	}

	grpcServer := grpc.NewServer()
	fileService := handler.NewGRPCListDirectoryServer()
	pb.RegisterFileServiceServer(grpcServer, fileService)

	log.Println("gRPC server listening on :9090")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve gRPC server: %v", err)
	}
}
