package basic

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/clustertest/v5/pkg/logger"
)

// gatewayPolicyCascadesToListenerSets records whether a BackendTrafficPolicy that
// targets a Gateway, with no sectionName, also applies to listeners contributed by
// a ListenerSet.
//
// It does, as observed on Envoy Gateway 1.10: every 443 listener collapses into a
// single Envoy listener and the gateway-wide response override lands on all of it,
// while a ListenerSet that carries its own policy still serves its own error page.
// envoyproxy/gateway#9409 and #9242 are both still open, but they describe policies
// attaching through routes, which is a different question from this one.
//
// This is pinned rather than merely logged so that an Envoy Gateway bump that
// changes the behaviour fails loudly, because the answer decides whether customers
// need one policy per ListenerSet or can rely on a gateway-wide one.
const gatewayPolicyCascadesToListenerSets = true

// listenerSetPolicyCascadeTests sends the same /status/503 request through three
// listeners and compares the error page each one produces. Only the listener
// traversed differs, so the body identifies the policy that was in scope.
func listenerSetPolicyCascadeTests() {
	// Control first: this is the chart's Gateway-level policy doing what it has
	// always done. If it fails the other two cases mean nothing.
	By("checking the gateway error page is served through the gateway's own listener")
	body := expect503Body(gatewayHostname())
	Expect(body).To(ContainSubstring(gatewayErrorPageMarker),
		"the gateway BackendTrafficPolicy no longer overrides 503 responses, so the ListenerSet comparisons below are meaningless")

	By("checking the chart ListenerSet serves its own error page")
	body = expect503Body(chartListenerSetHostname())
	Expect(body).To(ContainSubstring(listenerSetErrorPageMarker),
		"the ListenerSet-targeted BackendTrafficPolicy did not take effect")
	Expect(body).NotTo(ContainSubstring(gatewayErrorPageMarker),
		"the ListenerSet listener served the gateway error page instead of its own")

	By("checking whether the gateway policy cascades to a ListenerSet with no policy of its own")
	body = expect503Body(tenantListenerSetHostname())
	logger.Log("Tenant ListenerSet 503 body: %q", body)
	AddReportEntry("gateway policy cascade to ListenerSet", body)

	if gatewayPolicyCascadesToListenerSets {
		Expect(body).To(ContainSubstring(gatewayErrorPageMarker),
			"the gateway policy stopped cascading to ListenerSet listeners, check envoyproxy/gateway#9409 and #9242 before updating gatewayPolicyCascadesToListenerSets")
		return
	}
	Expect(body).NotTo(ContainSubstring(gatewayErrorPageMarker),
		"the gateway policy now cascades to ListenerSet listeners, which is a behaviour change: check envoyproxy/gateway#9409 and #9242, flip gatewayPolicyCascadesToListenerSets and update the docs")
}

// expect503Body asks go-httpbin for a 503 through hostname and returns the body the
// client actually received, which is the error page if a policy replaced it.
func expect503Body(hostname string) string {
	url := fmt.Sprintf("https://%s/status/503", hostname)
	var body string

	Eventually(func() error {
		client := &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				ServerName: hostname,
				MinVersion: tls.VersionTLS12,
			}},
			Timeout: 30 * time.Second,
		}
		resp, err := client.Get(url) //nolint:noctx
		if err != nil {
			return fmt.Errorf("GET %s: %w", url, err)
		}
		defer resp.Body.Close() //nolint:errcheck

		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("reading body of %s: %w", url, err)
		}
		if resp.StatusCode != http.StatusServiceUnavailable {
			return fmt.Errorf("GET %s returned %d, expected 503", url, resp.StatusCode)
		}

		body = strings.TrimSpace(string(raw))
		return nil
	}).
		WithTimeout(trafficTimeout).
		WithPolling(trafficPolling).
		Should(Succeed())

	return body
}
