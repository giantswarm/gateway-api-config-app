package basic

import (
	"fmt"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// certRotationEnvVar gates the certificate rotation half of envoyproxy/gateway#9614.
// It costs a fresh ACME order and eats into the Let's Encrypt duplicate-certificate
// budget on every run, so it is opt-in and meant for an Envoy Gateway bump.
const certRotationEnvVar = "E2E_LISTENERSET_CERT_ROTATION"

// listenerSetChartResourceTests verifies the chart rendered everything that backs a
// chart-managed ListenerSet, and that Envoy Gateway accepted each piece.
func listenerSetChartResourceTests() {
	ctx := state.GetContext()
	wcClient := wc()

	By("checking the chart rendered ListenerSet " + chartListenerSetName)
	ls := &gatewayv1.ListenerSet{}
	Eventually(func() error {
		return wcClient.Get(ctx, cr.ObjectKey{Name: chartListenerSetName, Namespace: gatewayNamespace}, ls)
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(5 * time.Second).
		Should(Succeed())

	Expect(string(ls.Spec.ParentRef.Name)).To(Equal(gatewayName))
	Expect(ls.Spec.Listeners).To(HaveLen(1))
	Expect(ls.Spec.Listeners[0].Hostname).NotTo(BeNil())
	Expect(string(*ls.Spec.Listeners[0].Hostname)).To(Equal(chartListenerSetHostname()))

	By("checking the chart rendered the ListenerSet Certificate")
	cert := &cmv1.Certificate{}
	Expect(wcClient.Get(ctx, cr.ObjectKey{Name: chartListenerSetPrefix + "-https", Namespace: gatewayNamespace}, cert)).To(Succeed())
	Expect(cert.Spec.DNSNames).To(ConsistOf(chartListenerSetHostname()))
	Expect(cert.Spec.SecretName).To(Equal(chartListenerSetPrefix + "-https-tls"))

	By("checking no DNSEndpoint shadows the record external-dns derives from the ListenerSet")
	// The suite deliberately runs both fixtures through the gateway-httproute
	// source. A DNSEndpoint for the same hostname would write the record from the
	// crd source instead, and the ListenerSet walk would never be exercised.
	dnsEndpoint := &unstructured.Unstructured{}
	dnsEndpoint.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "externaldns.k8s.io",
		Version: "v1alpha1",
		Kind:    "DNSEndpoint",
	})
	err := wcClient.Get(ctx, cr.ObjectKey{Name: chartListenerSetPrefix + "-https", Namespace: gatewayNamespace}, dnsEndpoint)
	Expect(errors.IsNotFound(err)).To(BeTrue(),
		"expected no DNSEndpoint for the chart ListenerSet, check dnsEndpoints.enabled in bundle_values.yaml, got err=%v", err)

	By("waiting for the chart ListenerSet to be Accepted and Programmed")
	expectListenerSetReady(chartListenerSetName, gatewayNamespace, "https", 15*time.Minute)

	By("checking the ListenerSet-targeted ClientTrafficPolicy and BackendTrafficPolicy are Accepted")
	// Envoy Gateway collapses every 443 listener into one Envoy listener, so a
	// ListenerSet policy sitting next to the gateway's own policy is where a
	// conflict would surface. Assert both reached Accepted on a ListenerSet ancestor.
	expectPolicyAcceptedForListenerSet("ClientTrafficPolicy", chartListenerSetPrefix, chartListenerSetName)
	expectPolicyAcceptedForListenerSet("BackendTrafficPolicy", chartListenerSetPrefix, chartListenerSetName)
}

// listenerSetTenantCertTests drives the tenant fixture through the ordering that
// broke in envoyproxy/gateway#9614: the ListenerSet is created first and only then
// does cert-manager mint its Secret. Without the "Secret is absent" step in the
// middle the ordering is not load-bearing and the test proves nothing.
func listenerSetTenantCertTests() {
	ctx := state.GetContext()
	wcClient := wc()

	By("checking the tenant TLS Secret genuinely does not exist yet")
	secret := &corev1.Secret{}
	err := wcClient.Get(ctx, cr.ObjectKey{Name: tenantSecretName, Namespace: tenantNamespace}, secret)
	Expect(errors.IsNotFound(err)).To(BeTrue(),
		"expected Secret %s/%s to be absent before the Certificate is created, got err=%v", tenantNamespace, tenantSecretName, err)

	// Record, rather than assert, how Envoy Gateway describes an unresolvable
	// certificateRef: the exact condition vocabulary is not ours to pin.
	reportListenerSetConditions("before Certificate creation")

	createTenantCertificate()

	By("waiting for cert-manager to publish the tenant TLS Secret")
	Eventually(func() error {
		return wcClient.Get(ctx, cr.ObjectKey{Name: tenantSecretName, Namespace: tenantNamespace}, secret)
	}).
		WithTimeout(20 * time.Minute).
		WithPolling(15 * time.Second).
		Should(Succeed())

	By("waiting for the tenant ListenerSet to pick up the Secret created after it")
	expectListenerSetReady(tenantListenerSetName, tenantNamespace, "https", 10*time.Minute)

	By("checking the parent Gateway reports both ListenerSets as attached")
	Eventually(func() (int32, error) {
		gw := &gatewayv1.Gateway{}
		if err := wcClient.Get(ctx, cr.ObjectKey{Name: gatewayName, Namespace: gatewayNamespace}, gw); err != nil {
			return 0, err
		}
		if gw.Status.AttachedListenerSets == nil {
			return 0, nil
		}
		return *gw.Status.AttachedListenerSets, nil
	}).
		WithTimeout(5 * time.Minute).
		WithPolling(10 * time.Second).
		Should(BeNumerically(">=", 2))
}

// listenerSetCertRotationTests covers the renewal half of envoyproxy/gateway#9614:
// a re-issued Secret must reach the data plane. Opt-in, see certRotationEnvVar.
func listenerSetCertRotationTests() {
	if os.Getenv(certRotationEnvVar) != "true" {
		Skip(fmt.Sprintf("certificate rotation costs a fresh ACME order, set %s=true to run it", certRotationEnvVar))
	}

	ctx := state.GetContext()
	wcClient := wc()
	host := tenantListenerSetHostname()

	By("recording the serial currently served for " + host)
	before, err := servedCertificate(host)
	Expect(err).NotTo(HaveOccurred())
	logger.Log("Served certificate serial before rotation: %s", before.SerialNumber)

	By("deleting the tenant TLS Secret so cert-manager re-issues it")
	secret := &corev1.Secret{}
	Expect(wcClient.Get(ctx, cr.ObjectKey{Name: tenantSecretName, Namespace: tenantNamespace}, secret)).To(Succeed())
	Expect(wcClient.Delete(ctx, secret)).To(Succeed())

	By("waiting for the served certificate to change")
	Eventually(func() (string, error) {
		leaf, err := servedCertificate(host)
		if err != nil {
			return "", err
		}
		return leaf.SerialNumber.String(), nil
	}).
		WithTimeout(25 * time.Minute).
		WithPolling(20 * time.Second).
		ShouldNot(Equal(before.SerialNumber.String()))
}

// expectListenerSetReady waits until a ListenerSet is Accepted and Programmed and
// the named listener resolved its certificateRefs.
func expectListenerSetReady(name, namespace, listener string, timeout time.Duration) {
	Eventually(func() error {
		ls := &gatewayv1.ListenerSet{}
		if err := wc().Get(state.GetContext(), cr.ObjectKey{Name: name, Namespace: namespace}, ls); err != nil {
			return err
		}
		if !meta.IsStatusConditionTrue(ls.Status.Conditions, string(gatewayv1.ListenerSetConditionAccepted)) {
			return fmt.Errorf("ListenerSet %s/%s is not Accepted: %s", namespace, name, conditionSummary(ls.Status.Conditions))
		}
		if !meta.IsStatusConditionTrue(ls.Status.Conditions, string(gatewayv1.ListenerSetConditionProgrammed)) {
			return fmt.Errorf("ListenerSet %s/%s is not Programmed: %s", namespace, name, conditionSummary(ls.Status.Conditions))
		}
		for _, entry := range ls.Status.Listeners {
			if string(entry.Name) != listener {
				continue
			}
			if !meta.IsStatusConditionTrue(entry.Conditions, string(gatewayv1.ListenerConditionResolvedRefs)) {
				return fmt.Errorf("listener %q of ListenerSet %s/%s has unresolved refs: %s", listener, namespace, name, conditionSummary(entry.Conditions))
			}
			return nil
		}
		return fmt.Errorf("ListenerSet %s/%s has no status for listener %q", namespace, name, listener)
	}).
		WithTimeout(timeout).
		WithPolling(10 * time.Second).
		Should(Succeed())
}

// expectPolicyAcceptedForListenerSet asserts an Envoy Gateway policy reports
// Accepted=True against a ListenerSet ancestor. Cheap, and it catches Envoy
// Gateway silently ignoring the targetRef without waiting on traffic.
func expectPolicyAcceptedForListenerSet(kind, name, listenerSetName string) {
	policy := &unstructured.Unstructured{}
	policy.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "gateway.envoyproxy.io",
		Version: "v1alpha1",
		Kind:    kind,
	})

	Eventually(func() error {
		if err := wc().Get(state.GetContext(), cr.ObjectKey{Name: name, Namespace: gatewayNamespace}, policy); err != nil {
			return err
		}
		ancestors, found, err := unstructured.NestedSlice(policy.Object, "status", "ancestors")
		if err != nil || !found {
			return fmt.Errorf("%s %s has no status.ancestors yet", kind, name)
		}
		for _, a := range ancestors {
			ancestor, ok := a.(map[string]any)
			if !ok {
				continue
			}
			ref, _, _ := unstructured.NestedMap(ancestor, "ancestorRef")
			if ref["kind"] != "ListenerSet" || ref["name"] != listenerSetName {
				continue
			}
			conditions, _, _ := unstructured.NestedSlice(ancestor, "conditions")
			for _, c := range conditions {
				condition, ok := c.(map[string]any)
				if !ok {
					continue
				}
				if condition["type"] == "Accepted" && condition["status"] == "True" {
					return nil
				}
			}
			return fmt.Errorf("%s %s is not Accepted on its ListenerSet ancestor: %v", kind, name, conditions)
		}
		return fmt.Errorf("%s %s has no ancestor of kind ListenerSet named %s", kind, name, listenerSetName)
	}).
		WithTimeout(10 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())
}

// reportListenerSetConditions attaches the tenant ListenerSet's conditions to the
// Ginkgo report. Deliberately not an assertion, see listenerSetTenantCertTests.
func reportListenerSetConditions(stage string) {
	ls := &gatewayv1.ListenerSet{}
	if err := wc().Get(state.GetContext(), cr.ObjectKey{Name: tenantListenerSetName, Namespace: tenantNamespace}, ls); err != nil {
		AddReportEntry("tenant ListenerSet conditions "+stage, fmt.Sprintf("get failed: %v", err))
		return
	}

	entry := conditionSummary(ls.Status.Conditions)
	for _, listener := range ls.Status.Listeners {
		entry += fmt.Sprintf(" | listener %s: %s", listener.Name, conditionSummary(listener.Conditions))
	}
	AddReportEntry("tenant ListenerSet conditions "+stage, entry)
	logger.Log("tenant ListenerSet conditions %s: %s", stage, entry)
}

func conditionSummary(conditions []metav1.Condition) string {
	parts := make([]string, 0, len(conditions))
	for _, c := range conditions {
		parts = append(parts, fmt.Sprintf("%s=%s(%s)", c.Type, c.Status, c.Reason))
	}
	return strings.Join(parts, ", ")
}
