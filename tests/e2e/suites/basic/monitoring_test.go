package basic

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v3/pkg/state"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
)

func gatewayMonitoringTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking PodMonitor giantswarm-default is generated in envoy-gateway-system")
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

	By("checking PodLogs giantswarm-default is generated in envoy-gateway-system")
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
}
