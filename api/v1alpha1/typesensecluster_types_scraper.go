package v1alpha1

import corev1 "k8s.io/api/core/v1"

type DocSearchScraperSpec struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	Config string `json:"config"`

	// +kubebuilder:validation:Pattern:=`(^((\*\/)?([0-5]?[0-9])((\,|\-|\/)([0-5]?[0-9]))*|\*)\s+((\*\/)?((2[0-3]|1[0-9]|[0-9]|00))((\,|\-|\/)(2[0-3]|1[0-9]|[0-9]|00))*|\*)\s+((\*\/)?([1-9]|[12][0-9]|3[01])((\,|\-|\/)([1-9]|[12][0-9]|3[01]))*|\*)\s+((\*\/)?([1-9]|1[0-2])((\,|\-|\/)([1-9]|1[0-2]))*|\*|(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|des))\s+((\*\/)?[0-6]((\,|\-|\/)[0-6])*|\*|00|(sun|mon|tue|wed|thu|fri|sat))\s*$)|@(annually|yearly|monthly|weekly|daily|hourly|reboot)`
	// +kubebuilder:validation:Type=string
	Schedule string `json:"schedule"`

	// +kubebuilder:validation:Optional
	AuthConfiguration *corev1.LocalObjectReference `json:"authConfiguration,omitempty"`
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
