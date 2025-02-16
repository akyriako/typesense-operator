package controller

import (
	"context"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
)

func (r *TypesenseKeyRequestReconciler) finalizeKeyRequest(ctx context.Context, apiKeyRequest *tsv1alpha1.TypesenseKeyRequest) error {
	r.logger.Info("starting finalizer on api key request")

	apiKeyID := apiKeyRequest.Status.KeyId
	if apiKeyID == nil {
		return nil
	}

	clusterObjectKey := client.ObjectKey{Namespace: apiKeyRequest.Namespace, Name: apiKeyRequest.Spec.Cluster.Name}
	var cluster tsv1alpha1.TypesenseCluster
	if err := r.Get(ctx, clusterObjectKey, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		} else {
			r.logger.Error(err, "unable to fetch cluster in finalizer", "finalizer", TypesenseKeyRequestFinalizer)
			return err
		}
	}

	var adminKeySecret v1.Secret
	adminKeySecretObjectKey := client.ObjectKey{Namespace: apiKeyRequest.Namespace, Name: cluster.Spec.AdminApiKey.Name}
	if err := r.Get(ctx, adminKeySecretObjectKey, &adminKeySecret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		} else {
			r.logger.Error(err, "unable to fetch admin secret in finalizer", "finalizer", TypesenseKeyRequestFinalizer)
			return err
		}
	}

	adminApiKeyBytes, exists := adminKeySecret.Data[ClusterAdminApiKeySecretKeyName]
	if !exists {
		return nil
	}
	adminApiKey := string(adminApiKeyBytes)

	r.logger.Info("deleting api key")
	err := r.deleteApiKeyHttpRequest(ctx, &cluster, adminApiKey, strconv.Itoa(int(*apiKeyID)))
	if err != nil {
		r.logger.Error(err, "deleting api key failed", "finalizer", TypesenseKeyRequestFinalizer)
		return err
	}

	r.logger.Info("completed finalizer on api key request", "finalizer", TypesenseKeyRequestFinalizer)
	return nil
}
