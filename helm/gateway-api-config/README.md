# gateway-api-config

![Version: 1.8.0](https://img.shields.io/badge/Version-1.8.0-informational?style=flat-square) ![AppVersion: 1.8.0](https://img.shields.io/badge/AppVersion-1.8.0-informational?style=flat-square)

Default configuration for Envoy Gateway

**Homepage:** <https://github.com/giantswarm/gateway-api-config-app>

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| baseDomain | string | `"example.com"` |  |
| gatewayClasses.default.enabled | bool | `true` |  |
| gatewayClasses.default.envoyProxy.enabled | bool | `true` |  |
| gatewayClasses.default.envoyProxy.envoyDeployment | object | `{}` |  |
| gatewayClasses.default.envoyProxy.envoyHpa | object | `{}` |  |
| gatewayClasses.default.envoyProxy.envoyPDB | object | `{}` |  |
| gatewayClasses.default.envoyProxy.envoyService | object | `{}` |  |
| gatewayClasses.default.envoyProxy.envoyServiceAccount | object | `{}` |  |
| gatewayClasses.default.errorPages.body | string | `""` |  |
| gatewayClasses.default.errorPages.contentType | string | `"text/html"` |  |
| gatewayClasses.default.errorPages.enabled | bool | `false` |  |
| gatewayClasses.default.errorPages.existingConfigMapName | string | `""` |  |
| gatewayClasses.default.errorPages.statusCodes[0].range.end | int | `404` |  |
| gatewayClasses.default.errorPages.statusCodes[0].range.start | int | `400` |  |
| gatewayClasses.default.errorPages.statusCodes[0].type | string | `"Range"` |  |
| gatewayClasses.default.errorPages.statusCodes[1].type | string | `"Value"` |  |
| gatewayClasses.default.errorPages.statusCodes[1].value | int | `500` |  |
| gatewayClasses.default.errorPages.statusCodes[2].type | string | `"Value"` |  |
| gatewayClasses.default.errorPages.statusCodes[2].value | int | `502` |  |
| gatewayClasses.default.errorPages.statusCodes[3].type | string | `"Value"` |  |
| gatewayClasses.default.errorPages.statusCodes[3].value | int | `503` |  |
| gatewayClasses.default.errorPages.statusCodes[4].type | string | `"Value"` |  |
| gatewayClasses.default.errorPages.statusCodes[4].value | int | `504` |  |
| gatewayClasses.default.name | string | `"giantswarm-default"` |  |
| gateways.default.className | string | `"giantswarm-default"` |  |
| gateways.default.dnsName | string | `"gateway"` |  |
| gateways.default.enabled | bool | `true` |  |
| gateways.default.envoyProxy.enabled | bool | `true` |  |
| gateways.default.envoyProxy.envoyDeployment | object | `{}` |  |
| gateways.default.envoyProxy.envoyHpa | object | `{}` |  |
| gateways.default.envoyProxy.envoyPDB | object | `{}` |  |
| gateways.default.envoyProxy.envoyService | object | `{}` |  |
| gateways.default.envoyProxy.envoyServiceAccount | object | `{}` |  |
| gateways.default.errorPages | object | `{}` |  |
| gateways.default.listeners.http.allowedRoutes.namespaces.from | string | `"All"` |  |
| gateways.default.listeners.http.httpsRedirectEnabled | bool | `false` |  |
| gateways.default.listeners.http.name | string | `"http"` |  |
| gateways.default.listeners.http.port | int | `80` |  |
| gateways.default.listeners.http.protocol | string | `"HTTP"` |  |
| gateways.default.listeners.https.allowedRoutes.namespaces.from | string | `"All"` |  |
| gateways.default.listeners.https.certificate.enabled | bool | `true` |  |
| gateways.default.listeners.https.certificate.issuer.kind | string | `""` |  |
| gateways.default.listeners.https.certificate.issuer.name | string | `""` |  |
| gateways.default.listeners.https.certificate.wildcard | bool | `false` |  |
| gateways.default.listeners.https.dnsEndpoints.annotations."giantswarm.io/external-dns" | string | `"managed"` |  |
| gateways.default.listeners.https.dnsEndpoints.enabled | bool | `true` |  |
| gateways.default.listeners.https.hostname | string | `"*.{{ $.Values.baseDomain }}"` |  |
| gateways.default.listeners.https.name | string | `"https"` |  |
| gateways.default.listeners.https.port | int | `443` |  |
| gateways.default.listeners.https.protocol | string | `"HTTPS"` |  |
| gateways.default.listeners.https.subdomains | list | `[]` |  |
| gateways.default.listeners.https.tls.certificateRefs | list | `[]` |  |
| gateways.default.listeners.https.tls.mode | string | `"Terminate"` |  |
| gateways.default.name | string | `"giantswarm-default"` |  |
| gateways.default.overrideBaseDomain | string | `""` |  |
| gateways.default.provider.aws.useNetworkLoadBalancer | bool | `true` |  |
| gateways.default.service.annotations | object | `{}` |  |
| gateways.default.service.externalTrafficPolicy | string | `""` |  |
| gateways.default.service.labels | object | `{}` |  |
| gateways.default.service.loadBalancerClass | string | `""` |  |
| gateways.default.tlsIssuer.email | string | `"accounts@giantswarm.io"` |  |
| gateways.default.tlsIssuer.enabled | bool | `true` |  |
| gateways.default.tlsIssuer.gateway | string | `"giantswarm-default"` |  |
| gateways.default.tlsIssuer.name | string | `"letsencrypt-giantswarm-gateway"` |  |
| kyvernoPolicies.observability.resourceName | string | `"{{ request.object.metadata.name }}"` |  |
| kyvernoPolicies.observability.resourceNamespace | string | `"{{ request.object.metadata.namespace }}"` |  |
| kyvernoPolicies.preconditions.key | string | `"{{ request.object.spec.gatewayClassName }}"` |  |

