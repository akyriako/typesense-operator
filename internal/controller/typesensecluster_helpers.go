package controller

import (
	"context"
	"fmt"
	"strings"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/version"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/csaupgrade"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const operatorFieldOwner = "typesense-operator"

// canManageNamespace reports whether the operator can read and write objects in namespace. In gapped mode
// both its cache and its RBAC are limited to the watched namespace.
func (r *TypesenseClusterReconciler) canManageNamespace(namespace string) bool {
	return r.WatchNamespace == "" || namespace == r.WatchNamespace
}

// migrateLegacyFieldOwnership moves the fields of obj that are owned by the operator's client-side
// manager (objects created via Create before they were server-side applied) to operatorFieldOwner.
// Without this, fields that were later removed from the spec stay co-owned by the legacy manager and
// server-side apply never prunes them. obj must be the live object, including its managedFields.
func (r *TypesenseClusterReconciler) migrateLegacyFieldOwnership(ctx context.Context, obj client.Object) error {
	patch, err := csaupgrade.UpgradeManagedFieldsPatch(obj, sets.New(r.legacyFieldManager()), operatorFieldOwner)
	if err != nil {
		return err
	}

	if patch == nil {
		return nil
	}

	return r.Patch(ctx, obj, client.RawPatch(types.JSONPatchType, patch))
}

// legacyFieldManager returns the field manager the api server records for requests without an explicit
// field owner: the user agent up to the first "/".
func (r *TypesenseClusterReconciler) legacyFieldManager() string {
	userAgent := rest.DefaultKubernetesUserAgent()
	if r.Configuration != nil && r.Configuration.UserAgent != "" {
		userAgent = r.Configuration.UserAgent
	}

	manager, _, _ := strings.Cut(userAgent, "/")
	return manager
}

func (r *TypesenseClusterReconciler) patchStatus(
	ctx context.Context,
	ts *tsv1alpha1.TypesenseCluster,
	patcher func(status *tsv1alpha1.TypesenseClusterStatus),
) error {
	patch := client.MergeFrom(ts.DeepCopy())
	patcher(&ts.Status)

	// Keep ObservedGeneration in sync with the current metadata.generation
	ts.Status.ObservedGeneration = ts.Generation

	err := r.Status().Patch(ctx, ts, patch)
	if err != nil {
		log.FromContext(ctx).Error(err, "unable to patch typesense cluster status")
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) IsFeatureSupported(minimum string) (bool, string, error) {
	serverVersion, err := r.getServerVersion()
	if err != nil {
		return false, "", err
	}

	ver, err := version.ParseGeneric(serverVersion)
	if err != nil {
		return false, serverVersion, err
	}

	req, err := version.ParseGeneric(fmt.Sprintf("v%s", minimum))
	if err != nil {
		return false, serverVersion, err
	}

	return ver.AtLeast(req), serverVersion, nil
}

// getServerVersion caches the Kubernetes server version; reconciles may run concurrently.
func (r *TypesenseClusterReconciler) getServerVersion() (string, error) {
	r.serverVersionMu.Lock()
	defer r.serverVersionMu.Unlock()

	if r.serverVersion == "" {
		info, err := r.DiscoveryClient.ServerVersion()
		if err != nil {
			return "", err
		}
		r.serverVersion = info.GitVersion
	}

	return r.serverVersion, nil
}

func (r *TypesenseClusterReconciler) IsApiGroupDeployed(apiGroup string) (bool, error) {
	apiGroupList, err := r.DiscoveryClient.ServerGroups()
	if err != nil {
		return false, err
	}

	for _, ag := range apiGroupList.Groups {
		if ag.Name == apiGroup {
			return true, nil
		}
	}

	return false, nil
}
