package basic

import (
	"fmt"
	"net"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	cr "sigs.k8s.io/controller-runtime/pkg/client"

	"e2e/internal/common"
)

const (
	stateMetricsName = "gateway-api-config-state-metrics"
	// stateMetricsRouteName names an HTTPRoute that only exists to be exported. It has no
	// parentRefs, so it attaches to no gateway and cannot change what the other specs see.
	stateMetricsRouteName = "state-metrics-fixture"
	stateMetricsProbeName = "state-metrics-probe"
	stateMetricsMetric    = "gatewayapi_httproute_rule_backend_info"
)

// gatewayStateMetricsTests checks the kube-state-metrics instance of the chart: it runs, it is
// scraped by a PodMonitor the observability platform picks up, and it exports the backends of an
// HTTPRoute. The metrics are fetched from another pod, so the request passes the instance's
// CiliumNetworkPolicy the same way a scrape does.
func gatewayStateMetricsTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)
	ctx := state.GetContext()

	By("checking Deployment " + stateMetricsName + " is ready")
	deployment := &appsv1.Deployment{}
	Eventually(func() error {
		if err := wcClient.Get(ctx, cr.ObjectKey{Name: stateMetricsName, Namespace: common.ProxyNamespace}, deployment); err != nil {
			return err
		}
		if deployment.Status.ReadyReplicas < 1 {
			return fmt.Errorf("deployment %s has %d ready replicas", stateMetricsName, deployment.Status.ReadyReplicas)
		}
		return nil
	}).
		WithTimeout(10 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking PodMonitor " + stateMetricsName + " carries the team label that routes its metrics")
	podMonitor := &unstructured.Unstructured{}
	podMonitor.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "PodMonitor",
	})
	Expect(wcClient.Get(ctx, cr.ObjectKey{Name: stateMetricsName, Namespace: common.ProxyNamespace}, podMonitor)).To(Succeed())
	Expect(podMonitor.GetLabels()).To(HaveKeyWithValue("application.giantswarm.io/team", "cabbage"))
	selector, _, err := unstructured.NestedStringMap(podMonitor.Object, "spec", "selector", "matchLabels")
	Expect(err).NotTo(HaveOccurred())
	Expect(selector).To(Equal(stateMetricsSelector()))
	endpoints, _, err := unstructured.NestedSlice(podMonitor.Object, "spec", "podMetricsEndpoints")
	Expect(err).NotTo(HaveOccurred())
	Expect(endpoints).To(HaveLen(1))
	Expect(endpoints[0]).To(HaveKeyWithValue("port", "http"))

	By("creating HTTPRoute " + stateMetricsRouteName + " with a weighted split over two backends")
	DeferCleanup(func() {
		if err := wcClient.Delete(ctx, stateMetricsRoute()); err != nil && !errors.IsNotFound(err) {
			logger.Log("could not delete HTTPRoute %s: %v", stateMetricsRouteName, err)
		}
	})
	Expect(wcClient.CreateOrUpdate(ctx, stateMetricsRoute())).To(Succeed())

	By("starting the probe pod " + stateMetricsProbeName)
	DeferCleanup(func() {
		grace := int64(0)
		if err := wcClient.Delete(ctx, stateMetricsProbePod(), &cr.DeleteOptions{GracePeriodSeconds: &grace}); err != nil && !errors.IsNotFound(err) {
			logger.Log("could not delete pod %s: %v", stateMetricsProbeName, err)
		}
	})
	// Create, not CreateOrUpdate: the spec of a Pod is immutable, and a probe left over from an
	// earlier attempt serves just as well.
	if err := wcClient.Create(ctx, stateMetricsProbePod()); err != nil && !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
	probe := &corev1.Pod{}
	Eventually(func() error {
		if err := wcClient.Get(ctx, cr.ObjectKey{Name: stateMetricsProbeName, Namespace: common.ProxyNamespace}, probe); err != nil {
			return err
		}
		if probe.Status.Phase != corev1.PodRunning {
			return fmt.Errorf("probe pod in %s phase", probe.Status.Phase)
		}
		return nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("fetching " + stateMetricsMetric + " for the HTTPRoute from the probe pod")
	// Each label of the rule's series. kube-state-metrics sorts labels, so they are matched one by
	// one instead of as a fixed string.
	expected := []string{
		`name="` + stateMetricsRouteName + `"`,
		`namespace="` + common.ProxyNamespace + `"`,
		`match_path="/state-metrics"`,
		`match_path_type="PathPrefix"`,
		`backend0_name="app-v1"`,
		`backend0_port="80"`,
		`backend0_weight="90"`,
		`backend1_name="app-v2"`,
		`backend1_namespace="other"`,
		`backend1_weight="10"`,
	}
	Eventually(func() error {
		podIP, err := readyStateMetricsPodIP()
		if err != nil {
			return err
		}
		url := "http://" + net.JoinHostPort(podIP, "8080") + "/metrics"
		stdout, stderr, err := wcClient.ExecInPod(ctx, stateMetricsProbeName, common.ProxyNamespace, stateMetricsProbeName,
			[]string{"wget", "-q", "-T", "10", "-O", "-", url})
		if err != nil {
			return fmt.Errorf("fetching %s: %v (stderr: %s)", url, err, strings.TrimSpace(stderr))
		}
		for _, line := range strings.Split(stdout, "\n") {
			if !strings.HasPrefix(line, stateMetricsMetric+"{") || !strings.Contains(line, expected[0]) {
				continue
			}
			for _, label := range expected[1:] {
				if !strings.Contains(line, label) {
					return fmt.Errorf("series for HTTPRoute %s lacks %s: %s", stateMetricsRouteName, label, line)
				}
			}
			return nil
		}
		return fmt.Errorf("no %s series for HTTPRoute %s yet", stateMetricsMetric, stateMetricsRouteName)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())
}

// stateMetricsSelector returns the selector labels of the kube-state-metrics pods.
func stateMetricsSelector() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":      stateMetricsName,
		"app.kubernetes.io/component": "state-metrics",
	}
}

