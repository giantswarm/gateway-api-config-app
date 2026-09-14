{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "name" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels
*/}}
{{- define "labels.common" -}}
app.kubernetes.io/name: {{ include "name" . | quote }}
application.giantswarm.io/team: {{ index .Chart.Annotations "io.giantswarm.application.team" | quote }}
giantswarm.io/managed-by: {{ .Release.Name | quote }}
helm.sh/chart: {{ include "chart" . | quote }}
{{- end -}}

{{/*
Gateway Service annotations
*/}}
{{- define "service.annotations" -}}
{{- $service := .gateway.service }}
{{- $annotations := dict }}

{{- /* Enable External-DNS */}}
{{- $_ := set $annotations "external-dns.alpha.kubernetes.io/hostname" (printf "%s.%s" .gateway.dnsName (.gateway.overrideBaseDomain | default .baseDomain)) }}
{{- $_ := set $annotations "giantswarm.io/external-dns" "managed" }}

{{- /* Use AWS NLB */}}
{{- if and (eq .provider "capa") (dig "provider" "aws" "useNetworkLoadBalancer" true .gateway) }}
{{- /* Enable PROXY Protocol */}}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-proxy-protocol" "*" }}

{{- /* Configure Health Checks on port 80 for all listeners */}}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-healthcheck-port" "http-80" }}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-healthcheck-path" "/healthz" }}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-healthcheck-healthy-threshold" "2" }}
{{- /* Detect an unhealthy node quickly so the NLB drain starts well before envoy exits */}}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-healthcheck-interval" "10" }}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-healthcheck-unhealthy-threshold" "2" }}

{{- /* Make LB public by default */}}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-scheme" "internet-facing" }}

{{- /* Configure attributes */}}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-attributes" "load_balancing.cross_zone.enabled=true" }}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-target-group-attributes" "target_health_state.unhealthy.connection_termination.enabled=false,target_health_state.unhealthy.draining_interval_seconds=120,preserve_client_ip.enabled=false" }}

{{- /* Tag the NLB with the owning gateway name and namespace */}}
{{- $_ := set $annotations "service.beta.kubernetes.io/aws-load-balancer-additional-resource-tags" (printf "gateway.envoyproxy.io/owning-gateway-name=%s,gateway.envoyproxy.io/owning-gateway-namespace=%s" .gateway.name .root.Release.Namespace) }}
{{- end }}

{{- $annotations = mergeOverwrite $annotations (deepCopy (default dict $service.annotations)) }}
{{- $annotations | toYaml }}
{{- end }}

{{/*
Gateway Service loadBalancerClass
*/}}
{{- define "service.loadBalancerClass" -}}
{{- $service := .gateway.service }}
{{- if and (eq .provider "capa") (dig "provider" "aws" "useNetworkLoadBalancer" true .gateway) }}
{{- default "service.k8s.aws/nlb" $service.loadBalancerClass }}
{{- else }}
{{- default "" $service.loadBalancerClass }}
{{- end }}
{{- end }}

{{/*
Gateway Service externalTrafficPolicy
*/}}
{{- define "service.externalTrafficPolicy" -}}
{{- $service := .gateway.service }}
{{- if and (eq .provider "capa") (dig "provider" "aws" "useNetworkLoadBalancer" true .gateway) }}
{{- default "Local" $service.externalTrafficPolicy }}
{{- else }}
{{- default "Cluster" $service.externalTrafficPolicy }}
{{- end }}
{{- end }}

{{/*
Gateway EnvoyService defaults - computes provider-specific envoyService configuration
*/}}
{{- define "gateway.envoyServiceDefaults" -}}
{{- $envoyService := dict }}
{{- $loadBalancerClass := (include "service.loadBalancerClass" .) }}
{{- if $loadBalancerClass }}
{{- $_ := set $envoyService "loadBalancerClass" $loadBalancerClass }}
{{- end }}
{{- $_ := set $envoyService "externalTrafficPolicy" (include "service.externalTrafficPolicy" .) }}
{{- $_ := set $envoyService "annotations" ((include "service.annotations" .) | fromYaml) }}
{{- if .gateway.service.labels }}
{{- $_ := set $envoyService "labels" ((tpl (.gateway.service.labels | toYaml | toString) .root) | fromYaml) }}
{{- end }}
{{- $envoyService | toYaml }}
{{- end }}

