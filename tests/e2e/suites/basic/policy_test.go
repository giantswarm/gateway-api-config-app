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

func gatewayClassPolicyTests() {
	By("checking ClusterPolicy generate-gateway-monitoring-giantswarm-default exists")
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
}
