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
{{- range $key, $value := dict "partitionClaim" .Values.config.partitionClaim "sweeperIdentity" .Values.config.sweeperIdentity "wristband.issuer" .Values.config.wristband.issuer "wristband.audience" .Values.config.wristband.audience "wristband.jwksUrl" .Values.config.wristband.jwksUrl -}}
{{- if not (trim (toString $value)) -}}
{{- fail (printf "config.%s must be set (see docs/config.md)" $key) -}}
{{- end -}}
{{- end -}}
{{- range .Values.extraEnv -}}
{{- if eq (toString .name) "SHUTDOWN_TIMEOUT" -}}
{{- fail "extraEnv must not set SHUTDOWN_TIMEOUT. Use config.shutdownTimeoutSeconds." -}}
{{- end -}}
{{- end -}}
{{- include "hyperfleet-desire-store.validateShutdownBudget" . -}}
{{- end }}

{{/*
Fail the render unless the termination grace period exceeds the preStop sleep
plus the shutdown timeout. Accept only a single sleep as a preStop hook.
*/}}
{{- define "hyperfleet-desire-store.validateShutdownBudget" -}}
{{- $timeout := int64 .Values.config.shutdownTimeoutSeconds -}}
{{- $grace := int64 .Values.terminationGracePeriodSeconds -}}
{{- $sleep := int64 0 -}}
{{- $preStop := dig "preStop" dict (.Values.lifecycle | default dict) -}}
{{- if $preStop -}}
{{- $operand := "" -}}
{{- $command := list -}}
{{- range (dig "exec" "command" (list) $preStop) -}}
{{- $command = append $command (toString .) -}}
{{- end -}}
{{- if ne (len (keys $preStop)) 1 -}}
{{- else if hasKey $preStop "sleep" -}}
{{- $operand = toJson (dig "sleep" "seconds" "" $preStop) -}}
{{- else if and (eq (len $command) 2) (eq (index $command 0) "sleep") -}}
{{- $operand = index $command 1 -}}
{{- else if and (eq (len $command) 3) (regexMatch "^(/usr)?(/bin/)?(ba)?sh$" (index $command 0)) (eq (index $command 1) "-c") (regexMatch "^\\s*sleep\\s+[0-9]+\\s*$" (index $command 2)) -}}
{{- $operand = regexFind "[0-9]+" (index $command 2) -}}
{{- end -}}
{{- if not (regexMatch "^[0-9]+$" $operand) -}}
{{- fail "lifecycle.preStop must be sleep.seconds, exec [\"sleep\", \"N\"] or exec [\"/bin/sh\", \"-c\", \"sleep N\"], so the render can check terminationGracePeriodSeconds against it" -}}
{{- end -}}
{{/* Strip leading zeros to force base 10. A failed conversion returns zero. */}}
{{- $decimal := regexReplaceAll "^0+" $operand "" | default "0" -}}
{{- $sleep = int64 $decimal -}}
{{- if ne (toString $sleep) $decimal -}}
{{- fail (printf "lifecycle.preStop sleep %q must fit in a signed 64-bit integer" $operand) -}}
{{- end -}}
{{- end -}}
{{- if le (sub $grace $sleep) $timeout -}}
{{- fail (printf "terminationGracePeriodSeconds (%d) must exceed the preStop sleep (%d) plus config.shutdownTimeoutSeconds (%d)" $grace $sleep $timeout) -}}
{{- end -}}
{{- end }}