{{/*
Gateway Shutdown defaults - computes provider-specific shutdown configuration
*/}}
{{- define "gateway.shutdownDefaults" -}}
{{- $shutdown := dict }}
{{- /* Set defaults for AWS NLBs */}}
{{- /*
  Drain timers are aligned so the node always outlives the NLB connection drain.

  Listener drain starts at SIGTERM, but healthCheckFailureDelay holds /healthz up
  for 30s first. With externalTrafficPolicy: Local, failing it immediately makes
  the pod non-ready while the NLB still forwards flows to the node, leaving it
  without a serving local endpoint until the NLB notices. The delay covers that
  window, so the NLB stops sending new flows before the endpoint goes away.

  The delay pushes every later step back by the same 30s: healthcheck detection
  (~20s) lands at ~50s and draining_interval_seconds (120s) then ends at ~170s, so
  a minDrainDuration of 180s keeps envoy (and therefore the node) alive until all
  in-flight NLB flows have moved off the node, avoiding RST/520 on node disruption.
*/}}
{{- if and (eq .provider "capa") (dig "provider" "aws" "useNetworkLoadBalancer" true .gateway) }}
{{- $_ := set $shutdown "healthCheckFailureDelay" "30s" }}
{{- $_ := set $shutdown "drainTimeout" "200s" }}
{{- $_ := set $shutdown "minDrainDuration" "180s" }}
{{- end }}
{{- $shutdown | toYaml }}
{{- end }}

{{/*
Gateway EnvoyDeployment defaults - computes provider-specific deployment configuration.
For AWS NLBs this ensures the pod's terminationGracePeriodSeconds stays above the
drain timeout, and spreads the proxy pods one-per-node so each NLB instance target
maps to a single envoy.
*/}}
{{- define "gateway.envoyDeploymentDefaults" -}}
{{- $envoyDeployment := dict }}
{{- if and (eq .provider "capa") (dig "provider" "aws" "useNetworkLoadBalancer" true .gateway) }}
{{- $pod := dict }}
{{- /* Prefer one proxy pod per node so each NLB instance target maps to a single
       envoy, improving NLB health-checking and traffic distribution. Selects pods by
       the owning-gateway labels Envoy Gateway stamps on the proxy pods. */}}
{{- $podAffinityTerm := dict
      "labelSelector" (dict "matchExpressions" (list
        (dict "key" "gateway.envoyproxy.io/owning-gateway-name" "operator" "In" "values" (list .gateway.name))
        (dict "key" "gateway.envoyproxy.io/owning-gateway-namespace" "operator" "In" "values" (list .namespace))
      ))
      "topologyKey" "kubernetes.io/hostname" }}
{{- $_ := set $pod "affinity" (dict "podAntiAffinity" (dict "preferredDuringSchedulingIgnoredDuringExecution" (list (dict "weight" 100 "podAffinityTerm" $podAffinityTerm)))) }}
{{- $_ := set $envoyDeployment "pod" $pod }}
{{- /* terminationGracePeriodSeconds has no dedicated field on EnvoyProxy, so patch it.
       It must stay above shutdown.drainTimeout (200s). */}}
{{- $_ := set $envoyDeployment "patch" (dict "type" "StrategicMerge" "value" (dict "spec" (dict "template" (dict "spec" (dict "terminationGracePeriodSeconds" 240))))) }}
{{- end }}
{{- $envoyDeployment | toYaml }}
{{- end }}

{{/*
EnvoyProxy spec - shared spec output for EnvoyProxy resources
Takes: envoyProxyValues (dict with all the envoyProxy configuration)
*/}}
{{/*
Resolve effective errorPages config by merging gatewayClass defaults with per-gateway overrides.
Per-gateway values take precedence over gatewayClass values.
Takes: dict with "class" (gatewayClass.errorPages) and "gateway" ($gateway.errorPages)
*/}}
{{- define "errorPages.effective" -}}
{{- $class := .class | default dict }}
{{- $gateway := .gateway | default dict }}
{{- $effective := deepCopy $class }}
{{- $effective = mergeOverwrite $effective (deepCopy $gateway) }}
{{- $effective | toYaml }}
{{- end -}}

