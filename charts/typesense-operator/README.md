## 🎯 Why TyKO?

**Simplify the deployment, scaling, and management of Typesense clusters in Kubernetes.**

- Deploy highly-available **Typesense** clusters with a **single declarative YAML manifest**
- Automates **Typesense** [lifecycle management](https://akyriako.github.io/typesense-operator-docs/docs/getting-started#key-features) (config maps, secrets, volumes, statefulsets, services, ingress or http routes, metrics, scrapers)
- Automates **Raft quorum [configuration, discovery and recovery](https://akyriako.github.io/typesense-operator-docs/docs/how-it-works/recovering-a-cluster-that-has-lost-quorum)** without additional sidecars or manual interventions
- **Self-Healing:** Automatically detects and instantly recovers deleted or modified managed resources (StatefulSets, Services, Ingress, etc.) to maintain the desired state
- **Global or Gapped Mode:** Run the operator cluster-wide or restrict it to a specific namespace for strict multi-tenant and secure environments
- Built with Go & Operator SDK — lightweight, Kubernetes-native, and flexible
- Community-driven, with plethora of examples for Kind, CCE, AKS, EKS, GCP, and more

## ⚡ Get Started

```bash
# Install the Operator
helm repo add tyko https://akyriako.github.io/typesense-operator/
helm repo update

helm upgrade --install typesense-operator tyko/typesense-operator -n typesense-system --create-namespace --reset-values
```

<details>
<summary>Quick example for Open Telekom Cloud CCE</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-otc-1
spec:
  image: typesense/typesense:30.2
  replicas: 3
  storage:
    storageClassName: csi-disk
```
</details>

<details>
<summary>Quick example for Open Telekom Cloud CCE with OBS S3 support</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-otc-2
spec:
  image: typesense/typesense:30.2
  replicas: 3
  storage:
    storageClassName: csi-obs
    accessMode: ReadWriteMany
    annotations:
      csi.storage.k8s.io/fstype: obsfs
      volume.beta.kubernetes.io/storage-provisioner: everest-csi-provisioner
      csi.storage.k8s.io/node-publish-secret-name: otc-aksk
      csi.storage.k8s.io/node-publish-secret-namespace: default
      everest.io/csi.volume-name-prefix: c-otc-2
```
</details>

<details>
<summary>Quick example for Open Telekom Cloud CCE with SFS Turbo S3 support</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-otc-3
spec:
  image: typesense/typesense:30.2
  replicas: 3
  storage:
    storageClassName: csi-obs
    accessMode: ReadWriteMany
    annotations:
      everest.io/obs-volume-type: STANDARD
      csi.storage.k8s.io/fstype: s3fs
      volume.beta.kubernetes.io/storage-provisioner: everest-csi-provisioner
      csi.storage.k8s.io/node-publish-secret-name: otc-aksk
      csi.storage.k8s.io/node-publish-secret-namespace: default
      everest.io/csi.volume-name-prefix: c-otc-3
```
</details>

<details>
<summary>Quick example for bare metal K3s with Democratic CSI</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-bm-k3s
spec:
  image: typesense/typesense:30.2
  replicas: 3
  storage:
    storageClassName: nfs
```
</details>

<details>
<summary>Quick example for Kind</summary>

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: typesense-local-path
provisioner: rancher.io/local-path
reclaimPolicy: Delete
allowVolumeExpansion: true
volumeBindingMode: WaitForFirstConsumer
---
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-kind
spec:
  image: typesense/typesense:30.2
  replicas: 3
  storage:
    size: 150Mi
    storageClassName: typesense-local-path
```
</details>

<details>
<summary>Quick example for AWS EKS</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-aws
spec:
  image: typesense/typesense:30.2
  replicas: 3
  storage:
    size: 100Mi
    storageClassName: gp2
```
</details>

<details>
<summary>Quick example for Azure AKS</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-azure
spec:
  image: typesense/typesense:30.2
  replicas: 3
  storage:
    storageClassName: managed-csi
```
</details>

<details>
<summary>Quick example for GCP</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: ts-gcp
spec:
  image: typesense/typesense:30.2
  replicas: 3
  storage:
    storageClassName: standard-rwo
```
</details>

<details>
<summary>Full Configuration Example (Ingress, Metrics, Scheduling & Resources)</summary>

```yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  name: ts-full-feature
spec:
  image: typesense/typesense:30.2
  replicas: 3
  apiPort: 8108
  storage:
    size: 50Gi
    storageClassName: standard
  resources:
    requests:
      cpu: 500m
      memory: 2Gi
    limits:
      cpu: 2
      memory: 8Gi
  nodeSelector:
    env: production
  ingress:
    host: search.example.com
    image: 'nginx:latest'
    ingressClassName: nginx
    replicas: 2
    readOnlyRootFilesystem: 
    path: /
    pathType: ImplementationSpecific
    # serviceType: ClusterIP
    # command: ["/bin/sh", "-c", "nginx -g 'daemon off;'"]
    # annotations:
    #  route.openshift.io/termination: "edge"
    # config: | 
    #   worker_processes auto;
    #   ... (You can completely override the Nginx config here)
  metrics:
    release: kube-prometheus-stack
    interval: 15
  # scrapers:
  #   - name: my-website-scraper
  #     image: algolia/docsearch-scraper
  #     schedule: "0 0 * * *"
  #     config: "{ ... }"
```
</details>

You can find more examples and analytical installation instructions in the [Installation](https://akyriako.github.io/typesense-operator-docs/docs/installation/) and [Configuration](https://akyriako.github.io/typesense-operator-docs/docs/crds) guides.

## 📚 Documentation

- [Getting Started](https://akyriako.github.io/typesense-operator-docs/docs/getting-started)
- [How it works](https://akyriako.github.io/typesense-operator-docs/docs/how-it-works)
- [Configuration & CRD Specs](https://akyriako.github.io/typesense-operator-docs/docs/crds)
- [Monitoring & Metrics](https://akyriako.github.io/typesense-operator-docs/docs/metrics)

## 💬 Community & Support

Join the conversation:
- **GitHub Discussions**: ask questions, share feedback, submit suggestions or help others
- **Issues**: report bugs or request features

## ⭐ Help us Grow
- Star the repo if you want this operator to grow
- Try TyKO in your own infrastructure and share your feedback or list yourselves as [Adopters](https://akyriako.github.io/typesense-operator-docs/docs/adopters)
- Spread the word: blog posts, tutorials, or community shoutouts help others discover it