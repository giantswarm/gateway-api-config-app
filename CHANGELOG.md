# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/giantswarm/gateway-api-config-app/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/giantswarm/gateway-api-config-app/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/giantswarm/gateway-api-config-app/releases/tag/v0.1.0
