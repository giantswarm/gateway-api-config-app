package basic

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/clustertest/v5/pkg/logger"
)

const (
	trafficTimeout = 10 * time.Minute
	trafficPolling = 15 * time.Second
)

// listenerSetTrafficTests drives real HTTPS through each ListenerSet listener.
// Certificate verification stays on against the system trust store, so a
// successful handshake is itself the public-trust assertion.
func listenerSetTrafficTests() {
	By("checking the chart ListenerSet serves HTTPS")
	expectHTTPSOK(chartListenerSetHostname(), "lset-chart")

	By("checking the tenant ListenerSet serves HTTPS")
	expectHTTPSOK(tenantListenerSetHostname(), "lset-tenant")
}

// expectHTTPSOK requests /status/200 over HTTPS and checks the marker header of
// the HTTPRoute that is supposed to serve that hostname.
//
// On failure it retries once against the load balancer hostname, keeping SNI and
// Host set to the ListenerSet hostname. That separates "DNS has not propagated"
// from "TLS or routing is broken" and puts the distinction in the failure message.
func expectHTTPSOK(hostname, marker string) {
	url := fmt.Sprintf("https://%s/status/200", hostname)

	Eventually(func() error {
		err := getAndCheck(nil, url, hostname, marker)
		if err == nil {
			return nil
		}

		lbHostname, lbErr := getGatewayLBHostname(wc())
		if lbErr != nil {
			return err
		}
		if viaLB := getAndCheck(&lbHostname, url, hostname, marker); viaLB == nil {
			return fmt.Errorf("%s fails by name but succeeds when dialling the load balancer %s directly, so TLS and routing are fine and DNS has not propagated: %w", hostname, lbHostname, err)
		}
		return err
	}).
		WithTimeout(trafficTimeout).
		WithPolling(trafficPolling).
		Should(Succeed())
}

// getAndCheck issues the request and validates status and marker header. When dialTo
// is set the connection goes to that host instead of the one in the URL, while SNI,
// Host header and certificate verification all still use the URL's hostname.
func getAndCheck(dialTo *string, url, hostname, marker string) error {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			ServerName: hostname,
			MinVersion: tls.VersionTLS12,
		},
	}
	if dialTo != nil {
		target := net.JoinHostPort(*dialTo, "443")
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 15 * time.Second}
			return dialer.DialContext(ctx, network, target)
		}
	}

	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned %d, expected 200", url, resp.StatusCode)
	}
	if got := resp.Header.Get(routeMarkerHeader); got != marker {
		return fmt.Errorf("GET %s was served by route %q, expected %q", url, got, marker)
	}

	logger.Log("GET %s -> 200 (%s: %s)", url, routeMarkerHeader, marker)
	return nil
}
