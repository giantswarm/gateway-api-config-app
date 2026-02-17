# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Fix EnvoyProxy templating when multiple Gateways or GatewayClasses where specified

## [1.7.0] - 2026-02-12

### Changed

- Add `certificate.wildcard` option to include wildcard hostname in certificate dnsNames (disabled by default).
- Default certificate issuer to listener-level `certificate.issuer.name`, falling back to `gateway.tlsIssuer.name`.
- Remove SecurityContext defaults from class, since Envoy Gateway already sets them.
- Add PDB, HPA and container repository defaults to GatewayClass.
- All GatewayClasses have the new defaults.
- Add support for missing EnvoyProxy fields in GatewayClass.
- Apply the same defaults/merge pattern from GatewayClass to Gateway EnvoyProxy.
- Add support for overriding envoyService fields via `envoyProxy.envoyService`.
- Extract shared `envoyProxy.spec` helper to deduplicate EnvoyProxy templates.

## [1.6.2] - 2026-01-29

### Changed

- Fixed missing yaml document separator for https redirect HTTPRoute, Issuer and ClusterPolicy templates.

## [1.6.1] - 2026-01-28

### Changed

- Split files into folders per Gateway and GatewayClass.
- Fixed internal helm template function usage. Handle unset values more gracefully.

## [1.6.0] - 2026-01-20

### Changed

- Add gateway_name and gateway_namespace labels on metrics and logs.

## [1.5.0] - 2026-01-19

### Changed

- Replace PodMonitor and PodLog resources template by "generate" kyverno policies.

## [1.4.1] - 2026-01-08

### Changed

- Update Chart.yaml to use updated `io.giantswarm.application.team` annotation

## [1.4.0] - 2025-12-18

### Changed

- Support listeners with apex domain or single subdomain hostnames.

## [1.3.0] - 2025-12-12

### Added

- Add PodLogs for pod collection.
- Add option to enable HTTP redirect per gateway.

## [1.2.0] - 2025-12-02

### Changed

- Support additional listeners with custom `hostname`
  - Move `gateways.<gateway>.subdomains` to `gateways.<gateway>.listeners.<listener>.subdomains`.
  - Move `gateways.<gateway>.certificate` to `gateways.<gateway>.listeners.<listener>.certificate`
  - Move `gateways.<gateway>.dnsEndpoints` to `gateways.<gateway>.listeners.<listener>.dnsEndpoints`
  - Add `annotations` to DNSEndpoints

## [1.1.0] - 2025-11-18

### Changed

- Set AWS NLBs annotations by default when AWS NLB is used.
- Enable PROXY protocol and set HealthCheck path when AWS NLB is used.
- Allow to configure shutdown manager.
- Allow to configure externalTrafficPolicy and loadBalancerClass for Service.

## [1.0.0] - 2025-11-06

### Changed

- Updated E2E tests to use apptest-framework v1.14.0.
- Allow for custom envoyProxy provider configuration the Gateway.
- Allow for custom envoyProxy provider configuration the GatewayClass.
- Set proxy image for default gateway to gsoci.azurecr.io/giantswarm/envoy.
- Set HPA and PDB values for the default gateway.

## [0.5.1] - 2025-06-24

### Fixed

- Ensure that the Gateway is correctly templated when only certificateRefs are used.

## [0.5.0] - 2025-06-13

### Changed

- Values.gateway.hostnames now accepts a list of subdomains only.
- baseDomain can be overridden per Gateway.

## [0.4.0] - 2025-03-05

### Added

- Add podMonitor for each Gateway.

## [0.3.0] - 2025-02-26

### Changed

- Always set external-dns annotation based on dnsName and baseDomain.
- Use hostnames list for Certificates and add dnsEndpoints CR.
- Label all resources with labels.common.

## [0.2.0] - 2025-02-12

### Changed

- Use Certificate resource instead of cert-manager annotation.
- Allow multiple certificateRefs in listener for custom Certificates.
- Allow subdomains in HTTPS listener.
- Support multiple dnsNames in Certificate CR.
- Rename gateway and class to `default` in Values schema.

## [0.1.0] - 2025-02-05

- changed: `app.giantswarm.io` label group was changed to `application.giantswarm.io`
- Make GatewayClass customizable.
- Make Gateway customizable.
- Add Issuer per Gateway.
- Add annotations and labels for the Gateways
- Move external-dns config to the Gateway level

[Unreleased]: https://github.com/giantswarm/gateway-api-config-app/compare/v1.7.0...HEAD
[1.7.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v1.6.2...v1.7.0
[1.6.2]: https://github.com/giantswarm/gateway-api-config-app/compare/v1.6.1...v1.6.2
[1.6.1]: https://github.com/giantswarm/gateway-api-config-app/compare/v1.6.0...v1.6.1
[1.6.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v1.5.0...v1.6.0
[1.5.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v1.4.1...v1.5.0
[1.4.1]: https://github.com/giantswarm/gateway-api-config-app/compare/v1.4.0...v1.4.1
[1.4.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v0.5.1...v1.0.0
[0.5.1]: https://github.com/giantswarm/gateway-api-config-app/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/giantswarm/gateway-api-config-app/releases/tag/v0.1.0
