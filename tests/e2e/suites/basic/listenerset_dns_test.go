package basic

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/giantswarm/apptest-framework/v5/pkg/state"
	"github.com/giantswarm/clustertest/v5/pkg/logger"

	appsv1 "k8s.io/api/apps/v1"
)

const (
	// Route53 answers NXDOMAIN with a 900s SOA negative TTL. A public recursor
	// would cache the first miss and flake every Eventually below, so records in
	// the workload cluster zone are always queried against its own authoritative
	// nameservers.
	dnsRecordTimeout = 10 * time.Minute
	dnsRecordPolling = 15 * time.Second
	dnsQueryTimeout  = 10 * time.Second
)

// listenerSetExternalDNSConfigTests asserts the cluster really runs external-dns
// with the ListenerSet-aware configuration this suite needs. Without it "no DNS
// record" would be indistinguishable from "the cluster values never took effect".
func listenerSetExternalDNSConfigTests() {
	By("checking external-dns runs with the gateway-httproute source and ListenerSet support")

	args := externalDNSArgs()
	logger.Log("external-dns args: %v", args)

	Expect(args).To(ContainElement("--source=gateway-httproute"),
		"external-dns is not watching HTTPRoutes, check global.apps.externalDns.values.sources in test_data/cluster_values.yaml")
	Expect(args).To(ContainElement("--gateway-listener-sets"),
		"external-dns cannot resolve ListenerSet parents, check global.apps.externalDns.values.enableGatewayListenerSets")

	for _, arg := range args {
		Expect(arg).NotTo(HavePrefix("--namespace="),
			"external-dns is namespace-scoped (%s), so the tenant HTTPRoute in %s would be invisible to it", arg, tenantNamespace)
	}
}

// externalDNSArgs returns the flags the running external-dns was started with. The
// Deployment is found by name rather than by label, so a chart rename of
// app.kubernetes.io/name does not turn this guard into a silent pass.
func externalDNSArgs() []string {
	deployments := &appsv1.DeploymentList{}
	Expect(wc().List(state.GetContext(), deployments)).To(Succeed())

	for _, deployment := range deployments.Items {
		if !strings.Contains(deployment.Name, "external-dns") {
			continue
		}
		var args []string
		for _, container := range deployment.Spec.Template.Spec.Containers {
			args = append(args, container.Args...)
		}
		for _, arg := range args {
			if strings.HasPrefix(arg, "--source=") {
				return args
			}
		}
	}

	Fail("no external-dns Deployment found in the workload cluster")
	return nil
}

