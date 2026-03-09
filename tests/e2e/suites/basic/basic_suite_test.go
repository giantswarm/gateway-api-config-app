package basic

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v3/pkg/state"
	"github.com/giantswarm/apptest-framework/v3/pkg/suite"
	"github.com/giantswarm/clustertest/v3/pkg/client"
	"github.com/giantswarm/clustertest/v3/pkg/logger"
	"github.com/giantswarm/clustertest/v3/pkg/wait"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
		Tests(func() {
			It("should have created a bundle application", func() {
				Expect(state.GetBundleApplication()).ToNot(BeNil())
				Expect(state.GetBundleApplication().AppName).ToNot(Equal(state.GetApplication().AppName))
			})

			It("should have deployed the bundle app", func() {
				Eventually(wait.IsAppDeployed(state.GetContext(), state.GetFramework().MC(), state.GetBundleApplication().InstallName, state.GetBundleApplication().InstallNamespace)).
					WithTimeout(30 * time.Second).
					WithPolling(50 * time.Millisecond).
					Should(BeTrue())
			})

			It("should have deployed the test app", func() {
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
			})

			It("should have deployed the test app with correct version", func() {
				Eventually(wait.IsAppVersion(state.GetContext(), state.GetFramework().MC(), state.GetApplication().InstallName, state.GetApplication().Organization.GetNamespace(), state.GetApplication().Version)).
					WithTimeout(5 * time.Minute).
					WithPolling(5 * time.Second).
					Should(BeTrue())
			})

			It("should have the envoy proxy pods running", func() {
				wcName := state.GetCluster().Name
				wcClient, _ := state.GetFramework().WC(wcName)
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
			})

			It("should have deployed the kyverno ClusterPolicy for the default gateway class", func() {
				wcName := state.GetCluster().Name
				wcClient, _ := state.GetFramework().WC(wcName)
				clusterPolicy := &unstructured.Unstructured{}
				clusterPolicy.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "kyverno.io",
					Version: "v1",
					Kind:    "ClusterPolicy",
				})
				Eventually(func() error {
					return wcClient.Get(state.GetContext(), cr.ObjectKey{Name: "generate-gateway-monitoring-giantswarm-default"}, clusterPolicy)
				}).
					WithTimeout(5 * time.Minute).
					WithPolling(5 * time.Second).
					Should(Succeed())
			})

			It("should have generated a PodMonitor for the default gateway", func() {
				wcName := state.GetCluster().Name
				wcClient, _ := state.GetFramework().WC(wcName)
				podMonitor := &unstructured.Unstructured{}
				podMonitor.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "monitoring.coreos.com",
					Version: "v1",
					Kind:    "PodMonitor",
				})
				Eventually(func() error {
					return wcClient.Get(state.GetContext(), cr.ObjectKey{
						Name:      "giantswarm-default",
						Namespace: "envoy-gateway-system",
					}, podMonitor)
				}).
					WithTimeout(5 * time.Minute).
					WithPolling(5 * time.Second).
					Should(Succeed())
			})

			It("should have generated a PodLogs for the default gateway", func() {
				wcName := state.GetCluster().Name
				wcClient, _ := state.GetFramework().WC(wcName)
				podLogs := &unstructured.Unstructured{}
				podLogs.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "monitoring.grafana.com",
					Version: "v1alpha2",
					Kind:    "PodLogs",
				})
				Eventually(func() error {
					return wcClient.Get(state.GetContext(), cr.ObjectKey{
						Name:      "giantswarm-default",
						Namespace: "envoy-gateway-system",
					}, podLogs)
				}).
					WithTimeout(5 * time.Minute).
					WithPolling(5 * time.Second).
					Should(Succeed())
			})

			It("should use the gsoci.azurecr.io/giantswarm/envoy image for the envoy proxy pods", func() {
				wcName := state.GetCluster().Name
				wcClient, _ := state.GetFramework().WC(wcName)
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
