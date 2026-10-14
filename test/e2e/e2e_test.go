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

package e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/akyriako/typesense-operator/test/utils"
)

const (
	namespace           = "typesense-operator-system"
	typesenseImage      = "typesense/typesense:30.2"
	typesenseImageOld   = "typesense/typesense:27.1"
	exporterImage       = "quay.io/akyriako/typesense-prometheus-exporter:0.1.9"
	healthcheckImage    = "quay.io/akyriako/typesense-healthcheck:0.2.0"
	nginxImage          = "nginx:alpine"
	scraperImage        = "typesense/docsearch-scraper:0.3.0"
	podPhaseRunning     = "Running"
	conditionStatusTrue = "True"
	phaseQuorumReady    = "QuorumReady"
	badCertBase64       = "YmFkLWNlcnQ="
)

func buildManifest(name, ns string) string {
	return fmt.Sprintf(`apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  name: %s
  namespace: %s
spec:
  image: %s
  replicas: 1
  apiPort: 8108
  peeringPort: 8107
  storage:
    storageClassName: standard
    accessMode: ReadWriteOnce
    size: 1Gi
`, name, ns, typesenseImage)
}

func verifyStatefulSetReadyReplicas(name, ns string, expected int) error {
	cmd := exec.Command("kubectl", "get", "statefulset", name, "-n", ns, "-o", "jsonpath={.status.readyReplicas}")
	output, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != strconv.Itoa(expected) {
		return fmt.Errorf("expected %d ready replicas, got %s", expected, string(output))
	}
	return nil
}

func verifyPodReady(name, ns string) error {
	cmd := exec.Command("kubectl", "get", "pod", name, "-n", ns, "-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}")
	output, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != conditionStatusTrue {
		return fmt.Errorf("expected pod %s to be Ready, got %s", name, string(output))
	}
	return nil
}

func verifyPVCBound(name, ns string) error {
	cmd := exec.Command("kubectl", "get", "pvc", name, "-n", ns, "-o", "jsonpath={.status.phase}")
	output, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != "Bound" {
		return fmt.Errorf("expected pvc %s phase Bound, got %s", name, string(output))
	}
	return nil
}

func verifyPodPhase(name, ns, expectedPhase string) error {
	cmd := exec.Command("kubectl", "get", "pod", name, "-n", ns, "-o", "jsonpath={.status.phase}")
	output, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != expectedPhase {
		return fmt.Errorf("expected pod %s phase %s, got %s", name, expectedPhase, string(output))
	}
	return nil
}

func verifyTypesenseClusterPhase(name, expectedPhase string) error {
	cmd := exec.Command("kubectl", "get", "typesensecluster", name, "-n", namespace, "-o", "jsonpath={.status.phase}")
	output, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != expectedPhase {
		return fmt.Errorf("expected typesensecluster %s phase %s, got %s", name, expectedPhase, string(output))
	}
	return nil
}

func verifyTypesenseClusterPhaseAndObservedGeneration(name, ns, expectedPhase string) error {
	cmd := exec.Command("kubectl", "get", "typesensecluster", name, "-n", ns, "-o", "jsonpath={.metadata.generation},{.status.observedGeneration},{.status.phase}")
	output, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	parts := strings.Split(string(output), ",")
	if len(parts) != 3 {
		return fmt.Errorf("unexpected output format: %s", string(output))
	}
	if parts[0] != parts[1] {
		return fmt.Errorf("generation mismatch: generation=%s, observedGeneration=%s", parts[0], parts[1])
	}
	if strings.TrimSpace(parts[2]) != expectedPhase {
		return fmt.Errorf("expected phase %s, got %s", expectedPhase, strings.TrimSpace(parts[2]))
	}
	return nil
}

func verifyTypesenseClusterCondition(name, ns, conditionType, expectedStatus string) error {
	cmd := exec.Command("kubectl", "get", "typesensecluster", name, "-n", ns, "-o", "jsonpath={.status.conditions[?(@.type==\""+conditionType+"\")].status}")
	output, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != expectedStatus {
		return fmt.Errorf("expected typesensecluster %s condition %s=%s, got %s", name, conditionType, expectedStatus, string(output))
	}
	return nil
}

func verifyTypesenseClusterPhaseSet(name, ns string) error {
	cmd := exec.Command("kubectl", "get", "typesensecluster", name, "-n", ns, "-o", "jsonpath={.status.phase}")
	output, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(output))) == 0 {
		return fmt.Errorf("expected typesensecluster %s phase to be set, but got empty string", name)
	}
	return nil
}

func verifyPodScheduledReason(name, ns, expectedReason string) error {
	cmd := exec.Command("kubectl", "get", "pod", name, "-n", ns, "-o", "jsonpath={.status.conditions[?(@.type==\"PodScheduled\")].reason}")
	output, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != expectedReason {
		return fmt.Errorf("expected pod %s PodScheduled reason %s, got %s", name, expectedReason, string(output))
	}
	return nil
}

func getPodUID(name, ns string) (string, error) {
	cmd := exec.Command("kubectl", "get", "pod", name, "-n", ns, "-o", "jsonpath={.metadata.uid}")
	output, err := utils.Run(cmd)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func verifyPVCPhase(name, ns, expectedPhase string) error {
	cmd := exec.Command("kubectl", "get", "pvc", name, "-n", ns, "-o", "jsonpath={.status.phase}")
	output, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(output)) != expectedPhase {
		return fmt.Errorf("expected pvc %s phase %s, got %s", name, expectedPhase, string(output))
	}
	return nil
}

func verifyControllerLogsContain(ns, substring string) error {
	cmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", ns, "--tail=200")
	output, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	if !strings.Contains(string(output), substring) {
		return fmt.Errorf("expected controller logs to contain %q", substring)
	}
	return nil
}

func buildManifestWithStorageClass(name, ns, storageClass string) string {
	return fmt.Sprintf(`apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  name: %s
  namespace: %s
spec:
  image: %s
  replicas: 1
  apiPort: 8108
  peeringPort: 8107
  storage:
    storageClassName: %s
    accessMode: ReadWriteOnce
    size: 1Gi
`, name, ns, typesenseImage, storageClass)
}

func verifyTypesenseClusterObservedGenerationMatches(name, ns string) error {
	cmd := exec.Command("kubectl", "get", "typesensecluster", name, "-n", ns, "-o", "jsonpath={.metadata.generation}={.status.observedGeneration}")
	output, err := utils.Run(cmd)
	if err != nil {
		return err
	}
	parts := strings.Split(string(output), "=")
	if len(parts) != 2 {
		return fmt.Errorf("unexpected output format: %s", string(output))
	}
	if parts[0] != parts[1] {
		return fmt.Errorf("generation mismatch: metadata.generation=%s, status.observedGeneration=%s", parts[0], parts[1])
	}
	return nil
}

