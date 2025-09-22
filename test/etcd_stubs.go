package test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	slices "github.com/samber/lo"
	"github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/reconcilers/etcd_cluster/s3_client"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

var EtcdSnapshotData = []byte("etcd snapshot data")

type EtcdClientStub struct {
	StatusError     error
	SnapshotError   error
	AlarmError      error
	DisarmError     error
	StatusResponses map[string]*clientv3.StatusResponse
	ActiveAlarms    []*etcdserverpb.AlarmMember
}

func NewEtcdClientStub() *EtcdClientStub {
	return &EtcdClientStub{
		StatusResponses: make(map[string]*clientv3.StatusResponse),
		ActiveAlarms:    make([]*etcdserverpb.AlarmMember, 0),
	}
}

func (s *EtcdClientStub) GetStatuses(_ context.Context) (map[string]*clientv3.StatusResponse, error) {
	if s.StatusError != nil {
		return nil, s.StatusError
	}
	return s.StatusResponses, nil
}

func (s *EtcdClientStub) CreateSnapshot(_ context.Context) (*clientv3.SnapshotResponse, error) {
	if s.SnapshotError != nil {
		return nil, s.SnapshotError
	}
	return &clientv3.SnapshotResponse{
		Header:   &etcdserverpb.ResponseHeader{ClusterId: 1},
		Snapshot: io.NopCloser(bytes.NewReader(EtcdSnapshotData)),
	}, nil
}

func (s *EtcdClientStub) ListAlarms(_ context.Context) (*clientv3.AlarmResponse, error) {
	if s.AlarmError != nil {
		return nil, s.AlarmError
	}
	return &clientv3.AlarmResponse{
		Header: &etcdserverpb.ResponseHeader{ClusterId: 1},
		Alarms: s.ActiveAlarms,
	}, nil
}

func (s *EtcdClientStub) DisarmAlarm(_ context.Context, alarm *clientv3.AlarmMember) error {
	if s.DisarmError != nil {
		return s.DisarmError
	}

	s.ActiveAlarms = slices.Filter(s.ActiveAlarms, func(a *etcdserverpb.AlarmMember, _ int) bool {
		return a.MemberID != alarm.MemberID || a.Alarm != alarm.Alarm
	})

	return nil
}

type S3ClientStub struct {
	UploadError      error
	LastUploadedBody []byte
}

var _ s3_client.S3Client = &S3ClientStub{}

func NewS3ClientStub() *S3ClientStub {
	return &S3ClientStub{}
}

func (s *S3ClientStub) Upload(_ context.Context, body io.ReadCloser) (err error) {
	defer func() {
		if closeErr := body.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to close body reader: %w", closeErr))
		}
	}()
	if s.UploadError != nil {
		return s.UploadError
	}

	if body != nil {
		data, err := io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("failed to read body: %w", err)
		}
		s.LastUploadedBody = data
	}

	return nil
}
