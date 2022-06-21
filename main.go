package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/XSAM/otelsql"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/contrib/propagators/aws/xray"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/propagation"
	controller "go.opentelemetry.io/otel/sdk/metric/controller/basic"
	processor "go.opentelemetry.io/otel/sdk/metric/processor/basic"
	"go.opentelemetry.io/otel/sdk/metric/selector/simple"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

const (
	serviceName         = "hello-go-app"
	serviceVersion      = "v1.0.0"
	instrumentationName = "otelsql-example"
)

var (
	tracer    = otel.Tracer("io.opentelemetry.traces.hello")
	propgator = propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
		xray.Propagator{})
	mysqlDSN = "user:pwsd@tcp(test-db-1.c3eveddczuf9.ap-southeast-1.rds.amazonaws.com)/mysql?parseTime=true"
	endpoint = os.Getenv("EXPORTER_ENDPOINT")
)

func main() {

	// Initialize the tracer provider
	initTracer()
	initMeter()

	// Connect to database
	db, err := otelsql.Open("mysql", mysqlDSN, otelsql.WithAttributes(
		semconv.DBSystemMySQL,
	))
	if err != nil {
		panic(err)
	}
	defer db.Close()

	err = otelsql.RegisterDBStatsMetrics(db, otelsql.WithAttributes(
		semconv.DBSystemMySQL,
	))
	if err != nil {
		panic(err)
	}

	err = query(db)
	if err != nil {
		panic(err)
	}

	// Start the microservice
	router := mux.NewRouter()
	router.Use(otelmux.Middleware(serviceName))
	router.HandleFunc("/hello", hello)
	router.HandleFunc("/s3", awss3)
	http.ListenAndServe(":8080", router)
	fmt.Println("Example finished updating, please visit :2222")
	select {}
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
	carrier := propagation.MapCarrier{}
	propgator.Inject(ctx, carrier)
	// This carrier is sent accros the process
	fmt.Println("", request.Header)
	//fmt.Printf("%v \n", carrier)

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
	Message string `json:"Message"`
}

func (r response) isValid() bool {
	return true
}

func initTracer() {
	ctx := context.Background()
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
func initMeter() {
	ctx := context.Background()
	metricOpts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithTimeout(5 * time.Second),
	}

	metricExporter, err := otlpmetricgrpc.New(ctx, metricOpts...)
	if err != nil {
		log.Fatalf("%s: %v", "failed to create exporter", err)
	}

	pusher := controller.New(
		processor.NewFactory(
			simple.NewWithHistogramDistribution(),
			metricExporter,
		),
		controller.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
		)),
		controller.WithExporter(metricExporter),
		controller.WithCollectPeriod(5*time.Second),
	)

	err = pusher.Start(ctx)
	if err != nil {
		log.Fatalf("%s: %v", "failed to start the pusher", err)
	}

	global.SetMeterProvider(pusher)
}

func query(db *sql.DB) error {
	// Create a span
	tracer := otel.GetTracerProvider()
	ctx, span := tracer.Tracer(instrumentationName).Start(context.Background(), "example")
	defer span.End()

	// Make a query
	rows, err := db.QueryContext(ctx, `SELECT CURRENT_TIMESTAMP`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var currentTime time.Time
	for rows.Next() {
		err = rows.Scan(&currentTime)
		if err != nil {
			return err
		}
	}
	fmt.Printf("sql query: %v \n", currentTime)
	return nil
}
