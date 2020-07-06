# Running the Humio-Operator on a Kubernetes Cluster

The below outlines the steps to run the humio-operator on any Kubernetes cluster. These steps will install Humio and Kafka in the *default* namespace. This cluster deployment uses Kubernetes hostpath and is *ephemeral*.

> **Note**: These instructions assume use of `helm v3`.

> **OpenShift Users**: Everywhere instructions mention `kubectl`, you can use swap that out with `oc`.

## (Optional) Prepare an installation of Kafka and Zookeeper

> **Note**: This step can be skipped if you already have existing Kafka and Zookeeper clusters available to use.

We will be using the Helm chart called cp-helm-charts to set up a Kafka and Zookeeper installation which we will use when starting up Humio clusters using the Humio operator.

```bash
helm repo add humio https://humio.github.io/cp-helm-charts

helm install humio humio/cp-helm-charts --namespace=default --set cp-zookeeper.servers=1 --set cp-kafka.brokers=1 --set cp-schema-registry.enabled=false --set cp-kafka-rest.enabled=false --set cp-kafka-connect.enabled=false --set cp-ksql-server.enabled=false --set cp-control-center.enabled=false
```

Check the pods to make sure Kafka and Zookeeper have started, this may take up to a minute:

```bash
kubectl get pods
NAME                   READY   STATUS    RESTARTS   AGE
humio-canary           1/1     Running   0          23s
humio-cp-kafka-0       2/2     Running   0          23s
humio-cp-zookeeper-0   2/2     Running   0          23s
```

> **Note**: The humio-canary pod my show a failed state in some cases, this isn't an issue.

## Install humio-operator

First we install the CRD's:

```bash
kubectl apply -f https://raw.githubusercontent.com/humio/humio-operator/humio-operator-0.0.6/deploy/crds/core.humio.com_humioclusters_crd.yaml
kubectl apply -f https://raw.githubusercontent.com/humio/humio-operator/humio-operator-0.0.6/deploy/crds/core.humio.com_humioexternalclusters_crd.yaml
kubectl apply -f https://raw.githubusercontent.com/humio/humio-operator/humio-operator-0.0.6/deploy/crds/core.humio.com_humioingesttokens_crd.yaml
kubectl apply -f https://raw.githubusercontent.com/humio/humio-operator/humio-operator-0.0.6/deploy/crds/core.humio.com_humioparsers_crd.yaml
kubectl apply -f https://raw.githubusercontent.com/humio/humio-operator/humio-operator-0.0.6/deploy/crds/core.humio.com_humiorepositories_crd.yaml
```

Installing the humio-operator on non-OpenShift installations:

```bash
helm repo add humio-operator https://humio.github.io/humio-operator

helm install humio-operator humio-operator/humio-operator \
  --namespace default \
  --values charts/humio-operator/values.yaml
```

For OpenShift installations:

```bash
helm repo add humio-operator https://humio.github.io/humio-operator

helm install humio-operator humio-operator/humio-operator \
  --namespace default \
  --set openshift=true \
  --values charts/humio-operator/values.yaml
```

Example output:

```bash
Release "humio-operator" does not exist. Installing it now.
NAME: humio-operator
LAST DEPLOYED: Tue Jun  2 15:31:52 2020
NAMESPACE: default
STATUS: deployed
REVISION: 1
TEST SUITE: None
```

## Create Humio cluster

At this point, we should have the humio-operator installed, so all we need to spin up the Humio cluster is to construct a YAML file containing the specifics around the desired configuration. We will be using the following YAML snippet. 

_Note: this configuration is not valid for a long-running or production cluster. For a persistent cluster, we recommend using ephemeral nodes backed by S3, or if that is not an option, persistent volumes. See the [examples](https://github.com/humio/humio-operator/tree/master/examples) directory for those configurations._

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: humio-test-cluster
spec:
  image: "humio/humio-core:1.12.0"
  environmentVariables:
    - name: "ZOOKEEPER_URL"
      value: "humio-cp-zookeeper-0.humio-cp-zookeeper-headless:2181"
    - name: "KAFKA_SERVERS"
      value: "humio-cp-kafka-0.humio-cp-kafka-headless:9092"
    - name: "AUTHENTICATION_METHOD"
      value: "single-user"
    - name: "SINGLE_USER_PASSWORD"
      value: "MyVeryS3cretPassword"
```

Save the YAML snippet to a file on your machine called `humio-test-cluster.yaml` and apply it:

```bash
kubectl apply -f humio-test-cluster.yaml
```

The Humio cluster should now be in a bootstrapping state:

```bash
kubectl get humioclusters
NAME                 STATE           NODES   VERSION
humio-test-cluster   Bootstrapping
```

After a few minutes the Humio pods should be started and the HumioCluster state should update to "Running":

```bash
kubectl get pods,humioclusters
NAME                                 READY   STATUS    RESTARTS   AGE
pod/humio-operator-b6884f9f5-vpdzc   1/1     Running   0          10m
pod/humio-test-cluster-core-cvpkfx   2/2     Running   0          3m
pod/humio-test-cluster-core-hffyvo   2/2     Running   0          5m
pod/humio-test-cluster-core-rxnhju   2/2     Running   0          7m

NAME                                               STATE     NODES   VERSION
humiocluster.core.humio.com/example-humiocluster   Running   3       1.12.0--build-128433343--sha-3969325cc0f4040b24fbdd0728df4a1effa58a52
```

## Logging in to the cluster

As the instructions are for the generic use-case, the external access to Humio will vary depending on the specifics for the Kubernetes cluster being used. Because of that we leverage `kubectl`s port-forward functionality to gain access to Humio.

It is worth noting that it is possible to adjust the YAML snippet for the HumioCluster such that it exposes Humio to be externally accessible, but that is left out from this example.

```bash
kubectl port-forward svc/humio-test-cluster 8080
```

Now open your browser and visit [http://127.0.0.1:8080](http://127.0.0.1:8080) to access the Humio cluster and in our case, we can use the username `developer` with the `MyVeryS3cretPassword`, as stated in the HumioCluster snippet.
