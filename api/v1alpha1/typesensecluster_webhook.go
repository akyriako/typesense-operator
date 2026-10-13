package v1alpha1

import (
	"context"
	"fmt"
	"os"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var typesenselog = logf.Log.WithName("typesensecluster-resource")

func (r *TypesenseCluster) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ts-opentelekomcloud-com-v1alpha1-typesensecluster,mutating=false,failurePolicy=fail,sideEffects=None,groups=ts.opentelekomcloud.com,resources=typesenseclusters,verbs=create;update,versions=v1alpha1,name=vtypesensecluster.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*TypesenseCluster] = &TypesenseCluster{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type
func (r *TypesenseCluster) ValidateCreate(ctx context.Context, obj *TypesenseCluster) (admission.Warnings, error) {
	typesenselog.Info("validate create", "name", obj.Name)
	if err := obj.validateClusterName(); err != nil {
		return nil, err
	}
	if err := obj.validateNamespace(); err != nil {
		return nil, err
	}
	return nil, obj.validateScrapers()
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type
func (r *TypesenseCluster) ValidateUpdate(ctx context.Context, oldObj, newObj *TypesenseCluster) (admission.Warnings, error) {
	typesenselog.Info("validate update", "name", newObj.Name)
	if err := newObj.validateClusterName(); err != nil {
		return nil, err
	}
	if err := newObj.validateNamespace(); err != nil {
		return nil, err
	}
	return nil, newObj.validateScrapers()
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type
func (r *TypesenseCluster) ValidateDelete(ctx context.Context, obj *TypesenseCluster) (admission.Warnings, error) {
	return nil, nil
}

func (r *TypesenseCluster) validateClusterName() error {
	if len(r.Name) > 24 {
		return fmt.Errorf("cluster name '%s' exceeds the maximum allowed length of 24 characters for Raft DNS endpoints (got %d characters)", r.Name, len(r.Name))
	}
	return nil
}

func (r *TypesenseCluster) validateNamespace() error {
	watchNamespace := strings.TrimSpace(os.Getenv("WATCH_NAMESPACE"))

	useCurrent := strings.EqualFold(strings.TrimSpace(os.Getenv("WATCH_CURRENT_NAMESPACE")), "true")
	if useCurrent {
		namespaceFile := "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
		if content, err := os.ReadFile(namespaceFile); err == nil {
			watchNamespace = strings.TrimSpace(string(content))
		}
	}

	if watchNamespace != "" && r.Namespace != watchNamespace {
		return fmt.Errorf("operator is running in gapped mode. Creation of TypesenseCluster is only allowed in namespace '%s', but got '%s'", watchNamespace, r.Namespace)
	}

	return nil
}

func (r *TypesenseCluster) validateScrapers() error {
	var errs []string
	for _, scraper := range r.Spec.Scrapers {
		cronJobName := fmt.Sprintf("%s-scraper-%s", r.Name, scraper.Name)
		if len(cronJobName) > 52 {
			errs = append(errs, fmt.Sprintf("scraper name '%s' combined with cluster name '%s' exceeds the 52 character limit for CronJobs (got %d characters: %s)", scraper.Name, r.Name, len(cronJobName), cronJobName))
		}
		// Without either, GetScraperConfig returns an empty configuration and the scraper has nothing to crawl.
		if scraper.GetScraperConfig() == "" {
			errs = append(errs, fmt.Sprintf("scraper '%s' must set either host or config", scraper.Name))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid scrapers configuration: %s", strings.Join(errs, "; "))
	}

	return nil
}
