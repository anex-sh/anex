{{/* Base chart name */}}
{{- define "chart.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/* Smart fullname:
     1) fullnameOverride wins
     2) If release name already includes/equals chart name, use the release name as-is
     3) Otherwise: <release>-<chart>
*/}}
{{- define "chart.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else if or (eq .Release.Name (include "chart.name" .)) (contains (include "chart.name" .) .Release.Name) (contains .Release.Name (include "chart.name" .)) -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "chart.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end }}
