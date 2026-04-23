package etcd_client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util/names"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	errorsUtil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/errors"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var (
	tracer                  = tracing.GetTracer("EtcdClient")
	errETCDCAFailedToAppend = errors.New("failed to append etcd CA certificate")
)

type EtcdClient interface {
	GetStatuses(ctx context.Context) (map[string]*clientv3.StatusResponse, error)
	OpenSnapshotStream(ctx context.Context) (*clientv3.SnapshotResponse, func() error, error)
	ListAlarms(ctx context.Context) (*clientv3.AlarmResponse, error)
	DisarmAlarm(ctx context.Context, alarm *clientv3.AlarmMember) error
}

type etcdClient struct {
	tracerProvider          trace.TracerProvider
	managementClusterClient *alias.ManagementClusterClient
	caPool                  *x509.CertPool
	clientCertificate       tls.Certificate
	endpoints               map[string]string
	anyEndpoint             string
	serverPort              int32
}

var _ EtcdClient = &etcdClient{}

type EtcdClientFactory = func(
	ctx context.Context,
	managementClusterClient *alias.ManagementClusterClient,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	serverPort int32,
) (EtcdClient, error)

func NewEtcdClient(
	ctx context.Context,
	tracerProvider trace.TracerProvider,
	managementClusterClient *alias.ManagementClusterClient,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
	serverPort int32,
) (EtcdClient, error) {
	return tracing.WithSpan(ctx, tracer, "NewEtcdClient",
		func(ctx context.Context, span trace.Span) (EtcdClient, error) {
			caPool, clientCertificate, err := getETCDConnectionTLS(
				ctx,
				managementClusterClient,
				hostedControlPlane,
				cluster,
			)
			if err != nil {
				return nil, err
			}
			return &etcdClient{
				tracerProvider:          tracerProvider,
				managementClusterClient: managementClusterClient,
				caPool:                  caPool,
				clientCertificate:       clientCertificate,
				endpoints:               names.GetEtcdDNSNames(cluster),
				serverPort:              serverPort,
				anyEndpoint: fmt.Sprintf("https://%s", net.JoinHostPort(
					names.GetEtcdClientServiceDNSName(cluster),
					strconv.Itoa(int(serverPort)),
				)),
			}, nil
		},
	)
}

func (e *etcdClient) GetStatuses(ctx context.Context) (map[string]*clientv3.StatusResponse, error) {
	return tracing.WithSpan(ctx, tracer, "EtcdClient.GetStatuses",
		func(ctx context.Context, span trace.Span) (map[string]*clientv3.StatusResponse, error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			return callETCDFuncOnAllMembers(
				ctx,
				e,
				clientv3.Client.Status,
			)
		},
	)
}

func (e *etcdClient) OpenSnapshotStream(ctx context.Context) (*clientv3.SnapshotResponse, func() error, error) {
	return tracing.WithSpan3(ctx, tracer, "EtcdClient.CreateSnapshot",
		func(ctx context.Context, span trace.Span) (_ *clientv3.SnapshotResponse, _ func() error, retErr error) {
			endpoint := e.anyEndpoint
			etcdClient, err := createEtcdClient(e, endpoint)
			closeFunc := func() error {
				var closeErr error
				closeEtcdClient(etcdClient, &closeErr, endpoint)
				return closeErr
			}
			if err != nil {
				return nil, closeFunc, err
			}

			snapshotResponse, err := callETCDFuncOnAnyMember(
				ctx,
				etcdClient,
				endpoint,
				clientv3.Client.SnapshotWithVersion,
			)
			return snapshotResponse, closeFunc, err
		},
	)
}

func closeEtcdClient(etcdClient *clientv3.Client, retErr *error, endpoint string) { //nolint:gocritic
	if etcdClient != nil {
		if err := etcdClient.Close(); err != nil {
			*retErr = errors.Join(
				*retErr,
				fmt.Errorf("failed to close etcd client for endpoint %s: %w", endpoint, err),
			)
		}
	}
}

func createEtcdClient(etcd *etcdClient, endpoint string) (*clientv3.Client, error) {
	etcdConfig := clientv3.Config{
		Endpoints: []string{endpoint},
		Logger:    zap.NewNop(),
		TLS: &tls.Config{
			InsecureSkipVerify: false,
			RootCAs:            etcd.caPool,
			Certificates:       []tls.Certificate{etcd.clientCertificate},
			MinVersion:         tls.VersionTLS12,
		},
		DialOptions: []grpc.DialOption{
			grpc.WithStatsHandler(
				otelgrpc.NewClientHandler(
					otelgrpc.WithTracerProvider(etcd.tracerProvider),
					otelgrpc.WithMessageEvents(otelgrpc.ReceivedEvents, otelgrpc.SentEvents),
				),
			),
		},
		DialTimeout: 10 * time.Second,
	}
	etcdClient, err := clientv3.New(etcdConfig)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create etcd client for endpoint %s: %w", endpoint,
			err,
		)
	}
	return etcdClient, nil
}

func (e *etcdClient) ListAlarms(ctx context.Context) (*clientv3.AlarmResponse, error) {
	return tracing.WithSpan(ctx, tracer, "EtcdClient.ListAlarms",
		func(ctx context.Context, span trace.Span) (_ *clientv3.AlarmResponse, retErr error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			endpoint := e.anyEndpoint
			etcdClient, err := createEtcdClient(e, endpoint)
			if err != nil {
				return nil, err
			}
			defer closeEtcdClient(etcdClient, &retErr, endpoint)
			return callETCDFuncOnAnyMember(
				ctx,
				etcdClient,
				endpoint,
				clientv3.Client.AlarmList,
			)
		},
	)
}

