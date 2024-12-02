package controller

import (
	"context"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const adminApiKeyName = "typesense-api-key"

func (r *TypesenseClusterReconciler) ReconcileSecret(ctx context.Context, ts tsv1alpha1.TypesenseCluster) error {
	secretName := fmt.Sprintf("%s-admin-key", ts.Name)
	secretExists := true
	r.logger.V(debugLevel).Info("reconciling api key")

	secretObjectKey := client.ObjectKey{
		Namespace: ts.Namespace,
		Name:      secretName,
	}

	var secret = &v1.Secret{}
	if err := r.Get(ctx, secretObjectKey, secret); err != nil {
		if apierrors.IsNotFound(err) {
			secretExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch secret: %s", secretName))
		}
	}

	if !secretExists {
		r.logger.V(debugLevel).Info("creating admin api key", "secret", secretObjectKey)

		_, err := r.createAdminApiKey(ctx, secretObjectKey, &ts)
		if err != nil {
			r.logger.Error(err, "creating admin api key failed", "secret", secretObjectKey)
			return err
		}
		return nil
	}
	return nil
}

func (r *TypesenseClusterReconciler) createAdminApiKey(
	ctx context.Context,
	secretObjectKey client.ObjectKey,
	ts *tsv1alpha1.TypesenseCluster,
) (*v1.Secret, error) {
	token, err := generateToken()
	if err != nil {
		return nil, err
	}

	secret := &v1.Secret{
		ObjectMeta: getObjectMeta(ts, &secretObjectKey.Name),
		Type:       v1.SecretTypeOpaque,
		Data: map[string][]byte{
			adminApiKeyName: []byte(token),
		},
	}

	err = ctrl.SetControllerReference(ts, secret, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, secret)
	if err != nil {
		return nil, err
	}

	return secret, nil
}