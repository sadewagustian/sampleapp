package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"

	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/contrib/propagators/aws/xray"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

const (
	serviceName    = "hello-go-app"
	serviceVersion = "v1.0.1"
)

var (
	tracer    = otel.Tracer("io.opentelemetry.traces.hello")
	propgator = propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
		xray.Propagator{})
)

func main() {

	// Initialize the tracer provider
	initTracer()

	// Start the microservice
	router := mux.NewRouter()
	router.Use(otelmux.Middleware(serviceName))
	router.HandleFunc("/hello", hello)
	router.HandleFunc("/s3", awss3)
	http.ListenAndServe(":8080", router)

}

func hello(writer http.ResponseWriter, request *http.Request) {

	ctx := request.Context()

	ctx, span := tracer.Start(ctx, "parentSpan")

	response := buildResponse(writer)
	//	buildResp.RecordError(errors.New("error"))
	if response.isValid() {
		log.Print("")
	}
	span.End()

	// Serialize the context into carrier
	carrier := propagation.HeaderCarrier{}
	propgator.Inject(ctx, carrier)
	// Print Header with JSON format
	jsonStr, err := json.Marshal(carrier)
	if err != nil {
		fmt.Printf("Error: %s", err.Error())
	} else {
		fmt.Println(string(jsonStr))
	}

	// Extract the context and create a custom span
	parentCtx := propgator.Extract(ctx, carrier)
	_, childSpan := tracer.Start(parentCtx, "childSpan")

	childSpan.AddEvent("test-dummy-event")
	childSpan.RecordError(errors.New("errors"))
	childSpan.End()

}

func buildResponse(writer http.ResponseWriter) response {

	writer.WriteHeader(http.StatusOK)
	writer.Header().Add("Content-Type",
		"application/json")

	response := response{"Hello World"}
	bytes, _ := json.Marshal(response)
	writer.Write(bytes)
	return response

}
func awss3(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	fmt.Println("", request.Header)
	ctx, span := tracer.Start(ctx, "parentSpanS3")
	response := s3response(writer)
	//	buildResp.RecordError(errors.New("error"))
	if response.isValid() {
		log.Print("")
	}
	span.End()

	// init aws config

	//creds := credentials.NewStaticCredentialsProvider("access_key", "secret_key", "")
	//region := os.Getenv("AWS_REGION")
	//cfg, err := awsConfig.LoadDefaultConfig((ctx),
	//	config.WithCredentialsProvider(creds), config.WithRegion(region))
	cfg, err := awsConfig.LoadDefaultConfig(ctx)
	if err != nil {
		panic("configuration error, " + err.Error())
	}

	// instrument all aws clients
	otelaws.AppendMiddlewares(&cfg.APIOptions)

	// S3
	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.Region = "ap-southeast-1"
	})
	input := &s3.ListBucketsInput{}
	result, err := s3Client.ListBuckets(ctx, input)
	if err != nil {
		fmt.Printf("Got an error retrieving buckets, %v", err)
		return
	}

	fmt.Println("Buckets:")
	for _, bucket := range result.Buckets {
		fmt.Println(*bucket.Name + ": " + bucket.CreationDate.Format("2006-01-02 15:04:05 Monday"))
	}

}

func s3response(writer http.ResponseWriter) response {

	writer.WriteHeader(http.StatusOK)
	writer.Header().Add("Content-Type",
		"application/json")

	response := response{"Buckets: "}
	bytes, _ := json.Marshal(response)
	writer.Write(bytes)
	return response

}

type response struct {
	Message string
}

func (r response) isValid() bool {
	return true
}

func initTracer() {
	ctx := context.Background()
	endpoint := os.Getenv("EXPORTER_ENDPOINT")
	// Resource to name traces/metrics
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String(serviceVersion),
			semconv.TelemetrySDKVersionKey.String("v1.4.1"),
			semconv.TelemetrySDKLanguageGo,
		),
	)
	if err != nil {
		log.Fatalf("%s: %v", "failed to create resource", err)
	}
	// Create and start new OTLP trace exporter
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithInsecure(), otlptracegrpc.WithEndpoint(endpoint))
	if err != nil {
		log.Fatalf("failed to create new OTLP trace exporter: %v", err)
	}

	idg := xray.NewIDGenerator()
	//exp, _ := stdouttrace.New(stdouttrace.WithPrettyPrint())
	//bsp := sdktrace.NewSimpleSpanProcessor(exp)
	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(trace.AlwaysSample()),
		//sdktrace.WithBatcher(traceExporter),
		sdktrace.WithIDGenerator(idg),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)
	otel.SetTracerProvider(tp)
	//	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
	//		propagation.TraceContext{},
	//		propagation.Baggage{},
	//		xray.Propagator{},
	//	))

	//	propgator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}, xray.Propagator{})
	otel.SetTextMapPropagator(propgator)

	//	ctx, span := tp.Tracer("test").Start(ctx, "parent-span-name")
	//	defer span.End()
	//	// Serialize the context into carrier
	//	carrier := propagation.MapCarrier{}
	//	propgator.Inject(ctx, carrier)
	// This carrier is sent accros the process
	//	fmt.Println(carrier)

}
