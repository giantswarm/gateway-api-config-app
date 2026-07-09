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
  With healthcheck detection (~20s) + draining_interval_seconds (120s) ~= 140s, a
  minDrainDuration of 150s keeps envoy (and therefore the node) alive until all
  in-flight NLB flows have moved off the node, avoiding RST/520 on node disruption.
*/}}
{{- if and (eq .provider "capa") (dig "provider" "aws" "useNetworkLoadBalancer" true .gateway) }}
{{- $_ := set $shutdown "drainTimeout" "170s" }}
{{- $_ := set $shutdown "minDrainDuration" "150s" }}
{{- end }}
{{- $shutdown | toYaml }}
{{- end }}

{{/*
Gateway EnvoyDeployment defaults - computes provider-specific deployment configuration.
For AWS NLBs this reduces voluntary Karpenter churn on gateway nodes, ensures the
pod's terminationGracePeriodSeconds stays above the drain timeout, and spreads the
proxy pods one-per-node so each NLB instance target maps to a single envoy.
*/}}
{{- define "gateway.envoyDeploymentDefaults" -}}
{{- $envoyDeployment := dict }}
{{- if and (eq .provider "capa") (dig "provider" "aws" "useNetworkLoadBalancer" true .gateway) }}
{{- $pod := dict }}
{{- /* Stop Karpenter consolidation/drift/expiry from churning gateway nodes */}}
{{- $_ := set $pod "annotations" (dict "karpenter.sh/do-not-disrupt" "true") }}
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
       It must stay above shutdown.drainTimeout (170s). */}}
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
