{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "humio.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels - base labels shared across all components.
*/}}
{{- define "humio.labels" -}}
app: '{{ .Chart.Name }}'
app.kubernetes.io/name: '{{ .Chart.Name }}'
app.kubernetes.io/instance: '{{ .Release.Name }}'
app.kubernetes.io/managed-by: '{{ .Release.Service }}'
{{- if .Values.commonLabels }}
{{ toYaml .Values.commonLabels }}
{{- end }}
{{- end }}

{{/*
Component-specific labels - includes common labels plus component.
*/}}
{{- define "humio.componentLabels" -}}
{{ include "humio.labels" . }}
app.kubernetes.io/component: '{{ .component }}'
{{- end }}

{{/*
Operator labels.
*/}}
{{- define "humio.operatorLabels" -}}
{{- $component := dict "component" "operator" -}}
{{- include "humio.componentLabels" (merge $component .) -}}
{{- end }}

{{/*
Webhook labels.
*/}}
{{- define "humio.webhookLabels" -}}
{{- $component := dict "component" "webhook" -}}
{{- include "humio.componentLabels" (merge $component .) -}}
{{- end }}