package basic

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"

	"github.com/giantswarm/apptest-framework/v3/pkg/suite"
	"github.com/giantswarm/clustertest/v3/pkg/client"
	"github.com/giantswarm/clustertest/v3/pkg/logger"
	"github.com/giantswarm/clustertest/v3/pkg/wait"

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
				gatewayIssuerTests()
				gatewayCertificateTests()
				gatewayHTTPRouteTests()
			})
			It("should have the gateway correctly deployed", func() {
				gatewayDeploymentTests()
				gatewayHPAAndPDBTests()
				gatewayMonitoringTests()
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
