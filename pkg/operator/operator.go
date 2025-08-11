// Package operator contains the main entrypoint for the operator.
package operator

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	cmclient "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	"github.com/go-logr/logr"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/hostedcontrolplane"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/etc"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

func Start(ctx context.Context, version string, operatorConfig etc.Config) (err error) {
	ctx = configureLogging(ctx, operatorConfig.LogFormat)

	scheme, err := NewScheme()
	if err != nil {
		return fmt.Errorf("failed to create scheme: %w", err)
	}
	options := ctrl.Options{
		Scheme:                        scheme,
		LeaderElectionReleaseOnCancel: true,
		Metrics: metricsserver.Options{
			BindAddress:    "0.0.0.0:8080",
			FilterProvider: filters.WithAuthenticationAndAuthorization,
		},
		HealthProbeBindAddress: ":8081",
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    9443,
			CertDir: operatorConfig.WebhookCertDir,
		}),
	}

	config, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes client config: %w", err)
	}
	tracingWraper := func(rt http.RoundTripper) http.RoundTripper {
		return otelhttp.NewTransport(rt)
	}
	config.Wrap(tracingWraper)

	options.LeaderElection = operatorConfig.LeaderElection
	options.LeaderElectionID = api.GroupName

	mgr, err := ctrl.NewManager(config, options)
	if err != nil {
		return fmt.Errorf("failed to construct manager: %w", err)
	}

	if err := mgr.AddReadyzCheck("webhook", mgr.GetWebhookServer().StartedChecker()); err != nil {
		return fmt.Errorf("failed to create readieness check: %w", err)
	}
	if err := mgr.AddHealthzCheck("webhook", mgr.GetWebhookServer().StartedChecker()); err != nil {
		return fmt.Errorf("failed to create health check: %w", err)
	}

	if err := setupControllers(ctx, mgr,
		operatorConfig.MaxConcurrentReconciles,
		tracingWraper,
	); err != nil {
		return err
	}

	tp, err := setupTracerProvider(ctx, operatorConfig.ServiceName, version)
	if err != nil {
		return err
	}
	defer func() {
		if shutdownErr := tp.Shutdown(ctx); shutdownErr != nil {
			err = kerrors.NewAggregate([]error{err, fmt.Errorf("shutting down trace provider: %w", shutdownErr)})
		}
	}()

	logr.FromContextAsSlogLogger(ctx).InfoContext(ctx, "Starting operator", "version", version)
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start manager: %w", err)
	}

	return nil
}

func configureLogging(ctx context.Context, format etc.LogFormat) context.Context {
	var stdoutHandler slog.Handler
	var stderrHandler slog.Handler

	switch format {
	case etc.JSON:
		stdoutHandler = slog.NewJSONHandler(os.Stdout, nil)
		stderrHandler = slog.NewJSONHandler(os.Stderr, nil)
	case etc.TEXT:
		stdoutHandler = slog.NewTextHandler(os.Stdout, nil)
		stderrHandler = slog.NewTextHandler(os.Stderr, nil)
	}

	handler := logging.NewTracingLoggingHandler(
		logging.NewDeduplicatingLoggingHandler(
			logging.NewSplittingLoggingHandler(
				stdoutHandler,
				stderrHandler,
			),
		),
	)

	logrLogger := logr.FromSlogHandler(handler)
	log.SetLogger(logrLogger)
	return logr.NewContextWithSlogLogger(logr.NewContext(ctx, logrLogger), slog.New(handler))
}

func setupControllers(
	ctx context.Context,
	mgr manager.Manager,
	maxConcurrentReconciles int,
	tracingWrapper func(rt http.RoundTripper) http.RoundTripper,
) error {
	hostedControlPlaneControllerName := "hcp-controller"
	predicateLogger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get logger from context: %w", err)
	}
	predicateLogger = predicateLogger.WithValues("controller", hostedControlPlaneControllerName)

	kubernetesClient, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	certManagerClient, err := cmclient.NewForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("failed to create cert-manager client: %w", err)
	}

	if err := (&hostedcontrolplane.HostedControlPlaneReconciler{
		Client:            client.WithFieldOwner(mgr.GetClient(), hostedControlPlaneControllerName),
		KubernetesClient:  kubernetesClient,
		CertManagerClient: certManagerClient,
		ManagementCluster: &hostedcontrolplane.Management{
			KubernetesClient: kubernetesClient,
			TracingWrapper:   tracingWrapper,
		},
		Recorder: mgr.GetEventRecorderFor(hostedControlPlaneControllerName),
	}).SetupWithManager(mgr, maxConcurrentReconciles, predicateLogger); err != nil {
		return fmt.Errorf("failed to setup controller: %w", err)
	}
	if err := (&v1alpha1.HostedControlPlane{}).SetupWebhookWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup webhook for HostedControlPlane: %w", err)
	}
	return nil
}

func setupTracerProvider(ctx context.Context, serviceName string, version string) (*trace.TracerProvider, error) {
	var exporter trace.SpanExporter
	if _, isSet := os.LookupEnv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"); isSet {
		otlpclient := otlptracegrpc.NewClient()

		var err error
		exporter, err = otlptrace.New(ctx, otlpclient)
		if err != nil {
			return nil, fmt.Errorf("failed to create otlptrace exporter: %w", err)
		}
	} else {
		exporter = &tracetest.NoopExporter{}
	}
	applicationResource, err := newResource(serviceName, version)
	if err != nil {
		return nil, fmt.Errorf("failed to create otlp applicationResource: %w", err)
	}
	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(applicationResource),
	)
	otel.SetTracerProvider(tp)
	return tp, nil
}

func newResource(serviceName string, version string) (*resource.Resource, error) {
	serviceResource, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to merge resources: %w", err)
	}

	return serviceResource, nil
}
