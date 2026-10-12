package ipmode

import (
	"fmt"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/client"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	corev1 "k8s.io/api/core/v1"
)

const zoneTopologyKey = "topology.kubernetes.io/zone"

// zoneSpreadTests checks that the proxy pods, all scheduled by readinessGatePodTests
// with every node already in place, run in as many zones as the replica count and
// the schedulable zones allow.
//
// The constraint is ScheduleAnyway, so the scheduler only prefers a spread. With
// spare capacity in every zone it spreads in practice; the cluster values give each
// zone a node with room for a proxy pod. A failure here on such a cluster means the
// constraint does not work as intended, which is what the issue asked to verify.
func zoneSpreadTests() {
	c := wcClient()

	By("finding the zones that have schedulable worker nodes")
	zones := schedulableZones(c)
	logger.Log("Schedulable zones: %v", zones)
	if len(zones) < 2 {
		Skip(fmt.Sprintf("the cluster has schedulable worker nodes in %d zone(s), a spread needs at least 2", len(zones)))
	}

	By("checking the proxy pods are spread across zones")
	nodeZones := map[string]string{}
	nodes := &corev1.NodeList{}
	Expect(c.List(state.GetContext(), nodes)).To(Succeed())
	for _, node := range nodes.Items {
		nodeZones[node.Name] = node.Labels[zoneTopologyKey]
	}

	pods, err := proxyPods(c)
	Expect(err).NotTo(HaveOccurred())
	podZones := map[string]string{}
	distinct := map[string]bool{}
	for _, pod := range pods {
		zone := nodeZones[pod.Spec.NodeName]
		podZones[pod.Name] = zone
		distinct[zone] = true
	}
	entry := fmt.Sprintf("proxy pods by zone: %v", podZones)
	logger.Log("%s", entry)
	AddReportEntry("envoy proxy zone spread", entry)

	want := min(len(pods), len(zones))
	Expect(len(distinct)).To(BeNumerically(">=", want),
		"%d proxy pods run in %d zone(s), want at least %d of the schedulable zones %v: %v",
		len(pods), len(distinct), want, zones, podZones)
}

// schedulableZones returns the zones of the Ready worker nodes that have no
// NoSchedule or NoExecute taint, the nodes nodeTaintsPolicy: Honor counts.
func schedulableZones(c *client.Client) []string {
	nodes := &corev1.NodeList{}
	Expect(c.List(state.GetContext(), nodes, client.DoesNotHaveLabels{"node-role.kubernetes.io/control-plane"})).To(Succeed())

	set := map[string]bool{}
	for _, node := range nodes.Items {
		if !isNodeReady(node) || hasSchedulingTaint(node) {
			continue
		}
		if zone := node.Labels[zoneTopologyKey]; zone != "" {
			set[zone] = true
		}
	}
	zones := []string{}
	for zone := range set {
		zones = append(zones, zone)
	}
	sort.Strings(zones)
	return zones
}

func isNodeReady(node corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func hasSchedulingTaint(node corev1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
			return true
		}
	}
	return false
}
