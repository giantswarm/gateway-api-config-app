package basic

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	corev1 "k8s.io/api/core/v1"

	"e2e/internal/common"
)

// gatewayZoneSpreadTests validates that the proxy pods of the default gateway carry
// the chart's zone spread constraint, and reports the zone of each pod. The
// constraint is ScheduleAnyway, so the scheduler prefers a spread but does not
// guarantee one, and the zones are only logged. It only reads cluster state.
func gatewayZoneSpreadTests() {
	wcClient, _ := state.GetFramework().WC(state.GetCluster().Name)

	By("checking envoy proxy pods carry the zone spread constraint")
	var proxyPods *corev1.PodList
	Eventually(func(g Gomega) {
		pods, err := common.GatewayProxyPods(wcClient)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(pods.Items).NotTo(BeEmpty(), "no envoy proxy pods found")
		for _, pod := range pods.Items {
			c := zoneSpreadConstraint(pod)
			g.Expect(c).NotTo(BeNil(),
				"pod %s/%s has no topologySpreadConstraint on %s", pod.Namespace, pod.Name, common.ZoneTopologyKey)
			g.Expect(c.WhenUnsatisfiable).To(Equal(corev1.ScheduleAnyway),
				"pod %s/%s zone spread whenUnsatisfiable", pod.Namespace, pod.Name)
			g.Expect(c.NodeTaintsPolicy).NotTo(BeNil(),
				"pod %s/%s zone spread has no nodeTaintsPolicy", pod.Namespace, pod.Name)
			g.Expect(*c.NodeTaintsPolicy).To(Equal(corev1.NodeInclusionPolicyHonor),
				"pod %s/%s zone spread nodeTaintsPolicy", pod.Namespace, pod.Name)
		}
		proxyPods = pods
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())

	nodes := &corev1.NodeList{}
	Expect(wcClient.List(state.GetContext(), nodes)).To(Succeed())
	nodeZones := map[string]string{}
	for _, node := range nodes.Items {
		nodeZones[node.Name] = node.Labels[common.ZoneTopologyKey]
	}
	podZones := map[string]string{}
	for _, pod := range proxyPods.Items {
		podZones[pod.Name] = fmt.Sprintf("%s (node %s)", nodeZones[pod.Spec.NodeName], pod.Spec.NodeName)
	}
	entry := fmt.Sprintf("proxy pods by zone: %v", podZones)
	logger.Log("Envoy %s", entry)
	AddReportEntry("envoy proxy zone spread", entry)
}

// zoneSpreadConstraint returns the pod's spread constraint on the zone topology key,
// or nil when it has none.
func zoneSpreadConstraint(pod corev1.Pod) *corev1.TopologySpreadConstraint {
	for i, c := range pod.Spec.TopologySpreadConstraints {
		if c.TopologyKey == common.ZoneTopologyKey {
			return &pod.Spec.TopologySpreadConstraints[i]
		}
	}
	return nil
}
