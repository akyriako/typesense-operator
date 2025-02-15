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
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"strconv"
	"time"
)

const TypesenseKeyRequestFinalizer = "typesensekeyrequest.ts.opentelekomcloud.com/finalizer"

// TypesenseKeyRequestReconciler reconciles a TypesenseKeyRequest object
type TypesenseKeyRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	logger logr.Logger
}

var (
	typesenseKeyRequestEventFilters = builder.WithPredicates(predicate.Funcs{
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
	r.logger = log.Log.WithName("TypesenseKeyRequest").WithValues("namespace", req.Namespace, "apiKeyRequest", req.Name)

	var apiKeyRequest tsv1alpha1.TypesenseKeyRequest
	if err := r.Get(ctx, req.NamespacedName, &apiKeyRequest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	r.logger = r.logger.WithValues("cluster", apiKeyRequest.Spec.Cluster.Name)
	r.logger.Info("reconciling api key request")

	// Check if the TypesenseKeyRequest instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	if apiKeyRequest.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(&apiKeyRequest, TypesenseKeyRequestFinalizer) {
			// Run finalization logic for TypesenseKeyRequestFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			if err := r.finalizeKeyRequest(ctx, &apiKeyRequest); err != nil {
				return ctrl.Result{}, err
			}

			r.logger.V(debugLevel).Info("removing finalizer")

			// Remove TypesenseKeyRequestFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			controllerutil.RemoveFinalizer(&apiKeyRequest, TypesenseKeyRequestFinalizer)
			err := r.Update(ctx, &apiKeyRequest)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		r.logger.Info("reconciling api key request completed")
		return ctrl.Result{}, nil
	}

	// Add finalizer for TypesenseKeyRequest
	if !controllerutil.ContainsFinalizer(&apiKeyRequest, TypesenseKeyRequestFinalizer) {
		r.logger.V(debugLevel).Info("adding finalizer")

		controllerutil.AddFinalizer(&apiKeyRequest, TypesenseKeyRequestFinalizer)
		err := r.Update(ctx, &apiKeyRequest)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// The apiKey is already created and the reconciliation should exit immediately.
	if apiKeyRequest.Status.KeyId != nil && apiKeyRequest.Status.Ready {
		r.logger.Info("reconciling api key request skipped: key already created")
		return ctrl.Result{}, nil
	}

	clusterObjectKey := client.ObjectKey{Namespace: apiKeyRequest.Namespace, Name: apiKeyRequest.Spec.Cluster.Name}
	var cluster tsv1alpha1.TypesenseCluster
	if err := r.Get(ctx, clusterObjectKey, &cluster); err != nil {
		r.logger.Error(err, "fetching typesense cluster ref failed")
		return ctrl.Result{}, err
	}

	clusterCondition := cluster.Status.Conditions[0]
	if clusterCondition.Type == ConditionTypeReady && clusterCondition.Status != metav1.ConditionTrue {
		err := fmt.Errorf("cluster %s is not ready", clusterObjectKey.String())
		r.logger.Error(err, "reconciling api key request postponed")

		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	adminKeySecretObjectKey := client.ObjectKey{Namespace: apiKeyRequest.Namespace, Name: cluster.Spec.AdminApiKey.Name}
	adminApiKeyValue, err := r.getApiKeyValue(ctx, adminKeySecretObjectKey)
	if err != nil {
		return ctrl.Result{}, err
	}

	if apiKeyRequest.Status.KeyId != nil && !apiKeyRequest.Status.Ready {
		err := r.deleteApiKeyHttpRequest(ctx, &cluster, adminApiKeyValue, strconv.Itoa(int(*apiKeyRequest.Status.KeyId)))
		if err != nil {
			r.logger.Error(err, "deleting api key failed")
			return ctrl.Result{}, err
		}

		_, err = r.updateKeyRequestKeyIdStatus(ctx, &apiKeyRequest, nil)
		if err != nil {
			return ctrl.Result{}, err
		}

		r.logger.V(debugLevel).Info("removed lingering api keys and requeuing")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	var apiKeyValue *string = nil
	if apiKeyRequest.Spec.ApiKeySecret != nil {
		apiKeySecretRef := apiKeyRequest.Spec.ApiKeySecret
		apiKeySecretObjectKey := client.ObjectKey{Namespace: apiKeySecretRef.Namespace, Name: apiKeySecretRef.Name}
		akv, err := r.getApiKeyValue(ctx, apiKeySecretObjectKey)
		if err != nil {
			return ctrl.Result{}, err
		}

		apiKeyValue = &akv
	}

	r.logger.Info("creating api key")
	apiKeyResponse, err := r.createApiKeyHttpRequest(ctx, &cluster, adminApiKeyValue, apiKeyRequest, apiKeyValue)
	if err != nil {
		return ctrl.Result{}, err
	}

	_, err = r.updateKeyRequestKeyIdStatus(ctx, &apiKeyRequest, apiKeyResponse)
	if err != nil {
		return ctrl.Result{}, err
	}

	if apiKeyRequest.Spec.ApiKeySecret == nil {
		r.logger.Info("creating api key secret")

		apiKeySecretObjectKey := client.ObjectKey{Namespace: apiKeyRequest.Namespace, Name: apiKeyRequest.Name}
		_, err = r.createApiKeySecret(ctx, apiKeySecretObjectKey, &apiKeyRequest, apiKeyResponse, &cluster)
		if err != nil {
			r.logger.Error(err, "creating api key secret failed", "secret", apiKeySecretObjectKey.Name)
		}
	}

	r.logger.Info("reconciling api key request completed")
	return r.updateKeyRequestReadyStatus(ctx, &apiKeyRequest, true)
}

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

func (r *TypesenseKeyRequestReconciler) updateKeyRequestStatus(ctx context.Context, apiKeyRequest *tsv1alpha1.TypesenseKeyRequest, newStatus tsv1alpha1.TypesenseKeyRequestStatus) (ctrl.Result, error) {
	apiKeyRequest.Status = newStatus
	if err := r.Status().Update(ctx, apiKeyRequest); err != nil {
		r.logger.Error(err, "updating api key request status failed")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TypesenseKeyRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tsv1alpha1.TypesenseKeyRequest{}, typesenseKeyRequestEventFilters).
		Complete(r)
}
