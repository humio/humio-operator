# Running the Humio-Operator on a Kubernetes Cluster

The below outlines the explicit steps to run the humio-operator on any Kubernetes cluster, this particular example uses AWS EKS. These steps will install Humio and Kafka in the *default* namespace. This cluster delployment uses Kubernetes hostpath and is *ephemeral*. 

## Begin by making a directory to work from
```
mkdir ~/humio-operator-test
cd ~/humio-operator-test
```

## Clone the cp-helm-charts to install Kafka and Zookeeper

```
git clone https://github.com/humio/cp-helm-charts.git humio-cp-helm-charts
helm template humio humio-cp-helm-charts --namespace=default --set cp-zookeeper.servers=1 --set cp-kafka.brokers=1 --set cp-schema-registry.enabled=false --set cp-kafka-rest.enabled=false --set cp-kafka-connect.enabled=false --set cp-ksql-server.enabled=false --set cp-control-center.enabled=false > cp-kafka-setup.yml
```

Apply the yaml that was generated:
```
kubectl apply -f cp-kafka-setup.yml 
```

Check the pods to make sure Kafka and Zookeeper have started, this may take up to a minute:
```
kubectl get pods
NAME                   READY   STATUS    RESTARTS   AGE
humio-canary           1/1     Running   0          23s
humio-cp-kafka-0       2/2     Running   0          23s
humio-cp-zookeeper-0   2/2     Running   0          23s
```

Note: The humio-canary pod my show a failed state in some cases, this isn't an issue.

## Clone the Humio operator and install prerequisite resources
```
git clone https://github.com/humio/humio-operator.git humio-operator

# setup service account and cluster roles/bindings
kubectl apply -f humio-operator/deploy/role.yaml
kubectl apply -f humio-operator/deploy/service_account.yaml
kubectl apply -f humio-operator/deploy/role_binding.yaml
kubectl apply -f humio-operator/deploy/cluster_role.yaml
kubectl apply -f humio-operator/deploy/cluster_role_binding.yaml
```

Example output:
```
kubectl apply -f humio-operator/deploy/role.yaml
role.rbac.authorization.k8s.io/humio-operator created
kubectl apply -f humio-operator/deploy/service_account.yaml
serviceaccount/humio-operator created
kubectl apply -f humio-operator/deploy/role_binding.yaml
rolebinding.rbac.authorization.k8s.io/humio-operator created
kubectl apply -f humio-operator/deploy/cluster_role.yaml
clusterrole.rbac.authorization.k8s.io/humio-operator created
kubectl apply -f humio-operator/deploy/cluster_role_binding.yaml
clusterrolebinding.rbac.authorization.k8s.io/humio-operator created
```

## Create the CRDs Humio uses
```
kubectl apply -f humio-operator/deploy/crds/core.humio.com_humioexternalclusters_crd.yaml
kubectl apply -f humio-operator/deploy/crds/core.humio.com_humioclusters_crd.yaml
kubectl apply -f humio-operator/deploy/crds/core.humio.com_humioingesttokens_crd.yaml
kubectl apply -f humio-operator/deploy/crds/core.humio.com_humioparsers_crd.yaml
kubectl apply -f humio-operator/deploy/crds/core.humio.com_humiorepositories_crd.yaml
```

Example output:
```
kubectl apply -f humio-operator/deploy/crds/core.humio.com_humioclusters_crd.yaml
customresourcedefinition.apiextensions.k8s.io/humioclusters.core.humio.com created
kubectl apply -f humio-operator/deploy/crds/core.humio.com_humioingesttokens_crd.yaml
customresourcedefinition.apiextensions.k8s.io/humioingesttokens.core.humio.com created
kubectl apply -f humio-operator/deploy/crds/core.humio.com_humioparsers_crd.yaml
customresourcedefinition.apiextensions.k8s.io/humioparsers.core.humio.com created
kubectl apply -f humio-operator/deploy/crds/core.humio.com_humiorepositories_crd.yaml
customresourcedefinition.apiextensions.k8s.io/humiorepositories.core.humio.com created
kubectl apply -f humio-operator/deploy/crds/core.humio.com_humioexternalclusters_crd.yaml
customresourcedefinition.apiextensions.k8s.io/humioexternalclusters.core.humio.com created
```

## Install the Humio Operator
```
kubectl apply -f humio-operator/examples/eks-simple-cluster/humio-operator.yml
deployment.apps/humio-operator created
```

Check that the humio-operator pod started:
```
kubectl get pods 
NAME                             READY   STATUS    RESTARTS   AGE
humio-canary                     0/1     Error     0          14m
humio-cp-kafka-0                 2/2     Running   1          14m
humio-cp-zookeeper-0             2/2     Running   0          14m
humio-operator-7b9f7846d-mk7cd   1/1     Running   0          15s
```

## Create Humio cluster
```
kubectl apply -f humio-operator/examples/eks-simple-cluster/humio-cluster-simple.yml 
```

The Humio cluster should now be in a bootstrapping state:
```
kubectl get HumioClusters
NAME                 STATE           NODES   VERSION
humio-test-cluster   Bootstrapping           
```

After a few minutes the Humio pods should be started:
```
kubectl get pods 
humio-test-cluster-core-cvpkfx   2/2     Running   0          3m
humio-test-cluster-core-hffyvo   2/2     Running   0          5m
humio-test-cluster-core-rxnhju   2/2     Running   0          7m
```


## Add a load balancer to access the cluster
```
 kubectl apply -f humio-operator/examples/eks-simple-cluster/humio-load-balancer.yml 
service/humio-lb created
```

Get the URL for the load balancer:
```
kubectl get services | grep humio-lb
humio-lb                      LoadBalancer   172.20.78.219   a93d8a942e6f740f18029fa580b4f478-346070595.us-west-2.elb.amazonaws.com   8080:32166/TCP      31m
```

## Logging in to the cluster
The cluster should now be available at the load balancer hostname on port 8080, IE http://a93d8a942e6f740f18029fa580b4f478-346070595.us-west-2.elb.amazonaws.com:8080, using the username "developer" and the password "MyVeryS3cretPassword"
