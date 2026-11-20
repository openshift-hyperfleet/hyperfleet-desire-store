{{/*
Expand the name of the chart.
*/}}
{{- define "hyperfleet-desire-store.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "hyperfleet-desire-store.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "hyperfleet-desire-store.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "hyperfleet-desire-store.labels" -}}
helm.sh/chart: {{ include "hyperfleet-desire-store.chart" . }}
{{ include "hyperfleet-desire-store.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | toString | trunc 63 | trimAll "-_." | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "hyperfleet-desire-store.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hyperfleet-desire-store.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "hyperfleet-desire-store.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "hyperfleet-desire-store.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Fail the render when the image or a required service setting is unset.
*/}}
{{- define "hyperfleet-desire-store.validateValues" -}}
{{- $registry := trim (toString .Values.image.registry) -}}
{{- if or (not $registry) (eq $registry "CHANGE_ME") -}}
{{- fail "image.registry must be set (e.g. --set image.registry=quay.io)" -}}
{{- end -}}
{{- $repository := trim (toString .Values.image.repository) -}}
{{- if or (not $repository) (eq $repository "CHANGE_ME") -}}
{{- fail "image.repository must be set (e.g. --set image.repository=openshift-hyperfleet/hyperfleet-desire-store)" -}}
{{- end -}}
{{- if not (trim (toString .Values.image.tag)) -}}
{{- fail "image.tag must be set (e.g. --set image.tag=abc1234)" -}}
{{- end -}}
{{- range $key, $value := dict "partitionClaim" .Values.config.partitionClaim "dbMaxConcurrency" .Values.config.dbMaxConcurrency "sweeperIdentity" .Values.config.sweeperIdentity "wristband.issuer" .Values.config.wristband.issuer "wristband.audience" .Values.config.wristband.audience "wristband.jwksUrl" .Values.config.wristband.jwksUrl -}}
{{- if not (trim (toString $value)) -}}
{{- fail (printf "config.%s must be set (see docs/config.md)" $key) -}}
{{- end -}}
{{- end -}}
{{- include "hyperfleet-desire-store.validateShutdownBudget" . -}}
{{- end }}

{{/*
Fail the render when the termination grace period cannot cover the preStop
sleep plus the shutdown timeout. Values the template cannot parse are
skipped; the service rejects them at startup anyway.
*/}}
{{- define "hyperfleet-desire-store.validateShutdownBudget" -}}
{{- $timeout := trim (toString .Values.config.shutdownTimeout) -}}
{{- if not $timeout -}}{{- $timeout = "20s" -}}{{- end -}}
{{- $grace := .Values.terminationGracePeriodSeconds | int -}}
{{- $sleep := 5 -}}
{{- $command := dig "preStop" "exec" "command" (list) (.Values.lifecycle | default dict) -}}
{{- if and (eq (len $command) 2) (eq (index $command 0) "sleep") (regexMatch "^[0-9]+$" (index $command 1)) -}}
{{- $sleep = index $command 1 | int -}}
{{- end -}}
{{- $seconds := -1.0 -}}
{{- if regexMatch "^[0-9]+(\\.[0-9]+)?$" $timeout -}}
{{- $seconds = $timeout | float64 -}}
{{- else if eq (trim (regexReplaceAll "[0-9]+(\\.[0-9]+)?(ms|s|m|h)" $timeout "")) "" -}}
{{- $seconds = 0.0 -}}
{{- range regexFindAll "[0-9]+(\\.[0-9]+)?(ms|s|m|h)" $timeout -1 -}}
{{- $part := . -}}
{{- $mult := 1.0 -}}
{{- if hasSuffix "ms" $part -}}
{{- $mult = 0.001 -}}
{{- $part = trimSuffix "ms" $part -}}
{{- else if hasSuffix "h" $part -}}
{{- $mult = 3600.0 -}}
{{- $part = trimSuffix "h" $part -}}
{{- else if hasSuffix "m" $part -}}
{{- $mult = 60.0 -}}
{{- $part = trimSuffix "m" $part -}}
{{- else -}}
{{- $part = trimSuffix "s" $part -}}
{{- end -}}
{{- $seconds = addf $seconds (mulf ($part | float64) $mult) -}}
{{- end -}}
{{- end -}}
{{- if ge $seconds 0.0 -}}
{{- if le ($grace | float64) (addf $seconds ($sleep | float64)) -}}
{{- fail (printf "terminationGracePeriodSeconds (%d) must exceed the preStop sleep (%d) plus config.shutdownTimeout (%s)" $grace $sleep $timeout) -}}
{{- end -}}
{{- end -}}
{{- end }}
