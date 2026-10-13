package v1alpha1

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestValidateScrapersRequiresHostOrConfig(t *testing.T) {
	cases := []struct {
		name    string
		scraper DocSearchScraperSpec
		wantErr bool
	}{
		{"host only", DocSearchScraperSpec{Name: "docs", Host: ptr.To("docs.example.com")}, false},
		{"config only", DocSearchScraperSpec{Name: "docs", Config: ptr.To(`{"index_name":"docs"}`)}, false},
		{"neither", DocSearchScraperSpec{Name: "docs"}, true},
		{"empty config", DocSearchScraperSpec{Name: "docs", Config: ptr.To("")}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := &TypesenseCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "foo"},
				Spec:       TypesenseClusterSpec{Scrapers: []DocSearchScraperSpec{tc.scraper}},
			}

			err := ts.validateScrapers()
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateScrapers() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "must set either host or config") {
				t.Errorf("unexpected error message: %v", err)
			}
		})
	}
}
