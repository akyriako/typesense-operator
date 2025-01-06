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
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"time"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
)

// TypesenseClusterReconciler reconciles a TypesenseCluster object
type TypesenseClusterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	logger   logr.Logger
	Recorder record.EventRecorder
}

type TypesenseClusterReconciliationPhase struct {
	Name      string
	Reconcile func(context.Context, *tsv1alpha1.TypesenseCluster) (ctrl.Result, error)
}

var (
	eventFilters = builder.WithPredicates(predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			// We only need to check generation changes here, because it is only
			// updated on spec changes. On the other hand RevisionVersion
			// changes also on status changes. We want to omit reconciliation
			// for status updates.
			return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration()
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// DeleteStateUnknown evaluates to false only if the object
			// has been confirmed as deleted by the api server.
			return !e.DeleteStateUnknown
		},
	})

	requeueAfter = time.Second * 30
)

// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesenseclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesenseclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesenseclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the TypesenseCluster object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.18.4/pkg/reconcile
func (r *TypesenseClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.Log.WithValues("namespace", req.Namespace, "cluster", req.Name)
	r.logger.Info("reconciling cluster")

	var ts tsv1alpha1.TypesenseCluster
	if err := r.Get(ctx, req.NamespacedName, &ts); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	err := r.initConditions(ctx, &ts)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.ReconcileSecret(ctx, ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonSecretNotReady, err)
		if cerr != nil {
			err = errors.Wrap(err, cerr.Error())
		}
		return ctrl.Result{}, err
	}

	updated, err := r.ReconcileConfigMap(ctx, ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonConfigMapNotReady, err)
		if cerr != nil {
			err = errors.Wrap(err, cerr.Error())
		}
		return ctrl.Result{}, err
	}

	err = r.ReconcileServices(ctx, ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonServicesNotReady, err)
		if cerr != nil {
			err = errors.Wrap(err, cerr.Error())
		}
		return ctrl.Result{}, err
	}

	err = r.ReconcileIngress(ctx, ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonIngressNotReady, err)
		if cerr != nil {
			err = errors.Wrap(err, cerr.Error())
		}
		return ctrl.Result{}, err
	}

	err = r.ReconcileScraper(ctx, ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonScrapersNotReady, err)
		if cerr != nil {
			err = errors.Wrap(err, cerr.Error())
		}
		return ctrl.Result{}, err
	}

	sts, err := r.ReconcileStatefulSet(ctx, ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonStatefulSetNotReady, err)
		if cerr != nil {
			err = errors.Wrap(err, cerr.Error())
		}
		return ctrl.Result{}, err
	}

	terminationGracePeriodSeconds := *sts.Spec.Template.Spec.TerminationGracePeriodSeconds
	delayPerReplicaPeriodSeconds := int64(ts.Spec.Replicas)

	toTitle := func(s string) string {
		return cases.Title(language.Und, cases.NoLower).String(s)
	}

	if *updated {
		condition, size, err := r.ReconcileQuorum(ctx, ts, *sts)
		if err != nil {
			r.logger.Error(err, "reconciling quorum failed")
		}

		if condition == ConditionReasonQuorumNeedsAttention {
			if err == nil {
				err = errors.New("quorum is not ready")
			}
			cerr := r.setConditionNotReady(ctx, &ts, string(condition), err)
			if cerr != nil {
				return ctrl.Result{}, cerr
			}

			r.Recorder.Eventf(&ts, "Warning", string(condition), toTitle(err.Error()))

			if size != 0 {
				delayPerReplicaPeriodSeconds = int64(size)
			}

			requeueAfter = time.Duration(delayPerReplicaPeriodSeconds*60+terminationGracePeriodSeconds) * time.Second
			return ctrl.Result{RequeueAfter: requeueAfter}, err
		} else {
			if condition != ConditionReasonQuorumReady {
				if err == nil {
					err = errors.New("quorum is not ready")
				}
				cerr := r.setConditionNotReady(ctx, &ts, string(condition), err)
				if cerr != nil {
					return ctrl.Result{}, cerr
				}

				r.Recorder.Eventf(&ts, "Warning", string(condition), toTitle(err.Error()))

				if size != 0 {
					delayPerReplicaPeriodSeconds = int64(size)
				}
			} else {
				report := ts.Status.Conditions[0].Status != metav1.ConditionTrue

				cerr := r.setConditionReady(ctx, &ts, string(condition))
				if cerr != nil {
					return ctrl.Result{}, cerr
				}

				if report {
					r.Recorder.Eventf(&ts, "Normal", string(condition), toTitle("quorum is ready"))
				}

				delayPerReplicaPeriodSeconds = int64(1)
			}
		}
	}

	requeueAfter = time.Duration(delayPerReplicaPeriodSeconds*60+terminationGracePeriodSeconds) * time.Second
	r.logger.Info("reconciling cluster completed", "requeueAfter", requeueAfter)

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TypesenseClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tsv1alpha1.TypesenseCluster{}, eventFilters).
		Complete(r)
}
