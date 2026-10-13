package controller

import (
	"context"
	"fmt"
	"strconv"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func (r *TypesenseClusterReconciler) ReconcileScraper(ctx context.Context, ts *tsv1alpha1.TypesenseCluster) (err error) {
	log.FromContext(ctx).V(debugLevel).Info("reconciling scrapers")

	labelSelector := getLabels(ts)
	listOptions := []client.ListOption{
		client.InNamespace(ts.Namespace),
		client.MatchingLabels(labelSelector),
	}

	var scraperCronJobs batchv1.CronJobList
	if err := r.List(ctx, &scraperCronJobs, listOptions...); err != nil {
		return err
	}

	inSpecs := func(cronJobName string, scrapers []tsv1alpha1.DocSearchScraperSpec) bool {
		for _, scraper := range scrapers {
			if cronJobName == fmt.Sprintf(ClusterScraperCronJob, ts.Name, scraper.Name) {
				return true
			}
		}

		return false
	}

	for _, scraperCronJob := range scraperCronJobs.Items {
		if ts.Spec.Scrapers == nil || !inSpecs(scraperCronJob.Name, ts.Spec.Scrapers) {
			cronjob := scraperCronJob
			err = r.deleteScraper(ctx, &cronjob)
			if err != nil {
				return err
			}
		}
	}

	if ts.Spec.Scrapers == nil {
		return nil
	}

	for _, scraper := range ts.Spec.Scrapers {
		scraperName := fmt.Sprintf(ClusterScraperCronJob, ts.Name, scraper.Name)

		if len(scraperName) > 52 {
			err := fmt.Errorf("scraper name '%s' combined with cluster name '%s' exceeds the 52 character limit for CronJobs (got %d characters)", scraper.Name, ts.Name, len(scraperName))
			r.Recorder.Event(ts, "Warning", "InvalidScraperConfiguration", err.Error())
			continue
		}

		scraperExists := true
		scraperObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: scraperName}

		var scraperCronJob = &batchv1.CronJob{}
		if err := r.Get(ctx, scraperObjectKey, scraperCronJob); err != nil {
			if apierrors.IsNotFound(err) {
				scraperExists = false
			} else {
				log.FromContext(ctx).Error(err, fmt.Sprintf("unable to fetch scraper cronjob: %s", scraperObjectKey))
				return err
			}
		}

		if !scraperExists {
			log.FromContext(ctx).V(debugLevel).Info("creating scraper cronjob", "cronjob", scraperObjectKey.Name)

			err = r.createScraper(ctx, scraperObjectKey, ts, &scraper)
			if err != nil {
				log.FromContext(ctx).Error(err, "creating scraper cronjob failed", "cronjob", scraperObjectKey.Name)
				return err
			}
		} else {
			hasChanged := false
			hasChangedConfig := false

			if len(scraperCronJob.Spec.JobTemplate.Spec.Template.Spec.Containers) == 0 {
				scraperCronJob.Spec.JobTemplate.Spec.Template.Spec.Containers = append(scraperCronJob.Spec.JobTemplate.Spec.Template.Spec.Containers, corev1.Container{})
			}
			container := scraperCronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0]

			for _, env := range container.Env {
				if env.Name == envTypesenseConfig && env.Value != scraper.Config {
					hasChangedConfig = true
				}
				if env.Name == envTypesensePort && env.Value != strconv.Itoa(ts.Spec.ApiPort) {
					hasChangedConfig = true
				}
			}

			hasChangedAuthConfig := !apiequality.Semantic.DeepEqual(container.EnvFrom, scraper.GetScraperAuthConfiguration())

			if scraperCronJob.Spec.Schedule != scraper.Schedule || container.Image != scraper.Image || hasChangedConfig || hasChangedAuthConfig {
				hasChanged = true
			}

			if hasChanged {
				log.FromContext(ctx).V(debugLevel).Info("updating scraper cronjob", "cronjob", scraperObjectKey.Name)

				desired := &batchv1.CronJob{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "batch/v1",
						Kind:       "CronJob",
					},
					ObjectMeta: getObjectMeta(ts, &scraperObjectKey.Name, nil),
					Spec: batchv1.CronJobSpec{
						ConcurrencyPolicy:          batchv1.ForbidConcurrent,
						SuccessfulJobsHistoryLimit: ptr.To[int32](1),
						FailedJobsHistoryLimit:     ptr.To[int32](1),
						Schedule:                   scraper.Schedule,
						JobTemplate: batchv1.JobTemplateSpec{
							Spec: batchv1.JobSpec{
								BackoffLimit: ptr.To[int32](0),
								Template: corev1.PodTemplateSpec{
									ObjectMeta: metav1.ObjectMeta{
										Labels: mergeLabels(getDefaultLabels(ts), map[string]string{
											"ts.opentelekomcloud.com/scraper": scraper.Name,
										}),
									},
									Spec: corev1.PodSpec{
										ImagePullSecrets: ts.Spec.ImagePullSecrets,
										RestartPolicy:    corev1.RestartPolicyNever,
										Containers: []corev1.Container{
											{
												Name:  fmt.Sprintf(ClusterScraperCronJobContainer, ts.Name, scraper.Name),
												Image: scraper.Image,
												Env: []corev1.EnvVar{
													{Name: envTypesenseConfig, Value: scraper.Config},
													{
														Name: envTypesenseApiKey,
														ValueFrom: &corev1.EnvVarSource{
															SecretKeyRef: &corev1.SecretKeySelector{
																Key: ClusterAdminApiKeySecretKeyName,
																LocalObjectReference: corev1.LocalObjectReference{
																	Name: r.getAdminApiKeyObjectKey(ts).Name,
																},
															},
														},
													},
													{Name: envTypesenseHost, Value: fmt.Sprintf(ClusterRestService, ts.Name)},
													{Name: envTypesensePort, Value: strconv.Itoa(ts.Spec.ApiPort)},
													{Name: envTypesenseProtocol, Value: protocolHttp},
												},
												EnvFrom: scraper.GetScraperAuthConfiguration(),
												Resources: corev1.ResourceRequirements{
													Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1024m"), corev1.ResourceMemory: resource.MustParse("512Mi")},
													Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("128m"), corev1.ResourceMemory: resource.MustParse("112Mi")},
												},
											},
										},
									},
								},
							},
						},
					},
				}

				//nolint:staticcheck
				err = r.Patch(ctx, desired, client.Apply, client.ForceOwnership, client.FieldOwner("typesense-operator"))
				if err != nil {
					log.FromContext(ctx).Error(err, "updating scraper cronjob failed", "cronjob", scraperObjectKey.Name)
					return err
				}
			}
		}
	}

	return nil
}

