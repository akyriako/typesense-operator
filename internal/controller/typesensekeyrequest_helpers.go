package controller

import (
	"context"
	"errors"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
)

func (r *TypesenseKeyRequestReconciler) getApiKeyValue(ctx context.Context, apiKeySecretObjectKey types.NamespacedName) (string, error) {
	var apiKeySecret v1.Secret
	if err := r.Get(ctx, apiKeySecretObjectKey, &apiKeySecret); err != nil {
		r.logger.Error(err, "getting admin api key failed", "secret", apiKeySecretObjectKey.Name)
		return "", err
	}

	apiKeyValueAsBytes, exists := apiKeySecret.Data[ClusterAdminApiKeySecretKeyName]
	if !exists {
		err := errors.New("typesense-api-key key-pair was not found in secret")
		r.logger.Error(err, "getting api key failed", "secret", apiKeySecretObjectKey.Name)
		return "", err
	}

	apiKeyValue := string(apiKeyValueAsBytes)
	return apiKeyValue, nil
}

func (r *TypesenseKeyRequestReconciler) updateKeyRequestReadyStatus(ctx context.Context, apiKeyRequest *tsv1alpha1.TypesenseKeyRequest, ready bool) (ctrl.Result, error) {
	newStatus := apiKeyRequest.Status
	newStatus.Ready = ready

	return r.updateKeyRequestStatus(ctx, apiKeyRequest, newStatus)
}

func (r *TypesenseKeyRequestReconciler) updateKeyRequestKeyIdStatus(ctx context.Context, apiKeyRequest *tsv1alpha1.TypesenseKeyRequest, apiKeyResponse *CreateApiKeySuccessHttpResponse,
) (ctrl.Result, error) {
	newStatus := apiKeyRequest.Status
	newStatus.KeyId = ptr.To(uint32(apiKeyResponse.Id))

	return r.updateKeyRequestStatus(ctx, apiKeyRequest, newStatus)
}
