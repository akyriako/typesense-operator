package controller

import (
	"context"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
)

func (r *TypesenseClusterReconciler) ReconcileScraper(ctx context.Context, ts tsv1alpha1.TypesenseCluster) (err error) {
	r.logger.V(debugLevel).Info("reconciling scrapers")

	labelSelector := getLabels(&ts)
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
			if cronJobName == fmt.Sprintf("%s-scraper", scraper.Name) {
				return true
			}
		}

		return false
	}

	for _, scraperCronJob := range scraperCronJobs.Items {
		if ts.Spec.Scrapers == nil || !inSpecs(scraperCronJob.Name, ts.Spec.Scrapers) {
			err = r.deleteScraper(ctx, &scraperCronJob)
			if err != nil {
				return err
			}
		}
	}

	if ts.Spec.Scrapers == nil {
		return nil
	}

	for _, scraper := range ts.Spec.Scrapers {
		scraperName := fmt.Sprintf("%s-scraper", scraper.Name)
		scraperExists := true
		scraperObjectKey := client.ObjectKey{Namespace: ts.Namespace, Name: scraperName}

		var scraperCronJob = &batchv1.CronJob{}
		if err := r.Get(ctx, scraperObjectKey, scraperCronJob); err != nil {
			if apierrors.IsNotFound(err) {
				scraperExists = false
			} else {
				r.logger.Error(err, fmt.Sprintf("unable to fetch scraper cronjob: %s", scraperObjectKey))
			}
		}

		if !scraperExists {
			r.logger.V(debugLevel).Info("creating scraper cronjob", "cronjob", scraperObjectKey.Name)

			err = r.createScraper(ctx, scraperObjectKey, &ts, &scraper)
			if err != nil {
				r.logger.Error(err, "creating scraper cronjob failed", "cronjob", scraperObjectKey.Name)
				return err
			}
		} else {
			r.logger.V(debugLevel).Info("updating scraper cronjob", "cronjob", scraperObjectKey.Name)

			hasChanged := false
			hasChangedConfig := false
			container := scraperCronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0]

			for _, env := range container.Env {
				if env.Name == "CONFIG" && env.Value != scraper.Config {
					hasChangedConfig = true
					break
				}
			}

			if scraperCronJob.Spec.Schedule != scraper.Schedule || container.Image != scraper.Image || hasChangedConfig {
				hasChanged = true
			}

			if hasChanged {
				err = r.deleteScraper(ctx, scraperCronJob)
				if err != nil {
					r.logger.Error(err, "deleting scraper cronjob failed", "cronjob", scraperObjectKey.Name)
					return err
				}

				err = r.createScraper(ctx, scraperObjectKey, &ts, &scraper)
				if err != nil {
					r.logger.Error(err, "creating scraper cronjob failed", "cronjob", scraperObjectKey.Name)
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
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{
								{
									Name:  fmt.Sprintf("%s-docsearch-scraper", scraperSpec.Name),
									Image: scraperSpec.Image,
									Env: []corev1.EnvVar{
										{
											Name:  "CONFIG",
											Value: scraperSpec.Config,
										},
										{
											Name: "TYPESENSE_API_KEY",
											ValueFrom: &corev1.EnvVarSource{
												SecretKeyRef: &corev1.SecretKeySelector{
													Key: "typesense-api-key",
													LocalObjectReference: corev1.LocalObjectReference{
														Name: fmt.Sprintf("%s-admin-key", ts.Name),
													},
												},
											},
										},
										{
											Name:  "TYPESENSE_HOST",
											Value: fmt.Sprintf("%s-svc", ts.Name),
										},
										{
											Name:  "TYPESENSE_PORT",
											Value: strconv.Itoa(ts.Spec.ApiPort),
										},
										{
											Name:  "TYPESENSE_PROTOCOL",
											Value: "http",
										},
									},
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
	if err != nil {
		return err
	}

	return nil
}
