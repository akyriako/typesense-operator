package v1alpha1

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

type DocSearchScraperSpec struct {
	Name string `json:"name"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="typesense/docsearch-scraper:0.12.0.rc14"
	Image string `json:"image,omitempty"`

	// +optional
	// +kubebuilder:default=https
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Enum=http;https
	Protocol string `json:"protocol,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern:=`^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])(\.([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]{0,61}[a-zA-Z0-9]))*$`
	Host *string `json:"host,omitempty"`

	// +kubebuilder:validation:Optional
	Config *string `json:"config,omitempty"`

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

func (s *DocSearchScraperSpec) GetScraperConfig() string {
	if s.Config != nil {
		return *s.Config
	}

	if s.Host == nil {
		return ""
	}

	host := *s.Host
	config := struct {
		IndexName             string                 `json:"index_name"`
		StartURLs             []string               `json:"start_urls"`
		SitemapURLs           []string               `json:"sitemap_urls"`
		SitemapAlternateLinks bool                   `json:"sitemap_alternate_links"`
		StopURLs              []string               `json:"stop_urls"`
		Selectors             map[string]interface{} `json:"selectors"`
		StripChars            string                 `json:"strip_chars"`
		CustomSettings        map[string]interface{} `json:"custom_settings"`
	}{
		IndexName:             s.Name,
		StartURLs:             []string{fmt.Sprintf("%s://%s", s.Protocol, host)},
		SitemapURLs:           []string{fmt.Sprintf("%s://%s/sitemap.xml", s.Protocol, host)},
		SitemapAlternateLinks: false,
		StopURLs:              []string{"/tests"},
		StripChars:            " .,;:#",

		Selectors: map[string]interface{}{
			"lvl0": map[string]interface{}{
				"selector":      "nav.menu a.menu__link--active, .navbar__item.navbar__link--active",
				"default_value": "Documentation",
			},
			"lvl1": "article h1, header h1",
			"lvl2": "article h2",
			"lvl3": "article h3",
			"lvl4": "article h4",
			"lvl5": "article h5",
			"lvl6": "article h6",
			"text": "article p, article li, article td",
		},

		CustomSettings: map[string]interface{}{
			"separatorsToIndex": "_",
			"attributesForFaceting": []string{
				"language",
				"version",
				"type",
				"docusaurus_tag",
			},
			"attributesToRetrieve": []string{
				"hierarchy",
				"content",
				"anchor",
				"url",
				"url_without_anchor",
				"type",
			},
		},
	}

	configJson, err := json.Marshal(config)
	if err != nil {
		return ""
	}

	return string(configJson)
}
