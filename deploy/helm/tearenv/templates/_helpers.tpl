{{- define "tearenv.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "tearenv.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else if contains .Chart.Name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name (include "tearenv.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "tearenv.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "tearenv.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "tearenv.selectorLabels" -}}
app.kubernetes.io/name: {{ include "tearenv.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "tearenv.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "tearenv.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- required "serviceAccount.name is required when serviceAccount.create is false" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "tearenv.bootstrapSecretName" -}}
{{- default (printf "%s-bootstrap" (include "tearenv.fullname" .)) .Values.bootstrap.existingSecret }}
{{- end }}

{{- define "tearenv.stateClaimName" -}}
{{- default (printf "%s-state" (include "tearenv.fullname" .)) .Values.persistence.existingClaim }}
{{- end }}

{{- define "tearenv.blueprintConfigMapName" -}}
{{- default (printf "%s-blueprint" (include "tearenv.fullname" .)) .Values.blueprint.existingConfigMap }}
{{- end }}

{{- define "tearenv.registrationSecretName" -}}
{{- default (printf "%s-registration" (include "tearenv.fullname" .)) .Values.registration.token.existingSecret }}
{{- end }}
