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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
)

// TypesenseClusterReconciler reconciles a TypesenseCluster object
type TypesenseClusterReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	Recorder        record.EventRecorder
	DiscoveryClient *discovery.DiscoveryClient
	ClientSet       *kubernetes.Clientset
	Configuration   *rest.Config
	InCluster       bool
	HttpClient      *http.Client

	// MaxConcurrentReconciles is the number of clusters reconciled in parallel; defaults to 1.
	MaxConcurrentReconciles int

	// WatchNamespace restricts the operator to a single namespace (gapped mode); empty means cluster-wide.
	WatchNamespace string

	serverVersionMu sync.Mutex
	serverVersion   string
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
	// kubelets sync configmaps by default every minute, but forcePodsConfigMapUpdate triggers an immediate sync
	configMapRequeuePeriod = 15 * time.Second
	reconcileRequeuePeriod = 60 * time.Second
)

type Action string

const (
	Bootstrapping Action = "bootstrapping"
	Reconciling   Action = "reconciling"
)

const (
	protocolHttp         = "http"
	containerTypesense   = "typesense"
	envTypesenseApiKey   = "TYPESENSE_API_KEY"
	envTypesenseProtocol = "TYPESENSE_PROTOCOL"
	envTypesenseConfig   = "CONFIG"
	envTypesenseHost     = "TYPESENSE_HOST"
	envTypesensePort     = "TYPESENSE_PORT"
	portHealthcheck      = "healthcheck"
	volumeNodeslist      = "nodeslist"
	volumeData           = "data"
)

