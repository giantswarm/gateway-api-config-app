package basic

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v3/pkg/state"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
)

func gatewayClassResourceTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking GatewayClass giantswarm-default exists")
	gatewayClass := &unstructured.Unstructured{}
	gatewayClass.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1",
		Kind:    "GatewayClass",
	})
	Eventually(func() error {
		return wcClient.Get(state.GetContext(), cr.ObjectKey{Name: "giantswarm-default"}, gatewayClass)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking GatewayClass controllerName")
	Expect(gatewayClass.Object["spec"]).NotTo(BeNil())
	spec := gatewayClass.Object["spec"].(map[string]any)
	Expect(spec["controllerName"]).To(Equal("gateway.envoyproxy.io/gatewayclass-controller"))

	By("checking GatewayClass parametersRef points to EnvoyProxy gatewayclass-giantswarm-default")
	parametersRef := spec["parametersRef"].(map[string]any)
	Expect(parametersRef["name"]).To(Equal("gatewayclass-giantswarm-default"))
	Expect(parametersRef["kind"]).To(Equal("EnvoyProxy"))
}

func gatewayClassEnvoyProxyTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking EnvoyProxy gatewayclass-giantswarm-default exists in envoy-gateway-system")
	envoyProxy := &unstructured.Unstructured{}
	envoyProxy.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.envoyproxy.io",
		Version: "v1alpha1",
		Kind:    "EnvoyProxy",
	})
	Eventually(func() error {
		return wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "gatewayclass-giantswarm-default",
			Namespace: "envoy-gateway-system",
		}, envoyProxy)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking EnvoyProxy imageRepository starts with gsoci.azurecr.io/giantswarm/envoy")
	envoySpec := envoyProxy.Object["spec"].(map[string]any)
	provider := envoySpec["provider"].(map[string]any)
	kubernetes := provider["kubernetes"].(map[string]any)
	envoyDeployment := kubernetes["envoyDeployment"].(map[string]any)
	container := envoyDeployment["container"].(map[string]any)
	imageRepo := container["imageRepository"].(string)
	Expect(strings.HasPrefix(imageRepo, "gsoci.azurecr.io/giantswarm/envoy")).To(BeTrue(),
		"expected imageRepository to start with gsoci.azurecr.io/giantswarm/envoy, got: %s", imageRepo)

	By("checking EnvoyProxy HPA minReplicas=2, maxReplicas=10")
	hpa := kubernetes["envoyHpa"].(map[string]any)
	Expect(hpa["minReplicas"]).To(BeEquivalentTo(2))
	Expect(hpa["maxReplicas"]).To(BeEquivalentTo(10))

	By("checking EnvoyProxy PDB maxUnavailable=25%")
	pdb := kubernetes["envoyPDB"].(map[string]any)
	Expect(pdb["maxUnavailable"]).To(Equal("25%"))
}

func gatewayClassKyvernoRBACTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking ClusterRole kyverno:gateway-api:allow-monitoring-viewing exists")
	viewingRole := &rbacv1.ClusterRole{}
	Eventually(func() error {
		return wcClient.Get(state.GetContext(), cr.ObjectKey{Name: "kyverno:gateway-api:allow-monitoring-viewing"}, viewingRole)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	By("checking ClusterRole kyverno:gateway-api:allow-monitoring-creation exists")
	creationRole := &rbacv1.ClusterRole{}
	Eventually(func() error {
		return wcClient.Get(state.GetContext(), cr.ObjectKey{Name: "kyverno:gateway-api:allow-monitoring-creation"}, creationRole)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())
}
