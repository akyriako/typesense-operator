package cert

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"
)

const (
	CertOrganization   = "typesense-operator"
	CertCommonName     = "typesense-operator-ca"
	CALifetimeYears    = 5
	LeafLifetimeMonths = 3
)

func makeSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 159))
	if err != nil {
		return nil, err
	}
	if serial.Sign() <= 0 {
		serial = serial.Add(serial, big.NewInt(1))
	}
	return serial, nil
}

func writePEM(bType string, payload []byte) ([]byte, error) {
	buf := new(bytes.Buffer)
	err := pem.Encode(buf, &pem.Block{Type: bType, Bytes: payload})
	return buf.Bytes(), err
}

func calculateSKID(pubKey *ecdsa.PublicKey) ([]byte, error) {
	pubBytes, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		return nil, err
	}
	hash := sha1.Sum(pubBytes)
	return hash[:], nil
}

func generateRootCA() (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte, []byte, error) {
	caSer, err := makeSerial()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	rootSKID, err := calculateSKID(&rootKey.PublicKey)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	rootCA := &x509.Certificate{
		SerialNumber: caSer,
		Subject: pkix.Name{
			Organization: []string{CertOrganization},
			CommonName:   CertCommonName,
		},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().AddDate(CALifetimeYears, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		SubjectKeyId:          rootSKID,
	}

	rootBytes, err := x509.CreateCertificate(rand.Reader, rootCA, rootCA, &rootKey.PublicKey, rootKey)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	caPEM, err := writePEM("CERTIFICATE", rootBytes)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	caKeyBytes, err := x509.MarshalPKCS8PrivateKey(rootKey)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	caKeyPEM, err := writePEM("PRIVATE KEY", caKeyBytes)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	return rootCA, rootKey, rootSKID, caPEM, caKeyPEM, nil
}

func GetWebhookSANs(serviceName, namespace string) []string {
	clusterDomain := os.Getenv("CLUSTER_DOMAIN")
	if clusterDomain == "" {
		clusterDomain = "cluster.local"
	}
	return []string{
		serviceName,
		serviceName + "." + namespace,
		serviceName + "." + namespace + ".svc",
		serviceName + "." + namespace + ".svc." + clusterDomain,
	}
}

func parseCA(certPEM, keyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		return nil, nil, errors.New("empty PEM")
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, errors.New("failed to decode CA cert")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, errors.New("failed to decode CA key")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	ecdsaKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("CA key is not ECDSA")
	}

	certPubKey, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, nil, errors.New("CA certificate public key is not ECDSA")
	}
	if !certPubKey.Equal(&ecdsaKey.PublicKey) {
		return nil, nil, errors.New("CA certificate and private key do not match")
	}

	return cert, ecdsaKey, nil
}

// GenerateWebhookCerts generates a self-signed CA and a server certificate for the webhook.
func GenerateWebhookCerts(serviceName, namespace string, existingCACert, existingCAKey []byte) ([]byte, []byte, []byte, []byte, error) {
	now := time.Now()
	start := now.Add(-5 * time.Minute)
	leafEnd := now.AddDate(0, LeafLifetimeMonths, 0)

	var rootCA *x509.Certificate
	var rootKey *ecdsa.PrivateKey
	var rootSKID []byte
	var caPEM, caKeyPEM []byte
	var err error

	if len(existingCACert) == 0 || len(existingCAKey) == 0 {
		rootCA, rootKey, rootSKID, caPEM, caKeyPEM, err = generateRootCA()
		if err != nil {
			return nil, nil, nil, nil, err
		}
	} else {
		rootCA, rootKey, err = parseCA(existingCACert, existingCAKey)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to parse existing CA (must be explicitly deleted to rotate root): %w", err)
		}

		if time.Until(rootCA.NotAfter) < 6*30*24*time.Hour {
			rootCA, rootKey, rootSKID, caPEM, caKeyPEM, err = generateRootCA()
			if err != nil {
				return nil, nil, nil, nil, err
			}
		} else {
			caPEM = existingCACert
			caKeyPEM = existingCAKey
			if len(rootCA.SubjectKeyId) > 0 {
				rootSKID = rootCA.SubjectKeyId
			} else {
				rootSKID, err = calculateSKID(&rootKey.PublicKey)
				if err != nil {
					return nil, nil, nil, nil, err
				}
			}
		}
	}

	leafSer, err := makeSerial()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	leafSKID, err := calculateSKID(&leafKey.PublicKey)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	leaf := &x509.Certificate{
		SerialNumber: leafSer,
		Subject: pkix.Name{
			Organization: []string{CertOrganization},
			CommonName:   serviceName,
		},
		DNSNames:              GetWebhookSANs(serviceName, namespace),
		NotBefore:             start,
		NotAfter:              leafEnd,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		SubjectKeyId:          leafSKID,
		AuthorityKeyId:        rootSKID,
	}

	leafBytes, err := x509.CreateCertificate(rand.Reader, leaf, rootCA, &leafKey.PublicKey, rootKey)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	certPEM, err := writePEM("CERTIFICATE", leafBytes)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	keyBytes, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	keyPEM, err := writePEM("PRIVATE KEY", keyBytes)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return caPEM, certPEM, keyPEM, caKeyPEM, nil
}
