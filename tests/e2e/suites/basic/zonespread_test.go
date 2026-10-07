package basic

import (
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	corev1 "k8s.io/api/core/v1"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// zoneTopologyKey is the topology key of the zone spread constraint the chart
	// sets on the proxy pods of an AWS NLB gateway.
	zoneTopologyKey = "topology.kubernetes.io/zone"
	// podTemplateHashLabel identifies the ReplicaSet a proxy pod belongs to.
	podTemplateHashLabel = "pod-template-hash"
	// proxyNamespace is where Envoy Gateway runs the proxy pods.
	proxyNamespace = "envoy-gateway-system"
	// readinessGateInjectLabel makes the AWS Load Balancer Controller inject its pod
	// readiness gate into the pods of the namespace that carries it.
	readinessGateInjectLabel = "elbv2.k8s.aws/pod-readiness-gate-inject"
)

// gatewayZoneSpreadTests validates that the proxy pods of the default gateway carry
// the chart's zone spread constraint, and that they run in at least two availability
// zones whenever the scheduler was bound to put them there. It only reads cluster
// state.
func gatewayZoneSpreadTests() {
	wcClient, _ := state.GetFramework().WC(state.GetCluster().Name)

	By("checking envoy proxy pods carry the zone spread constraint")
	Eventually(func() error {
		proxyPods, err := gatewayProxyPods(wcClient)
		if err != nil {
			return err
		}
		for _, pod := range proxyPods.Items {
			if zoneSpreadConstraint(pod) == nil {
				return fmt.Errorf("pod %s/%s has no DoNotSchedule topologySpreadConstraint on %s", pod.Namespace, pod.Name, zoneTopologyKey)
			}
		}
		return nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())

	// Wait for a settled state: every proxy pod Ready and from one ReplicaSet. A pod
	// the constraint cannot place stays Pending, so this also fails if the spread
	// is unsatisfiable on this cluster.
	By("checking envoy proxy pods are all ready and from one ReplicaSet")
	var proxyPods *corev1.PodList
	Eventually(func() error {
		pods, err := gatewayProxyPods(wcClient)
		if err != nil {
			return err
		}
		hashes := map[string]bool{}
		for _, pod := range pods.Items {
			if pod.DeletionTimestamp != nil {
				return fmt.Errorf("pod %s/%s is terminating", pod.Namespace, pod.Name)
			}
			if !isPodReady(pod) {
				return fmt.Errorf("pod %s/%s is not ready", pod.Namespace, pod.Name)
			}
			hashes[pod.Labels[podTemplateHashLabel]] = true
		}
		if len(hashes) != 1 {
			return fmt.Errorf("envoy proxy pods belong to %d ReplicaSets, waiting for the rollout to finish", len(hashes))
		}
		proxyPods = pods
		return nil
	}).
		WithTimeout(10 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())

	By("checking envoy proxy pods run in at least two availability zones")
	nodes := &corev1.NodeList{}
	Expect(wcClient.List(state.GetContext(), nodes)).To(Succeed())
	nodeZones := map[string]string{}
	for _, node := range nodes.Items {
		nodeZones[node.Name] = node.Labels[zoneTopologyKey]
	}

	podZones := map[string]string{}
	zoneCounts := map[string]int{}
	oldest := proxyPods.Items[0].CreationTimestamp
	for _, pod := range proxyPods.Items {
		zone := nodeZones[pod.Spec.NodeName]
		podZones[pod.Name] = fmt.Sprintf("%s (node %s)", zone, pod.Spec.NodeName)
		zoneCounts[zone]++
		if pod.CreationTimestamp.Before(&oldest) {
			oldest = pod.CreationTimestamp
		}
	}
	logger.Log("Envoy proxy pods by zone: %v", podZones)

	// The scheduler spreads over the zones of the nodes it knows about, and nothing
	// moves a pod afterwards. With the chart's nodeTaintsPolicy Honor, the default
	// nodeAffinityPolicy (Honor), and proxy pods without node affinity, it counts the
	// zone of every node with a zone label whose NoSchedule and NoExecute taints the
	// pod tolerates, so tainted control plane nodes are left out. Only the nodes that
	// existed before the oldest pod of the current ReplicaSet were known for every
	// placement. If those eligible nodes span two zones, a
	// second pod in the same zone as the first would have broken maxSkew 1 and
	// stayed Pending, so two Ready pods must be in two zones. Otherwise the outcome
	// depends on node timing rather than on the chart, and is only reported.
	if reason := zoneAssertionSkipReason(proxyPods, nodes, oldest.Time); reason != "" {
		entry := fmt.Sprintf("zone assertion skipped: %s; proxy pods by zone: %v", reason, podZones)
		logger.Log("Envoy proxy %s", entry)
		AddReportEntry("envoy proxy zone spread", entry)
		return
	}
	Expect(len(zoneCounts)).To(BeNumerically(">=", 2),
		"envoy proxy pods run in zones %v, expected at least 2: %v", zoneCounts, podZones)
}

// zoneAssertionSkipReason returns why the zone assertion cannot be relied on, or an
// empty string when the scheduler was bound to place the pods in two or more zones.
func zoneAssertionSkipReason(proxyPods *corev1.PodList, nodes *corev1.NodeList, oldestPod time.Time) string {
	if len(proxyPods.Items) < 2 {
		return fmt.Sprintf("only %d proxy pod(s)", len(proxyPods.Items))
	}
	for _, pod := range proxyPods.Items {
		c := zoneSpreadConstraint(pod)
		if c.NodeAffinityPolicy != nil && *c.NodeAffinityPolicy == corev1.NodeInclusionPolicyIgnore {
			continue
		}
		if len(pod.Spec.NodeSelector) > 0 || (pod.Spec.Affinity != nil && pod.Spec.Affinity.NodeAffinity != nil &&
			pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil) {
			return fmt.Sprintf("pod %s has a node selector or required node affinity, so the eligible zones depend on it", pod.Name)
		}
	}

	// All pods come from one ReplicaSet, so they share the constraint and tolerations.
	pod := proxyPods.Items[0]
	c := zoneSpreadConstraint(pod)
	honorTaints := c.NodeTaintsPolicy != nil && *c.NodeTaintsPolicy == corev1.NodeInclusionPolicyHonor

	knownZones := map[string]bool{}
	for _, node := range nodes.Items {
		zone := node.Labels[zoneTopologyKey]
		if zone == "" || !node.CreationTimestamp.Time.Before(oldestPod) {
			continue
		}
		if honorTaints && !toleratesNodeTaints(pod, node) {
			continue
		}
		knownZones[zone] = true
	}
	if len(knownZones) < 2 {
		zones := make([]string, 0, len(knownZones))
		for zone := range knownZones {
			zones = append(zones, zone)
		}
		sort.Strings(zones)
		return fmt.Sprintf("the eligible nodes created before the oldest proxy pod (%s) span %d zone(s) %v, so the scheduler may have placed every pod in one zone",
			oldestPod.Format(time.RFC3339), len(zones), zones)
	}
	return ""
}

// gatewayReadinessGateLabelTests validates that the chart's Kyverno policy labelled
// the proxy namespace, so the AWS Load Balancer Controller injects its readiness gate
// into the proxy pods when the NLB uses IP targets.
func gatewayReadinessGateLabelTests() {
	wcClient, _ := state.GetFramework().WC(state.GetCluster().Name)

	By(fmt.Sprintf("checking namespace %s has the %s label", proxyNamespace, readinessGateInjectLabel))
	Eventually(func() error {
		ns := &corev1.Namespace{}
		if err := wcClient.Get(state.GetContext(), cr.ObjectKey{Name: proxyNamespace}, ns); err != nil {
			return err
		}
		if got := ns.Labels[readinessGateInjectLabel]; got != "enabled" {
			return fmt.Errorf("namespace %s has label %s=%q, expected \"enabled\"", proxyNamespace, readinessGateInjectLabel, got)
		}
		return nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())
}

// zoneSpreadConstraint returns the pod's DoNotSchedule spread constraint on the
// zone topology key, or nil when it has none.
func zoneSpreadConstraint(pod corev1.Pod) *corev1.TopologySpreadConstraint {
	for i, c := range pod.Spec.TopologySpreadConstraints {
		if c.TopologyKey == zoneTopologyKey && c.WhenUnsatisfiable == corev1.DoNotSchedule {
			return &pod.Spec.TopologySpreadConstraints[i]
		}
	}
	return nil
}

// toleratesNodeTaints reports whether the pod tolerates every NoSchedule and
// NoExecute taint of the node, the taints nodeTaintsPolicy Honor considers.
func toleratesNodeTaints(pod corev1.Pod, node corev1.Node) bool {
	for i := range node.Spec.Taints {
		taint := &node.Spec.Taints[i]
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		tolerated := false
		for j := range pod.Spec.Tolerations {
			if pod.Spec.Tolerations[j].ToleratesTaint(logr.Discard(), taint, true) {
				tolerated = true
				break
			}
		}
		if !tolerated {
			return false
		}
	}
	return true
}

func isPodReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
