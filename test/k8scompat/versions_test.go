package k8scompat

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/teutonet/cluster-api-provider-hosted-control-plane/test"
)

func TestWalkStableVersions_StopsAtFirst404(t *testing.T) {
	g, ctx, _ := G(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stable-1.33.txt":
			_, _ = w.Write([]byte("v1.33.13\n"))
		case "/stable-1.34.txt":
			_, _ = w.Write([]byte("v1.34.12\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	versions, err := walkStableVersions(ctx, server.URL, 33)

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(versions).To(Equal([]string{"v1.33.13", "v1.34.12"}))
}

func TestWalkStableVersions_NoneReleasedIsAnError(t *testing.T) {
	g, ctx, _ := G(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := walkStableVersions(ctx, server.URL, 33)

	g.Expect(err).To(HaveOccurred())
}

func TestWalkStableVersions_ServerErrorIsAnError(t *testing.T) {
	g, ctx, _ := G(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := walkStableVersions(ctx, server.URL, 33)

	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("unexpected status 500"))
}

func TestCurrentKubernetesMinor_MatchesGoMod(t *testing.T) {
	g, ctx, _ := G(t)

	minor, err := currentKubernetesMinor(ctx)

	g.Expect(err).NotTo(HaveOccurred())
	// this module pins k8s.io/kubernetes v1.36.x at the time of writing; only
	// asserting a floor so bumping go.mod doesn't require touching this test.
	g.Expect(minor).To(BeNumerically(">=", 36))
}