{{- define "envoyProxy.spec" -}}
provider:
  type: Kubernetes
  kubernetes:
    {{- with .envoyDeployment }}
    envoyDeployment:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- with .envoyService }}
    envoyService:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- with .envoyHpa }}
    envoyHpa:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- with .envoyPDB }}
    envoyPDB:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- with .envoyServiceAccount }}
    envoyServiceAccount:
      {{- toYaml . | nindent 6 }}
    {{- end }}
{{- with .logging }}
logging:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .telemetry }}
telemetry:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .bootstrap }}
bootstrap:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- if .concurrency }}
concurrency: {{ .concurrency }}
{{- end }}
{{- with .extraArgs }}
extraArgs:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- if .mergeGateways }}
mergeGateways: {{ .mergeGateways }}
{{- end }}
{{- with .shutdown }}
shutdown:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- if .mergeType }}
mergeType: {{ .mergeType }}
{{- end }}
{{- end }}

{{/*
Resolve effective errorPages for a gateway by looking up its gatewayClass by name
and merging the class defaults with the per-gateway overrides.
Takes: dict with "gateway" and "root"
Returns: YAML, consume with fromYaml
*/}}
{{- define "gateway.errorPages" -}}
{{- $gateway := .gateway }}
{{- $classErrorPages := dict }}
{{- range $_, $class := .root.Values.gatewayClasses }}
{{- if eq $class.name $gateway.className }}
{{- $classErrorPages = $class.errorPages | default dict }}
{{- end }}
{{- end }}
{{- include "errorPages.effective" (dict "class" $classErrorPages "gateway" $gateway.errorPages) }}
{{- end -}}

{{/*
Name shared by the per-listener resources (Certificate, DNSEndpoint, TLS Secret).
Takes: dict with "gateway", "listener" and optionally "listenerSet" (the key of a
gateways.<k>.listenerSets entry). The listener set segment keeps names unique across
gateways, since a listener set key is only unique within its own gateway.
*/}}
{{- define "listener.resourceName" -}}
{{- if .listenerSet -}}
{{- printf "gateway-%s-%s-%s" .gateway.name .listenerSet .listener.name -}}
{{- else -}}
{{- printf "gateway-%s-%s" .gateway.name .listener.name -}}
{{- end -}}
{{- end -}}

{{/*
Base domain for a listener: the gateway's overrideBaseDomain when set, otherwise the
listener hostname with any wildcard prefix stripped.
Takes: dict with "gateway", "listener" and "root"
*/}}
{{- define "listener.baseDomain" -}}
{{- trimPrefix "*." (tpl (.gateway.overrideBaseDomain | default .listener.hostname) .root) -}}
{{- end -}}

{{/*
A single Gateway API listener entry. The schema is identical for Gateway.spec.listeners
and ListenerSet.spec.listeners, so both render through this.
Takes: dict with "gateway", "listener", "root" and optionally "listenerSet"
Emits at zero indent, callers apply nindent.
*/}}
{{- define "gatewayapi.listener" -}}
{{- $l := .listener -}}
- name: {{ $l.name }}
  protocol: {{ $l.protocol }}
  port: {{ $l.port }}
  {{- with $l.allowedRoutes }}
  allowedRoutes:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with $l.hostname }}
  hostname: {{ tpl (. | quote) $.root }}
  {{- end }}
  {{- with $l.tls }}
  tls:
    mode: {{ .mode }}
    {{- if or (.certificateRefs) (dig "certificate" "enabled" false $l) }}
    certificateRefs:
    {{- if and (eq .mode "Terminate") (dig "certificate" "enabled" false $l) }}
    - kind: Secret
      name: {{ printf "%s-tls" (include "listener.resourceName" $) }}
    {{- end }}
    {{- range .certificateRefs }}
    - kind: Secret
      name: {{ .name }}
      {{- if .namespace }}
      namespace: {{ .namespace }}
      {{- end }}
    {{- end }}
    {{- end }}
  {{- end }}
{{- end -}}

