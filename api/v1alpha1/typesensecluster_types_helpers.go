package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *TypesenseClusterSpec) GetResources() corev1.ResourceRequirements {
	if s.Resources != nil {
		return *s.Resources
	}

	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1000m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}
}

func (s *TypesenseClusterSpec) GetAdditionalServerConfiguration() []corev1.EnvFromSource {
	if s.AdditionalServerConfiguration != nil {
		return []corev1.EnvFromSource{
			{
				ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: *s.AdditionalServerConfiguration,
				},
			},
		}
	}

	return []corev1.EnvFromSource{}
}

func (s *DocSearchScraperSpec) GetScraperAuthConfiguration() []corev1.EnvFromSource {
	if s.AuthConfiguration != nil {
		return []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: *s.AuthConfiguration,
				},
			},
		}
	}

	return []corev1.EnvFromSource{}
}

func (s *TypesenseClusterSpec) GetCorsDomains() string {
	if s.CorsDomains == nil {
		return ""
	}
	return *s.CorsDomains
}

func (s *TypesenseClusterSpec) GetStorage() StorageSpec {
	if s.Storage != nil {
		return *s.Storage
	}

	return StorageSpec{
		Size:             resource.MustParse("100Mi"),
		StorageClassName: "standard",
	}
}

func (s *TypesenseClusterSpec) GetSnapshotStorage() SnapshotStorageSpec {
	if s.Backup.SnapshotStorage != nil {
		return *s.Backup.SnapshotStorage
	}

	return SnapshotStorageSpec{
		Auto:             false,
		Size:             resource.MustParse("100Mi"),
		StorageClassName: "standard",
	}
}

var (
	defaultBackupPreHookCommand = []string{
		"sh",
		"-lc",
		`set -euo pipefail

		exec 1>>/proc/1/fd/1
		exec 2>>/proc/1/fd/2
		
		log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')][$1] $2"; }
		log_error() { echo "[$(date '+%Y-%m-%d %H:%M:%S')][ERROR] $1" >&2; }
		trap 'log_error "Backup pre-hook failed at line $LINENO"' ERR
		
		SNAP_DIR="${TYPESENSE_SNAPSHOTS_DIR:-/snapshots}/"
		log INFO "Snapshot path: ${SNAP_DIR}"
		
		RESPONSE=$(curl -sS --fail --no-progress-meter -X POST "http://127.0.0.1:8108/operations/snapshot?snapshot_path=${SNAP_DIR}" \
		  -H "X-TYPESENSE-API-KEY: ${TYPESENSE_API_KEY}" \
		  -H "Content-Type: application/json")
		
		log INFO "Snapshot API response: ${RESPONSE}"
        ls -lhR "${SNAP_DIR}" || true

		`,
	}

	defaultBackupPostHookCommand = []string{"sh", "-lc"}
)

func (s *TypesenseClusterSpec) GetDefaultBackupHook() *ActionHooksSpec {
	if s.Backup != nil && s.Backup.BackupHooks != nil {
		return s.Backup.BackupHooks
	}

	return &ActionHooksSpec{
		Pre: &HookSpec{
			TimeoutInMinutes: 10,
			OnErrorPolicy:    "Fail",
			CommandOverride:  defaultBackupPreHookCommand,
		},
		Post: nil,
	}
}

func (s *TypesenseClusterSpec) GetTopologySpreadConstraints(labels map[string]string) []corev1.TopologySpreadConstraint {
	tscs := make([]corev1.TopologySpreadConstraint, 0)

	for _, tsc := range s.TopologySpreadConstraints {
		if tsc.LabelSelector == nil {
			tsc.LabelSelector = &metav1.LabelSelector{
				MatchLabels: labels,
			}
		}
		tscs = append(tscs, tsc)
	}
	return tscs
}

func (s *TypesenseClusterSpec) GetMetricsExporterSpecs() MetricsExporterSpec {
	if s.Metrics != nil {
		return *s.Metrics
	}

	return MetricsExporterSpec{
		Release:           "promstack",
		Image:             "akyriako78/typesense-prometheus-exporter:0.1.9",
		IntervalInSeconds: 15,
	}
}

func (s *TypesenseClusterSpec) GetMetricsExporterResources() corev1.ResourceRequirements {
	if s.Metrics != nil && s.Metrics.Resources != nil {
		return *s.Metrics.Resources
	}

	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("32Mi"),
		},
	}
}

func (s *TypesenseClusterSpec) GetHealthCheckSidecarSpecs() HealthCheckSpec {
	if s.HealthCheck != nil {
		return *s.HealthCheck
	}

	return HealthCheckSpec{
		Image: "akyriako78/typesense-healthcheck:0.1.8",
	}
}

func (s *TypesenseClusterSpec) GetHealthCheckSidecarResources() corev1.ResourceRequirements {
	if s.HealthCheck != nil && s.HealthCheck.Resources != nil {
		return *s.HealthCheck.Resources
	}

	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("32Mi"),
		},
	}
}

func (s *TypesenseClusterSpec) GetHooksSidecarResources() corev1.ResourceRequirements {
	//if s.Backup != nil && s.Backup. != nil {
	//	return *s.HealthCheck.Resources
	//}

	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("32Mi"),
		},
	}
}

func (s *IngressSpec) GetReverseProxyResources() corev1.ResourceRequirements {
	if s.Resources != nil {
		return *s.Resources
	}

	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("150m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("32Mi"),
		},
	}
}