var _ = Describe("controller", Ordered, func() {
	BeforeAll(func() {
		By("pulling and loading required images into Kind")
		imagesToLoad := []string{
			typesenseImage,
			typesenseImageOld,
			exporterImage,
			healthcheckImage,
			nginxImage,
			scraperImage,
		}
		for _, img := range imagesToLoad {
			// Allow failures for local-only images
			cmd := exec.Command("docker", "pull", img)
			_, _ = utils.Run(cmd)

			// Verify the image is actually present locally before proceeding.
			// This prevents build/load errors if the image tag doesn't exist on Docker Hub.
			inspectCmd := exec.Command("docker", "image", "inspect", img)
			if _, err := utils.Run(inspectCmd); err != nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "warning: image %s not found remotely or locally, skipping load...\n", img)
				continue
			}

			// Workaround for Docker Desktop containerd multi-arch export bug:
			// Wrap the pulled image in a new single-platform build so that 'docker save'
			// (which kind load uses) doesn't export a multi-arch manifest with missing blobs.
			buildCmd := exec.Command("docker", "build", "-t", img, "-")
			buildCmd.Stdin = strings.NewReader(fmt.Sprintf("FROM %s\n", img))
			if _, err := utils.Run(buildCmd); err != nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "warning: failed to repackage image %s: %v\n", img, err)
			}

			err := utils.LoadImageToKindClusterWithName(img)
			if err != nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "warning: failed to load image %s into kind: %v\n", img, err)
			}
		}

		By("installing prometheus operator")
		Expect(utils.InstallPrometheusOperator()).To(Succeed())

		By("installing Gateway API CRDs")
		Expect(utils.InstallGatewayAPICRDs()).To(Succeed())

		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	AfterAll(func() {
		By("cleaning up all TypesenseClusters to release finalizers")
		cmd := exec.Command("kubectl", "delete", "typesensecluster", "--all", "--all-namespaces", "--timeout=2m")
		_, _ = utils.Run(cmd)

		By("removing cluster-scoped webhook configuration")
		cmd = exec.Command("kubectl", "delete", "validatingwebhookconfigurations", "typesense-operator-validating-webhook-configuration", "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("uninstalling the Prometheus manager bundle")
		utils.UninstallPrometheusOperator()

		By("uninstalling the Gateway API CRDs")
		utils.UninstallGatewayAPICRDs()

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace, "--timeout=2m")
		_, _ = utils.Run(cmd)
	})

	Context("Operator", func() {
		It("should run successfully", func() {
			var controllerPodName string
			var err error

			// projectimage stores the name of the image used in the example
			var projectimage = "example.com/typesense-operator:v0.0.1"

			By("building the manager(Operator) image")
			cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("loading the the manager(Operator) image on Kind")
			err = utils.LoadImageToKindClusterWithName(projectimage)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("installing CRDs")
			cmd = exec.Command("make", "install")
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("deploying the controller-manager")
			cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func() error {
				// Get pod name

				cmd = exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				podNames := utils.GetNonEmptyLines(string(podOutput))
				if len(podNames) != 1 {
					return fmt.Errorf("expect 1 controller pods running, but got %d", len(podNames))
				}
				controllerPodName = podNames[0]
				ExpectWithOffset(2, controllerPodName).Should(ContainSubstring("controller-manager"))

				// Validate pod status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}",
					"-n", namespace,
				)
				status, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				if string(status) != conditionStatusTrue {
					return fmt.Errorf("controller pod is not ready, status: %s", status)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyControllerUp, 2*time.Minute, time.Second).Should(Succeed())

			By("waiting for webhook service endpoints to be populated")
			verifyWebhookEndpoints := func() error {
				cmd = exec.Command("kubectl", "get", "endpoints", "typesense-operator-webhook-service", "-n", namespace, "-o", "jsonpath={.subsets[*].addresses[*].ip}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if len(strings.TrimSpace(string(output))) == 0 {
					return fmt.Errorf("webhook endpoints not populated yet")
				}
				return nil
			}
			EventuallyWithOffset(1, verifyWebhookEndpoints, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying the operator generated and injected the webhook certificates")
			verifyCertInjection := func() error {
				cmd = exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if !strings.Contains(string(output), "Generating self-signed webhook certificates") {
					return fmt.Errorf("certificate generation log not found")
				}
				if !strings.Contains(string(output), "Successfully patched ValidatingWebhookConfiguration") {
					return fmt.Errorf("webhook patching log not found")
				}
				return nil
			}
			EventuallyWithOffset(1, verifyCertInjection, 2*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should manage, protect, and autonomously roll over webhook certificates", func() {
			secretName := "typesense-operator-webhook-cert"
			var originalCACrt, originalTLSCrt string

			By("verifying the webhook secret is created and populated")
			verifySecret := func() error {
				cmd := exec.Command("kubectl", "get", "secret", secretName, "-n", namespace, "-o", "jsonpath={.data['ca\\.crt']}")
				outCA, err := utils.Run(cmd)
				if err != nil || len(outCA) == 0 {
					return fmt.Errorf("missing ca.crt")
				}
				originalCACrt = string(outCA)

				cmd = exec.Command("kubectl", "get", "secret", secretName, "-n", namespace, "-o", "jsonpath={.data['tls\\.crt']}")
				outTLS, err := utils.Run(cmd)
				if err != nil || len(outTLS) == 0 {
					return fmt.Errorf("missing tls.crt")
				}
				originalTLSCrt = string(outTLS)
				return nil
			}
			EventuallyWithOffset(1, verifySecret, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("simulating leaf certificate expiration/loss (corrupting tls.crt)")
			patch := fmt.Sprintf(`{"data":{"tls.crt":"%s"}}`, badCertBase64)
			cmd := exec.Command("kubectl", "patch", "secret", secretName, "-n", namespace, "--type", "merge", "-p", patch)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the operator regenerates the leaf certificate but keeps the CA intact")
			var newTLSCrt string
			verifyLeafRollover := func() error {
				cmd := exec.Command("kubectl", "get", "secret", secretName, "-n", namespace, "-o", "jsonpath={.data['tls\\.crt']}")
				outTLS, err := utils.Run(cmd)
				if err != nil || len(outTLS) == 0 {
					return fmt.Errorf("tls.crt not regenerated yet")
				}
				newTLSCrt = string(outTLS)
				if newTLSCrt == originalTLSCrt || newTLSCrt == badCertBase64 {
					return fmt.Errorf("tls.crt was not rotated")
				}

				cmd = exec.Command("kubectl", "get", "secret", secretName, "-n", namespace, "-o", "jsonpath={.data['ca\\.crt']}")
				outCA, _ := utils.Run(cmd)
				if string(outCA) != originalCACrt {
					return fmt.Errorf("ca.crt was unexpectedly modified during leaf rotation")
				}
				return nil
			}
			EventuallyWithOffset(1, verifyLeafRollover, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("simulating leaf certificate nearing expiration (< 30 days)")
			priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			leafTemplate := &x509.Certificate{
				SerialNumber: big.NewInt(1),
				NotBefore:    time.Now().Add(-1 * time.Hour),
				NotAfter:     time.Now().Add(15 * 24 * time.Hour), // 15 days left
				DNSNames:     []string{"typesense-operator-webhook-service", "typesense-operator-webhook-service." + namespace, "typesense-operator-webhook-service." + namespace + ".svc", "typesense-operator-webhook-service." + namespace + ".svc.cluster.local"},
			}
			leafCertBytes, _ := x509.CreateCertificate(rand.Reader, leafTemplate, leafTemplate, &priv.PublicKey, priv)
			leafCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafCertBytes})
			expiringLeafB64 := base64.StdEncoding.EncodeToString(leafCertPEM)

			patchLeaf := fmt.Sprintf(`{"data":{"tls.crt":"%s"}}`, expiringLeafB64)
			cmd = exec.Command("kubectl", "patch", "secret", secretName, "-n", namespace, "--type", "merge", "-p", patchLeaf)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the operator detects impending leaf expiration and rotates it")
			verifyLeafExpRollover := func() error {
				cmd := exec.Command("kubectl", "get", "secret", secretName, "-n", namespace, "-o", "jsonpath={.data['tls\\.crt']}")
				outTLS, err := utils.Run(cmd)
				if err != nil || len(outTLS) == 0 {
					return fmt.Errorf("tls.crt missing")
				}
				if string(outTLS) == expiringLeafB64 {
					return fmt.Errorf("tls.crt was not rotated")
				}
				return nil
			}
			EventuallyWithOffset(1, verifyLeafExpRollover, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("simulating CA certificate nearing expiration (< 6 months)")
			caTemplate := &x509.Certificate{
				SerialNumber:          big.NewInt(2),
				IsCA:                  true,
				BasicConstraintsValid: true,
				NotBefore:             time.Now().Add(-1 * time.Hour),
				NotAfter:              time.Now().Add(5 * 30 * 24 * time.Hour), // 5 months left
			}
			caCertBytes, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &priv.PublicKey, priv)
			caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertBytes})
			caExpiringB64 := base64.StdEncoding.EncodeToString(caCertPEM)

			caKeyBytes, _ := x509.MarshalPKCS8PrivateKey(priv)
			caKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: caKeyBytes})
			caKeyB64 := base64.StdEncoding.EncodeToString(caKeyPEM)

			patchCA := fmt.Sprintf(`{"data":{"ca.crt":"%s", "ca.key":"%s"}}`, caExpiringB64, caKeyB64)
			cmd = exec.Command("kubectl", "patch", "secret", secretName, "-n", namespace, "--type", "merge", "-p", patchCA)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the operator detects impending CA expiration and rotates the entire chain")
			verifyCAExpRollover := func() error {
				cmd := exec.Command("kubectl", "get", "secret", secretName, "-n", namespace, "-o", "jsonpath={.data['ca\\.crt']}")
				outCA, err := utils.Run(cmd)
				if err != nil || len(outCA) == 0 {
					return fmt.Errorf("ca.crt missing")
				}
				if string(outCA) == caExpiringB64 {
					return fmt.Errorf("ca.crt was not rotated")
				}
				return nil
			}
			EventuallyWithOffset(1, verifyCAExpRollover, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("simulating CA corruption (modifying ca.crt with invalid base64 data)")
			patch = fmt.Sprintf(`{"data":{"ca.crt":"%s"}}`, badCertBase64)
			cmd = exec.Command("kubectl", "patch", "secret", secretName, "-n", namespace, "--type", "merge", "-p", patch)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the operator refuses to silently overwrite a corrupted CA (Model A protection)")
			verifyNoSilentRollover := func() error {
				cmd := exec.Command("kubectl", "get", "secret", secretName, "-n", namespace, "-o", "jsonpath={.data['ca\\.crt']}")
				outCA, _ := utils.Run(cmd)
				if string(outCA) != badCertBase64 {
					return fmt.Errorf("CA was silently rotated, breaking Model A protection")
				}
				return nil
			}
			ConsistentlyWithOffset(1, verifyNoSilentRollover, 15*time.Second, 2*time.Second).Should(Succeed())

			By("simulating administrator intervention by explicitly deleting the corrupted secret")
			cmd = exec.Command("kubectl", "delete", "secret", secretName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the operator generates a completely new trust chain after explicit deletion")
			verifyCARollover := func() error {
				cmd := exec.Command("kubectl", "get", "secret", secretName, "-n", namespace, "-o", "jsonpath={.data['ca\\.crt']}")
				outCA, err := utils.Run(cmd)
				if err != nil || len(outCA) == 0 {
					return fmt.Errorf("ca.crt not regenerated yet")
				}
				if string(outCA) == originalCACrt || string(outCA) == badCertBase64 {
					return fmt.Errorf("ca.crt was not rotated")
				}

				cmd = exec.Command("kubectl", "get", "secret", secretName, "-n", namespace, "-o", "jsonpath={.data['tls\\.crt']}")
				outTLS, _ := utils.Run(cmd)
				if string(outTLS) == newTLSCrt {
					return fmt.Errorf("tls.crt was not rotated alongside CA")
				}
				return nil
			}
			EventuallyWithOffset(1, verifyCARollover, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying the ValidatingWebhookConfiguration has the updated CA bundle")
			verifyWebhookPatch := func() error {
				cmd := exec.Command("kubectl", "get", "validatingwebhookconfigurations", "typesense-operator-validating-webhook-configuration", "-o", "jsonpath={.webhooks[0].clientConfig.caBundle}")
				outCA, err := utils.Run(cmd)
				if err != nil {
					return err
				}

				cmd = exec.Command("kubectl", "get", "secret", secretName, "-n", namespace, "-o", "jsonpath={.data['ca\\.crt']}")
				expectedCA, _ := utils.Run(cmd)

				if string(outCA) != string(expectedCA) {
					return fmt.Errorf("webhook caBundle does not match secret ca.crt")
				}
				return nil
			}
			EventuallyWithOffset(1, verifyWebhookPatch, 2*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should provision a TypesenseCluster successfully", func() {
			By("creating a TypesenseCluster custom resource")
			typesenseManifest := buildManifest("typesense-e2e", namespace)
			manifestPath, err := filepath.Abs("e2e-ts-cluster.yaml")
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(manifestPath, []byte(typesenseManifest), 0644)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.Remove(manifestPath) }()

			applyCluster := func() error {
				cmd := exec.Command("kubectl", "apply", "-f", manifestPath)
				_, err := utils.Run(cmd)
				if err != nil {
					logCmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
					logs, _ := utils.Run(logCmd)
					fmt.Printf("\n--- OPERATOR LOGS ---\n%s\n--------------------\n\n", string(logs))
				}
				return err
			}
			EventuallyWithOffset(1, applyCluster, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("waiting for the TypesenseCluster StatefulSet to become ready")
			verifyStatefulSetReady := func() error {
				return verifyStatefulSetReadyReplicas("typesense-e2e-sts", namespace, 1)
			}
			// Give the cluster 3 minutes to pull the Typesense image and start
			EventuallyWithOffset(1, verifyStatefulSetReady, 3*time.Minute, 5*time.Second).Should(Succeed(), func() string {
				logCmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
				logs, _ := utils.Run(logCmd)
				podCmd := exec.Command("kubectl", "describe", "pods", "-n", namespace)
				pods, _ := utils.Run(podCmd)
				return fmt.Sprintf("\n--- OPERATOR LOGS ---\n%s\n--- PODS ---\n%s\n--------------------\n\n", string(logs), string(pods))
			})

			By("verifying the first Typesense pod is ready and its PVC is bound")
			EventuallyWithOffset(1, func() error {
				if err := verifyPodReady("typesense-e2e-sts-0", namespace); err != nil {
					return err
				}
				return verifyPVCBound("data-typesense-e2e-sts-0", namespace)
			}, 2*time.Minute, 5*time.Second).Should(Succeed(), func() string {
				podCmd := exec.Command("kubectl", "describe", "pod", "typesense-e2e-sts-0", "-n", namespace)
				podDesc, _ := utils.Run(podCmd)
				pvcCmd := exec.Command("kubectl", "describe", "pvc", "data-typesense-e2e-sts-0", "-n", namespace)
				pvcDesc, _ := utils.Run(pvcCmd)
				return fmt.Sprintf("\n--- POD ---\n%s\n--- PVC ---\n%s\n--------------------\n\n", string(podDesc), string(pvcDesc))
			})
		})

		It("should tolerate a pending PVC in an unschedulable pod and recover once storage is available", func() {
			const (
				delayStorageClass = "typesense-e2e-delay-sc"
				delayClusterName  = "typesense-delay-e2e"
				delayPodName      = "typesense-delay-e2e-sts-0"
				delayPVCName      = "data-typesense-delay-e2e-sts-0"
				delayPVName       = "pv-typesense-delay-e2e-sts-0"
			)

			By("creating a StorageClass that does not provision volumes automatically")
			scYAML := fmt.Sprintf(`apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: %s
provisioner: kubernetes.io/no-provisioner
volumeBindingMode: WaitForFirstConsumer
reclaimPolicy: Retain
`, delayStorageClass)
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(scYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				cmd := exec.Command("kubectl", "delete", "storageclass", delayStorageClass, "--ignore-not-found")
				_, _ = utils.Run(cmd)
			}()

			By("creating a TypesenseCluster that uses the delayed StorageClass")
			typesenseManifest := buildManifestWithStorageClass(delayClusterName, namespace, delayStorageClass)
			manifestPath, err := filepath.Abs("e2e-ts-delay-cluster.yaml")
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(manifestPath, []byte(typesenseManifest), 0644)
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				_ = os.Remove(manifestPath)
				cmd := exec.Command("kubectl", "delete", "typesensecluster", delayClusterName, "-n", namespace, "--ignore-not-found", "--wait=false")
				_, _ = utils.Run(cmd)
				cmd = exec.Command("kubectl", "delete", "pvc", delayPVCName, "-n", namespace, "--ignore-not-found")
				_, _ = utils.Run(cmd)
				cmd = exec.Command("kubectl", "delete", "pv", delayPVName, "--ignore-not-found")
				_, _ = utils.Run(cmd)

				clusterName := "kind"
				if v, ok := os.LookupEnv("KIND_CLUSTER"); ok {
					clusterName = v
				}
				cmd = exec.Command("docker", "exec", clusterName+"-control-plane", "rm", "-rf", "/tmp/"+delayPVName)
				_, _ = utils.Run(cmd)
			}()

			By("applying the delayed TypesenseCluster manifest")
			applyCluster := func() error {
				cmd := exec.Command("kubectl", "apply", "-f", manifestPath)
				_, err := utils.Run(cmd)
				return err
			}
			EventuallyWithOffset(1, applyCluster, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("waiting for the pod to become pending due to an unschedulable PVC")
			verifyPendingUnschedulable := func() error {
				cmd := exec.Command("kubectl", "get", "pod", delayPodName, "-n", namespace, "-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if strings.TrimSpace(string(output)) != "Pending" {
					return fmt.Errorf("expected pod %s to be Pending, got %s", delayPodName, string(output))
				}
				if err := verifyPodScheduledReason(delayPodName, namespace, "Unschedulable"); err != nil {
					return err
				}
				return verifyPVCPhase(delayPVCName, namespace, "Pending")
			}
			EventuallyWithOffset(1, verifyPendingUnschedulable, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying the operator requeues the reconcile instead of deleting the pod")
			EventuallyWithOffset(1, func() error {
				return verifyControllerLogsContain(namespace, "requeueAfter")
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("ensuring the operator does not immediately delete the pending pod")
			initialUID, err := getPodUID(delayPodName, namespace)
			Expect(err).NotTo(HaveOccurred())
			verifyUIDStaysSame := func() error {
				currentUID, err := getPodUID(delayPodName, namespace)
				if err != nil {
					return err
				}
				if currentUID != initialUID {
					return fmt.Errorf("expected pod UID to remain %s, but got %s", initialUID, currentUID)
				}
				return nil
			}
			ConsistentlyWithOffset(1, verifyUIDStaysSame, 30*time.Second, 5*time.Second).Should(Succeed())

			By("creating a matching PersistentVolume so the PVC can bind")
			clusterName := "kind"
			if v, ok := os.LookupEnv("KIND_CLUSTER"); ok {
				clusterName = v
			}
			cmd = exec.Command("docker", "exec", clusterName+"-control-plane", "mkdir", "-m", "0777", "-p", "/tmp/"+delayPVName)
			_, _ = utils.Run(cmd)

			pvYAML := fmt.Sprintf(`apiVersion: v1
kind: PersistentVolume
metadata:
  name: %s
spec:
  capacity:
    storage: 1Gi
  accessModes:
    - ReadWriteOnce
  storageClassName: %s
  persistentVolumeReclaimPolicy: Retain
  claimRef:
    namespace: %s
    name: %s
  hostPath:
    path: "/tmp/%s"
    type: DirectoryOrCreate
`, delayPVName, delayStorageClass, namespace, delayPVCName, delayPVName)
			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(pvYAML)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the PVC becomes Bound")
			EventuallyWithOffset(1, func() error {
				return verifyPVCBound(delayPVCName, namespace)
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying the delayed pod eventually becomes Ready")
			EventuallyWithOffset(1, func() error {
				return verifyPodReady(delayPodName, namespace)
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			By("verifying the delayed TypesenseCluster becomes ready")
			EventuallyWithOffset(1, func() error {
				return verifyTypesenseClusterPhase(delayClusterName, phaseQuorumReady)
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("should provision a TypesenseCluster in a different tenant namespace", func() {
			const tenantNamespace = "test-tenant-ns"

			By("creating the tenant namespace")
			cmd := exec.Command("kubectl", "create", "ns", tenantNamespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			defer func() {
				cmd := exec.Command("kubectl", "delete", "ns", tenantNamespace)
				_, _ = utils.Run(cmd)
			}()

			By("creating a TypesenseCluster custom resource in the tenant namespace")
			typesenseManifest := buildManifest("typesense-tenant", tenantNamespace)
			manifestPath, err := filepath.Abs("e2e-ts-tenant.yaml")
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(manifestPath, []byte(typesenseManifest), 0644)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.Remove(manifestPath) }()

			cmd = exec.Command("kubectl", "apply", "-f", manifestPath)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the tenant TypesenseCluster StatefulSet to become ready")
			verifyStatefulSetReady := func() error {
				return verifyStatefulSetReadyReplicas("typesense-tenant-sts", tenantNamespace, 1)
			}
			EventuallyWithOffset(1, verifyStatefulSetReady, 3*time.Minute, 5*time.Second).Should(Succeed(), func() string {
				logCmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
				logs, _ := utils.Run(logCmd)
				podCmd := exec.Command("kubectl", "describe", "pods", "-n", tenantNamespace)
				pods, _ := utils.Run(podCmd)
				return fmt.Sprintf("\n--- OPERATOR LOGS ---\n%s\n--- PODS ---\n%s\n--------------------\n\n", string(logs), string(pods))
			})
		})

		It("should verify admin secret and configmap are created", func() {
			By("verifying admin api key secret exists")
			cmd := exec.Command("kubectl", "get", "secret", "typesense-e2e-admin-key", "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying secret contains api key data")
			cmd = exec.Command("kubectl", "get", "secret", "typesense-e2e-admin-key", "-n", namespace, "-o", "jsonpath={.data}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(ContainSubstring("typesense-api-key"))

			By("verifying nodes configmap exists")
			cmd = exec.Command("kubectl", "get", "configmap", "typesense-e2e-nodeslist", "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying configmap contains node entries")
			cmd = exec.Command("kubectl", "get", "configmap", "typesense-e2e-nodeslist", "-n", namespace, "-o", "jsonpath={.data.fallback}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(ContainSubstring("typesense-e2e-sts"))
		})

		It("should verify services are created with correct ports", func() {
			By("verifying headless service exists")
			cmd := exec.Command("kubectl", "get", "service", "typesense-e2e-sts-svc", "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying headless service has correct api port")
			cmd = exec.Command("kubectl", "get", "service", "typesense-e2e-sts-svc", "-n", namespace, "-o", "jsonpath={.spec.ports[0].port}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("8108"))

			By("verifying resolver service exists")
			cmd = exec.Command("kubectl", "get", "service", "typesense-e2e-svc", "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying resolver service has healthcheck port")
			cmd = exec.Command("kubectl", "get", "service", "typesense-e2e-svc", "-n", namespace, "-o", "jsonpath={.spec.ports[?(@.name==\"healthcheck\")].port}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("8808"))
		})

		It("should scale replicas and maintain quorum", func() {
			By("scaling cluster to 3 replicas")
			cmd := exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", `{"spec":{"replicas":3}}`)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for all 3 replicas to become ready")
			verifyScaledUp := func() error {
				return verifyStatefulSetReadyReplicas("typesense-e2e-sts", namespace, 3)
			}
			EventuallyWithOffset(1, verifyScaledUp, 5*time.Minute, 10*time.Second).Should(Succeed(), func() string {
				logCmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
				logs, _ := utils.Run(logCmd)
				podCmd := exec.Command("kubectl", "describe", "pods", "-n", namespace)
				pods, _ := utils.Run(podCmd)
				return fmt.Sprintf("\n--- OPERATOR LOGS ---\n%s\n--- PODS ---\n%s\n--------------------\n\n", string(logs), string(pods))
			})

			By("verifying configmap nodes list is updated")
			cmd = exec.Command("kubectl", "get", "configmap", "typesense-e2e-nodeslist", "-n", namespace, "-o", "jsonpath={.data.fallback}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(ContainSubstring("typesense-e2e-sts-0"))
			Expect(string(output)).To(ContainSubstring("typesense-e2e-sts-1"))
			Expect(string(output)).To(ContainSubstring("typesense-e2e-sts-2"))
		})

		It("should verify PodDisruptionBudget is created", func() {
			var pdbName string
			By("verifying PodDisruptionBudget exists")
			verifyPDB := func() error {
				cmd := exec.Command("kubectl", "get", "pdb", "-n", namespace, "-o", "name")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				lines := utils.GetNonEmptyLines(string(output))
				for _, line := range lines {
					if strings.Contains(line, "typesense-e2e") {
						parts := strings.Split(line, "/")
						if len(parts) == 2 {
							pdbName = parts[1]
						} else {
							pdbName = line
						}
						return nil
					}
				}
				return fmt.Errorf("expected PDB for typesense-e2e to exist, got: %s", string(output))
			}
			EventuallyWithOffset(1, verifyPDB, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying PDB targets the correct pods")
			cmd := exec.Command("kubectl", "get", "pdb", pdbName, "-n", namespace, "-o", "jsonpath={.spec.selector.matchLabels}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(ContainSubstring("typesense-e2e"))
		})

		It("should update ports and restart pods", func() {
			By("updating api port to 8109")
			cmd := exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", `{"spec":{"apiPort":8109}}`)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying service ports are updated")
			verifyPortUpdated := func() error {
				cmd := exec.Command("kubectl", "get", "service", "typesense-e2e-sts-svc", "-n", namespace, "-o", "jsonpath={.spec.ports[0].port}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != "8109" {
					return fmt.Errorf("expected port 8109, got %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyPortUpdated, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("waiting for the cluster to stabilize after first port change")
			verifyReady := func() error {
				return verifyTypesenseClusterPhaseAndObservedGeneration("typesense-e2e", namespace, phaseQuorumReady)
			}
			EventuallyWithOffset(1, verifyReady, 10*time.Minute, 10*time.Second).Should(Succeed(), func() string {
				logCmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
				logs, _ := utils.Run(logCmd)
				return fmt.Sprintf("\n--- OPERATOR LOGS ---\n%s\n--------------------\n\n", string(logs))
			})

			By("reverting api port back to 8108")
			cmd = exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", `{"spec":{"apiPort":8108}}`)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying service ports are reverted")
			verifyPortReverted := func() error {
				cmd := exec.Command("kubectl", "get", "service", "typesense-e2e-sts-svc", "-n", namespace, "-o", "jsonpath={.spec.ports[0].port}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != "8108" {
					return fmt.Errorf("expected port 8108, got %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyPortReverted, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("waiting for the cluster to stabilize and become ready again after port changes")
			EventuallyWithOffset(1, verifyReady, 10*time.Minute, 10*time.Second).Should(Succeed(), func() string {
				logCmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
				logs, _ := utils.Run(logCmd)
				return fmt.Sprintf("\n--- OPERATOR LOGS ---\n%s\n--------------------\n\n", string(logs))
			})
		})

		It("should transition to Upgrading during a rolling update", func() {
			By("patching podAnnotations to safely trigger a rolling update")
			cmd := exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", `{"spec":{"podAnnotations":{"trigger-update":"now"}}}`)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the phase changes to Upgrading")
			verifyUpgrading := func() error {
				return verifyTypesenseClusterPhaseAndObservedGeneration("typesense-e2e", namespace, "Upgrading")
			}
			EventuallyWithOffset(1, verifyUpgrading, 1*time.Minute, 2*time.Second).Should(Succeed())

			By("waiting for the cluster to become ready again")
			verifyReady := func() error {
				return verifyTypesenseClusterPhaseAndObservedGeneration("typesense-e2e", namespace, phaseQuorumReady)
			}
			EventuallyWithOffset(1, verifyReady, 10*time.Minute, 10*time.Second).Should(Succeed(), func() string {
				logCmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
				logs, _ := utils.Run(logCmd)
				return fmt.Sprintf("\n--- OPERATOR LOGS ---\n%s\n--------------------\n\n", string(logs))
			})
		})

		It("should verify pod health check readiness gates", func() {
			By("verifying pod exists with readiness gates")
			verifyReadinessGate := func() error {
				cmd := exec.Command("kubectl", "get", "pod", "typesense-e2e-sts-0", "-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type==\"RaftQuorumReady\")].status}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != conditionStatusTrue {
					return fmt.Errorf("expected RaftQuorumReady to be True, got %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyReadinessGate, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying pod is in running state")
			verifyPhase := func() error {
				return verifyPodPhase("typesense-e2e-sts-0", namespace, podPhaseRunning)
			}
			EventuallyWithOffset(1, verifyPhase, 2*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should handle pod deletion and recovery", func() {
			By("deleting a pod to trigger recovery")
			// --wait=false ensures the test doesn't block while the pod is gracefully terminating
			cmd := exec.Command("kubectl", "delete", "pod", "typesense-e2e-sts-0", "-n", namespace, "--wait=false")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying cluster becomes Degraded")
			verifyDegraded := func() error {
				return verifyTypesenseClusterPhase("typesense-e2e", "Degraded")
			}
			EventuallyWithOffset(1, verifyDegraded, 1*time.Minute, 2*time.Second).Should(Succeed())

			By("waiting for pod to be recreated and ready")
			verifyPodRecovered := func() error {
				return verifyPodReady("typesense-e2e-sts-0", namespace)
			}
			EventuallyWithOffset(1, verifyPodRecovered, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying quorum is healthy after recovery")
			verifyQuorumReady := func() error {
				return verifyTypesenseClusterPhase("typesense-e2e", phaseQuorumReady)
			}
			EventuallyWithOffset(1, verifyQuorumReady, 2*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should properly sync the observedGeneration in status", func() {
			By("verifying observedGeneration matches the current generation")
			verifyGeneration := func() error {
				return verifyTypesenseClusterObservedGenerationMatches("typesense-e2e", namespace)
			}
			EventuallyWithOffset(1, verifyGeneration, 1*time.Minute, 2*time.Second).Should(Succeed())
		})

		It("should verify typesensecluster status conditions", func() {
			By("checking ready condition exists")
			verifyReadyCondition := func() error {
				return verifyTypesenseClusterCondition("typesense-e2e", namespace, "Ready", conditionStatusTrue)
			}
			EventuallyWithOffset(1, verifyReadyCondition, 5*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying phase is set")
			verifyPhaseSet := func() error {
				return verifyTypesenseClusterPhaseSet("typesense-e2e", namespace)
			}
			EventuallyWithOffset(1, verifyPhaseSet, 1*time.Minute, 2*time.Second).Should(Succeed())
		})

		It("should respect node selector when configured", func() {
			By("patching cluster with node selector")
			cmd := exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", `{"spec":{"nodeSelector":{"test-node-role":"worker"}}}`)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying statefulset template respects node selector")
			verifyNodeSelector := func() error {
				cmd := exec.Command("kubectl", "get", "statefulset", "typesense-e2e-sts", "-n", namespace, "-o", "jsonpath={.spec.template.spec.nodeSelector}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if !strings.Contains(string(output), "test-node-role") {
					return fmt.Errorf("expected node selector not found on pod spec, got: %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyNodeSelector, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("reverting node selector")
			cmd = exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "json", "-p", `[{"op": "remove", "path": "/spec/nodeSelector"}]`)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should create and manage resource quotas", func() {
			By("verifying statefulset resources are set")
			cmd := exec.Command("kubectl", "get", "statefulset", "typesense-e2e-sts", "-n", namespace, "-o", "jsonpath={.spec.template.spec.containers[0].resources}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			// The operator automatically sets default resources if none are specified
			Expect(string(output)).To(ContainSubstring("requests"))
		})

		It("should scale down cluster while maintaining stability", func() {
			By("scaling cluster back to 1 replica")
			cmd := exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", `{"spec":{"replicas":1}}`)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for scale down to complete")
			verifyScaledDown := func() error {
				cmd := exec.Command("kubectl", "get", "statefulset", "typesense-e2e-sts", "-n", namespace, "-o", "jsonpath={.status.replicas}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != "1" {
					return fmt.Errorf("expected 1 replica, got %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyScaledDown, 3*time.Minute, 10*time.Second).Should(Succeed(), func() string {
				logCmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
				logs, _ := utils.Run(logCmd)
				podCmd := exec.Command("kubectl", "describe", "pods", "-n", namespace)
				pods, _ := utils.Run(podCmd)
				return fmt.Sprintf("\n--- OPERATOR LOGS ---\n%s\n--- PODS ---\n%s\n--------------------\n\n", string(logs), string(pods))
			})

			By("verifying remaining pod is ready")
			verifyRemainingPodReady := func() error {
				cmd := exec.Command("kubectl", "get", "pod", "typesense-e2e-sts-0", "-n", namespace, "-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != conditionStatusTrue {
					return fmt.Errorf("expected pod to be Ready, got %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyRemainingPodReady, 2*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should provision HTTPRoutes successfully", func() {
			By("patching the TypesenseCluster to include HTTPRoutes")
			patch := `{"spec":{"httpRoutes":[{"name":"public","parentRef":{"name":"traefik","namespace":"kube-system"},"hostnames":["search.example.com"]}]}}`
			cmd := exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", patch)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the HTTPRoute is created")
			verifyRoute := func() error {
				cmd := exec.Command("kubectl", "get", "httproute", "typesense-e2e-public", "-n", namespace, "-o", "jsonpath={.spec.hostnames[0]}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != "search.example.com" {
					return fmt.Errorf("expected hostname search.example.com, got %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyRoute, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("cleaning up the HTTPRoute configuration")
			patch = `[{"op": "remove", "path": "/spec/httpRoutes"}]`
			cmd = exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "json", "-p", patch)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should expand persistent volume claims when storage size is increased", func() {
			By("patching the standard storage class to allow volume expansion")
			cmd := exec.Command("kubectl", "patch", "storageclass", "standard", "-p", `{"allowVolumeExpansion": true}`)
			_, _ = utils.Run(cmd)

			By("patching the TypesenseCluster to request 2Gi of storage")
			patch := `{"spec":{"storage":{"size":"2Gi"}}}`
			cmd = exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", patch)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the PVC is expanded to 2Gi")
			verifyPVCExpanded := func() error {
				cmd := exec.Command("kubectl", "get", "pvc", "data-typesense-e2e-sts-0", "-n", namespace, "-o", "jsonpath={.spec.resources.requests.storage}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != "2Gi" {
					return fmt.Errorf("expected PVC storage to be 2Gi, got %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyPVCExpanded, 2*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should provision an Ingress and Reverse Proxy successfully", func() {
			By("patching the TypesenseCluster to include an Ingress configuration")
			patch := fmt.Sprintf(`{"spec":{"ingress":{"host":"search.example.com","image":"%s","ingressClassName":"nginx"}}}`, nginxImage)
			cmd := exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", patch)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Ingress is created")
			verifyIngress := func() error {
				cmd := exec.Command("kubectl", "get", "ingress", "typesense-e2e-reverse-proxy", "-n", namespace, "-o", "jsonpath={.spec.rules[0].host}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != "search.example.com" {
					return fmt.Errorf("expected ingress host search.example.com, got %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyIngress, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying the Reverse Proxy Deployment is created")
			verifyDeployment := func() error {
				cmd := exec.Command("kubectl", "get", "deployment", "typesense-e2e-reverse-proxy", "-n", namespace, "-o", "name")
				_, err := utils.Run(cmd)
				return err
			}
			EventuallyWithOffset(1, verifyDeployment, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("cleaning up the Ingress configuration")
			patch = `[{"op": "remove", "path": "/spec/ingress"}]`
			cmd = exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "json", "-p", patch)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should provision a PodMonitor when metrics are enabled", func() {
			By("patching the TypesenseCluster to enable metrics")
			patch := `{"spec":{"metrics":{"release":"test-release","intervalInSeconds":30}}}`
			cmd := exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", patch)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the PodMonitor is created")
			verifyPodMonitor := func() error {
				cmd := exec.Command("kubectl", "get", "podmonitor", "typesense-e2e-podmonitor", "-n", namespace, "-o", "jsonpath={.metadata.labels.release}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != "test-release" {
					return fmt.Errorf("expected release test-release, got %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyPodMonitor, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("cleaning up the metrics configuration")
			patch = `[{"op": "remove", "path": "/spec/metrics"}]`
			cmd = exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "json", "-p", patch)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should provision a DocSearch Scraper CronJob", func() {
			By("patching the TypesenseCluster to add a scraper")
			patch := fmt.Sprintf(`{"spec":{"scrapers":[{"name":"test-scraper","image":"%s","schedule":"*/5 * * * *","config":"test"}]}}`, scraperImage)
			cmd := exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", patch)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the CronJob is created")
			verifyCronJob := func() error {
				cmd := exec.Command("kubectl", "get", "cronjob", "typesense-e2e-scraper-test-scraper", "-n", namespace, "-o", "name")
				_, err := utils.Run(cmd)
				return err
			}
			EventuallyWithOffset(1, verifyCronJob, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("cleaning up the scraper configuration")
			patch = `[{"op": "remove", "path": "/spec/scrapers"}]`
			cmd = exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "json", "-p", patch)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should reject scraper configurations that exceed the 52 character limit", func() {
			By("attempting to patch the TypesenseCluster with a scraper name that is too long")
			patch := fmt.Sprintf(`{"spec":{"scrapers":[{"name":"this-is-a-very-long-scraper-name-that-exceeds-the-limit","image":"%s","schedule":"*/5 * * * *","config":"test"}]}}`, scraperImage)
			cmd := exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", patch)
			output, err := utils.Run(cmd)

			By("verifying the webhook rejects the patch")
			Expect(err).To(HaveOccurred())
			Expect(string(output)).To(ContainSubstring("exceeds the 52 character limit for CronJobs"))
		})

		It("should perform a rolling update when the image version changes", func() {
			By("patching the TypesenseCluster to use a different image version")
			patch := fmt.Sprintf(`{"spec":{"image":"%s"}}`, typesenseImageOld)
			cmd := exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", patch)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the StatefulSet image is updated")
			verifyImage := func() error {
				cmd := exec.Command("kubectl", "get", "statefulset", "typesense-e2e-sts", "-n", namespace, "-o", "jsonpath={.spec.template.spec.containers[0].image}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(output) != typesenseImageOld {
					return fmt.Errorf("expected image %s, got %s", typesenseImageOld, string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyImage, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("waiting for the cluster to become ready again after image upgrade")
			verifyReady := func() error {
				return verifyTypesenseClusterPhase("typesense-e2e", phaseQuorumReady)
			}
			EventuallyWithOffset(1, verifyReady, 5*time.Minute, 10*time.Second).Should(Succeed(), func() string {
				logCmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
				logs, _ := utils.Run(logCmd)
				return fmt.Sprintf("\n--- OPERATOR LOGS ---\n%s\n--------------------\n\n", string(logs))
			})
		})

		It("should apply AdditionalServerConfiguration and trigger a rolling update", func() {
			By("creating a ConfigMap with custom Typesense configuration")
			cmd := exec.Command("kubectl", "create", "configmap", "custom-ts-config", "--from-literal=TYPESENSE_MAX_SEARCH_TIME_MS=2000", "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			defer func() {
				cmd := exec.Command("kubectl", "delete", "configmap", "custom-ts-config", "-n", namespace, "--ignore-not-found")
				_, _ = utils.Run(cmd)
			}()

			By("patching the TypesenseCluster to use the additional server configuration")
			patch := `{"spec":{"additionalServerConfiguration":{"name":"custom-ts-config"}}}`
			cmd = exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "merge", "-p", patch)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the StatefulSet includes the ConfigMap in envFrom")
			verifyEnvFrom := func() error {
				cmd := exec.Command("kubectl", "get", "statefulset", "typesense-e2e-sts", "-n", namespace, "-o", "jsonpath={.spec.template.spec.containers[0].envFrom[*].configMapRef.name}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if !strings.Contains(string(output), "custom-ts-config") {
					return fmt.Errorf("expected envFrom to contain custom-ts-config, got %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyEnvFrom, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("waiting for the cluster to become ready again after configuration update")
			verifyReady := func() error {
				return verifyTypesenseClusterPhase("typesense-e2e", phaseQuorumReady)
			}
			EventuallyWithOffset(1, verifyReady, 10*time.Minute, 10*time.Second).Should(Succeed(), func() string {
				logCmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
				logs, _ := utils.Run(logCmd)
				return fmt.Sprintf("\n--- OPERATOR LOGS ---\n%s\n--------------------\n\n", string(logs))
			})

			By("cleaning up the additional server configuration")
			patch = `[{"op": "remove", "path": "/spec/additionalServerConfiguration"}]`
			cmd = exec.Command("kubectl", "patch", "typesensecluster", "typesense-e2e", "-n", namespace, "--type", "json", "-p", patch)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Operator in Gapped Mode", func() {
		It("should clean up existing resources to ensure a clean state", func() {
			By("deleting all existing TypesenseClusters across all namespaces")
			cmd := exec.Command("kubectl", "delete", "typesensecluster", "--all", "--all-namespaces", "--timeout=2m")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should configure the operator to watch a specific namespace", func() {
			By("removing cluster-wide role binding to simulate gapped mode RBAC")
			cmd := exec.Command("kubectl", "delete", "clusterrolebinding", "typesense-operator-manager-rolebinding", "--ignore-not-found")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("creating a namespace-scoped role binding")
			cmd = exec.Command("kubectl", "create", "rolebinding", "typesense-operator-manager-rolebinding", "--clusterrole=typesense-operator-manager-role", "--serviceaccount="+namespace+":typesense-operator-controller-manager", "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("removing the validating webhook configuration to simulate Helm gapped installation")
			cmd = exec.Command("kubectl", "delete", "validatingwebhookconfigurations", "typesense-operator-validating-webhook-configuration", "--ignore-not-found")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("patching the deployment to set WATCH_NAMESPACE and disable webhook")
			cmd = exec.Command("kubectl", "set", "env", "deployment/typesense-operator-controller-manager", "WATCH_NAMESPACE="+namespace, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			// Inject the --enable-webhook=false flag to simulate the new Gapped-Mode Helm behavior
			patch := `{"spec":{"template":{"spec":{"containers":[{"name":"manager","args":["--leader-elect","--health-probe-bind-address=:8081","--zap-log-level=debug","--enable-webhook=false"]}]}}}}`
			cmd = exec.Command("kubectl", "patch", "deployment", "typesense-operator-controller-manager", "-n", namespace, "-p", patch)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new operator pod to rollout")
			cmd = exec.Command("kubectl", "rollout", "status", "deployment/typesense-operator-controller-manager", "-n", namespace, "--timeout=2m")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should completely disable the webhook server and skip certificate generation", func() {
			verifyNoWebhookLog := func() error {
				// Find the new, active pod (ignoring the terminating one)
				cmd := exec.Command("kubectl", "get", "pods", "-l", "control-plane=controller-manager", "-o", "go-template={{ range .items }}{{ if not .metadata.deletionTimestamp }}{{ .metadata.name }}{{ \"\\n\" }}{{ end }}{{ end }}", "-n", namespace)
				podOutput, err := utils.Run(cmd)
				if err != nil {
					return fmt.Errorf("failed to get pod name: %v", err)
				}
				podNames := utils.GetNonEmptyLines(string(podOutput))
				if len(podNames) != 1 {
					return fmt.Errorf("expected 1 running controller pod, but got %d", len(podNames))
				}
				podName := podNames[0]

				// Read the logs of the correct pod
				cmd = exec.Command("kubectl", "logs", podName, "-n", namespace)
				out, err := utils.Run(cmd)
				if err != nil {
					return fmt.Errorf("failed to fetch logs for pod %s: %v", podName, err)
				}

				logs := string(out)
				if strings.Contains(logs, "Generating self-signed webhook certificates") {
					return fmt.Errorf("expected webhook certificate generation to be skipped, but it ran in pod %s", podName)
				}
				if strings.Contains(logs, "Setting up webhook for TypesenseCluster") {
					return fmt.Errorf("expected webhook server setup to be skipped, but it ran in pod %s", podName)
				}
				if !strings.Contains(logs, "starting manager") {
					return fmt.Errorf("manager has not started yet")
				}
				return nil
			}
			EventuallyWithOffset(1, verifyNoWebhookLog, 1*time.Minute, 5*time.Second).Should(Succeed(), func() string {
				cmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
				out, _ := utils.Run(cmd)
				return fmt.Sprintf("\n--- OPERATOR LOGS ---\n%s\n--------------------\n\n", string(out))
			})
		})

		It("should completely ignore custom resources in other namespaces", func() {
			const ignoredNamespace = "test-ignored-ns"

			By("creating the ignored namespace")
			cmd := exec.Command("kubectl", "create", "ns", ignoredNamespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			defer func() {
				cmd := exec.Command("kubectl", "delete", "ns", ignoredNamespace)
				_, _ = utils.Run(cmd)
			}()

			By("creating a TypesenseCluster in the ignored namespace (should succeed in K8s but be ignored by operator)")
			typesenseManifest := buildManifest("typesense-ignored", ignoredNamespace)
			manifestPath, err := filepath.Abs("e2e-ts-ignored.yaml")
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(manifestPath, []byte(typesenseManifest), 0644)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.Remove(manifestPath) }()

			cmd = exec.Command("kubectl", "apply", "-f", manifestPath)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying that no StatefulSet is ever created for the ignored cluster")
			ConsistentlyWithOffset(1, func() error {
				cmd := exec.Command("kubectl", "get", "statefulset", "typesense-ignored-sts", "-n", ignoredNamespace)
				_, err := utils.Run(cmd)
				if err == nil {
					return fmt.Errorf("statefulset was created, but should have been ignored")
				}
				return nil
			}, 15*time.Second, 2*time.Second).Should(Succeed())
		})

		It("should gracefully handle scraper names exceeding the 52 character limit in gapped mode", func() {
			By("creating a TypesenseCluster with a scraper name that exceeds the limit")
			badManifest := `apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  name: typesense-bad-scraper
  namespace: ` + namespace + `
spec:
  image: ` + typesenseImage + `
  replicas: 1
  apiPort: 8108
  peeringPort: 8107
  storage:
    storageClassName: standard
    size: 1Gi
  scrapers:
    - name: this-is-a-very-long-scraper-name-that-exceeds-the-limit
      image: ` + scraperImage + `
      schedule: "*/5 * * * *"
      config: "test"
`
			manifestPath, err := filepath.Abs("e2e-ts-bad-scraper.yaml")
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(manifestPath, []byte(badManifest), 0644)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.Remove(manifestPath) }()

			cmd := exec.Command("kubectl", "apply", "-f", manifestPath)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the operator emits a warning event for the invalid scraper configuration")
			verifyEvent := func() error {
				cmd := exec.Command("kubectl", "get", "events", "-n", namespace, "--field-selector", "reason=InvalidScraperConfiguration", "-o", "jsonpath={.items[*].message}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if !strings.Contains(string(output), "exceeds the 52 character limit") {
					return fmt.Errorf("expected warning event not found, got: %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyEvent, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("cleaning up the invalid cluster")
			cmd = exec.Command("kubectl", "delete", "-f", manifestPath)
			_, _ = utils.Run(cmd)
		})
	})

	Context("Operator in Gapped Mode with Webhooks Enabled", func() {
		It("should restore webhooks and actively reject out-of-namespace resources", func() {
			By("re-deploying the controller-manager to restore webhooks and cluster RBAC (Simulating Opt-in)")
			cmd := exec.Command("make", "deploy", "IMG=example.com/typesense-operator:v0.0.1")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("patching the deployment to maintain WATCH_NAMESPACE")
			cmd = exec.Command("kubectl", "set", "env", "deployment/typesense-operator-controller-manager", "WATCH_NAMESPACE="+namespace, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("forcing a rollout to ensure a fresh pod starts and executes the webhook patching")
			cmd = exec.Command("kubectl", "rollout", "restart", "deployment/typesense-operator-controller-manager", "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new operator pod to rollout")
			cmd = exec.Command("kubectl", "rollout", "status", "deployment/typesense-operator-controller-manager", "-n", namespace, "--timeout=2m")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for webhook service endpoints to be populated")
			verifyWebhookEndpoints := func() error {
				cmd = exec.Command("kubectl", "get", "endpoints", "typesense-operator-webhook-service", "-n", namespace, "-o", "jsonpath={.subsets[*].addresses[*].ip}")
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if len(strings.TrimSpace(string(output))) == 0 {
					return fmt.Errorf("webhook endpoints not populated yet")
				}
				return nil
			}
			EventuallyWithOffset(1, verifyWebhookEndpoints, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying the operator generated and injected the webhook certificates")
			verifyCertInjection := func() error {
				cmd := exec.Command("kubectl", "get", "pods", "-l", "control-plane=controller-manager", "-o", "go-template={{ range .items }}{{ if not .metadata.deletionTimestamp }}{{ .metadata.name }}{{ \"\\n\" }}{{ end }}{{ end }}", "-n", namespace)
				podOutput, err := utils.Run(cmd)
				if err != nil {
					return fmt.Errorf("failed to get pod name: %v", err)
				}
				podNames := utils.GetNonEmptyLines(string(podOutput))
				if len(podNames) != 1 {
					return fmt.Errorf("expected 1 running controller pod, but got %d", len(podNames))
				}
				podName := podNames[0]

				cmd = exec.Command("kubectl", "logs", podName, "-n", namespace)
				out, err := utils.Run(cmd)
				if err != nil {
					return fmt.Errorf("failed to fetch logs for pod %s: %v", podName, err)
				}
				if !strings.Contains(string(out), "Successfully patched ValidatingWebhookConfiguration") {
					return fmt.Errorf("expected webhook patching log not found in pod %s", podName)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyCertInjection, 2*time.Minute, 5*time.Second).Should(Succeed(), func() string {
				cmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
				out, _ := utils.Run(cmd)
				return fmt.Sprintf("\n--- OPERATOR LOGS ---\n%s\n--------------------\n\n", string(out))
			})

			const rejectedNamespace = "test-webhook-reject-ns"
			By("creating a test namespace to test webhook rejection")
			cmd = exec.Command("kubectl", "create", "ns", rejectedNamespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			defer func() {
				cmd := exec.Command("kubectl", "delete", "ns", rejectedNamespace)
				_, _ = utils.Run(cmd)
			}()

			By("creating a TypesenseCluster in the rejected namespace (should fail)")
			typesenseManifest := buildManifest("typesense-rejected", rejectedNamespace)
			manifestPath, err := filepath.Abs("e2e-ts-rejected.yaml")
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(manifestPath, []byte(typesenseManifest), 0644)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.Remove(manifestPath) }()

			// The webhook uses failurePolicy Ignore, so a request that cannot reach it yet (e.g. while the
			// service routing catches up with the new pod) is admitted. Retry until the webhook itself rejects
			// the resource, and remove a resource that slipped through before retrying.
			verifyRejected := func() error {
				cmd := exec.Command("kubectl", "apply", "-f", manifestPath)
				output, err := utils.Run(cmd)
				if err == nil {
					cmd = exec.Command("kubectl", "delete", "-f", manifestPath, "--ignore-not-found", "--wait=false")
					_, _ = utils.Run(cmd)
					return fmt.Errorf("resource was admitted, webhook not reachable yet")
				}
				if !strings.Contains(string(output), "only allowed in namespace") {
					return fmt.Errorf("unexpected error, expected a rejection by the webhook: %s", string(output))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyRejected, 2*time.Minute, 5*time.Second).Should(Succeed())
		})
	})
})
