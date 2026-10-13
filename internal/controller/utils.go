package controller

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"strings"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	letters    = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	debugLevel = 1
	labelApp   = "app"
)

func generateToken() (string, error) {
	token := make([]byte, 256)
	_, err := rand.Read(token)
	if err != nil {
		return "", err
	}

	base64EncodedToken := base64.StdEncoding.EncodeToString(token)
	return base64EncodedToken, nil
}

func mergeMaps(maps ...map[string]string) map[string]string {
	size := 0
	for _, m := range maps {
		size += len(m)
	}

	if size == 0 {
		return nil
	}

	merged := make(map[string]string, size)
	for _, m := range maps {
		for k, v := range m {
			merged[k] = v
		}
	}

	return merged
}

func mergeLabels(maps ...map[string]string) map[string]string {
	return mergeMaps(maps...)
}

func mergeAnnotations(maps ...map[string]string) map[string]string {
	return mergeMaps(maps...)
}

func getMergedAnnotations(ts *tsv1alpha1.TypesenseCluster) map[string]string {
	// Remove ts.Spec.ServiceAnnotations in 0.5.0
	annotations := ts.Spec.ServiceAnnotations
	if ts.Spec.Service != nil && ts.Spec.Service.Annotations != nil {
		annotations = mergeAnnotations(annotations, ts.Spec.Service.Annotations)
	}

	return annotations
}

func getMergedLabels(def map[string]string, scoped map[string]string) map[string]string {
	return mergeLabels(def, scoped)
}

func getDefaultLabels(ts *tsv1alpha1.TypesenseCluster) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "typesense-operator",
		"app.kubernetes.io/name":       "typesense",
		"app.kubernetes.io/instance":   ts.Name,
	}
}

func getLabels(ts *tsv1alpha1.TypesenseCluster) map[string]string {
	return map[string]string{
		labelApp: fmt.Sprintf(ClusterAppLabel, ts.Name),
	}
}

func getObjectMeta(ts *tsv1alpha1.TypesenseCluster, name *string, annotations map[string]string) metav1.ObjectMeta {
	if name == nil {
		name = &ts.Name
	}

	return metav1.ObjectMeta{
		Name:        *name,
		Namespace:   ts.Namespace,
		Labels:      getMergedLabels(getDefaultLabels(ts), getLabels(ts)),
		Annotations: annotations,
	}
}

func getReverseProxyLabels(ts *tsv1alpha1.TypesenseCluster) map[string]string {
	return map[string]string{
		labelApp: fmt.Sprintf(ClusterReverseProxyAppLabel, ts.Name),
	}
}

func getReverseProxyObjectMeta(ts *tsv1alpha1.TypesenseCluster, name *string, annotations map[string]string) metav1.ObjectMeta {
	if name == nil {
		name = &ts.Name
	}

	return metav1.ObjectMeta{
		Name:        *name,
		Namespace:   ts.Namespace,
		Labels:      getMergedLabels(getDefaultLabels(ts), getReverseProxyLabels(ts)),
		Annotations: annotations,
	}
}

func getPodMonitorLabels(ts *tsv1alpha1.TypesenseCluster) map[string]string {
	return map[string]string{
		labelApp: fmt.Sprintf(ClusterMetricsPodMonitorAppLabel, ts.Name),
	}
}

func getIngressObjectMeta(ts *tsv1alpha1.TypesenseCluster, name *string, labels, annotations map[string]string) metav1.ObjectMeta {
	if name == nil {
		name = &ts.Name
	}

	defaultLabels := getMergedLabels(getDefaultLabels(ts), getLabels(ts))

	return metav1.ObjectMeta{
		Name:        *name,
		Namespace:   ts.Namespace,
		Labels:      getMergedLabels(defaultLabels, labels),
		Annotations: annotations,
	}
}

