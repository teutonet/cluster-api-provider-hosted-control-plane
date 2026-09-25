package etcd_client_test

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	clientv3 "go.etcd.io/etcd/client/v3"

	operatorutil "github.com/teutonet/cluster-api-provider-hosted-control-plane/pkg/operator/util"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
)

// TestEtcdClientServerCompatibility proves that the pinned go.etcd.io/etcd/client/v3
// library can actually talk to the etcd server version the operator deploys by default
// (operatorutil.ResolveETCDImage). The image tag and the client library version are
// tracked by renovate independently (see images.go), so matching version numbers is no
// longer guaranteed by construction — this round-trip against a real server is what
// actually guarantees they keep working together.
func TestEtcdClientServerCompatibility(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	g, ctx, _ := G(t)

	image := operatorutil.ResolveETCDImage(nil)

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        image,
			ExposedPorts: []string{"2379/tcp"},
			Cmd: []string{
				"etcd",
				"--data-dir=/tmp/etcd-data",
				"--listen-client-urls=http://0.0.0.0:2379",
				"--advertise-client-urls=http://0.0.0.0:2379",
				"--listen-peer-urls=http://0.0.0.0:2380",
				"--initial-advertise-peer-urls=http://0.0.0.0:2380",
				"--initial-cluster=default=http://0.0.0.0:2380",
				"--initial-cluster-state=new",
			},
			WaitingFor: wait.ForLog("ready to serve client requests").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to start etcd image %s", image)
	t.Cleanup(func() {
		g.Expect(container.Terminate(context.Background())).To(Succeed())
	})

	endpoint, err := container.PortEndpoint(ctx, "2379/tcp", "http")
	g.Expect(err).NotTo(HaveOccurred())

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 10 * time.Second,
	})
	g.Expect(err).NotTo(HaveOccurred())
	defer func() {
		g.Expect(client.Close()).To(Succeed())
	}()

	putCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = client.Put(putCtx, "compat-check", "ok")
	g.Expect(err).NotTo(HaveOccurred(), "client library failed to Put against etcd image %s", image)

	getCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	response, err := client.Get(getCtx, "compat-check")
	g.Expect(err).NotTo(HaveOccurred(), "client library failed to Get against etcd image %s", image)
	g.Expect(response.Kvs).To(HaveLen(1))
	g.Expect(string(response.Kvs[0].Value)).To(Equal("ok"))
}
