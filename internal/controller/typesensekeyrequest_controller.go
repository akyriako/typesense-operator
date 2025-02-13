/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const keyRequestFinalizer = "keyrequest.ts.opentelekomcloud.com/finalizer"

// TypesenseKeyRequestReconciler reconciles a TypesenseKeyRequest object
type TypesenseKeyRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	logger logr.Logger
}

var (
	keyRequestEventFilters = builder.WithPredicates(predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			// We only need to check generation changes here, because it is only
			// updated on spec changes. On the other hand RevisionVersion
			// changes also on status changes. We want to omit reconciliation
			// for status updates.
			return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration()
		},
	})
)

// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesensekeyrequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesensekeyrequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesensekeyrequests/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the TypesenseKeyRequest object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *TypesenseKeyRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.Log.WithValues("namespace", req.Namespace, "keyRequest", req.Name)
	r.logger.Info("reconciling keyRequest")

	var keyRequest tsv1alpha1.TypesenseKeyRequest
	if err := r.Get(ctx, req.NamespacedName, &keyRequest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	clusterObjectKey := client.ObjectKey{Namespace: keyRequest.Namespace, Name: keyRequest.Spec.Cluster.Name}
	var cluster tsv1alpha1.TypesenseCluster
	if err := r.Get(ctx, clusterObjectKey, &cluster); err != nil {
		return ctrl.Result{}, err
	}

	var adminKeySecret v1.Secret
	adminKeySecretObjectKey := client.ObjectKey{Namespace: keyRequest.Namespace, Name: cluster.Spec.AdminApiKey.Name}
	if err := r.Get(ctx, adminKeySecretObjectKey, &adminKeySecret); err != nil {
		return ctrl.Result{}, err
	}

	encodedValue, exists := adminKeySecret.Data["typesense-api-key"]
	if !exists {

		return ctrl.Result{}, errors.New("Typesense API key not found in secret")
	}
	decodedValue := string(encodedValue)

	// Check if the Memcached instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isKeyRequestMarkedToBeDeleted := keyRequest.GetDeletionTimestamp() != nil
	if isKeyRequestMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(&keyRequest, keyRequestFinalizer) {
			// Run finalization logic for memcachedFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			if err := r.finalizeKeyRequest(ctx, decodedValue, &keyRequest, &cluster); err != nil {
				return ctrl.Result{}, err
			}

			// Remove keyRequestFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			controllerutil.RemoveFinalizer(&keyRequest, keyRequestFinalizer)
			err := r.Update(ctx, &keyRequest)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer for this CR
	if !controllerutil.ContainsFinalizer(&keyRequest, keyRequestFinalizer) {
		controllerutil.AddFinalizer(&keyRequest, keyRequestFinalizer)
		err := r.Update(ctx, &keyRequest)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	var apiKeySecret v1.Secret
	apiKeySecretExists := true
	apiKeySecretObjectKey := client.ObjectKey{Namespace: keyRequest.Namespace, Name: keyRequest.Name}

	if err := r.Get(ctx, apiKeySecretObjectKey, &apiKeySecret); err != nil {
		if apierrors.IsNotFound(err) {
			apiKeySecretExists = false
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch secret: %s", apiKeySecret.Name))
			return ctrl.Result{}, err
		}
	}

	if apiKeySecretExists {
		return ctrl.Result{}, nil
	}

	//TODO: change url back
	//svcURL := fmt.Sprintf("http://%s:%d/keys", fmt.Sprintf(ClusterRestService, cluster.Name), cluster.Spec.ApiPort)
	svcURL := fmt.Sprintf("http://%s:%d/keys", "localhost", cluster.Spec.ApiPort)

	apiKeyResponse, err := r.CreateAPIKey(ctx, decodedValue, svcURL, keyRequest)
	if err != nil {
		r.logger.Error(err, "Failed to create the api key")
		return ctrl.Result{}, err
	}

	r.logger.Info(apiKeyResponse.Value)

	_, err = r.createApiKeySecret(ctx, apiKeySecretObjectKey, *apiKeyResponse, &cluster, &keyRequest)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *TypesenseKeyRequestReconciler) createApiKeySecret(
	ctx context.Context,
	secretObjectKey client.ObjectKey,
	response KeyResponse,
	ts *tsv1alpha1.TypesenseCluster,
	keyRequest *tsv1alpha1.TypesenseKeyRequest,
) (*v1.Secret, error) {

	secret := &v1.Secret{
		ObjectMeta: getObjectMeta(ts, &secretObjectKey.Name, nil),
		Type:       v1.SecretTypeOpaque,
		Immutable:  ptr.To(true),
		StringData: map[string]string{
			ClusterApiKeySecretIdName: fmt.Sprintf("%d", response.Id),
		},
		Data: map[string][]byte{
			ClusterApiKeySecretKeyName: []byte(response.Value),
		},
	}

	err := ctrl.SetControllerReference(keyRequest, secret, r.Scheme)
	if err != nil {
		return nil, err
	}

	err = r.Create(ctx, secret)
	if err != nil {
		return nil, err
	}

	return secret, nil
}

func (r *TypesenseKeyRequestReconciler) finalizeKeyRequest(ctx context.Context, adminApiKey string, keyRequest *tsv1alpha1.TypesenseKeyRequest, cluster *tsv1alpha1.TypesenseCluster) error {
	var apiKeySecret v1.Secret
	apiKeySecretObjectKey := client.ObjectKey{Namespace: keyRequest.Namespace, Name: keyRequest.Name}

	if err := r.Get(ctx, apiKeySecretObjectKey, &apiKeySecret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		} else {
			r.logger.Error(err, fmt.Sprintf("unable to fetch secret in finalizer: %s", apiKeySecret.Name))
			return err
		}
	}
	apiKeyID := string(apiKeySecret.Data[ClusterApiKeySecretIdName])

	//TODO: change url back
	//svcURL := fmt.Sprintf("http://%s:%d/keys", fmt.Sprintf(ClusterRestService, cluster.Name), cluster.Spec.ApiPort)
	svcURL := fmt.Sprintf("http://%s:%d/keys", "localhost", cluster.Spec.ApiPort)

	err := r.DeleteAPIKey(ctx, adminApiKey, apiKeyID, svcURL)
	if err != nil {
		r.logger.Error(err, "Failed to remove the api key")
		return err
	}
	r.logger.Info(fmt.Sprintf("Successfully finalized: %s", apiKeyID))
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TypesenseKeyRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tsv1alpha1.TypesenseKeyRequest{}, keyRequestEventFilters).
		Complete(r)
}