func getPodMonitorObjectMeta(ts *tsv1alpha1.TypesenseCluster, name *string, annotations map[string]string) metav1.ObjectMeta {
	if name == nil {
		name = &ts.Name
	}

	return metav1.ObjectMeta{
		Name:        *name,
		Namespace:   ts.Namespace,
		Labels:      getMergedLabels(getDefaultLabels(ts), getPodMonitorLabels(ts)),
		Annotations: annotations,
	}
}

func getHttpRouteLabels(ts *tsv1alpha1.TypesenseCluster, spec tsv1alpha1.HttpRouteSpec) map[string]string {
	route := map[string]string{
		labelApp: fmt.Sprintf(ClusterAppLabel, ts.Name),
		"route":  fmt.Sprintf(ClusterHttpRoute, ts.Name, spec.Name),
	}

	defaults := getDefaultLabels(ts)

	return mergeLabels(defaults, route)
}

func getHttpRouteObjectMeta(ts *tsv1alpha1.TypesenseCluster, spec tsv1alpha1.HttpRouteSpec, name *string, labels, annotations map[string]string) metav1.ObjectMeta {
	if name == nil {
		name = &ts.Name
	}

	return metav1.ObjectMeta{
		Name:        *name,
		Namespace:   ts.Namespace,
		Labels:      mergeLabels(getHttpRouteLabels(ts, spec), labels),
		Annotations: annotations,
	}
}

func getReferenceGrantObjectMeta(ts *tsv1alpha1.TypesenseCluster, spec tsv1alpha1.HttpRouteSpec) metav1.ObjectMeta {
	ns := ts.Namespace
	if spec.ParentRef.Namespace != nil {
		ns = string(*spec.ParentRef.Namespace)
	}

	return metav1.ObjectMeta{
		Name:      getReferenceGrantName(ts, spec.Name),
		Namespace: ns, // namespace of the *target* (Gateway)
		Labels:    getHttpRouteLabels(ts, spec),
	}
}

func getReferenceGrantName(ts *tsv1alpha1.TypesenseCluster, routeName string) string {
	return fmt.Sprintf(ClusterHttpRouteReferenceGrant, ts.Namespace, ts.Name, routeName)
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}

	return false
}

func hasIP4Prefix(s string) bool {
	return net.ParseIP(s) != nil
}

func toTitle(s string) string {
	return cases.Title(language.Und, cases.NoLower).String(s)
}

func filterMap(m map[string]string, filters ...string) map[string]string {
	if len(m) == 0 {
		return m
	}

	filtered := make(map[string]string, len(m))
	for key, value := range m {
		skip := false
		for _, f := range filters {
			if strings.Contains(key, f) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		filtered[key] = value
	}

	return filtered
}

// withoutKeys returns a copy of m without the given keys. Unlike filterMap, keys are matched exactly,
// so user labels such as "app.kubernetes.io/part-of" are not dropped because they contain "app".
func withoutKeys(m map[string]string, keys ...string) map[string]string {
	if len(m) == 0 {
		return m
	}

	filtered := make(map[string]string, len(m))
	for key, value := range m {
		if !contains(keys, key) {
			filtered[key] = value
		}
	}

	return filtered
}

func getImageTag(image string) string {
	pos := strings.LastIndex(image, ":")
	if pos == -1 {
		return image
	}
	return image[pos+1:]
}

func externalTrafficPolicyEqual(
	current corev1.ServiceExternalTrafficPolicy,
	desired *corev1.ServiceExternalTrafficPolicy,
) bool {
	if desired == nil {
		// Kubernetes automatically defaults ExternalTrafficPolicy to "Cluster" for
		// LoadBalancer/NodePort services when it is not explicitly provided.
		// Both "" and "Cluster" should be considered equal to nil (no override).
		return current == "" || current == corev1.ServiceExternalTrafficPolicyCluster
	}

	return current == *desired
}

func isPodUnschedulable(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodPending {
		return false
	}

	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == corev1.PodReasonUnschedulable {
			return true
		}
	}
	return false
}
