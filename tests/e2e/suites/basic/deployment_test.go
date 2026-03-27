package basic

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v3/pkg/state"
	"github.com/giantswarm/clustertest/v3/pkg/logger"
	"github.com/giantswarm/clustertest/v3/pkg/wait"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
)

func deploymentAppTests() {
	By("checking bundle application is created")
	Expect(state.GetBundleApplication()).ToNot(BeNil())
	Expect(state.GetBundleApplication().AppName).ToNot(Equal(state.GetApplication().AppName))

	By("checking the bundle app is deployed")
	Eventually(wait.IsAppDeployed(state.GetContext(), state.GetFramework().MC(), state.GetBundleApplication().InstallName, state.GetBundleApplication().InstallNamespace)).
		WithTimeout(30 * time.Second).
		WithPolling(50 * time.Millisecond).
		Should(BeTrue())

	By("checking the test app is deployed")
	Eventually(func() (bool, error) {
		done, err := wait.IsAppDeployed(state.GetContext(), state.GetFramework().MC(), state.GetApplication().InstallName, state.GetApplication().Organization.GetNamespace())()
		if err != nil {
			if errors.IsNotFound(err) {
				logger.Log("App '%s/%s' doesn't exist yet", state.GetApplication().Organization.GetNamespace(), state.GetApplication().InstallName)
				return false, nil
			}
			return false, err
		}
		return done, nil
	}).
		WithTimeout(15 * time.Minute).
		WithPolling(5 * time.Second).
		Should(BeTrue())

	By("checking the test app is deployed at the correct version")
	Eventually(wait.IsAppVersion(state.GetContext(), state.GetFramework().MC(), state.GetApplication().InstallName, state.GetApplication().Organization.GetNamespace(), state.GetApplication().Version)).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(BeTrue())
}

func gatewayDeploymentTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking envoy proxy pods are running and ready")
	proxyPodsListOptions := &cr.ListOptions{
		Namespace: "envoy-gateway-system",
		LabelSelector: labels.SelectorFromSet(map[string]string{
			"app.kubernetes.io/component": "proxy",
			"app.kubernetes.io/name":      "envoy",
		}),
	}
	Eventually(arePodsRunning(state.GetContext(), wcClient, proxyPodsListOptions)).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(BeTrue())

	Eventually(func() error {
		proxyPods := &corev1.PodList{}
		if err := wcClient.List(state.GetContext(), proxyPods, proxyPodsListOptions); err != nil {
			return err
		}
		if len(proxyPods.Items) == 0 {
			return fmt.Errorf("no proxy pods found")
		}
		for _, pod := range proxyPods.Items {
			for _, cs := range pod.Status.ContainerStatuses {
				if !cs.Ready {
					return fmt.Errorf("pod %s/%s container %s is not ready", pod.Namespace, pod.Name, cs.Name)
				}
			}
		}
		return nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking envoy proxy pods use the gsoci image")
	Eventually(func() (bool, error) {
		proxyPods := &corev1.PodList{}
		err := wcClient.List(state.GetContext(), proxyPods, &cr.ListOptions{
			Namespace: "envoy-gateway-system",
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"app.kubernetes.io/component": "proxy",
				"app.kubernetes.io/name":      "envoy",
			}),
		})
		if err != nil {
			return false, err
		}
		for _, pod := range proxyPods.Items {
			for _, container := range pod.Spec.Containers {
				if !strings.HasPrefix(container.Image, "gsoci.azurecr.io/giantswarm/envoy") {
					return false, fmt.Errorf("pod %s/%s is using image %s", pod.Namespace, pod.Name, container.Image)
				}
			}
		}
		return true, nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(BeTrue())

	By("checking service has external-dns annotations for the default gateway")
	Eventually(func() error {
		svcList := &corev1.ServiceList{}
		err := wcClient.List(state.GetContext(), svcList, &cr.ListOptions{
			Namespace: "envoy-gateway-system",
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"gateway.envoyproxy.io/owning-gateway-name":      "giantswarm-default",
				"gateway.envoyproxy.io/owning-gateway-namespace": "envoy-gateway-system",
			}),
		})
		if err != nil {
			return err
		}
		if len(svcList.Items) == 0 {
			return fmt.Errorf("no services found for gateway giantswarm-default")
		}
		svc := svcList.Items[0]
		annotations := svc.Annotations
		if annotations["giantswarm.io/external-dns"] != "managed" {
			return fmt.Errorf("expected annotation giantswarm.io/external-dns=managed, got %q", annotations["giantswarm.io/external-dns"])
		}
		hostname, ok := annotations["external-dns.alpha.kubernetes.io/hostname"]
		if !ok || hostname == "" {
			return fmt.Errorf("expected annotation external-dns.alpha.kubernetes.io/hostname to be set")
		}
		if !strings.HasPrefix(hostname, "ingress.") {
			return fmt.Errorf("expected hostname annotation to start with 'ingress.', got %q", hostname)
		}
		return nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())
}

func gatewayHPAAndPDBTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	proxyLabelSelector := labels.SelectorFromSet(map[string]string{
		"gateway.envoyproxy.io/owning-gateway-name":      "giantswarm-default",
		"gateway.envoyproxy.io/owning-gateway-namespace": "envoy-gateway-system",
	})

	By("checking HorizontalPodAutoscaler exists for the giantswarm-default proxy")
	Eventually(func() error {
		hpaList := &autoscalingv2.HorizontalPodAutoscalerList{}
		if err := wcClient.List(state.GetContext(), hpaList, &cr.ListOptions{
			Namespace:     "envoy-gateway-system",
			LabelSelector: proxyLabelSelector,
		}); err != nil {
			return err
		}
		if len(hpaList.Items) == 0 {
			return fmt.Errorf("no HorizontalPodAutoscaler found for gateway giantswarm-default")
		}
		return nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking PodDisruptionBudget exists for the giantswarm-default proxy")
	Eventually(func() error {
		pdbList := &policyv1.PodDisruptionBudgetList{}
		if err := wcClient.List(state.GetContext(), pdbList, &cr.ListOptions{
			Namespace:     "envoy-gateway-system",
			LabelSelector: proxyLabelSelector,
		}); err != nil {
			return err
		}
		if len(pdbList.Items) == 0 {
			return fmt.Errorf("no PodDisruptionBudget found for gateway giantswarm-default")
		}
		return nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())
}
