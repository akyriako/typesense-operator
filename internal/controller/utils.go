package controller

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	letters    = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	debugLevel = 1
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

func generateSecureRandomString(length int) (string, error) {
	result := make([]byte, length)
	_, err := rand.Read(result)
	if err != nil {
		return "", err
	}

	for i := range result {
		result[i] = letters[int(result[i])%len(letters)]
	}
	return string(result), nil
}

func getLabels(ts *tsv1alpha1.TypesenseCluster) map[string]string {
	return map[string]string{
		"app": fmt.Sprintf("%s-sts", ts.Name),
	}
}

func getReverseProxyLabels(ts *tsv1alpha1.TypesenseCluster) map[string]string {
	return map[string]string{
		"app": fmt.Sprintf("%s-rp", ts.Name),
	}
}

func getObjectMeta(ts *tsv1alpha1.TypesenseCluster, name *string, annotations map[string]string) metav1.ObjectMeta {
	if name == nil {
		name = &ts.Name
	}

	return metav1.ObjectMeta{
		Name:        *name,
		Namespace:   ts.Namespace,
		Labels:      getLabels(ts),
		Annotations: annotations,
	}
}
