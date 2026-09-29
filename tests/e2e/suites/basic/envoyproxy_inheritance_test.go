package basic

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// inheritedAnnotation is set on the GatewayClass EnvoyProxy in bundle_values.yaml. The chart
	// also computes it as a provider default for every CAPA+NLB gateway, with a different value.
	inheritedAnnotation      = "service.beta.kubernetes.io/aws-load-balancer-healthcheck-healthy-threshold"
	inheritedAnnotationValue = "3"
	// defaultOnlyAnnotation is a provider default the GatewayClass does not set, so the gateway
	// EnvoyProxy keeps rendering it.
	defaultOnlyAnnotation      = "service.beta.kubernetes.io/aws-load-balancer-proxy-protocol"
	defaultOnlyAnnotationValue = "*"
)

// gatewayEnvoyProxyInheritanceTests verifies the gateway EnvoyProxy leaves out a provider default
// the GatewayClass EnvoyProxy already sets, so the class value is inherited through StrategicMerge
// instead of overridden, while defaults the class does not set are still rendered.
func gatewayEnvoyProxyInheritanceTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	gatewayProxy := &unstructured.Unstructured{}
	gatewayProxy.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.envoyproxy.io",
		Version: "v1alpha1",
		Kind:    "EnvoyProxy",
	})
	Eventually(func() error {
		return wcClient.Get(state.GetContext(), cr.ObjectKey{
			Name:      "gateway-giantswarm-default",
			Namespace: "envoy-gateway-system",
		}, gatewayProxy)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	annotations, found, err := unstructured.NestedStringMap(gatewayProxy.Object,
		"spec", "provider", "kubernetes", "envoyService", "annotations")
	Expect(err).NotTo(HaveOccurred())
	Expect(found).To(BeTrue(), "expected gateway EnvoyProxy to render envoyService annotations")

	By("checking gateway EnvoyProxy leaves out the annotation the GatewayClass sets")
	Expect(annotations).NotTo(HaveKey(inheritedAnnotation))

	By("checking gateway EnvoyProxy still renders the defaults the GatewayClass does not set")
	Expect(annotations).To(HaveKeyWithValue(defaultOnlyAnnotation, defaultOnlyAnnotationValue))
}

// gatewayServiceInheritanceTests verifies the LoadBalancer Service Envoy Gateway renders from the
// merged EnvoyProxy carries the GatewayClass value, not the chart provider default.
func gatewayServiceInheritanceTests() {
	wcName := state.GetCluster().Name
	wcClient, _ := state.GetFramework().WC(wcName)

	By("checking gateway Service carries the GatewayClass annotation and the remaining defaults")
	Eventually(func() error {
		svcList := &corev1.ServiceList{}
		if err := wcClient.List(state.GetContext(), svcList, &cr.ListOptions{
			Namespace: "envoy-gateway-system",
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"gateway.envoyproxy.io/owning-gateway-name":      "giantswarm-default",
				"gateway.envoyproxy.io/owning-gateway-namespace": "envoy-gateway-system",
			}),
		}); err != nil {
			return err
		}
		if len(svcList.Items) == 0 {
			return fmt.Errorf("no services found for gateway giantswarm-default")
		}
		annotations := svcList.Items[0].Annotations
		if got := annotations[inheritedAnnotation]; got != inheritedAnnotationValue {
			return fmt.Errorf("expected annotation %s=%q from the GatewayClass, got %q", inheritedAnnotation, inheritedAnnotationValue, got)
		}
		if got := annotations[defaultOnlyAnnotation]; got != defaultOnlyAnnotationValue {
			return fmt.Errorf("expected annotation %s=%q from the gateway defaults, got %q", defaultOnlyAnnotation, defaultOnlyAnnotationValue, got)
		}
		return nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())
}