{{/*
cert-manager Certificate for a listener.
Takes: dict with "gateway", "listener", "root" and optionally "listenerSet"
*/}}
{{- define "listener.certificate" -}}
{{- $gateway := .gateway -}}
{{- $listener := .listener -}}
{{- $root := .root -}}
{{- $name := include "listener.resourceName" . -}}
{{- $baseDomain := include "listener.baseDomain" . -}}
{{- $dnsNames := list -}}
{{- if $listener.certificate.wildcard -}}
{{- $dnsNames = append $dnsNames (printf "*.%s" $baseDomain) -}}
{{- else if and $listener.hostname (not (hasPrefix "*." $listener.hostname)) -}}
{{- $dnsNames = append $dnsNames (tpl $listener.hostname $root) -}}
{{- else if and $gateway.dnsName (eq $baseDomain $root.Values.baseDomain) -}}
{{- $dnsNames = append $dnsNames (printf "%s.%s" $gateway.dnsName $baseDomain) -}}
{{- end -}}
{{- range $k, $v := $listener.subdomains -}}
{{- $dnsNames = append $dnsNames (printf "%s.%s" $v $baseDomain) -}}
{{- end -}}
{{- if not $dnsNames -}}
{{- fail (printf "gateway %q listener %q: certificate.enabled is true but no DNS name can be derived. Set the listener hostname, or the gateway dnsName together with a matching baseDomain." $gateway.name $listener.name) -}}
{{- end -}}
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: {{ $name }}
  namespace: {{ $root.Release.Namespace }}
  labels:
    {{- include "labels.common" $root | nindent 4 }}
spec:
  dnsNames:
  {{- range $dnsNames }}
  - {{ . | quote }}
  {{- end }}
  issuerRef:
    group: cert-manager.io
    kind: {{ dig "issuer" "kind" "" $listener.certificate | default "Issuer" }}
    name: {{ dig "issuer" "name" "" $listener.certificate | default (dig "tlsIssuer" "name" "" $gateway) }}
  secretName: {{ printf "%s-tls" $name }}
{{- end -}}

{{/*
external-dns DNSEndpoint for a listener, one CNAME per record pointing at the gateway apex.
Takes: dict with "gateway", "listener", "root", "records" (list of FQDNs) and optionally "listenerSet"
*/}}
{{- define "listener.dnsEndpoint" -}}
{{- $gateway := .gateway -}}
{{- $listener := .listener -}}
{{- $root := .root -}}
apiVersion: externaldns.k8s.io/v1alpha1
kind: DNSEndpoint
metadata:
  name: {{ include "listener.resourceName" . }}
  namespace: {{ $root.Release.Namespace }}
  {{- with $listener.dnsEndpoints.annotations }}
  annotations:
    {{- . | toYaml | nindent 4}}
  {{- end }}
  labels:
    {{- include "labels.common" $root | nindent 4 }}
spec:
  endpoints:
  {{- range .records }}
  - dnsName: {{ . | quote }}
    recordTTL: 300
    recordType: CNAME
    targets:
    - {{ $gateway.dnsName }}.{{ $gateway.overrideBaseDomain | default $root.Values.baseDomain }}
  {{- end }}
{{- end -}}

{{/*
Whether a gateway fronts an AWS NLB, which drives the ClientTrafficPolicy defaults.
Takes: dict with "gateway" and "root"
*/}}
{{- define "gateway.isNLB" -}}
{{- if and (eq .root.Values.provider "capa") (dig "provider" "aws" "useNetworkLoadBalancer" true .gateway) -}}
true
{{- end -}}
{{- end -}}

{{/*
ClientTrafficPolicy spec body: NLB defaults with the user's values merged on top.
Takes: dict with "clientTrafficPolicy" (the user block) and "isNLB"
Returns empty when there is nothing to render, so callers can guard with "with".
*/}}
{{- define "clientTrafficPolicy.spec" -}}
{{- $defaults := dict }}
{{- if .isNLB }}
{{- $_ := set $defaults "proxyProtocol" (dict "optional" false) }}
{{- $_ := set $defaults "healthCheck" (dict "path" "/healthz") }}
{{- end }}
{{- $spec := mergeOverwrite $defaults (deepCopy (omit (.clientTrafficPolicy | default dict) "enabled")) }}
{{- if $spec }}
{{- toYaml $spec }}
{{- end }}
{{- end -}}