// --- Core Kubernetes API ---
// +kubebuilder:rbac:groups="",resources=configmaps;persistentvolumeclaims;pods;secrets;services,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups="",resources=pods/status,verbs=get;patch;update
// --- Workloads & Batch ---
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=create;delete;get;list;patch;update;watch
// --- Networking & Gateway API ---
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes;referencegrants,verbs=create;delete;get;list;patch;update;watch
// --- Monitoring ---
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=podmonitors;servicemonitors,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/finalizers,verbs=update
// --- Webhook Management ---
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=get;list;watch;update;patch
// --- Typesense Custom Resources ---
// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesenseclusters,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesenseclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=ts.opentelekomcloud.com,resources=typesenseclusters/status,verbs=get;patch;update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the TypesenseCluster object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.18.4/pkg/reconcile
//
//nolint:gocyclo // Reconcile sequentially coordinates multiple child controllers
func (r *TypesenseClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	var ts tsv1alpha1.TypesenseCluster
	if err := r.Get(ctx, req.NamespacedName, &ts); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	const typesenseClusterFinalizer = "ts.opentelekomcloud.com/finalizer"

	if ts.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&ts, typesenseClusterFinalizer) {
			controllerutil.AddFinalizer(&ts, typesenseClusterFinalizer)
			if err := r.Update(ctx, &ts); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	} else {
		if controllerutil.ContainsFinalizer(&ts, typesenseClusterFinalizer) {
			if err := r.deleteOrphanedReferenceGrants(ctx, &ts); err != nil {
				return ctrl.Result{}, err
			}

			controllerutil.RemoveFinalizer(&ts, typesenseClusterFinalizer)
			if err := r.Update(ctx, &ts); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	log.FromContext(ctx).Info("reconciling cluster")

	err := r.initConditions(ctx, &ts)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(ts.Name) > 24 {
		err := fmt.Errorf("cluster name '%s' exceeds the maximum allowed length of 24 characters (got %d characters)", ts.Name, len(ts.Name))
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonInvalidClusterName, err)
		if cerr != nil {
			err = errors.Join(err, cerr)
		}
		return ctrl.Result{}, err
	}

	// Update strategy: Admin Secret is Immutable, will not be updated on any future change
	secret, err := r.ReconcileSecret(ctx, &ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonSecretNotReady, err)
		if cerr != nil {
			err = errors.Join(err, cerr)
		}
		return ctrl.Result{}, err
	}

	// Update strategy: Update the existing object, if changes are identified in the desired.Data["nodes"]
	configMapUpdated, err := r.ReconcileConfigMap(ctx, &ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonConfigMapNotReady, err)
		if cerr != nil {
			err = errors.Join(err, cerr)
		}
		return ctrl.Result{}, err
	}

	// Update strategy: Update the existing objects, if changes are identified in api and peering ports
	err = r.ReconcileServices(ctx, &ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonServicesNotReady, err)
		if cerr != nil {
			err = errors.Join(err, cerr)
		}
		return ctrl.Result{}, err
	}

	// Update strategy: Update the existing objects, if changes are identified in api and peering ports
	err = r.ReconcileIngress(ctx, &ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonIngressNotReady, err)
		if cerr != nil {
			err = errors.Join(err, cerr)
		}
		return ctrl.Result{}, err
	}

	// Update strategy: Update the existing objects, if changes are identified
	err = r.ReconcileHttpRoute(ctx, &ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonHttpRouteNotReady, err)
		if cerr != nil {
			err = errors.Join(err, cerr)
		}
		return ctrl.Result{}, err
	}

	// Update strategy: Drop the existing objects and recreate them, if changes are identified
	err = r.ReconcileScraper(ctx, &ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonScrapersNotReady, err)
		if cerr != nil {
			err = errors.Join(err, cerr)
		}
		return ctrl.Result{}, err
	}

	// Update strategy: Update the Deployment if image changed. Drop the existing ServiceMonitor and recreate it, if changes are identified
	err = r.ReconcilePodMonitor(ctx, &ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonMetricsExporterNotReady, err)
		if cerr != nil {
			err = errors.Join(err, cerr)
		}
		return ctrl.Result{}, err
	}

	// Update strategy: Update the whole specs when changes are identified
	sts, _, err := r.ReconcileStatefulSet(ctx, &ts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonStatefulSetNotReady, err)
		if cerr != nil {
			err = errors.Join(err, cerr)
		}
		return ctrl.Result{}, err
	}

	err = r.ReconcilePodDisruptionBudget(ctx, &ts, sts)
	if err != nil {
		cerr := r.setConditionNotReady(ctx, &ts, ConditionReasonPodDisruptionBudgetNotReady, err)
		if cerr != nil {
			err = errors.Join(err, cerr)
		}
		return ctrl.Result{}, err
	}

	gracePeriodSeconds := ptr.Deref(sts.Spec.Template.Spec.TerminationGracePeriodSeconds, terminationGracePeriodSeconds)
	requeueAfter := reconcileRequeuePeriod + (time.Duration(gracePeriodSeconds) * time.Second)

	cond := ConditionReasonQuorumStateUnknown
	action := Bootstrapping
	if configMapUpdated != nil {
		action = Reconciling
	}

	if configMapUpdated == nil {
		if action == Bootstrapping {
			requeueAfter = 15 * time.Second
		}
		log.FromContext(ctx).Info(fmt.Sprintf("%s cluster completed", string(action)), "condition", cond, "requeueAfter", requeueAfter)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	if *configMapUpdated {
		err = r.forcePodsConfigMapUpdate(ctx, &ts)
		if err != nil {
			if agg, ok := err.(utilerrors.Aggregate); ok {
				for _, e := range agg.Errors() {
					log.FromContext(ctx).Error(e, "failed to force configmap update", "configmap", fmt.Sprintf(ClusterNodesConfigMap, ts.Name))
				}
			} else {
				log.FromContext(ctx).Error(err, "failed to force pods configmap update", "configmap", fmt.Sprintf(ClusterNodesConfigMap, ts.Name))
			}
		}

		cond = ConditionReasonQuorumNotReadyWaitATerm
		requeueAfter = configMapRequeuePeriod

		err = errors.New("wait on configmap updates")
		cerr := r.setConditionNotReady(ctx, &ts, string(cond), err)
		if cerr != nil {
			return ctrl.Result{}, cerr
		}
		r.Recorder.Event(&ts, "Warning", string(cond), toTitle(err.Error()))
	}

	condition, _, err := r.ReconcileQuorum(ctx, &ts, secret, client.ObjectKeyFromObject(sts))
	if err != nil {
		log.FromContext(ctx).Error(err, "reconciling quorum health failed")
	}

	if strings.Contains(string(condition), "QuorumNeedsAttention") {
		eram := "cluster needs manual administrative attention: "

		if condition == ConditionReasonQuorumNeedsAttentionClusterIsLagging {
			eram += "queued_writes > healthyWriteLagThreshold"
		}

		if condition == ConditionReasonQuorumNeedsAttentionMemoryOrDiskIssue {
			eram += "out of memory or disk"
		}

		erram := errors.New(eram)

		shouldEmit := true
		if len(ts.Status.Conditions) > 0 && ts.Status.Conditions[0].Reason == string(condition) {
			shouldEmit = false
		}

		cerr := r.setConditionNotReady(ctx, &ts, string(condition), erram)
		if cerr != nil {
			return ctrl.Result{}, cerr
		}
		if shouldEmit {
			r.Recorder.Event(&ts, "Warning", string(condition), toTitle(erram.Error()))
		}

	} else {
		if condition != ConditionReasonQuorumReady {
			eventType := "Warning"
			if err == nil {
				switch condition {
				case ConditionReasonUpgrading:
					err = errors.New("cluster is performing a rolling update")
					eventType = "Normal"
				case ConditionReasonDegraded:
					err = errors.New("quorum is intact, but cluster is degraded (missing nodes)")
				default:
					err = errors.New("quorum is not ready")
				}
			}

			shouldEmit := true
			if len(ts.Status.Conditions) > 0 && ts.Status.Conditions[0].Reason == string(condition) {
				shouldEmit = false
			}

			cerr := r.setConditionNotReady(ctx, &ts, string(condition), err)
			if cerr != nil {
				return ctrl.Result{}, cerr
			}

			if shouldEmit {
				r.Recorder.Event(&ts, eventType, string(condition), toTitle(err.Error()))
			}
		} else {
			report := ts.Status.Conditions[0].Status != metav1.ConditionTrue

			cerr := r.setConditionReady(ctx, &ts, string(condition))
			if cerr != nil {
				return ctrl.Result{}, cerr
			}

			if report {
				r.Recorder.Event(&ts, "Normal", string(condition), toTitle("quorum is ready"))
			}
		}
	}

	cond = condition

	if cond == ConditionReasonUpgrading || cond == ConditionReasonDegraded || strings.Contains(string(cond), "NotReady") {
		requeueAfter = 15 * time.Second
	}

	log.FromContext(ctx).Info(fmt.Sprintf("%s cluster completed", string(action)), "condition", cond, "requeueAfter", requeueAfter)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TypesenseClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tsv1alpha1.TypesenseCluster{}, eventFilters).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Owns(&networkingv1.Ingress{}).
		Owns(&batchv1.CronJob{}).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.mapOwnedResourceToTypesenseCluster)).
		Watches(&corev1.PersistentVolumeClaim{}, handler.EnqueueRequestsFromMapFunc(mapPersistentVolumeClaimToTypesenseCluster)).
		Named("typesense-kubernetes-operator").
		// The same cluster is never reconciled concurrently; workers only process different clusters in parallel.
		WithOptions(crcontroller.Options{MaxConcurrentReconciles: max(1, r.MaxConcurrentReconciles)}).
		Complete(r)
}

