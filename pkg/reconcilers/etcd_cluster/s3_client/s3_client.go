package s3_client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/api/v1alpha1"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/alias"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/util/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go4.org/readerutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capiv2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var (
	tracer                    = tracing.GetTracer("S3Client")
	errCredentialIsMissingKey = errors.New("s3 credential is missing key")
)

type S3Client interface {
	Upload(ctx context.Context, body io.ReadCloser) error
}

type s3Client struct {
	spanAttributes []attribute.KeyValue
	uploader       *transfermanager.Client
	bucket         string
	keyTemplate    string
}

var _ S3Client = &s3Client{}

type S3ClientFactory = func(
	ctx context.Context,
	managementClusterClient *alias.ManagementClusterClient,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (S3Client, error)

func NewS3Client(
	ctx context.Context,
	managementClusterClient *alias.ManagementClusterClient,
	hostedControlPlane *v1alpha1.HostedControlPlane,
	cluster *capiv2.Cluster,
) (S3Client, error) {
	return tracing.WithSpan(ctx, tracer, "NewS3Client", func(ctx context.Context, span trace.Span) (S3Client, error) {
		etcdBackupSecretConfig := hostedControlPlane.Spec.ETCD.Backup.Secret
		secretNamespace := ptr.Deref(etcdBackupSecretConfig.Namespace, hostedControlPlane.Namespace)
		secretName := etcdBackupSecretConfig.Name
		accessKeyIDKey := etcdBackupSecretConfig.AccessKeyIDKeyOrDefault()
		secretAccessKeyKey := etcdBackupSecretConfig.SecretAccessKeyKeyOrDefault()
		spanAttributes := []attribute.KeyValue{
			attribute.String("etcd.backup.s3.secret.namespace", secretNamespace),
			attribute.String("etcd.backup.s3.secret.name", secretName),
			attribute.String("etcd.backup.s3.secret.accessKeyIDKey", accessKeyIDKey),
			attribute.String("etcd.backup.s3.secret.secretAccessKey", secretAccessKeyKey),
			attribute.String("etcd.backup.s3.bucket", hostedControlPlane.Spec.ETCD.Backup.Bucket),
			attribute.String("etcd.backup.s3.region", hostedControlPlane.Spec.ETCD.Backup.Region),
			attribute.String("etcd.backup.s3.key", fmt.Sprintf("%s/<timestamp>.etcd", cluster.Name)),
			attribute.String("etcd.backup.schedule", hostedControlPlane.Spec.ETCD.Backup.Schedule),
		}
		span.SetAttributes(spanAttributes...)
		s3Secret, err := managementClusterClient.CoreV1().Secrets(secretNamespace).
			Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get S3 credentials secret: %w", err)
		}
		accessKeyID, ok := s3Secret.Data[accessKeyIDKey]
		if !ok {
			return nil, fmt.Errorf("missing %s: %w", accessKeyIDKey, errCredentialIsMissingKey)
		}
		secretAccessKey, ok := s3Secret.Data[secretAccessKeyKey]
		if !ok {
			return nil, fmt.Errorf("missing %s: %w", secretAccessKeyKey, errCredentialIsMissingKey)
		}

		defaultConfig, err := config.LoadDefaultConfig(ctx,
			config.WithRegion(hostedControlPlane.Spec.ETCD.Backup.Region),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
				string(accessKeyID),
				string(secretAccessKey),
				"",
			)),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS config: %w", err)
		}
		return &s3Client{
			spanAttributes: spanAttributes,
			uploader:       transfermanager.New(s3.NewFromConfig(defaultConfig)),
			bucket:         hostedControlPlane.Spec.ETCD.Backup.Bucket,
			keyTemplate:    fmt.Sprintf("%s/%%s.etcd", cluster.Name),
		}, nil
	})
}

func (s *s3Client) Upload(ctx context.Context, body io.ReadCloser) error {
	return tracing.WithSpan1(ctx, tracer, "Upload", func(ctx context.Context, span trace.Span) (retErr error) {
		span.SetAttributes(s.spanAttributes...)
		defer func() {
			if closeErr := body.Close(); closeErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("failed to close body reader: %w", closeErr))
			}
		}()
		countingReader := readerutil.CountingReader{Reader: body}
		if result, err := s.uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(fmt.Sprintf(s.keyTemplate, time.Now().Format(time.RFC3339))),
			Body:   countingReader,
		}); err != nil {
			return fmt.Errorf("failed to upload content to S3: %w", err)
		} else {
			span.SetAttributes(
				attribute.String("etcd.backup.s3.upload.location", *result.Location),
				attribute.Int64("etcd.backup.s3.upload.bytes", *countingReader.N),
			)
		}
		return nil
	})
}
