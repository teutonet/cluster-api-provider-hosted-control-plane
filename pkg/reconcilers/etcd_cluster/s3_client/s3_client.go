package s3_client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/tracing/smithyoteltracing"
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
	errBucketNameEmpty        = errors.New("s3 bucket name is empty")
)

type ProgressWatcher struct {
	Watcher func()
}

var _ transfermanager.ObjectBytesTransferredListener = &ProgressWatcher{}

func (p *ProgressWatcher) OnObjectBytesTransferred(_ context.Context, _ *transfermanager.ObjectBytesTransferredEvent) {
	if p.Watcher != nil {
		p.Watcher()
	}
}

type S3Client interface {
	Upload(ctx context.Context, body io.ReadCloser, progressWatcher func()) error
}

type s3Client struct {
	uploader        *transfermanager.Client
	progressWatcher *ProgressWatcher
	bucket          string
	keyTemplate     string
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
	tracerProvider trace.TracerProvider,
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
		endpointKey := etcdBackupSecretConfig.HostKeyOrDefault()
		regionKey := etcdBackupSecretConfig.RegionKeyOrDefault()
		bucketKey := etcdBackupSecretConfig.BucketKeyOrDefault()
		span.SetAttributes(
			attribute.String("etcd.backup.s3.secret.namespace", secretNamespace),
			attribute.String("etcd.backup.s3.secret.name", secretName),
			attribute.String(
				"etcd.backup.s3.key",
				fmt.Sprintf("%s/%s/<timestamp>.etcd", cluster.Namespace, cluster.Name),
			),
			attribute.String("etcd.backup.schedule", hostedControlPlane.Spec.ETCD.Backup.Schedule),
		)
		s3Secret, err := managementClusterClient.CoreV1().Secrets(secretNamespace).
			Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get S3 credentials secret: %w", err)
		}
		accessKeyID, found := s3Secret.Data[accessKeyIDKey]
		if !found {
			return nil, fmt.Errorf("missing %s: %w", accessKeyIDKey, errCredentialIsMissingKey)
		}
		secretAccessKey, found := s3Secret.Data[secretAccessKeyKey]
		if !found {
			return nil, fmt.Errorf("missing %s: %w", secretAccessKeyKey, errCredentialIsMissingKey)
		}
		endpoint := ""
		endpointBytes, found := s3Secret.Data[endpointKey]
		if found {
			endpoint = string(endpointBytes)
			span.SetAttributes(attribute.String("etcd.backup.s3.endpoint", endpoint))
		}
		regionBytes, found := s3Secret.Data[regionKey]
		if !found {
			return nil, fmt.Errorf("missing %s: %w", regionKey, errCredentialIsMissingKey)
		}
		region := string(regionBytes)
		span.SetAttributes(attribute.String("etcd.backup.s3.region", region))
		bucketBytes, found := s3Secret.Data[bucketKey]
		if !found {
			return nil, fmt.Errorf("missing %s: %w", bucketKey, errCredentialIsMissingKey)
		}
		bucketString := string(bucketBytes)

		parts := strings.SplitN(bucketString, "/", 2)
		bucket := parts[0]
		if bucket == "" {
			return nil, fmt.Errorf(
				"bucket secret key %q resolved to an empty bucket name: %w",
				bucketKey,
				errBucketNameEmpty,
			)
		}
		span.SetAttributes(attribute.String("etcd.backup.s3.bucket", bucket))
		prefix := ""
		if len(parts) == 2 {
			prefix = parts[1]
			if prefix != "" {
				prefix += "/"
				span.SetAttributes(attribute.String("etcd.backup.s3.prefix", prefix))
			}
		}

		s3ConfigOptions := []func(options *config.LoadOptions) error{
			config.WithRegion(region),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
				string(accessKeyID),
				string(secretAccessKey),
				"",
			)),
		}
		s3Options := []func(options *s3.Options){
			func(options *s3.Options) {
				options.TracerProvider = smithyoteltracing.Adapt(tracerProvider)
			},
		}
		if endpoint != "" {
			s3ConfigOptions = append(s3ConfigOptions,
				config.WithBaseEndpoint(fmt.Sprintf("https://%s", endpoint)),
			)
			s3Options = append(s3Options, func(options *s3.Options) {
				options.UsePathStyle = true
				options.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
			})
		}

		s3Config, err := config.LoadDefaultConfig(ctx, s3ConfigOptions...)
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS config: %w", err)
		}

		rawS3 := s3.NewFromConfig(s3Config, s3Options...)
		var s3APIClient transfermanager.S3APIClient = rawS3
		if endpoint != "" {
			s3APIClient = &checksumStrippingS3Client{rawS3}
		}

		progressWatcher := &ProgressWatcher{}

		transfermanagerOptions := []func(options *transfermanager.Options){
			func(options *transfermanager.Options) {
				options.Concurrency = 1
				options.PartSizeBytes = 32 * 1024 * 1024
				options.ObjectProgressListeners = transfermanager.ObjectProgressListeners{
					ObjectBytesTransferred: []transfermanager.ObjectBytesTransferredListener{progressWatcher},
				}
			},
		}

		return &s3Client{
			uploader:        transfermanager.New(s3APIClient, transfermanagerOptions...),
			progressWatcher: progressWatcher,
			bucket:          bucket,
			keyTemplate:     fmt.Sprintf("%s%s/%s/%%s.etcd", prefix, cluster.Namespace, cluster.Name),
		}, nil
	})
}

func (s *s3Client) Upload(ctx context.Context, body io.ReadCloser, progressWatcher func()) error {
	return tracing.WithSpan1(ctx, tracer, "Upload", func(ctx context.Context, span trace.Span) (retErr error) {
		defer func() {
			if closeErr := body.Close(); closeErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("failed to close body reader: %w", closeErr))
			}
		}()
		s.progressWatcher.Watcher = progressWatcher
		countingReader := readerutil.CountingReader{Reader: body, N: new(int64)}
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

// checksumStrippingS3Client wraps *s3.Client and clears ChecksumAlgorithm from upload
// requests. S3-compatible backends (OpenStack Swift s3api, Ceph RGW) do not support the
// aws-chunked trailing checksum encoding that the transfermanager enables unconditionally
// by setting ChecksumAlgorithm=CRC32 on every request.
type checksumStrippingS3Client struct {
	*s3.Client
}

func (c *checksumStrippingS3Client) CreateMultipartUpload(
	ctx context.Context, input *s3.CreateMultipartUploadInput, opts ...func(*s3.Options),
) (*s3.CreateMultipartUploadOutput, error) {
	input.ChecksumAlgorithm = ""
	out, err := c.Client.CreateMultipartUpload(ctx, input, opts...)
	if err != nil {
		return nil, fmt.Errorf("CreateMultipartUpload: %w", err)
	}
	return out, nil
}

func (c *checksumStrippingS3Client) UploadPart(
	ctx context.Context, input *s3.UploadPartInput, opts ...func(*s3.Options),
) (*s3.UploadPartOutput, error) {
	input.ChecksumAlgorithm = ""
	out, err := c.Client.UploadPart(ctx, input, opts...)
	if err != nil {
		return nil, fmt.Errorf("UploadPart: %w", err)
	}
	return out, nil
}

func (c *checksumStrippingS3Client) PutObject(
	ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	input.ChecksumAlgorithm = ""
	out, err := c.Client.PutObject(ctx, input, opts...)
	if err != nil {
		return nil, fmt.Errorf("PutObject: %w", err)
	}
	return out, nil
}
