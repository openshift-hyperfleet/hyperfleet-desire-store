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
sleep plus the shutdown timeout. The preStop hook must be absent or a single
sleep in one of the shapes read below; any other hook fails the render. A
timeout the template cannot read as a non-negative Go duration also fails it.
*/}}
{{- define "hyperfleet-desire-store.validateShutdownBudget" -}}
{{- $timeout := trim (toString .Values.config.shutdownTimeout) -}}
{{- if not $timeout -}}{{- $timeout = "20s" -}}{{- end -}}
{{- $grace := .Values.terminationGracePeriodSeconds | int -}}
{{- $sleep := 0 -}}
{{- $preStop := dig "preStop" dict (.Values.lifecycle | default dict) -}}
{{- if $preStop -}}
{{- $sleep = -1 -}}
{{- $command := list -}}
{{- range (dig "exec" "command" (list) $preStop) -}}
{{- $command = append $command (toString .) -}}
{{- end -}}
{{- $native := toString (dig "sleep" "seconds" "" $preStop) -}}
{{- if ne (len (keys $preStop)) 1 -}}
{{- else if regexMatch "^[0-9]+$" $native -}}
{{- $sleep = $native | int -}}
{{- else if and (eq (len $command) 2) (eq (index $command 0) "sleep") (regexMatch "^[0-9]+$" (index $command 1)) -}}
{{- $sleep = index $command 1 | int -}}
{{- else if and (eq (len $command) 3) (regexMatch "^(/usr)?(/bin/)?(ba)?sh$" (index $command 0)) (eq (index $command 1) "-c") (regexMatch "^\\s*sleep\\s+[0-9]+\\s*$" (index $command 2)) -}}
{{- $sleep = regexFind "[0-9]+" (index $command 2) | int -}}
{{- end -}}
{{- if lt $sleep 0 -}}
{{- fail "lifecycle.preStop must be sleep.seconds, exec [\"sleep\", \"N\"] or exec [\"/bin/sh\", \"-c\", \"sleep N\"], so the render can check terminationGracePeriodSeconds against it" -}}
{{- end -}}
{{- end -}}
{{- $unit := "([0-9]+(\\.[0-9]*)?|\\.[0-9]+)(ns|us|µs|μs|ms|s|m|h)" -}}
{{- $duration := trimPrefix "+" $timeout -}}
{{- $seconds := -1.0 -}}
{{- if regexMatch "^[0-9]+(\\.[0-9]+)?$" $duration -}}
{{- $seconds = $duration | float64 -}}
{{- else if regexMatch (printf "^(%s)+$" $unit) $duration -}}
{{- $seconds = 0.0 -}}
{{- range regexFindAll $unit $duration -1 -}}
{{- $part := . -}}
{{- $mult := 1.0 -}}
{{- range $suffix, $m := dict "ns" 0.000000001 "us" 0.000001 "µs" 0.000001 "μs" 0.000001 "ms" 0.001 -}}
{{- if hasSuffix $suffix $part -}}
{{- $mult = $m -}}
{{- $part = trimSuffix $suffix $part -}}
{{- end -}}
{{- end -}}
{{- if eq $mult 1.0 -}}
{{- if hasSuffix "h" $part -}}
{{- $mult = 3600.0 -}}
{{- $part = trimSuffix "h" $part -}}
{{- else if hasSuffix "m" $part -}}
{{- $mult = 60.0 -}}
{{- $part = trimSuffix "m" $part -}}
{{- else -}}
{{- $part = trimSuffix "s" $part -}}
{{- end -}}
{{- end -}}
{{- $seconds = addf $seconds (mulf (printf "0%s" $part | float64) $mult) -}}
{{- end -}}
{{- end -}}
{{- if lt $seconds 0.0 -}}
{{- fail (printf "config.shutdownTimeout (%s) must be a non-negative Go duration such as 20s or 1m30s" $timeout) -}}
{{- end -}}
{{- if le ($grace | float64) (addf $seconds ($sleep | float64)) -}}
{{- fail (printf "terminationGracePeriodSeconds (%d) must exceed the preStop sleep (%d) plus config.shutdownTimeout (%s)" $grace $sleep $timeout) -}}
{{- end -}}
{{- end }}