func (r *TypesenseClusterReconciler) mapOwnedResourceToTypesenseCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	ownerRef := metav1.GetControllerOf(obj)
	if ownerRef == nil || ownerRef.Kind != "StatefulSet" || ownerRef.APIVersion != "apps/v1" {
		return nil
	}

	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: ownerRef.Name}, sts); err != nil {
		log.FromContext(ctx).V(debugLevel).Info("unable to resolve owning statefulset", "ownerRef", ownerRef, "error", err)
		return nil
	}

	stsOwnerRef := metav1.GetControllerOf(sts)
	if stsOwnerRef == nil || stsOwnerRef.Kind != "TypesenseCluster" || !strings.HasPrefix(stsOwnerRef.APIVersion, "ts.opentelekomcloud.com/") {
		return nil
	}

	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: sts.Namespace, Name: stsOwnerRef.Name}}}
}

// mapPersistentVolumeClaimToTypesenseCluster maps PVCs by the labels inherited from the StatefulSet's
// volumeClaimTemplate. PVCs carry no controller reference to the StatefulSet (its retention policy keeps
// them on deletion and only adds a pod owner reference on scale-down), so mapping by owner never matches.
func mapPersistentVolumeClaimToTypesenseCluster(_ context.Context, obj client.Object) []reconcile.Request {
	labels := obj.GetLabels()
	cluster := labels[labelInstance]
	if labels[labelManagedBy] != labelManagedByValue || cluster == "" {
		return nil
	}

	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: cluster}}}
}
