package ipmode

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/client"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	cr "sigs.k8s.io/controller-runtime/pkg/client"

	"e2e/internal/common"
)

const (
	// readinessGateLabel on a namespace makes the AWS Load Balancer Controller webhook
	// inject a target health readiness gate into pods created in it.
	readinessGateLabel = "elbv2.k8s.aws/pod-readiness-gate-inject"
	// readinessGatePrefix is followed by the TargetGroupBinding name in the condition
	// type of the injected readiness gate.
	readinessGatePrefix = "target-health.elbv2.k8s.aws/"
	// readinessGatePolicy is the Kyverno ClusterPolicy the chart renders in IP mode.
	readinessGatePolicy = "gateway-api-pod-readiness-gate-inject"
)

// tgbName is the TargetGroupBinding of the gateway Service, found by
// readinessGateWiringTests and used by readinessGatePodTests.
var tgbName string

// readinessGateWiringTests checks everything the AWS Load Balancer Controller needs
// before it injects a readiness gate: the chart's Kyverno policy is Ready, it
// labelled the proxy namespace, and the controller created an IP-mode
// TargetGroupBinding for the gateway Service.
func readinessGateWiringTests() {
	c := common.WC()

	By("checking the readiness gate ClusterPolicy is Ready")
	Eventually(func() error {
		policy := &unstructured.Unstructured{}
		policy.SetGroupVersionKind(schema.GroupVersionKind{Group: "kyverno.io", Version: "v1", Kind: "ClusterPolicy"})
		if err := c.Get(state.GetContext(), cr.ObjectKey{Name: readinessGatePolicy}, policy); err != nil {
			return err
		}
		conditions, _, _ := unstructured.NestedSlice(policy.Object, "status", "conditions")
		for _, raw := range conditions {
			condition, _ := raw.(map[string]any)
			if condition["type"] == "Ready" && condition["status"] == "True" {
				return nil
			}
		}
		return fmt.Errorf("ClusterPolicy %s is not Ready yet", readinessGatePolicy)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking the proxy namespace carries the readiness gate label")
	Eventually(func() (string, error) {
		ns := &corev1.Namespace{}
		if err := c.Get(state.GetContext(), cr.ObjectKey{Name: common.ProxyNamespace}, ns); err != nil {
			return "", err
		}
		return ns.Labels[readinessGateLabel], nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Equal("enabled"))

	By("checking the AWS Load Balancer Controller created an IP-mode TargetGroupBinding for the gateway Service")
	Eventually(func() error {
		name, err := gatewayTargetGroupBinding(c)
		if err != nil {
			return err
		}
		tgbName = name
		return nil
	}).
		WithTimeout(10 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())
	logger.Log("Gateway Service uses TargetGroupBinding %s", tgbName)
}

// readinessGatePodTests replaces each proxy pod and checks that its replacement has
// the target health readiness gate and only turns Ready once the NLB reports its
// target healthy.
//
// The pods are replaced because the webhook injects the gate only at pod creation,
// and only once the namespace label and the TargetGroupBinding exist. Pods created
// before that, for example on a fresh install or on the upgrade that brings the
// policy, run without the gate until they are next replaced. The suite reports how
// many such pods it found instead of failing on them.
//
// The pods are deleted one at a time rather than with a rollout restart: Envoy
// Gateway owns the proxy Deployment and may revert a pod template change, and the
// ReplicaSet recreates a deleted pod with the same template hash, so the zone spread
// counts the remaining pods when it places the new one.
func readinessGatePodTests() {
	c := common.WC()
	gate := corev1.PodConditionType(readinessGatePrefix + tgbName)

	pods, err := proxyPods(c)
	Expect(err).NotTo(HaveOccurred())
	ungated := 0
	for _, pod := range pods {
		if !hasReadinessGate(pod, gate) {
			ungated++
		}
	}
	entry := fmt.Sprintf("%d of %d proxy pods created before the readiness gate wiring was in place have no %s gate", ungated, len(pods), gate)
	logger.Log("%s", entry)
	AddReportEntry("proxy pods without readiness gate before replacement", entry)

	for _, old := range pods {
		By(fmt.Sprintf("replacing proxy pod %s", old.Name))
		Expect(c.Delete(state.GetContext(), &old)).To(Succeed())

		// The new pod is not Ready while its target registers, so waiting for all pods
		// to be Ready also waits out the NLB health checks.
		waitForReadyProxyPods(c, 10*time.Minute)
	}

	By("checking every proxy pod has the target health readiness gate and it is True")
	pods, err = proxyPods(c)
	Expect(err).NotTo(HaveOccurred())
	Expect(pods).To(HaveLen(expectedReplicas))
	for _, pod := range pods {
		Expect(hasReadinessGate(pod, gate)).To(BeTrue(),
			"pod %s has no readiness gate %s, readinessGates: %v", pod.Name, gate, pod.Spec.ReadinessGates)
		Expect(podConditionTrue(pod, gate)).To(BeTrue(),
			"pod %s readiness gate %s is not True", pod.Name, gate)
		Expect(isPodReady(pod)).To(BeTrue(), "pod %s is not Ready", pod.Name)
	}
}

func hasReadinessGate(pod corev1.Pod, gate corev1.PodConditionType) bool {
	for _, g := range pod.Spec.ReadinessGates {
		if g.ConditionType == gate {
			return true
		}
	}
	return false
}

// gatewayTargetGroupBinding returns the name of the IP-mode TargetGroupBinding the
// AWS Load Balancer Controller created for the gateway's LoadBalancer Service.
func gatewayTargetGroupBinding(c *client.Client) (string, error) {
	services := &corev1.ServiceList{}
	if err := c.List(state.GetContext(), services, &cr.ListOptions{Namespace: common.ProxyNamespace, LabelSelector: common.DefaultGatewayProxySelector}); err != nil {
		return "", err
	}
	serviceName := ""
	for _, svc := range services.Items {
		if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
			serviceName = svc.Name
		}
	}
	if serviceName == "" {
		return "", fmt.Errorf("no LoadBalancer Service for gateway %s found", common.DefaultGatewayName)
	}

	tgbs := &unstructured.UnstructuredList{}
	tgbs.SetGroupVersionKind(schema.GroupVersionKind{Group: "elbv2.k8s.aws", Version: "v1beta1", Kind: "TargetGroupBindingList"})
	if err := c.List(state.GetContext(), tgbs, cr.InNamespace(common.ProxyNamespace)); err != nil {
		return "", err
	}
	for _, tgb := range tgbs.Items {
		ref, _, _ := unstructured.NestedString(tgb.Object, "spec", "serviceRef", "name")
		if ref != serviceName {
			continue
		}
		targetType, _, _ := unstructured.NestedString(tgb.Object, "spec", "targetType")
		if !strings.EqualFold(targetType, "ip") {
			return "", fmt.Errorf("TargetGroupBinding %s for Service %s has targetType %q, want ip", tgb.GetName(), serviceName, targetType)
		}
		return tgb.GetName(), nil
	}
	return "", fmt.Errorf("no TargetGroupBinding for Service %s found", serviceName)
}
