package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

const (
	watchedNamespace = "apps"
	gatewayNamespace = "gateway-system"
)

// newReferenceGrantTestReconciler mimics the gapped mode cache: reads and writes outside the watched
// namespace fail with an error that is not NotFound.
func newReferenceGrantTestReconciler(t *testing.T, watchNamespace string) (*TypesenseClusterReconciler, *record.FakeRecorder) {
	t.Helper()

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, tsv1alpha1.AddToScheme, gatewayv1beta1.SchemeBuilder.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	outside := func(namespace string) error {
		if watchNamespace != "" && namespace != watchNamespace {
			return fmt.Errorf("unable to access namespace %s because of unknown namespace for the cache", namespace)
		}
		return nil
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if err := outside(key.Namespace); err != nil {
				return err
			}
			return c.Get(ctx, key, obj, opts...)
		},
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if err := outside(obj.GetNamespace()); err != nil {
				return err
			}
			return c.Create(ctx, obj, opts...)
		},
	}).Build()

	recorder := record.NewFakeRecorder(10)
	return &TypesenseClusterReconciler{Client: c, Scheme: scheme, Recorder: recorder, WatchNamespace: watchNamespace}, recorder
}

func newReferenceGrantTestCluster() (*tsv1alpha1.TypesenseCluster, tsv1alpha1.HttpRouteSpec) {
	ts := &tsv1alpha1.TypesenseCluster{ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: watchedNamespace}}
	ns := gatewayv1.Namespace(gatewayNamespace)
	spec := tsv1alpha1.HttpRouteSpec{Name: "api"}
	spec.ParentRef.Name = "shared-gateway"
	spec.ParentRef.Namespace = &ns
	return ts, spec
}

// In gapped mode a Gateway in another namespace must not fail the reconcile.
func TestUpdateReferenceGrantSkipsUnmanagedNamespace(t *testing.T) {
	t.Run("reference grant not requested", func(t *testing.T) {
		r, recorder := newReferenceGrantTestReconciler(t, watchedNamespace)
		ts, spec := newReferenceGrantTestCluster()

		if err := r.updateReferenceGrant(context.Background(), spec, ts); err != nil {
			t.Fatalf("updateReferenceGrant() error = %v", err)
		}
		if len(recorder.Events) != 0 {
			t.Errorf("expected no event, got %q", <-recorder.Events)
		}
	})

	t.Run("reference grant requested", func(t *testing.T) {
		r, recorder := newReferenceGrantTestReconciler(t, watchedNamespace)
		ts, spec := newReferenceGrantTestCluster()
		spec.ReferenceGrant = ptr.To(true)

		if err := r.updateReferenceGrant(context.Background(), spec, ts); err != nil {
			t.Fatalf("updateReferenceGrant() error = %v", err)
		}
		select {
		case event := <-recorder.Events:
			if !strings.Contains(event, "ReferenceGrantNotManaged") || !strings.Contains(event, gatewayNamespace) {
				t.Errorf("unexpected event %q", event)
			}
		default:
			t.Error("expected a warning event about the unmanaged reference grant")
		}
	})
}

// Cluster-wide, the grant is still created in the Gateway's namespace.
func TestUpdateReferenceGrantCreatesGrantClusterWide(t *testing.T) {
	r, _ := newReferenceGrantTestReconciler(t, "")
	ts, spec := newReferenceGrantTestCluster()
	spec.ReferenceGrant = ptr.To(true)

	if err := r.updateReferenceGrant(context.Background(), spec, ts); err != nil {
		t.Fatalf("updateReferenceGrant() error = %v", err)
	}

	grant := &gatewayv1beta1.ReferenceGrant{}
	key := client.ObjectKey{Namespace: gatewayNamespace, Name: getReferenceGrantName(ts, spec.Name)}
	if err := r.Get(context.Background(), key, grant); err != nil {
		t.Fatalf("expected reference grant %s to be created: %v", key, err)
	}
}
