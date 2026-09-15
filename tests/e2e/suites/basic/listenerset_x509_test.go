package basic

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	corev1 "k8s.io/api/core/v1"
	cr "sigs.k8s.io/controller-runtime/pkg/client"
)

// listenerSetCertificateContentTests checks each ListenerSet really got its own
// publicly trusted certificate.
//
// The exact-SAN assertion is the load-bearing one: the gateway's *.<baseDomain>
// wildcard also matches these single-label hostnames, so without it the suite
// would pass even if the ListenerSet certificate were never used.
func listenerSetCertificateContentTests() {
	By("checking the chart ListenerSet certificate covers exactly its own hostname")
	assertPubliclyIssuedFor(gatewayNamespace, chartListenerSetPrefix+"-https-tls", chartListenerSetHostname())

	By("checking the tenant ListenerSet certificate covers exactly its own hostname")
	assertPubliclyIssuedFor(tenantNamespace, tenantSecretName, tenantListenerSetHostname())
}

// assertPubliclyIssuedFor reads a cert-manager TLS Secret and asserts the leaf is a
// currently valid Let's Encrypt certificate for exactly the given hostname.
func assertPubliclyIssuedFor(namespace, secretName, hostname string) {
	leaf, err := leafFromSecret(namespace, secretName)
	Expect(err).NotTo(HaveOccurred())

	Expect(leaf.DNSNames).To(ConsistOf(hostname),
		"certificate in %s/%s must cover exactly %q, otherwise the gateway wildcard would mask a broken ListenerSet certificate", namespace, secretName, hostname)
	Expect(strings.Join(leaf.Issuer.Organization, ",")).To(ContainSubstring("Let's Encrypt"),
		"certificate in %s/%s was not issued by Let's Encrypt: %s", namespace, secretName, leaf.Issuer)

	now := time.Now()
	Expect(now).To(BeTemporally(">=", leaf.NotBefore))
	Expect(now).To(BeTemporally("<", leaf.NotAfter))

	logger.Log("Certificate %s/%s: SANs=%v issuer=%q valid until %s", namespace, secretName, leaf.DNSNames, leaf.Issuer.CommonName, leaf.NotAfter)
}

// leafFromSecret parses the leaf certificate out of a kubernetes.io/tls Secret.
func leafFromSecret(namespace, secretName string) (*x509.Certificate, error) {
	secret := &corev1.Secret{}
	if err := wc().Get(state.GetContext(), cr.ObjectKey{Name: secretName, Namespace: namespace}, secret); err != nil {
		return nil, fmt.Errorf("getting secret %s/%s: %w", namespace, secretName, err)
	}

	raw, ok := secret.Data[corev1.TLSCertKey]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s has no %s", namespace, secretName, corev1.TLSCertKey)
	}

	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("secret %s/%s does not hold a PEM certificate", namespace, secretName)
	}

	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing certificate from %s/%s: %w", namespace, secretName, err)
	}
	return leaf, nil
}

// servedCertificate returns the leaf certificate Envoy actually presents for a
// hostname, by handshaking against the gateway load balancer with that SNI.
func servedCertificate(hostname string) (*x509.Certificate, error) {
	lbHostname, err := getGatewayLBHostname(wc())
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(lbHostname, "443"), &tls.Config{
		ServerName: hostname,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return nil, fmt.Errorf("TLS handshake for %s via %s: %w", hostname, lbHostname, err)
	}
	defer conn.Close() //nolint:errcheck

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, fmt.Errorf("no peer certificates presented for %s", hostname)
	}
	return certs[0], nil
}