func (e *etcdClient) DisarmAlarm(ctx context.Context, alarm *clientv3.AlarmMember) error {
	return tracing.WithSpan1(ctx, tracer, "EtcdClient.DisarmAlarm",
		func(ctx context.Context, span trace.Span) (retErr error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			endpoint := e.anyEndpoint
			etcdClient, err := createEtcdClient(e, endpoint)
			if err != nil {
				return err
			}
			defer closeEtcdClient(etcdClient, &retErr, endpoint)
			_, err = callETCDFuncOnAnyMember(
				ctx,
				etcdClient,
				endpoint,
				func(client clientv3.Client, ctx context.Context) (*clientv3.AlarmResponse, error) {
					response, err := client.AlarmDisarm(ctx, alarm)
					if err != nil {
						return nil, fmt.Errorf(
							"failed to disarm etcd alarm %s for member %d: %w",
							alarm.Alarm.String(), alarm.MemberID, err,
						)
					}
					return response, nil
				},
			)
			return err
		},
	)
}

func callETCDFuncOnAllMembers[R any](
	ctx context.Context,
	etcd *etcdClient,
	etcdFunc func(client clientv3.Client, ctx context.Context, endpoint string) (*R, error),
) (map[string]*R, error) {
	return tracing.WithSpan(ctx, tracer, "CallETCDFuncOnAllMembers",
		func(ctx context.Context, span trace.Span) (map[string]*R, error) {
			endpointMap := slices.MapValues(etcd.endpoints, func(endpoint string, _ string) string {
				return fmt.Sprintf("https://%s", net.JoinHostPort(endpoint, strconv.Itoa(int(etcd.serverPort))))
			})
			results := make(map[string]*R, len(endpointMap))
			var errs error
			for _, endpoint := range endpointMap {
				etcdClient, err := createEtcdClient(etcd, endpoint)
				if err != nil {
					errs = errors.Join(errs, err)
					continue
				}
				result, err := callETCDFuncOnMember(ctx, etcdClient, endpoint, etcdFunc)
				closeEtcdClient(etcdClient, &err, endpoint)
				results[endpoint] = result
				errs = errors.Join(errs, err)
			}

			return results, errs
		},
	)
}

func getETCDConnectionTLS(
	ctx context.Context,
	managementClusterClient *alias.ManagementClusterClient,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (*x509.CertPool, tls.Certificate, error) {
	return tracing.WithSpan3(ctx, tracer, "GetETCDConnectionTLS",
		func(ctx context.Context, span trace.Span) (*x509.CertPool, tls.Certificate, error) {
			secrets := managementClusterClient.CoreV1().Secrets(hostedControlPlane.Namespace)
			etcdCASecret, err := secrets.
				Get(ctx, names.GetEtcdCASecretName(cluster), metav1.GetOptions{})
			if err != nil {
				return nil, tls.Certificate{}, fmt.Errorf("failed to get etcd CA secret: %w", err)
			}

			caPool := x509.NewCertPool()
			if !caPool.AppendCertsFromPEM(etcdCASecret.Data[corev1.TLSCertKey]) {
				return nil, tls.Certificate{}, errETCDCAFailedToAppend
			}

			clientCertificateSecret, err := secrets.
				Get(ctx, names.GetEtcdControllerClientCertificateSecretName(cluster), metav1.GetOptions{})
			if err != nil {
				return nil, tls.Certificate{}, fmt.Errorf(
					"failed to get etcd API server client certificate secret: %w",
					err,
				)
			}
			clientCertificate, err := tls.X509KeyPair(
				clientCertificateSecret.Data[corev1.TLSCertKey],
				clientCertificateSecret.Data[corev1.TLSPrivateKeyKey],
			)
			if err != nil {
				return nil, tls.Certificate{}, fmt.Errorf("failed to load etcd API server client certificate: %w", err)
			}
			return caPool, clientCertificate, nil
		},
	)
}

func callETCDFuncOnMember[R any](
	ctx context.Context,
	etcdClient *clientv3.Client,
	endpoint string,
	etcdFunc func(client clientv3.Client, ctx context.Context, endpoint string) (R, error),
) (R, error) {
	return tracing.WithSpan(ctx, tracer, "CallETCDFuncOnMember",
		func(ctx context.Context, span trace.Span) (R, error) {
			span.SetAttributes(
				attribute.String("etcd.endpoint", endpoint),
			)
			var err error
			var result R
			_, _, err = slices.AttemptWithDelay(4, 5*time.Second, func(_ int, _ time.Duration) (err error) {
				result, err = etcdFunc(*etcdClient, ctx, endpoint)
				return err
			})

			return result, errorsUtil.IfErrErrorf("failed to call etcd function on endpoint %s: %w", endpoint, err)
		},
	)
}

func callETCDFuncOnAnyMember[R any](
	ctx context.Context,
	etcdClient *clientv3.Client,
	endpoint string,
	etcdFunc func(client clientv3.Client, ctx context.Context) (R, error),
) (R, error) {
	return tracing.WithSpan(ctx, tracer, "CallETCDFuncOnAnyMember",
		func(ctx context.Context, span trace.Span) (R, error) {
			return callETCDFuncOnMember(ctx, etcdClient, endpoint,
				func(client clientv3.Client, ctx context.Context, _ string) (R, error) {
					return etcdFunc(client, ctx)
				})
		},
	)
}