func (r *TypesenseClusterReconciler) createScraper(ctx context.Context, key client.ObjectKey, ts *tsv1alpha1.TypesenseCluster, scraperSpec *tsv1alpha1.DocSearchScraperSpec) error {
	scraper := &batchv1.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1",
			Kind:       "CronJob",
		},
		ObjectMeta: getObjectMeta(ts, &key.Name, nil),
		Spec: batchv1.CronJobSpec{
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			SuccessfulJobsHistoryLimit: ptr.To[int32](1),
			FailedJobsHistoryLimit:     ptr.To[int32](1),
			Schedule:                   scraperSpec.Schedule,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					BackoffLimit: ptr.To[int32](0),
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: mergeLabels(getDefaultLabels(ts), map[string]string{
								"ts.opentelekomcloud.com/scraper": scraperSpec.Name,
							}),
						},
						Spec: corev1.PodSpec{
							ImagePullSecrets: ts.Spec.ImagePullSecrets,
							RestartPolicy:    corev1.RestartPolicyNever,
							Containers: []corev1.Container{
								{
									Name:  fmt.Sprintf(ClusterScraperCronJobContainer, ts.Name, scraperSpec.Name),
									Image: scraperSpec.Image,
									Env: []corev1.EnvVar{
										{
											Name:  envTypesenseConfig,
											Value: scraperSpec.Config,
										},
										{
											Name: envTypesenseApiKey,
											ValueFrom: &corev1.EnvVarSource{
												SecretKeyRef: &corev1.SecretKeySelector{
													Key: ClusterAdminApiKeySecretKeyName,
													LocalObjectReference: corev1.LocalObjectReference{
														Name: r.getAdminApiKeyObjectKey(ts).Name,
													},
												},
											},
										},
										{
											Name:  envTypesenseHost,
											Value: fmt.Sprintf(ClusterRestService, ts.Name),
										},
										{
											Name:  envTypesensePort,
											Value: strconv.Itoa(ts.Spec.ApiPort),
										},
										{
											Name:  envTypesenseProtocol,
											Value: protocolHttp,
										},
									},
									EnvFrom: scraperSpec.GetScraperAuthConfiguration(),
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1024m"),
											corev1.ResourceMemory: resource.MustParse("512Mi"),
										},
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("128m"),
											corev1.ResourceMemory: resource.MustParse("112Mi"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	err := ctrl.SetControllerReference(ts, scraper, r.Scheme)
	if err != nil {
		return err
	}

	err = r.Create(ctx, scraper)
	if err != nil {
		return err
	}

	return nil
}

func (r *TypesenseClusterReconciler) deleteScraper(ctx context.Context, scraper *batchv1.CronJob) error {
	err := r.Delete(ctx, scraper)
	return client.IgnoreNotFound(err)
}
