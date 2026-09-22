package basic

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"

	"github.com/giantswarm/apptest-framework/v5/pkg/suite"
	"github.com/giantswarm/clustertest/v5/pkg/client"
	"github.com/giantswarm/clustertest/v5/pkg/logger"
	"github.com/giantswarm/clustertest/v5/pkg/wait"

	corev1 "k8s.io/api/core/v1"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	isUpgrade = false
)

func TestBasic(t *testing.T) {
	suite.New().
		InAppBundle("gateway-api-bundle").
		WithInstallNamespace("envoy-gateway-system").
		WithIsUpgrade(isUpgrade).
		WithValuesFile("./values.yaml").
		AfterClusterReady(func() {
			installDependencies()
		}).
		AfterSuite(func() {
			cleanupListenerSetFixtures()
			cleanupDependencies()
		}).
		Tests(func() {
			It("should have the app correctly deployed", func() {
				deploymentAppTests()
			})
			It("should have the gatewayclass resources correctly configured", func() {
				gatewayClassResourceTests()
				gatewayClassEnvoyProxyTests()
				gatewayClassPolicyTests()
				gatewayClassKyvernoRBACTests()
			})
			It("should have the gateway resources correctly configured", func() {
				gatewayGatewayTests()
				gatewayEnvoyProxyTests()
				gatewayClientTrafficPolicyTests()
				gatewayBackendTrafficPolicyTests()
				gatewayIssuerTests()
				gatewayCertificateTests()
				gatewayHTTPRouteTests()
			})
			It("should have the gateway correctly deployed", func() {
				gatewayDeploymentTests()
				gatewayHPAAndPDBTests()
				gatewayHTTPRedirectBehaviorTest()
				gatewayHealthCheckBehaviorTest()
				gatewayClientIPBehaviorTest()
				gatewayMonitoringTests()
				gatewayKyvernoRegenerationTest()
			})
			It("should have the gateway integrated with karpenter", func() {
				gatewayKarpenterNodeTests()
				gatewayKarpenterProxyPodTests()
			})
			// The ListenerSet blocks come last on purpose: the chart listener set's
			// ACME order and DNS record have been in flight since the app was
			// installed, so by now they are effectively free, and the namespace and
			// routes they add must not perturb the assertions above.
			//
			// Ordered so the fixtures live in a BeforeAll and a failure skips the rest
			// of the block. Left inside a spec, a timeout on the first assertion would
			// leave the later specs waiting out every Eventually against objects that
			// were never created.
			Describe("listenersets", Ordered, func() {
				BeforeAll(func() {
					createTenantNamespace()
					deployHttpbin()
					// The tenant ListenerSet is created here, and its Certificate only
					// in listenerSetTenantCertTests, so the TLS Secret genuinely
					// arrives after the ListenerSet. That ordering is the
					// envoyproxy/gateway#9614 regression.
					createTenantListenerSet()
					createFixtureRoutes()
				})

				It("should have the chart-managed listenerset configured and accepted", func() {
					listenerSetChartResourceTests()
					listenerSetExternalDNSConfigTests()
				})
				It("should have both listenersets serving traffic end to end", func() {
					listenerSetTenantCertTests()
					listenerSetCertificateContentTests()
					listenerSetDNSTests()
					listenerSetTrafficTests()
					listenerSetPolicyCascadeTests()
				})
				It("should reload a rotated listenerset certificate", func() {
					listenerSetCertRotationTests()
				})
			})
		}).
		Run(t, "Gateway-API Config Test")
}

func arePodsRunning(ctx context.Context, kubeClient *client.Client, listOptions *cr.ListOptions) wait.WaitCondition {
	return func() (bool, error) {
		podList := &corev1.PodList{}
		var err error

		if listOptions != nil {
			err = kubeClient.List(ctx, podList, listOptions)
		} else {
			err = kubeClient.List(ctx, podList)
		}

		if err != nil {
			return false, err
		}

		for _, pod := range podList.Items {
			phase := pod.Status.Phase
			if phase != corev1.PodRunning && phase != corev1.PodSucceeded {
				logger.Log("pod %s/%s in %s phase", pod.Namespace, pod.Name, phase)
				return false, fmt.Errorf("pod %s/%s in %s phase", pod.Namespace, pod.Name, phase)
			}
		}

		logger.Log("All (%d) pods currently in a running or completed state", len(podList.Items))
		return true, nil
	}
}