// listenerSetDNSTests asserts both ListenerSet hostnames resolve to the gateway's
// own load balancer.
func listenerSetDNSTests() {
	zone := wcZone()

	// Without this the assertions below would pass on any hostname at all. A
	// wildcard in the zone (dns-operator-route53 creates one for clusters with an
	// ingress controller or a wildcard-cname-target annotation) makes the whole
	// section vacuous, so fail loudly rather than pass silently.
	By("checking the zone has no wildcard record that would make DNS assertions vacuous")
	// Resolve the nameservers first: a failure to reach them must surface as its
	// own error, not as a nonce that "did not resolve".
	resolver, err := authoritativeResolver(zone)
	Expect(err).NotTo(HaveOccurred())

	nonce := fmt.Sprintf("e2e-no-such-record-%d.%s", time.Now().UnixNano(), zone)
	ctx, cancel := context.WithTimeout(state.GetContext(), dnsQueryTimeout)
	addrs, err := resolver.LookupHost(ctx, nonce)
	cancel()
	Expect(err).To(HaveOccurred(),
		"%s resolved to %v, so this zone has a wildcard record and no DNS assertion here proves anything", nonce, addrs)

	// A wildcard CNAME whose target lives outside the zone (what
	// dns-operator-route53 writes for wildcard-cname-target) answers with the CNAME
	// alone, and the authoritative nameserver above does not recurse, so the lookup
	// fails and the check above passes on a zone that does have a wildcard. Ask for
	// the CNAME itself as well. resolveInZone chases those hops manually, so
	// expectResolvesToGatewayLB below would otherwise succeed for any name at all.
	ctx, cancel = context.WithTimeout(state.GetContext(), dnsQueryTimeout)
	cname, cnameErr := resolver.LookupCNAME(ctx, nonce)
	cancel()
	if cnameErr == nil {
		Expect(strings.TrimSuffix(cname, ".")).To(Equal(strings.TrimSuffix(nonce, ".")),
			"%s is a CNAME for %s, so this zone has a wildcard record and no DNS assertion here proves anything", nonce, cname)
	}

	lbAddrs := gatewayLBAddresses()
	logger.Log("Gateway load balancer addresses: %v", lbAddrs)

	// The gateway apex is the control request of the policy cascade matrix, so
	// check it here where a DNS failure reads as a DNS failure.
	By("checking the gateway apex resolves to the gateway load balancer")
	expectResolvesToGatewayLB(zone, gatewayHostname(), lbAddrs)

	By("checking the chart ListenerSet hostname resolves to the gateway load balancer")
	expectResolvesToGatewayLB(zone, chartListenerSetHostname(), lbAddrs)

	By("checking the tenant ListenerSet hostname resolves to the gateway load balancer")
	expectResolvesToGatewayLB(zone, tenantListenerSetHostname(), lbAddrs)

	// The registry record shape depends on installation-level txtPrefix/txtOwnerId
	// and on whether the legacy or the new TXT format is in use, so it is logged
	// for diagnosis rather than asserted.
	logTXTRegistryRecords(zone, chartListenerSetHostname())
	logTXTRegistryRecords(zone, tenantListenerSetHostname())
}

// expectResolvesToGatewayLB waits until host resolves to an address set that
// intersects the gateway load balancer's own addresses.
//
// Intersection rather than a CNAME comparison: external-dns' AWS provider writes a
// Route53 ALIAS A record for an in-account ELB (a CNAME only with
// --aws-prefer-cname), and alias records flatten, so LookupCNAME just echoes the
// queried name back. Matching on addresses covers both shapes, and it is the
// stronger claim: it proves external-dns walked ListenerSet -> parentRef ->
// Gateway -> Service to compute the target.
func expectResolvesToGatewayLB(zone, host string, lbAddrs []string) {
	Eventually(func() error {
		addrs, err := resolveInZone(zone, host)
		if err != nil {
			return err
		}
		for _, addr := range addrs {
			for _, lbAddr := range lbAddrs {
				if addr == lbAddr {
					logger.Log("%s resolves to %v, matching the gateway load balancer at %s", host, addrs, addr)
					return nil
				}
			}
		}
		return fmt.Errorf("%s resolves to %v, none of which is a gateway load balancer address %v", host, addrs, lbAddrs)
	}).
		WithTimeout(dnsRecordTimeout).
		WithPolling(dnsRecordPolling).
		Should(Succeed())
}

// gatewayLBAddresses resolves the gateway's AWS load balancer hostname through the
// normal resolver, since it lives outside the workload cluster zone.
func gatewayLBAddresses() []string {
	var addrs []string
	Eventually(func() error {
		lbHostname, err := getGatewayLBHostname(wc())
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(state.GetContext(), dnsQueryTimeout)
		defer cancel()
		addrs, err = net.DefaultResolver.LookupHost(ctx, lbHostname)
		if err != nil {
			return fmt.Errorf("resolving gateway load balancer %s: %w", lbHostname, err)
		}
		if len(addrs) == 0 {
			return fmt.Errorf("gateway load balancer %s has no addresses", lbHostname)
		}
		return nil
	}).
		WithTimeout(10 * time.Minute).
		WithPolling(15 * time.Second).
		Should(Succeed())
	return addrs
}

