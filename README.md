# Typesense Kubernetes Operator
![Static Badge](https://img.shields.io/badge/status-beta-orange)

The **Typesense Kubernetes Operator** is designed to manage the deployment and lifecycle of [Typesense](https://typesense.org/) clusters within Kubernetes environments. 
The operator is developed in Go using [Operator SDK Framework](https://sdk.operatorframework.io/), an open source toolkit to manage Kubernetes native applications, called Operators, in an effective, automated, and scalable way. 

## Description

Key features of Typesense Kubernetes Operator include:

- **Custom Resource Management**: Provides a Kubernetes-native interface to define and manage Typesense cluster configurations using a CRD named `TypesenseCluster`.
- **Typesense Lifecycle Automation**: Simplifies deploying, scaling, and managing Typesense clusters. Handles aspects like:
    - bootstrap Typesense's Admin API Key creation as a `Secret`,
    - deploy Typesense as a `StatefulSet`,
    - provision Typesense services (headless & discovery `Services`),
    - actively discover and update Typesense's nodes list (quorum configuration mounted as `ConfigMap`),
    - place claims for Typesense `PersistentVolumes`
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
and the remaining node(s) cannot safely build consensus on which node is the leader. To avoid a potential split brain issue,
Typesense then stops accepting writes and reads **until some manual verification and intervention is done**.

![image](https://github.com/user-attachments/assets/a28357bf-199f-45e7-9ce4-9557043bfc20)

> [!NOTE]
> Illustration's been taken from [Free Gophers Pack](https://github.com/MariaLetta/free-gophers-pack)

In production environments, manual intervention is sometimes impossible or undesirable, and downtime for a service like
Typesense may be unacceptable. The Typesense Kubernetes Operator addresses both of these challenges.

### Problem 1: Quorum reconfiguration

The Typesense Kubernetes Operator manages the entire lifecycle of Typesense Clusters within Kubernetes:

1. A random token is generated and stored as a base64-encoded value in a new `Secret`. This token serves as the Admin API key for bootstrapping the Typesense cluster.
2. A `ConfigMap` is created, containing the endpoints of the cluster nodes as a single concatenated string in its `data` field.
   During each reconciliation loop, the operator identifies any changes in endpoints and updates the `ConfigMap`. This `ConfigMap`
   is mounted in every `Pod` at the path where raft expects the quorum configuration, ensuring quorum configuration stays always updated.
   The Fully Qualified Domain Name (FQDN) for each endpoint of the headless service adheres to the following naming convention:

`{cluster-name}-sts-{pod-index}.{cluster-name}-sts-svc.{namespace}.svc.cluster.local:{peering-port}:{api-port}`

> [!IMPORTANT]
> **This completely eliminates the need for a sidecar** to translate the endpoints of the headless `Service` into `Pod` IP addresses.
> The FQDN of the endpoints automatically resolves to the new IP addresses, and raft will begin contacting these endpoints
> within its 30-second polling interval.

3. Next, the reconciler creates a headless `Service` required for the `StatefulSet`, along with a standard Kubernetes
   Service of type `ClusterIP`. The latter exposes the REST/API endpoints of the Typesense cluster to external systems.
4. A `StatefulSet` is then created. The quorum configuration stored in the `ConfigMap` is mounted as a volume in each `Pod`
   under `/usr/share/typesense/nodelist`. No `Pod` restart is necessary when the `ConfigMap` changes, as raft automatically
   detects and applies the updates.

![image](https://github.com/user-attachments/assets/30b6989c-c872-46ef-8ece-86c5d4911667)

> [!NOTE]
> The interval between reconciliation loops depends on the number of nodes. This approach ensures raft has sufficient
> "breathing room" to carry out its operations—such as leader election, log replication, and bootstrapping—before the
> next quorum health reconciliation begins.

5. The controller assesses the quorum's health by probing each node at `http://{nodeUrl}:{api-port}/health`. Based on the
   results, it formulates an action plan for the next reconciliation loop. This process is detailed in the following section:

### Problem 2: Recovering a cluster that has lost quorum

During configuration changes, we cannot switch directly from the old configuration to the next, because conflicting
majorities could arise. When that happens, no leader can be elected and eventually raft declares the whole cluster
as unavailable which leaves it in a hot loop. One way to solve it, is to force the cluster downgrade to a single instance
cluster and then gradually introduce new nodes (by scaling up the `StatefulSet`). With that approach we avoid the need
of manual intervention in order to recover a cluster that has lost quorum.

![image](https://github.com/user-attachments/assets/007852ba-e173-43a4-babf-d250f8a34ad1)

> [!IMPORTANT]
> Scaling the `StatefulSet` down and subsequently up, would typically be the manual intervention needed to recover a cluster that has lost its quorum.
> **However**, the controller automates this process, as long as is not a memory or disk capacity issue, ensuring no service
> interruption and **eliminating the need for any administration action**.

![image](https://github.com/user-attachments/assets/55fda493-d35a-405c-8a58-a6f9436a28db)

**Left Path:**

1. The quorum reconciler probes each cluster node at `http://{nodeUrl}:{api-port}/health`. If every node responds with `{ ok: true }`,
   the `ConditionReady` status of the `TypesenseCluster` custom resource is updated to `QuorumReady`, indicating that the cluster is fully healthy and operational.
2.
    - If the cluster size matches the desired size defined in the `TypesenseCluster` custom resource (and was not downgraded
      during a previous loop—this scenario will be discussed later), the quorum reconciliation loop sets the `ConditionReady`
      status of the `TypesenseCluster` custom resource to `QuorumReady`, exits, and hands control back to the main controller loop.
    - If the cluster was downgraded to a single instance during a previous reconciliation loop, the quorum reconciliation loop
      sets the `ConditionReady` status of the `TypesenseCluster` custom resource to `QuorumUpgraded`. It then returns control
      to the main controller loop, which will attempt to restore the cluster to the desired size defined in the `TypesenseCluster`
      custom resource during the next reconciliation loop. Raft will then identify the new quorum configuration and elect a new leader.
    - If a node runs out of memory or disk, the health endpoint response will include an additional `resource_error` field,
      set to either `OUT_OF_MEMORY` or `OUT_OF_DISK`, depending on the issue. In this case, the quorum reconciler marks the
      `ConditionReady` status of the `TypesenseCluster` as `QuorumNeedsIntervention`, triggers a Kubernetes `Event`, and
      returns control to the main controller loop. **In this scenario, manual intervention is required**. You must adjust the
      resources in the `PodSpec` or the storage in the `PersistentVolumeClaim` of the `StatefulSet` to provide new memory limits
      or increased storage size. This can be done by modifying and re-applying the corresponding `TypesenseCluster` manifest.

**Right Path:**

1. The quorum reconciler probes each node of the cluster at http://{nodeUrl}:{api-port}/health.
    - If the required number of nodes (at least `(N-1)/2`) return `{ ok: true }`, the `ConditionReady` status of the
      `TypesenseCluster` custom resource is set to `QuorumReady`, indicating that the cluster is healthy and operational,
      **even if** some nodes are unavailable. Control is then returned to the main controller loop.
    - If the required number of nodes (at least `(N-1)/2`) return `{ ok: false }`, the `ConditionReady` status of the
      `TypesenseCluster` custom resource is set to `QuorumDowngrade`, marking the cluster as unhealthy. As part of the
      mitigation plan, the cluster is scheduled for a downgrade to a single instance, with the intent to allow raft to automatically recover the quorum.
      The quorum reconciliation loop then returns control to the main controller loop.
    - In the next quorum reconciliation, the process will take the **Left Path**, that will eventually discover a healthy quorum,
      nevertheless with the wrong amount of nodes; thing that will lead to setting the `ConditionReady` condition of the `TypesenseCluster` as `QuorumUpgraded`.
      What happens next is already described in the **Left Path**.

## Custom Resource Definitions

### TypesenseCluster

Typesense Kubernetes Operator is controlling the lifecycle of multiple Typesense instances in the same Kubernetes cluster by
introducing `TypesenseCluster`, a new Custom Resource Definition:

![image](https://github.com/user-attachments/assets/23e40781-ca21-4297-93bf-2b5dbebc7e0e)

**Spec**

| Name              | Description                                  | Optional | Default |
|-------------------|----------------------------------------------|----------|---------|
| image             | Typesense image                              |          |         |
| replicas          | Size of the cluster                          |          | 1       |
| apiPort           | REST/API port                                |          | 8108    |
| peeringPort       | Peering port                                 |          | 8107    |
| resetPeersOnError | automatic reset of peers on error            |          | true    |
| corsDomains       | domains that would be allowed for CORS calls | X        |         |
| storage           | check StorageSpec below                      |          |         |
| ingress           | check IngressSpec below                      | X        |         |

**StorageSpec** (optional)

| Name             | Description               | Optional | Default  |
|------------------|---------------------------|----------|----------|
| size             | Size of the underlying PV | X        | 100Mi    |
| storageClassName | Storage Class to use      |          | standard |

**IngressSpec** (optional)

| Name             | Description                          | Optional | Default |
|------------------|--------------------------------------|----------|---------|
| referer          | FQDN allowed to access reverse proxy | X        |         |
| host             | Ingress Host                         |          |         |
| clusterIssuer    | cert-manager ClusterIssuer           |          |         |
| ingressClassName | Ingress to use                       |          |         |
| annotations      | User-Defined annotations             | X        |         |

> [!CAUTION]
> Although in Typesense documentation under _Production Best Practices_ -> _Configuration_ is stated:
> "_Typesense comes built-in with a high performance HTTP server (opens new window)that is used by likes of Fastly (opens new window)in 
> their edge servers at scale. So Typesense can be directly exposed to incoming public-facing internet traffic, 
> without the need to place it behind another web server like Nginx / Apache or your backend API._" it is highly recommended
> , from this operator's perspective, to always expose Typesense behind a reverse proxy (using the `referer` option).


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

| Suffix        | Description        | CSI Driver                                 | Storage Class         |
|---------------|--------------------|--------------------------------------------|-----------------------|
|               | Generic            |                                            | standard              |
| azure         | Microsoft Azure    | disk.csi.azure.com                         | managed-csi           |
| aws           | AWS                | ebs.csi.aws.com                            | gp2                   |
| opentelekomcloud | Open Telekom Cloud | disk.csi.everest.io<br/>obs.csi.everest.io | csi-disk<br/>csi-obs  |
| bm            | Bare Metal         | democratic-csi (iscsi/nfs)                 | iscsi<br/>nfs         |
| kind          | KiND               |                                            | rancher.io/local-path |

```sh
kubectl apply -f config/samples/ts_v1alpha1_typesensecluster_{{Suffix}}
```

e.g. for AWS looks like:

```yaml title=ts_v1alpha1_typesensecluster_aws.yaml
apiVersion: ts.opentelekomcloud.com/v1alpha1
kind: TypesenseCluster
metadata:
  labels:
    app.kubernetes.io/name: typesense-operator
    app.kubernetes.io/managed-by: kustomize
  name: cluster-1
spec:
  image: typesense/typesense:27.1
  replicas: 3
  storage:
    size: 100Mi
    storageClassName: gp2
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

**NOTE:** You can also run this in one step by running: `make install run`

### Modifying the API definitions
If you are editing the API definitions, generate the manifests such as CRs or CRDs using:

```sh
make generate && make manifests
```

**NOTE:** Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

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
