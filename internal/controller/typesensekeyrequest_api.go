package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"io"
	"net/http"
	"strings"
)

type CreateApiKeySuccessHttpResponse struct {
	Actions     []string `json:"actions"`
	Collections []string `json:"collections"`
	Description string   `json:"description"`
	Id          int      `json:"id"`
	Value       string   `json:"value"`
}

type CreateApiKeyFailHttpResponse struct {
	Message string `json:"message"`
}

func (r *TypesenseKeyRequestReconciler) createApiKeyHttpRequest(
	ctx context.Context,
	cluster *tsv1alpha1.TypesenseCluster,
	adminApiKey string,
	apiKeyRequest tsv1alpha1.TypesenseKeyRequest,
	apiKeyValue *string,
) (*CreateApiKeySuccessHttpResponse, error) {
	apiKeysUrl := getApiKeysUrl(cluster)

	r.logger.V(debugLevel).Info("preparing http request", "url", apiKeysUrl)

	var payload string
	if apiKeyValue != nil {
		payload = fmt.Sprintf(
			`{"description":"%s","actions": %s, "collections": %s, "value": "%s"}`,
			apiKeyRequest.Spec.Description,
			apiKeyRequest.Spec.Actions,
			apiKeyRequest.Spec.Collections,
			*apiKeyValue,
		)
	} else {
		payload = fmt.Sprintf(
			`{"description":"%s","actions": %s, "collections": %s}`,
			apiKeyRequest.Spec.Description,
			apiKeyRequest.Spec.Actions,
			apiKeyRequest.Spec.Collections,
		)
	}

	payloadAsBytes := []byte(payload)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, apiKeysUrl, bytes.NewBuffer(payloadAsBytes))
	if err != nil {
		return nil, err
	}

	request.Header.Set(strings.ToUpper(HttpRequestTypesenseApiKeyHeaderKey), adminApiKey)
	request.Header.Set("Content-Type", "application/json")

	client := http.Client{}

	response, err := client.Do(request)
	if err != nil {
		r.logger.Error(err, "http request failed")
		return nil, err
	}
	defer request.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		data := &CreateApiKeyFailHttpResponse{}
		if err := json.Unmarshal(body, data); err != nil {
			return nil, err
		}

		err = fmt.Errorf("creating api key failed: %s", strings.ToLower(data.Message))
		r.logger.Error(err, "http request failed", "httpStatusCode", response.StatusCode)

		return nil, err
	}

	data := &CreateApiKeySuccessHttpResponse{}
	if err := json.Unmarshal(body, data); err != nil {
		return nil, err
	}

	r.logger.V(debugLevel).Info("finished http request", "url", apiKeysUrl)
	return data, nil
}

func (r *TypesenseKeyRequestReconciler) deleteApiKeyHttpRequest(
	ctx context.Context,
	cluster *tsv1alpha1.TypesenseCluster,
	adminApiKey string,
	keyId string,
) error {
	apiKeysUrl := getApiKeysUrl(cluster)
	apiKeysUrl = fmt.Sprintf("%s/%s", apiKeysUrl, keyId)

	r.logger.V(debugLevel).Info("starting http request", "url", apiKeysUrl)

	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiKeysUrl, nil)
	if err != nil {
		return err
	}

	request.Header.Set(strings.ToUpper(HttpRequestTypesenseApiKeyHeaderKey), adminApiKey)

	client := http.Client{}

	response, err := client.Do(request)
	if err != nil {
		r.logger.Error(err, "http request failed")
		return err
	}

	if !(response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNotFound) {
		err := fmt.Errorf("deleting api key failed")
		r.logger.Error(err, "http request failed", "httpStatusCode", response.StatusCode)

		return err
	}

	r.logger.V(debugLevel).Info("finished http request", "url", apiKeysUrl)
	return nil
}
