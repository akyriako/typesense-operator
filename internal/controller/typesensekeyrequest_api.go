package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"io"
	"net/http"
)

type KeyResponse struct {
	Actions     []string `json:"actions"`
	Collections []string `json:"collections"`
	Description string   `json:"description"`
	Id          int      `json:"id"`
	Value       string   `json:"value"`
}

func (r *TypesenseKeyRequestReconciler) CreateAPIKey(ctx context.Context, apiKey string, apiKeysURL string, keyRequest tsv1alpha1.TypesenseKeyRequest) (*KeyResponse, error) {
	//payload := fmt.Sprintf("{'description':'%s','actions': '%s' , 'collections': '%s' }", keyRequest.Spec.Description, keyRequest.Spec.Actions, keyRequest.Spec.Collections)
	payload := fmt.Sprintf(`{"description":"%s","actions": %s, "collections": %s}`, keyRequest.Spec.Description, keyRequest.Spec.Actions, keyRequest.Spec.Collections)
	payloadAsBytes := []byte(payload)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, apiKeysURL, bytes.NewBuffer(payloadAsBytes))
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-TYPESENSE-API-KEY", apiKey)
	request.Header.Set("Content-Type", "application/json")

	client := http.Client{}

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer request.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("unexpected status code: %d", response.StatusCode)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	data := &KeyResponse{}
	if err := json.Unmarshal(body, data); err != nil {
		return nil, err
	}

	return data, nil
}
