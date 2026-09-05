{{- define "kube-oom-policy.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}

{{/* Include namespace identity so releases in different namespaces can coexist. */}}
{{- define "kube-oom-policy.rbacName" -}}
{{- $identity := printf "%s/%s" .Release.Namespace .Release.Name -}}
{{- printf "%s-%s-%s" (.Release.Namespace | trunc 25 | trimSuffix "-") (.Release.Name | trunc 25 | trimSuffix "-") ($identity | sha256sum | trunc 8) -}}
{{- end }}

{{/* Use the same node conditions for scheduling and application revalidation. */}}
{{- define "kube-oom-policy.nodeSelector" -}}
{{- $parts := list -}}
{{- range $key, $value := .Values.nodeSelector -}}
{{- $parts = append $parts (printf "%s=%s" $key $value) -}}
{{- end -}}
{{- range .Values.nodeSelectorExpressions -}}
{{- if eq .operator "In" -}}
{{- $parts = append $parts (printf "%s in (%s)" .key (join "," .values)) -}}
{{- else if eq .operator "NotIn" -}}
{{- $parts = append $parts (printf "%s notin (%s)" .key (join "," .values)) -}}
{{- else if eq .operator "Exists" -}}
{{- $parts = append $parts .key -}}
{{- else if eq .operator "DoesNotExist" -}}
{{- $parts = append $parts (printf "!%s" .key) -}}
{{- end -}}
{{- end -}}
{{- join "," $parts -}}
{{- end }}
