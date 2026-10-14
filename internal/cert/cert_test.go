package cert

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
)

const (
	testServiceName = "typesense-webhook-service"
	testNamespace   = "typesense-system"
)

func TestGenerateWebhookCerts(t *testing.T) {
	caPEM, certPEM, keyPEM, caKeyPEM, err := GenerateWebhookCerts(testServiceName, testNamespace, nil, nil)
	if err != nil {
		t.Fatalf("GenerateWebhookCerts returned an error: %v", err)
	}

	if len(caPEM) == 0 || len(certPEM) == 0 || len(keyPEM) == 0 || len(caKeyPEM) == 0 {
		t.Fatal("GenerateWebhookCerts returned empty PEM blocks")
	}

	// Verify that the generated CA certificate is valid PEM and a true CA
	caBlock, _ := pem.Decode(caPEM)
	if caBlock == nil || caBlock.Type != "CERTIFICATE" {
		t.Fatal("Failed to decode CA CERTIFICATE PEM block")
	}

	parsedCA, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse x509 CA certificate: %v", err)
	}

	if !parsedCA.IsCA {
		t.Error("Generated CA does not have the IsCA flag set to true")
	}

	expectedCAEnd := time.Now().AddDate(CALifetimeYears, 0, 0)
	if diff := parsedCA.NotAfter.Sub(expectedCAEnd); diff < -time.Minute || diff > time.Minute {
		t.Errorf("Expected CA validity to be exactly %d years, got diff %v", CALifetimeYears, diff)
	}

	// Verify that the generated server certificate is valid PEM
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		t.Fatal("Failed to decode CERTIFICATE PEM block")
	}

	parsedCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse x509 certificate: %v", err)
	}

	// Verify Key identifiers link Leaf to CA properly
	if len(parsedCA.SubjectKeyId) == 0 {
		t.Error("CA SubjectKeyId is empty")
	}
	if !bytes.Equal(parsedCert.AuthorityKeyId, parsedCA.SubjectKeyId) {
		t.Error("Leaf AuthorityKeyId does not match CA SubjectKeyId")
	}

	// Verify the correct DNS names are injected
	expectedDNSNames := GetWebhookSANs(testServiceName, testNamespace)

	if len(parsedCert.DNSNames) != len(expectedDNSNames) {
		t.Fatalf("Expected %d DNS names, got %d", len(expectedDNSNames), len(parsedCert.DNSNames))
	}
	for i, expected := range expectedDNSNames {
		if parsedCert.DNSNames[i] != expected {
			t.Errorf("Expected SAN %s at index %d, got %s", expected, i, parsedCert.DNSNames[i])
		}
	}

	// Verify the expiration date is set exactly to LeafLifetimeMonths
	expectedLeafEnd := time.Now().AddDate(0, LeafLifetimeMonths, 0)
	if diff := parsedCert.NotAfter.Sub(expectedLeafEnd); diff < -time.Minute || diff > time.Minute {
		t.Errorf("Expected Leaf validity to be exactly %d months, got diff %v", LeafLifetimeMonths, diff)
	}
}

func TestGenerateWebhookCerts_ReusesValidCA(t *testing.T) {
	// First pass: generate a fresh set
	caPEM, _, _, caKeyPEM, err := GenerateWebhookCerts(testServiceName, testNamespace, nil, nil)
	if err != nil {
		t.Fatalf("Initial generation failed: %v", err)
	}

	// Second pass: supply the generated CA
	newCAPEM, newCertPEM, newKeyPEM, newCAKeyPEM, err := GenerateWebhookCerts(testServiceName, testNamespace, caPEM, caKeyPEM)
	if err != nil {
		t.Fatalf("Subsequent generation failed: %v", err)
	}

	// Verify the CA was absolutely untouched
	if !bytes.Equal(caPEM, newCAPEM) {
		t.Error("CA certificate was unexpectedly regenerated despite being valid")
	}
	if !bytes.Equal(caKeyPEM, newCAKeyPEM) {
		t.Error("CA private key was unexpectedly regenerated despite being valid")
	}

	// Verify we still got leaf certificates back
	if len(newCertPEM) == 0 || len(newKeyPEM) == 0 {
		t.Error("Subsequent generation returned empty leaf certificate or key")
	}
}

func TestGenerateWebhookCerts_RegeneratesBrokenCA(t *testing.T) {
	_, _, _, _, err := GenerateWebhookCerts(testServiceName, testNamespace, []byte("bad cert"), []byte("bad key"))
	if err == nil {
		t.Fatal("GenerateWebhookCerts should return an error for broken CA input, but got nil")
	}
}
