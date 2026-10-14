package ipmode

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"

	"github.com/giantswarm/apptest-framework/v5/pkg/suite"

	"e2e/internal/common"
)

const (
	isUpgrade = false
)

// TestIPMode runs the default gateway with NLB IP targets and a fixed replica count
// on a cluster whose nodes span several availability zones. It covers what the
// read-only basic suite cannot: that new proxy pods carry the AWS Load Balancer
// Controller readiness gate and only become Ready once their NLB target is healthy,
// and that freshly scheduled proxy pods actually land in more than one zone.
//
// Unlike the basic suite, this suite replaces the proxy pods.
func TestIPMode(t *testing.T) {
	suite.New().
		InAppBundle("gateway-api-bundle").
		WithInstallNamespace("envoy-gateway-system").
		WithIsUpgrade(isUpgrade).
		WithValuesFile("./values.yaml").
		AfterClusterReady(func() {
			common.InstallDependencies()
		}).
		AfterSuite(func() {
			common.CleanupDependencies()
		}).
		Tests(func() {
			// Ordered so a failure skips the rest: each step builds on the state the
			// previous one waited for.
			Describe("ip target mode", Ordered, func() {
				It("should have the gateway proxies running", func() {
					proxyRunningTests()
				})
				It("should label the proxy namespace for NLB readiness gates", func() {
					readinessGateWiringTests()
				})
				It("should gate new proxy pods on NLB target health", func() {
					readinessGatePodTests()
				})
				// After the pods were replaced, so their placement was decided with the
				// zone spread constraint and every zone's nodes already in place.
				It("should spread the proxy pods across zones", func() {
					zoneSpreadTests()
				})
			})
		}).
		Run(t, "Gateway-API Config IP Mode Test")
}