// readyStateMetricsPodIP returns the IP of a running, ready kube-state-metrics pod, skipping
// pods that are terminating or left over from an earlier rollout.
func readyStateMetricsPodIP() (string, error) {
	wcClient, _ := state.GetFramework().WC(state.GetCluster().Name)
	pods := &corev1.PodList{}
	if err := wcClient.List(state.GetContext(), pods,
		cr.InNamespace(common.ProxyNamespace),
		cr.MatchingLabels(stateMetricsSelector())); err != nil {
		return "", err
	}
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
			continue
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				return pod.Status.PodIP, nil
			}
		}
	}
	return "", fmt.Errorf("no ready pod of %s yet", stateMetricsName)
}

func stateMetricsRoute() *unstructured.Unstructured {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1",
		Kind:    "HTTPRoute",
	})
	route.SetName(stateMetricsRouteName)
	route.SetNamespace(common.ProxyNamespace)
	route.Object["spec"] = map[string]any{
		"rules": []any{map[string]any{
			"matches": []any{map[string]any{
				"path": map[string]any{
					"type":  "PathPrefix",
					"value": "/state-metrics",
				},
			}},
			"backendRefs": []any{
				map[string]any{
					"name":   "app-v1",
					"port":   int64(80),
					"weight": int64(90),
				},
				map[string]any{
					"name":      "app-v2",
					"namespace": "other",
					"port":      int64(80),
					"weight":    int64(10),
				},
			},
		}},
	}
	return route
}

func stateMetricsProbePod() *corev1.Pod {
	user := int64(65534)
	runAsNonRoot := true
	allowPrivilegeEscalation := false

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      stateMetricsProbeName,
			Namespace: common.ProxyNamespace,
		},
		Spec: corev1.PodSpec{
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   &runAsNonRoot,
				RunAsUser:      &user,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:    stateMetricsProbeName,
				Image:   clientIPProbeImage,
				Command: []string{"sleep", "3600"},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &allowPrivilegeEscalation,
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
		},
	}
}
