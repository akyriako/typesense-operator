package controller

import (
	"context"
	"fmt"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	grafanaApiGroup = "grafana.integreatly.org"
)

func (r *TypesenseClusterReconciler) ReconcileGrafanaDashboard(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) error {
	if !ts.Spec.GetMetricsExporterSpecs().EnableGrafanaDashboard {
		return nil
	}

	if deployed, err := r.IsApiGroupDeployed(grafanaApiGroup); err != nil || !deployed {
		if ts.Spec.Metrics != nil {
			err := fmt.Errorf("api group %s was not found in cluster", grafanaApiGroup)
			r.logger.Error(err, "reconciling grafana dashboard skipped")
		}
		return nil
	}

	r.logger.V(debugLevel).Info("reconciling grafana dashboard")

	grafanaDashboardName := fmt.Sprintf(ClusterMetricsGrafanaDashboard, ts.Name)
	grafanaDashboardExists := true
	grafanaDashboardObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: grafanaDashboardName}

	desired, err := r.buildGrafanaDashboard(ts)
	if err != nil {
		r.logger.Error(err, "building grafana dashboard failed")
		return err
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(desired.GroupVersionKind())
	err = r.Get(ctx, grafanaDashboardObjectKey, existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			grafanaDashboardExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch grafana dashboard: %s", grafanaDashboardName))
			return err
		}
	}

	metricsSpecs := ts.Spec.GetMetricsExporterSpecs()
	if metricsSpecs.EnableGrafanaDashboard == false {
		if grafanaDashboardExists {
			err := r.Delete(ctx, existing)
			if err != nil {
				r.logger.Error(err, "deleting grafana dashboard failed", "grafana_dashboard", grafanaDashboardObjectKey.Name)
				return err
			}
		}

		return nil
	}

	if !grafanaDashboardExists {
		r.logger.V(debugLevel).Info("creating grafana dashboard", "grafana_dashboard", grafanaDashboardObjectKey.Name)

		err := func(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) error {
			err := r.Create(ctx, desired)
			if err != nil {
				return err
			}

			err = ctrl.SetControllerReference(ts, desired, r.Scheme)
			if err != nil {
				return err
			}

			return nil
		}(ctx, ts)
		if err != nil {
			r.logger.Error(err, "creating grafana dashboard failed", "grafana_dashboard", grafanaDashboardObjectKey.Name)
			return err
		}

		return nil
	}

	existingSpec, found, err := unstructured.NestedMap(existing.Object, "spec")
	if err != nil {
		r.logger.Error(err, "reading existing grafana dashboard spec failed", "grafana_dashboard", grafanaDashboardObjectKey.Name)
		return err
	}
	if !found {
		existingSpec = map[string]interface{}{}
	}

	desiredSpec, found, err := unstructured.NestedMap(desired.Object, "spec")
	if err != nil {
		r.logger.Error(err, "reading desired grafana dashboard spec failed", "grafana_dashboard", grafanaDashboardObjectKey.Name)
		return err
	}
	if !found {
		desiredSpec = map[string]interface{}{}
	}

	if fmt.Sprintf("%v", existingSpec) == fmt.Sprintf("%v", desiredSpec) &&
		fmt.Sprintf("%v", existing.GetLabels()) == fmt.Sprintf("%v", desired.GetLabels()) {
		return nil
	}

	existing.Object["spec"] = desiredSpec
	existing.SetLabels(desired.GetLabels())

	r.logger.V(debugLevel).Info("updating grafana dashboard", "grafana_dashboard", grafanaDashboardObjectKey.Name)
	err = r.Update(ctx, existing)
	if err != nil {
		r.logger.Error(err, "updating grafana dashboard failed", "grafana_dashboard", grafanaDashboardObjectKey.Name)
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) buildGrafanaDashboard(ts *tsv1alpha1.TypesenseCluster) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   grafanaApiGroup,
		Version: "v1beta1",
		Kind:    "GrafanaDashboard",
	})
	u.SetName(fmt.Sprintf(ClusterMetricsGrafanaDashboard, ts.Name))
	u.SetNamespace(ts.Namespace)
	u.SetLabels(getMergedLabels(getDefaultLabels(ts), getLabels(ts)))

	if err := unstructured.SetNestedField(
		u.Object,
		map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"dashboards": "grafana",
			},
		},
		"spec", "instanceSelector",
	); err != nil {
		return nil, err
	}

	if err := unstructured.SetNestedField(
		u.Object,
		true,
		"spec", "allowCrossNamespaceImport",
	); err != nil {
		return nil, err
	}

	if err := unstructured.SetNestedField(
		u.Object,
		map[string]interface{}{
			"name": fmt.Sprintf(ClusterMetricsGrafanaDashboard, ts.Name),
			"key":  "dashboard.json",
		},
		"spec", "configMapRef",
	); err != nil {
		return nil, err
	}

	return u, nil
}
