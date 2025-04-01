# Typesense Kubernetes Operator
![Status Badge](https://img.shields.io/badge/status-beta-orange) ![28_0](https://img.shields.io/badge/typesense-28.0-lightgreen?logoColor=black&link=https%3A%2F%2Ftypesense.org%2Fdocs%2F28.0%2Fapi%2F)
![27_1](https://img.shields.io/badge/typesense-27.1-lightgreen?logoColor=black&link=https%3A%2F%2Ftypesense.org%2Fdocs%2F27.1%2Fapi%2F)
![27_0](https://img.shields.io/badge/typesense-27.0-lightgreen?logoColor=black&link=https%3A%2F%2Ftypesense.org%2Fdocs%2F27.0%2Fapi%2F)
![26_0](https://img.shields.io/badge/typesense-26.0-lightgreen?logoColor=black&link=https%3A%2F%2Ftypesense.org%2Fdocs%2F26.0%2Fapi%2F)
![Kubernetes](https://img.shields.io/badge/kubernetes-1.26+-lightgreen?labelColor=blue&link=https%3A%2F%2Ftypesense.org%2Fdocs%2F27.0%2Fapi%2F)

The **Typesense Kubernetes Operator** is designed to manage the deployment and lifecycle of [Typesense](https://typesense.org/) clusters within Kubernetes environments. 
The operator is developed in Go using [Operator SDK Framework](https://sdk.operatorframework.io/), an open source toolkit to manage Kubernetes native applications, called Operators, in an effective, automated, and scalable way. 

## Description

Key features of Typesense Kubernetes Operator include:

- **Custom Resource Management**: Provides a Kubernetes-native interface to define and manage Typesense cluster configurations using a CRD named `TypesenseCluster`.
- **Typesense Lifecycle Automation**: Simplifies deploying, scaling, and managing Typesense clusters. Handles aspects like:
    - bootstrap Typesense's Admin API Key creation as a `Secret`,
    - deploy Typesense as a `StatefulSet`, each Pod contains two containers: 
      1. the _Typesense node_ itself based on the image provided in the `specs`
      2. the _Typesense node metrics exporter_ (as a sidecar), based on the image provided in the `spec.metricsSpec`
    - provision Typesense services (headless & discovery `Services`),
    - actively discover and update Typesense's nodes list (quorum configuration mounted as `NodesListConfigMap`),
    - place claims for Typesense `PersistentVolumes`
    - _optionally_ expose Typesense API endpoint via an `Ingress`
    - _optionally_ provision one or multiple instances (one per target URL) of DocSearch as `Cronjobs`
    - _optionally_ provision Prometheus [targets](https://prometheus.io/docs/guides/multi-target-exporter/) for the `Pod` metrics via a `PodMonitor`
- **Raft Quorum Configuration & Recovery Automation**:
    - Continuous active (re)discovery of the quorum configuration reacting to changes in `ReplicaSet` **without the need of an additional sidecar container**,
    - Automatic recovery of a cluster that has lost quorum **without the need of manual intervention**.

## Background

Typesense is using raft in the background to establish its clusters. Raft is a consensus algorithm based on the
paper "[Raft: In Search of an Understandable Consensus Algorithm](https://raft.github.io/raft.pdf)".

Raft nodes operate in one of three possible states: _follower_, _candidate_, or _leader_. Every new node always joins the
quorum as a follower. Followers can receive log entries from the leader and participate in voting for electing a leader. If no
log entries are received for a specified period of time, a follower transitions to the candidate state. As a candidate, the node
can accept votes from its peers nodes. Upon receiving a majority of votes, the candidate is becoming the leader of the quorum.
The leader’s responsibilities include handling new log entries and replicating them to other nodes.

Another thing to consider is what happens when the node set changes, when nodes join or leave the cluster.
If a quorum of nodes is **available**, raft can dynamically modify the node set without any issue (this happens every 30sec).
But if the cluster cannot form a quorum, then problems start to appear or better to pile up. A cluster with `N` nodes can tolerate
a failure of at most `(N-1)/2` nodes without losing its quorum. If the available nodes go below this threshold then two events
are taking place:

- raft declares the whole cluster as **unavailable** (no leader can be elected, no more log entries can be processed)
- the remaining nodes are restarted in bootstrap mode

In a Kubernetes environment, the nodes are actually `Pods` which are rather volatile by nature and their lifetime is quite ephemeral and subjects
to potential restarts, and that puts the whole concept of raft protocol consensus under a tough spot. As we can read in the official
documentation of Typesense when it comes to [recovering a cluster that has lost quorum](https://typesense.org/docs/guide/high-availability.html#recovering-a-cluster-that-has-lost-quorum),
it is explicitly stated:

> If a Typesense cluster loses more than `(N-1)/2` nodes at the same time, the cluster becomes unstable because it loses quorum
> and the remaining node(s) cannot safely build consensus on which node is the leader. To avoid a potential split brain issue,
> Typesense then stops accepting writes and reads **until some manual verification and intervention is done**.

![image](https://github.com/user-attachments/assets/a28357bf-199f-45e7-9ce4-9557043bfc20)

> [!NOTE]
> Illustration's been taken from [Free Gophers Pack](https://github.com/MariaLetta/free-gophers-pack)

In production environments, manual intervention is sometimes impossible or undesirable, and downtime for a service like
Typesense may be unacceptable. The Typesense Kubernetes Operator addresses both of these challenges.

### Problem 1: Quorum reconfiguration

The Typesense Kubernetes Operator manages the entire lifecycle of Typesense Clusters within Kubernetes:

1. A random token is generated and stored as a base64-encoded value in a new `Secret`. This token serves as the Admin API 
   key for bootstrapping the Typesense cluster.

> [!NOTE]
> You can alternative provide your own `Secret` by setting the value of `adminApiKey` in `TypesenseCluster` specs; this will be used instead. 
> The data key name has to be **always** `typesense-api-key`!
> ```yaml
> apiVersion: v1
> kind: Secret
> metadata:
>  name: typesense-common-bootstrap-key
>  type: Opaque
> data:
>  typesense-api-key: SXdpVG9CcnFYTHZYeTJNMG1TS1hPaGt0dlFUY3VWUloxc1M5REtsRUNtMFFwQU93R1hoanVIVWJLQnE2ejdlSQ==
> ``` 

2. A `NodesListConfigMap` is created, containing the endpoints of the cluster nodes as a single concatenated string in its `data` field.
   During each reconciliation loop, the operator identifies any changes in endpoints and updates the `NodesListConfigMap`. This `NodesListConfigMap`
   is mounted in every `Pod` at the path where raft expects the quorum configuration, ensuring quorum configuration stays always updated.
   The endpoint of each `Pod` the headless service adheres to the following naming convention:

        `{cluster-name}-sts-{pod-index}.{cluster-name}-sts-svc`

> [!IMPORTANT]
> * **This completely eliminates the need for a sidecar** to translate the endpoints of the headless `Service` into `Pod` IP addresses.
> The endpoints automatically resolves to the new IP addresses, and raft will begin contacting these endpoints
> within its 30-second polling interval.
> * Be cautious while choosing the cluster name (`Spec.Name`) in `TypesenseCluster` CRDs, as raft expects the combined endpoint name and 
> API and Peering ports (e.g. `{cluster-name}-sts-{pod-index}.{cluster-name}-sts-svc:8107:8108`) **not** to exceed  **64** characters 
> in length.

3. Next, the reconciler creates a headless `Service` required for the `StatefulSet`, along with a standard Kubernetes
   Service of type `ClusterIP`. The latter exposes the REST/API endpoints of the Typesense cluster to external systems.
4. A `StatefulSet` is then created. The quorum configuration stored in the `NodesListConfigMap` is mounted as a volume in each `Pod`
   under `/usr/share/typesense/nodelist`. No `Pod` restart is necessary when the `NodesListConfigMap` changes, as raft automatically
   detects and applies the updates.
5. Optionally, an **nginx:alpine** workload is provisioned as `Deployment` and published via an `Ingress`, in order to exposed safely 
   the Typesense REST/API endpoint outside the Kubernetes cluster **only** to selected referrers. The configuration of the 
   nginx workload is stored in a `NodesListConfigMap`.
6. Optionally, one or more instances of **DocSearch** are deployed as distinct `CronJobs` (one per scraping target URL),
   which based on user-defined schedules, periodically scrape the target sites and store the results in Typesense.

![Untitled-2025-02-24-0826](https://github.com/user-attachments/assets/6e6d67cf-4bab-4eac-ada0-8e4c6f46537d)

> [!NOTE]
> The interval between reconciliation loops depends on the number of nodes. This approach ensures raft has sufficient
> "breathing room" to carry out its operations—such as leader election, log replication, and bootstrapping—before the
> next quorum health reconciliation begins.

7. The controller assesses the quorum's health by probing each node at `http://{nodeUrl}:{api-port}/health`. Based on the
   results, it formulates an action plan for the next reconciliation loop. This process is detailed in the following section:

### Problem 2: Recovering a cluster that has lost quorum

During configuration changes, we cannot switch directly from the old configuration to the next, because conflicting
majorities could arise. When that happens, no leader can be elected and eventually raft declares the whole cluster
as unavailable which leaves it in a hot loop. One way to solve it, is to force the cluster downgrade to a single instance
cluster and then gradually introduce new nodes (by scaling up the `StatefulSet`). With that approach we avoid the need
of manual intervention in order to recover a cluster that has lost quorum.

![image](https://github.com/user-attachments/assets/007852ba-e173-43a4-babf-d250f8a34ad1)

> [!IMPORTANT]
> [Scaling the StatefulSet down and subsequently (gradually) up](https://typesense.org/docs/guide/high-availability.html#recovering-a-cluster-that-has-lost-quorum), 
> would typically be the manual intervention needed to recover a cluster that has lost its quorum.
> **However**, the controller automates this process, as long as is not a memory or disk capacity issue, ensuring no service
> interruption and **eliminating the need for any administration action**.


0. The quorum reconciler probes each cluster node **status** endpoint: `http://{nodeUrl}:{api-port}/status`. The response 
    looks like this:
    ```json
    {"committed_index":1,"queued_writes":0,"state":"LEADER"}
    ```
   `state` can be one of the following values: `LEADER`, `FOLLOWER`, `NOT_READY`. Based on the values retrieved for each node,
    the controller will evaluate the status of the whole cluster which can be:

    | Status            | Description                                                    |
    |-------------------|----------------------------------------------------------------|
    | OK                | A single `LEADER` node was found                               |
    | SPLIT_BRAIN       | More than one `LEADER`s were found                             |
    | NOT_READY         | More than the minimum required nodes were in `NOT_READY` state |
    | ELECTION_DEADLOCK | No `LEADER` node was found                                     |

> [!IMPORTANT]
> The evaluated cluster status is guarantying neither the aggregated health/availability of the cluster nor
> of its individual nodes. It is just an indication of what's going on internally in the pods/nodes.

1. If the cluster status is evaluated as `SPLIT_BRAIN`, it is instantly downgraded to a single node cluster
    giving Typesense the chance to try recover a healthy quorum fast and reliable. 

2. For any other cluster status outcome, the quorum reconciler, proceeds to probe each cluster node health endpoint: 
`http://{nodeUrl}:{api-port}/health`. The various response values of this request can be:

    | Response                                             | HTTP Status Code | Description                                         |
    |------------------------------------------------------|:----------------:|-----------------------------------------------------|
    | `{ok: true}`                                         |      `200`       | The node is healthy and active member of the quorum |
    | `{ok: false}`                                        |      `503`       | The node is unhealthy (various reasons)             |
    | `{ok: false, resource_error: "OUT_OF_{MEMORY/DISK}}` |      `503`       | The node requires manual intervention               |

   If every single node returns `{ok: true}` then the cluster is marked as **ready and fully operational**.
3. If the cluster status is evaluated as `ELECTION_DEADLOCK`, it is instantly downgraded to a single node cluster
   giving Typesense the chance to try recover a healthy quorum fast and reliable.

4.  - If the cluster status is evaluated as `NOT_READY` and it's either a single node cluster or the healthy evaluated 
      nodes are less than the minimum required nodes (at least `(N-1)/2`) then the cluster is instantly downgraded to a 
      single node cluster giving Typesense the chance to try recover a healthy quorum fast and reliable and waits a term 
      before starting the reconciliation again. If nothing of the above conditions are met, then the reconciler proceeds 
      to the next check point:
    - If the cluster status is evaluated as `OK` but the number of actual `StatefulSet` replicas is less than the desired 
      number of replicas specified in the `typesense.specs.replicas`, it is upgraded (either instantly or gradually; 
      depends on the value of `typesense.specs.incrementalQuorumRecovery`) and restarts the reconciliation after 
      approximately a minute. If none of the conditions above are met, the reconciler proceeds to the next check point:
    - If the healthy evaluated nodes are less than the minimum required nodes (at least `(N-1)/2`), then the cluster is
      marked as not ready and returns the control back to the reconciler waiting a term till it restarts the reconciliation loop,
    - If none of these checkpoints led to a restart of the reconciliation loop without a quorum recovery, then 
      the then the cluster is marked as **ready and fully operational**. 

## Custom Resource Definitions

### TypesenseCluster

Typesense Kubernetes Operator is controlling the lifecycle of multiple Typesense instances in the same Kubernetes cluster by
introducing `TypesenseCluster`, a new Custom Resource Definition:

![image](https://github.com/user-attachments/assets/841ad290-0401-4d7c-b85f-60a219ba88c5)

**Spec**

| Name                          | Description                                                            | Optional | Default       |
|-------------------------------|------------------------------------------------------------------------|----------|---------------|
| image                         | Typesense image                                                        |          |               |
| adminApiKey                   | Reference to the `Secret` to be used for bootstrap                     | X        |               |
| replicas                      | Size of the cluster (allowed 1, 3, 5 or 7)                             |          | 3             |
| apiPort                       | REST/API port                                                          |          | 8108          |
| peeringPort                   | Peering port                                                           |          | 8107          |
| resetPeersOnError             | automatic reset of peers on error                                      |          | true          |
| enableCors                    | enables CORS                                                           | X        | false         |
| corsDomains                   | comma separated list of domains allowed for CORS                       | X        |               |
| resources                     | resource request & limit                                               | X        | _check specs_ |
| affinity                      | group of affinity scheduling rules                                     | X        |               |
| nodeSelector                  | node selection constraint                                              | X        |               |
| tolerations                   | schedule pods with matching taints                                     | X        |               |
| additionalServerConfiguration | a reference to a `NodesListConfigMap` holding extra configuration      | X        |               |
| storage                       | check `StorageSpec` below                                              |          |               |
| ingress                       | check `IngressSpec` below                                              | X        |               |
| scrapers                      | array of `DocSearchScraperSpec`; check below                           | X        |               |
| metrics                       | check `MetricsSpec` below                                              | X        |               |
| topologySpreadConstraints     | how to spread a  group of pods across topology domains                 | X        |               |
| incrementalQuorumRecovery     | add nodes gradually to the statefulset while recovering                | X        | false         |

> [!IMPORTANT]
> * Any Typesense server configuration variable that is defined in Spec is overriding any additional reference of
>   the same variable in `additionalServerConfiguration`. You can find an example of providing an additional `NodesListConfigMap`
>   in: **config/samples/ts_v1alpha1_typesensecluster_kind.yaml**
> * Add additional Typesense server configuration variables in `NodesListConfigMap` as described in:
>   https://typesense.org/docs/27.1/api/server-configuration.html#using-environment-variables
> * In heavy datasets is advised to set `incrementalQuorumRecovery` to `true` and let the controller reconstruct the quorum
>   node by node. That will smooth the leader election process while new nodes are joining but it will make recovery process last longer.

**StorageSpec** (optional)

| Name             | Description                 | Optional | Default  |
|------------------|-----------------------------|----------|----------|
| size             | Size of the underlying `PV` | X        | 100Mi    |
| storageClassName | `StorageClass` to be used   |          | standard |

**IngressSpec** (optional)

| Name              | Description                          | Optional | Default |
|-------------------|--------------------------------------|----------|---------|
| referer           | FQDN allowed to access reverse proxy | X        |         |
| HttpDirectives    | Nginx Proxy HttpDirectives           | X        |         |
| serverDirectives  | Nginx Proxy serverDirectives         | X        |         |
| locationDirectives| Nginx Proxy locationDirectives       | X        |         |
| host              | Ingress Host                         |          |         |
| clusterIssuer     | cert-manager `ClusterIssuer`         |          |         |
| ingressClassName  | Ingress to be used                   |          |         |
| annotations       | User-Defined annotations             | X        |         |

> [!IMPORTANT]
> This feature requires the existence of [cert-manager](https://cert-manager.io/) in the cluster, but **does not** actively enforce it with an error.
> If you are targeting Open Telekom Cloud, you might be interested in provisioning additionally the designated DNS solver webhook
> for Open Telekom Cloud. You can find it [here](https://github.com/akyriako/cert-manager-webhook-opentelekomcloud).

**DocSearchScraperSpec** (optional)

| Name        | Description                              | Optional | Default |
|-------------|------------------------------------------|----------|---------|
| name        | name of the scraper                      |          |         |
| image       | container image to use                   |          |         |
| config      | config to use                            |          |         |
| schedule    | cron expression; no timezone; no seconds |          |         |

> [!CAUTION]
> Although in Typesense documentation under _Production Best Practices_ -> _Configuration_ is stated:
> "_Typesense comes built-in with a high performance HTTP server (opens new window)that is used by likes of Fastly (opens new window)in 
> their edge servers at scale. So Typesense can be directly exposed to incoming public-facing internet traffic, 
> without the need to place it behind another web server like Nginx / Apache or your backend API._" 
> 
> It is highly recommended, from this operator's perspective, to always expose Typesense behind a reverse proxy (using the `referer` option).

**MetricsSpec** (optional)

| Name     | Description                               | Optional | Default                                        |
|----------|-------------------------------------------|----------|------------------------------------------------|
| image    | container image to use                    | X        | akyriako78/typesense-prometheus-exporter:0.1.7 |
| release  | Prometheus release to become a target of  |          |                                                |
| interval | interval in _seconds_ between two scrapes | X        | 15                                             |

> [!TIP]
> If you've provisioned Prometheus via [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/blob/main/charts/kube-prometheus-stack/README.md), 
> you can find the corresponding `release` value of your Prometheus instance by checking the labels of the operator pod e.g:
> 
> ```bash
> kubectl describe pod {kube-prometheus-stack-operator-pod} -n {kube-prometheus-stack-namespace}
> 
> name:             promstack-kube-prometheus-operator-755485dc68-dmkw2
> Namespace:        monitoring
> [...]
> Labels:           app=kube-prometheus-stack-operator
> app.kubernetes.io/component=prometheus-operator
> app.kubernetes.io/instance=promstack
> app.kubernetes.io/managed-by=Helm
> app.kubernetes.io/name=kube-prometheus-stack-prometheus-operator
> app.kubernetes.io/part-of=kube-prometheus-stack
> app.kubernetes.io/version=67.8.0
> chart=kube-prometheus-stack-67.8.0
> heritage=Helm
> pod-template-hash=755485dc68
> release=promstack
> [...]
> ```


**Status**

| Condition      | Value | Reason                  | Description                                                |
|----------------|-------|-------------------------|------------------------------------------------------------|
| ConditionReady | true  | QuorumReady             | Cluster is Operational                                     |
|                | false | QuorumNotReady          | Cluster is not Operational                                 |
|                | false | QuorumDegraded          | Cluster is not Operational; Scheduled to Single-Instance   |
|                | false | QuorumUpgraded          | Cluster is Operational; Scheduled to Original Size         |
|                | false | QuorumNeedsIntervention | Cluster is not Operational; Administrative Action Required |

## Getting Started
You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster.
**Note:** Your controller will automatically use the current context in your kubeconfig file (i.e. whatever cluster `kubectl cluster-info` shows).

### Deploy Using Helm

If you are deploying on a production environment, it is **highly recommended** to deploy the
controller to the cluster using a **Helm chart** from its repo:

```sh
helm repo add typesense-operator https://akyriako.github.io/typesense-operator/
helm repo update

helm upgrade --install typesense-operator typesense-operator/typesense-operator -n typesense-system --create-namespace
```

### Running on the cluster

#### Deploy from Sources

1. Build and push your image to the location specified by `IMG`:

```sh
make docker-build docker-push IMG=<some-registry>/typesense-operator:<tag>
```

2. Deploy the controller to the cluster with the image specified by `IMG`:

```sh
make deploy IMG=<some-registry>/typesense-operator:<tag>
```

3. Install Instances of Custom Resources:

Provision one of the samples available in `config/samples`:

| Suffix           | Description        | CSI Driver                                 | Storage Class         |
|------------------|--------------------|--------------------------------------------|-----------------------|
|                  | Generic            |                                            | standard              |
| azure            | Microsoft Azure    | disk.csi.azure.com                         | managed-csi           |
| aws              | AWS                | ebs.csi.aws.com                            | gp2                   |
| opentelekomcloud | Open Telekom Cloud | disk.csi.everest.io<br/>obs.csi.everest.io | csi-disk<br/>csi-obs  |
| bm               | Bare Metal         | democratic-csi (iscsi/nfs)                 | iscsi<br/>nfs         |
| kind             | KiND               |                                            | rancher.io/local-path |

```sh
kubectl apply -f config/samples/ts_v1alpha1_typesensecluster_{{Suffix}}.yaml
```

e.g. for Open Telekom Cloud it would look like:

```yaml title=ts_v1alpha1_typesensecluster_aws.yaml
apiVersion: v1
kind: Secret
metadata:
  name: typesense-bootstrap-key
type: Opaque
data:
  typesense-api-key: SXdpVG9CcnFYTHZYeTJNMG1TS1hPaGt0dlFUY3VWUloxc1M5REtsRUNtMFFwQU93R1hoanVIVWJLQnE2ejdlSQ==
---
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  name: cluster-1
spec:
  image: typesense/typesense:27.1
  replicas: 3
  storage:
    size: 100Mi
    storageClassName: csi-disk
  ingress:
    host: ts.example.de
    ingressClassName: nginx
    clusterIssuer: opentelekomcloud-letsencrypt
  adminApiKey:
    name: typesense-common-bootstrap-key
  scrapers:
    - name: docusaurus-example-com
      image: typesense/docsearch-scraper:0.11.0
      config: "{\"index_name\":\"docusaurus-example\",\"start_urls\":[\"https://docusaurus.example.com/\"],\"sitemap_urls\":[\"https://docusaurus.example.com/sitemap.xml\"],\"sitemap_alternate_links\":true,\"stop_urls\":[\"/tests\"],\"selectors\":{\"lvl0\":{\"selector\":\"(//ul[contains(@class,'menu__list')]//a[contains(@class, 'menu__link menu__link--sublist menu__link--active')]/text() | //nav[contains(@class, 'navbar')]//a[contains(@class, 'navbar__link--active')]/text())[last()]\",\"type\":\"xpath\",\"global\":true,\"default_value\":\"Documentation\"},\"lvl1\":\"header h1\",\"lvl2\":\"article h2\",\"lvl3\":\"article h3\",\"lvl4\":\"article h4\",\"lvl5\":\"article h5, article td:first-child\",\"lvl6\":\"article h6\",\"text\":\"article p, article li, article td:last-child\"},\"strip_chars\":\" .,;:#\",\"custom_settings\":{\"separatorsToIndex\":\"_\",\"attributesForFaceting\":[\"language\",\"version\",\"type\",\"docusaurus_tag\"],\"attributesToRetrieve\":[\"hierarchy\",\"content\",\"anchor\",\"url\",\"url_without_anchor\",\"type\"]},\"conversation_id\":[\"833762294\"],\"nb_hits\":46250}"
      schedule: '*/2 * * * *'
```

#### Uninstall CRDs
To delete the CRDs from the cluster:

```sh
make uninstall
```

#### Undeploy controller
UnDeploy the controller from the cluster:

```sh
make undeploy
```

### How it works
This project aims to follow the Kubernetes [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/).

It uses [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/),
which provide a reconcile function responsible for synchronizing resources until the desired state is reached on the cluster.

### Test It Out
1. Install the CRDs into the cluster:

```sh
make install
```

2. Run your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

```sh
make run
```

### Modifying the API definitions
If you are editing the API definitions, generate the manifests such as CRs or CRDs using:

```sh
make generate && make manifests
```

> [!NOTE] 
> Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

### Debugging

When debugging (or running the controller out-of-cluster with `make run`) all **health** and **status** requests to individual pods
will fail as the node endpoints are not available to your development machine. For that matter you will need to deploy
on your environment [KubeVPN](https://github.com/KubeNetworks/kubevpn). KubeVPN, offers a Cloud Native Dev Environment 
that connects to your Kubernetes cluster network. It facilitates the interception of inbound traffic from remote 
Kubernetes cluster services or pods to your local PC so you can access them using either their FQDN or their IP address.

Follow the [official installation instructions](https://github.com/KubeNetworks/kubevpn?tab=readme-ov-file#quickstart) to install and configure KubeVPN.

## License

Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
