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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
)

var _ = Describe("TypesenseCluster Controller", func() {
	const defaultNamespace = "default"

	// Helper function moved to the top level so that ALL Context blocks can use it
	newControllerReconciler := func() *TypesenseClusterReconciler {
		clientSet, err := kubernetes.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())

		httpClient, err := rest.HTTPClientFor(cfg)
		Expect(err).NotTo(HaveOccurred())

		return &TypesenseClusterReconciler{
			Client:          k8sClient,
			Scheme:          k8sClient.Scheme(),
			ClientSet:       clientSet,
			DiscoveryClient: clientSet.DiscoveryClient,
			Recorder:        record.NewFakeRecorder(100),
			Configuration:   cfg,
			HttpClient:      httpClient,
		}
	}

	runReconcile := func(ctx context.Context, r *TypesenseClusterReconciler, req reconcile.Request) {
		res, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		//nolint:staticcheck // Ignore SA1019 deprecation warning for Requeue
		if res.Requeue || res.RequeueAfter > 0 {
			_, err = r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
		}
	}

	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: defaultNamespace,
		}

		BeforeEach(func(ctx context.Context) {
			By("creating the custom resource for the Kind TypesenseCluster")
			tsResource := &tsv1alpha1.TypesenseCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, tsResource)
			if err != nil && errors.IsNotFound(err) {
				tsResource = &tsv1alpha1.TypesenseCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: defaultNamespace,
					},
					Spec: tsv1alpha1.TypesenseClusterSpec{
						Image:       "typesense/typesense:30.0",
						Replicas:    1,
						ApiPort:     8108,
						PeeringPort: 8107,
						Storage: &tsv1alpha1.StorageSpec{
							StorageClassName: "default",
							Size:             resource.MustParse("1Gi"),
						},
					},
				}
				Expect(k8sClient.Create(ctx, tsResource)).To(Succeed())
			}
		})

		AfterEach(func(ctx context.Context) {
			tsResource := &tsv1alpha1.TypesenseCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, tsResource)
			if err == nil {
				By("Cleanup the specific resource instance TypesenseCluster")
				Expect(k8sClient.Delete(ctx, tsResource)).To(Succeed())

				controllerReconciler := newControllerReconciler()
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})

				Eventually(func() bool {
					err := k8sClient.Get(ctx, typeNamespacedName, &tsv1alpha1.TypesenseCluster{})
					return errors.IsNotFound(err)
				}, time.Second*30, time.Millisecond*500).Should(BeTrue())
			}
		})

		It("should successfully reconcile the resource", func(ctx context.Context) {
			By("Reconciling the created resource")
			controllerReconciler := newControllerReconciler()

			runReconcile(ctx, controllerReconciler, reconcile.Request{NamespacedName: typeNamespacedName})

			// Verify that the StatefulSet was really created by the operator
			sts := &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      resourceName + "-sts",
				Namespace: defaultNamespace,
			}, sts)
			Expect(err).NotTo(HaveOccurred())
			Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
		})

		It("should create the admin api key secret, configmap, and services", func(ctx context.Context) {
			controllerReconciler := newControllerReconciler()

			runReconcile(ctx, controllerReconciler, reconcile.Request{NamespacedName: typeNamespacedName})

			tsResource := &tsv1alpha1.TypesenseCluster{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, tsResource)).To(Succeed())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, controllerReconciler.getAdminApiKeyObjectKey(tsResource), secret)).To(Succeed())
			Expect(secret.Type).To(Equal(corev1.SecretTypeOpaque))
			Expect(secret.Data).To(HaveKey(ClusterAdminApiKeySecretKeyName))

			configMap := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf(ClusterNodesConfigMap, resourceName),
				Namespace: defaultNamespace,
			}, configMap)).To(Succeed())
			Expect(configMap.Data["nodes"]).ToNot(BeEmpty())
			Expect(configMap.Data["fallback"]).ToNot(BeEmpty())

			resolverSvc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf(ClusterRestService, resourceName),
				Namespace: defaultNamespace,
			}, resolverSvc)).To(Succeed())
			Expect(resolverSvc.Spec.Ports[0].Port).To(Equal(int32(8108)))
			Expect(resolverSvc.Spec.Ports).To(HaveLen(2))
		})

		It("should update the StatefulSet when replicas are changed", func(ctx context.Context) {
			tsResource := &tsv1alpha1.TypesenseCluster{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, tsResource)).To(Succeed())

			controllerReconciler := newControllerReconciler()
			runReconcile(ctx, controllerReconciler, reconcile.Request{NamespacedName: typeNamespacedName})

			// Fetch the latest version before modifying, because Reconcile updated the Status/ResourceVersion!
			Expect(k8sClient.Get(ctx, typeNamespacedName, tsResource)).To(Succeed())

			// Simulate a healthy cluster so the operator allows the scale-up
			meta.SetStatusCondition(&tsResource.Status.Conditions, metav1.Condition{
				Type:    ConditionTypeReady,
				Status:  metav1.ConditionTrue,
				Reason:  string(ConditionReasonQuorumReady),
				Message: "Cluster is Ready",
			})
			Expect(k8sClient.Status().Update(ctx, tsResource)).To(Succeed())
			Expect(k8sClient.Get(ctx, typeNamespacedName, tsResource)).To(Succeed())

			tsResource.Spec.Replicas = 3
			Expect(k8sClient.Update(ctx, tsResource)).To(Succeed())

			runReconcile(ctx, controllerReconciler, reconcile.Request{NamespacedName: typeNamespacedName})

			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      resourceName + "-sts",
				Namespace: defaultNamespace,
			}, sts)).To(Succeed())
			Expect(*sts.Spec.Replicas).To(Equal(int32(3)))

			configMap := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf(ClusterNodesConfigMap, resourceName),
				Namespace: defaultNamespace,
			}, configMap)).To(Succeed())
			Expect(strings.Count(configMap.Data["nodes"], ",")).To(Equal(2))
		})

		It("should update services when the API port changes", func(ctx context.Context) {
			tsResource := &tsv1alpha1.TypesenseCluster{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, tsResource)).To(Succeed())

			controllerReconciler := newControllerReconciler()
			runReconcile(ctx, controllerReconciler, reconcile.Request{NamespacedName: typeNamespacedName})

			// Fetch the latest version before modifying, because Reconcile updated the Status/ResourceVersion!
			Expect(k8sClient.Get(ctx, typeNamespacedName, tsResource)).To(Succeed())

			tsResource.Spec.ApiPort = 9200
			Expect(k8sClient.Update(ctx, tsResource)).To(Succeed())

			runReconcile(ctx, controllerReconciler, reconcile.Request{NamespacedName: typeNamespacedName})

			headlessSvc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf(ClusterHeadlessService, resourceName),
				Namespace: defaultNamespace,
			}, headlessSvc)).To(Succeed())
			Expect(headlessSvc.Spec.Ports[0].Port).To(Equal(int32(9200)))

			resolverSvc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf(ClusterRestService, resourceName),
				Namespace: defaultNamespace,
			}, resolverSvc)).To(Succeed())
			Expect(resolverSvc.Spec.Ports).To(HaveLen(2))
		})

		It("should update the reverse proxy deployment when ingress configuration changes", func(ctx context.Context) {
			tsResource := &tsv1alpha1.TypesenseCluster{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, tsResource)).To(Succeed())

			// Enable Ingress with initial directives
			tsResource.Spec.Ingress = &tsv1alpha1.IngressSpec{
				Host:             "ts.example.com",
				HttpDirectives:   ptr.To("client_max_body_size 10m;"),
				IngressClassName: "nginx",
			}
			Expect(k8sClient.Update(ctx, tsResource)).To(Succeed())

			controllerReconciler := newControllerReconciler()
			runReconcile(ctx, controllerReconciler, reconcile.Request{NamespacedName: typeNamespacedName})

			deploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf(ClusterReverseProxy, resourceName),
				Namespace: defaultNamespace,
			}, deploy)).To(Succeed())

			initialHash := deploy.Spec.Template.Annotations[NginxConfigHashAnnotationKey]
			Expect(initialHash).ToNot(BeEmpty())

			// Update Ingress directives
			Expect(k8sClient.Get(ctx, typeNamespacedName, tsResource)).To(Succeed())
			tsResource.Spec.Ingress.HttpDirectives = ptr.To("client_max_body_size 20m;")
			Expect(k8sClient.Update(ctx, tsResource)).To(Succeed())

			runReconcile(ctx, controllerReconciler, reconcile.Request{NamespacedName: typeNamespacedName})

			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf(ClusterReverseProxy, resourceName),
				Namespace: defaultNamespace,
			}, deploy)).To(Succeed())

			newHash := deploy.Spec.Template.Annotations[NginxConfigHashAnnotationKey]
			Expect(newHash).ToNot(BeEmpty())
			Expect(newHash).ToNot(Equal(initialHash))

			// Re-reconcile without changes and check that the hash remains the same
			runReconcile(ctx, controllerReconciler, reconcile.Request{NamespacedName: typeNamespacedName})

			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf(ClusterReverseProxy, resourceName),
				Namespace: defaultNamespace,
			}, deploy)).To(Succeed())

			finalHash := deploy.Spec.Template.Annotations[NginxConfigHashAnnotationKey]
			Expect(finalHash).To(Equal(newHash))
		})
	})

	Context("When reconciling a resource in a custom namespace", func() {
		const resourceName = "test-resource-scoped"
		const customNamespace = "custom-scoped-ns"

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: customNamespace,
		}

		BeforeEach(func(ctx context.Context) {
			// Create the namespace for the test
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: customNamespace},
			}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: customNamespace}, &corev1.Namespace{})
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			}

			By("creating the custom resource in the custom namespace")
			tsResource := &tsv1alpha1.TypesenseCluster{}
			err = k8sClient.Get(ctx, typeNamespacedName, tsResource)
			if err != nil && errors.IsNotFound(err) {
				tsResource = &tsv1alpha1.TypesenseCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: customNamespace,
					},
					Spec: tsv1alpha1.TypesenseClusterSpec{
						Image:       "typesense/typesense:30.0",
						Replicas:    1,
						ApiPort:     8108,
						PeeringPort: 8107,
						Storage: &tsv1alpha1.StorageSpec{
							StorageClassName: "default",
							Size:             resource.MustParse("1Gi"),
						},
					},
				}
				Expect(k8sClient.Create(ctx, tsResource)).To(Succeed())
			}
		})

		AfterEach(func(ctx context.Context) {
			tsResource := &tsv1alpha1.TypesenseCluster{}
			err := k8sClient.Get(ctx, typeNamespacedName, tsResource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, tsResource)).To(Succeed())

				controllerReconciler := newControllerReconciler()
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})

				Eventually(func() bool {
					err := k8sClient.Get(ctx, typeNamespacedName, &tsv1alpha1.TypesenseCluster{})
					return errors.IsNotFound(err)
				}, time.Second*30, time.Millisecond*500).Should(BeTrue())
			}

			// Cleanup the custom namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: customNamespace},
			}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ns))).To(Succeed())
		})

		It("should successfully create dependent resources in the correct namespace", func(ctx context.Context) {
			// Use the helper here as well!
			controllerReconciler := newControllerReconciler()

			runReconcile(ctx, controllerReconciler, reconcile.Request{NamespacedName: typeNamespacedName})

			// Verify if the StatefulSet was REALLY created in the customNamespace
			sts := &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: resourceName + "-sts", Namespace: customNamespace}, sts)
			Expect(err).NotTo(HaveOccurred())

			// Verify if the Headless Service was REALLY created in the customNamespace
			svc := &corev1.Service{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: resourceName + "-sts-svc", Namespace: customNamespace}, svc)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When validating scrapers in the CRD", func() {
		// The rule is enforced by the API server itself, so it also applies when the admission webhook is disabled.
		newClusterWithScraper := func(name string, scraper tsv1alpha1.DocSearchScraperSpec) *tsv1alpha1.TypesenseCluster {
			scraper.Schedule = "0 0 * * *"
			return &tsv1alpha1.TypesenseCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: defaultNamespace},
				Spec: tsv1alpha1.TypesenseClusterSpec{
					Image:       "typesense/typesense:30.0",
					Replicas:    1,
					ApiPort:     8108,
					PeeringPort: 8107,
					Storage: &tsv1alpha1.StorageSpec{
						StorageClassName: "default",
						Size:             resource.MustParse("1Gi"),
					},
					Scrapers: []tsv1alpha1.DocSearchScraperSpec{scraper},
				},
			}
		}

		It("should reject a scraper without host and config", func(ctx context.Context) {
			ts := newClusterWithScraper("scraper-invalid", tsv1alpha1.DocSearchScraperSpec{Name: "docs"})

			err := k8sClient.Create(ctx, ts)
			Expect(errors.IsInvalid(err)).To(BeTrue(), "expected an invalid error, got %v", err)
			Expect(err.Error()).To(ContainSubstring("either host or config must be set"))
		})

		It("should accept a scraper with a host", func(ctx context.Context) {
			ts := newClusterWithScraper("scraper-valid", tsv1alpha1.DocSearchScraperSpec{Name: "docs", Host: ptr.To("docs.example.com")})

			Expect(k8sClient.Create(ctx, ts)).To(Succeed())
			Expect(k8sClient.Delete(ctx, ts)).To(Succeed())
		})
	})
})