// resolveInZone resolves host, querying the zone's authoritative nameservers for as
// long as the CNAME chain stays inside the zone and falling back to the normal
// resolver once it leaves (an ELB hostname, typically).
func resolveInZone(zone, host string) ([]string, error) {
	authoritative, err := authoritativeResolver(zone)
	if err != nil {
		return nil, err
	}

	current := host
	for hop := 0; hop < 5; hop++ {
		resolver := net.DefaultResolver
		if inZone(current, zone) {
			resolver = authoritative
		}

		ctx, cancel := context.WithTimeout(state.GetContext(), dnsQueryTimeout)
		addrs, lookupErr := resolver.LookupHost(ctx, current)
		cancel()
		if lookupErr == nil && len(addrs) > 0 {
			return addrs, nil
		}

		// Route53 chases CNAMEs within a hosted zone, so this only matters when the
		// chain crosses a zone boundary.
		ctx, cancel = context.WithTimeout(state.GetContext(), dnsQueryTimeout)
		cname, cnameErr := resolver.LookupCNAME(ctx, current)
		cancel()
		next := strings.TrimSuffix(cname, ".")
		if cnameErr != nil || next == "" || strings.EqualFold(next, strings.TrimSuffix(current, ".")) {
			if lookupErr != nil {
				return nil, lookupErr
			}
			return nil, fmt.Errorf("no addresses for %s", host)
		}
		current = next
	}
	return nil, fmt.Errorf("CNAME chain starting at %s is too long", host)
}

// resolverCache keeps one resolver per zone: the NS lookup is stable for the life
// of the suite and every Eventually below would otherwise repeat it.
var resolverCache = map[string]*net.Resolver{}

// authoritativeResolver builds a resolver that talks directly to the zone's own
// nameservers, walking up the domain until an NS record set is found.
func authoritativeResolver(zone string) (*net.Resolver, error) {
	if resolver, ok := resolverCache[zone]; ok {
		return resolver, nil
	}

	var lastErr error
	for name := zone; strings.Count(name, ".") >= 1; name = name[strings.Index(name, ".")+1:] {
		ctx, cancel := context.WithTimeout(state.GetContext(), dnsQueryTimeout)
		nameservers, err := net.DefaultResolver.LookupNS(ctx, name)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		if len(nameservers) == 0 {
			lastErr = fmt.Errorf("%s has an empty NS record set", name)
			continue
		}

		server := net.JoinHostPort(strings.TrimSuffix(nameservers[0].Host, "."), "53")
		logger.Log("Using authoritative nameserver %s for zone %s", server, name)
		resolver := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				dialer := &net.Dialer{Timeout: dnsQueryTimeout}
				return dialer.DialContext(ctx, network, server)
			},
		}
		resolverCache[zone] = resolver
		return resolver, nil
	}

	if lastErr == nil {
		return nil, fmt.Errorf("no authoritative nameserver found for %s", zone)
	}
	return nil, fmt.Errorf("no authoritative nameserver found for %s: %w", zone, lastErr)
}

func inZone(host, zone string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")
	return host == zone || strings.HasSuffix(host, "."+zone)
}

// logTXTRegistryRecords records whatever external-dns left in its TXT registry for
// a hostname. Logged only, never asserted, see listenerSetDNSTests.
func logTXTRegistryRecords(zone, host string) {
	resolver, err := authoritativeResolver(zone)
	if err != nil {
		logger.Log("Could not look up TXT registry records for %s: %v", host, err)
		return
	}

	for _, name := range []string{host, "cname-" + host, "a-" + host} {
		ctx, cancel := context.WithTimeout(state.GetContext(), dnsQueryTimeout)
		records, err := resolver.LookupTXT(ctx, name)
		cancel()
		if err != nil || len(records) == 0 {
			continue
		}
		logger.Log("external-dns TXT registry %s: %v", name, records)
		AddReportEntry("external-dns TXT registry "+name, strings.Join(records, " "))
	}
}
