package controller

import (
	"context"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//func (r *TypesenseClusterReconciler) ReconcileApiKeySecret(ctx context.Context) error {
//
//}

func (r *TypesenseKeyRequestReconciler) createApiKeySecret(
	ctx context.Context,
	apiKeySecretObjectKey client.ObjectKey,
	apiKeyRequest *tsv1alpha1.TypesenseKeyRequest,
	apiKeyResponse *CreateApiKeySuccessHttpResponse,
	ts *tsv1alpha1.TypesenseCluster,
) (*v1.Secret, error) {

	secret := &v1.Secret{
		ObjectMeta: getObjectMeta(ts, &apiKeySecretObjectKey.Name, nil),
		Type:       v1.SecretTypeOpaque,
		Immutable:  ptr.To(true),
		Data: map[string][]byte{
			ClusterApiKeySecretKeyName: []byte(apiKeyResponse.Value),
		},
	}

	err := ctrl.SetControllerReference(apiKeyRequest, secret, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, secret)
	if err != nil {
		return nil, err
	}

	return secret, nil
}
