# Migrating from Humio Helm Charts

This guide describes how to migration from an existing cluster running the 
[Humio Helm Chart](https://github.com/humio/humio-helm-charts) to the Humio Operator and `HumioCluster` custom resource.

## Prerequisites

### Identify method of deployment

There are two different approaches to migration depending on how the existing helm chart is deployed.

* Using ephemeral nodes with bucket storage
* Using PVCs

By default, the original helm chart uses PVCs. If the existing chart is deployed with the environment variable 
`S3_STORAGE_BUCKET`, then it is using ephemeral nodes with bucket storage.

### Migrate Kafka and Zookeeper

The Humio Operator does not run Kafka and Zookeeper built-in alongside Humio as the Humio Helm Charts do. In order to
migrate to the Operator, Humio must point to a Kafka and Zookeeper that is not managed by Humio. There are a number of
Open Source Operators for running Kafka and Zookeeper, for example:
* [Banzai Cloud](https://github.com/banzaicloud/kafka-operator)
* [Strimzi](https://github.com/strimzi/strimzi-kafka-operator)

If you're running on AWS, then MSK is recommended for ease of use: [MSK](https://aws.amazon.com/msk/)

It is necessary to perform the Kafka and Zookeeper migration before continuing with the migration to the operator. This 
can be done by taking these steps:
1) Start up Kafka and Zookeeper (not managed by the operator)
2) Shut down Humio nodes
3) Reconfigure the values.yaml to use the new Kafka and Zookeeper connection. For example: 
    ```yaml
    humio-core:
      external:
        kafkaBrokers: 192.168.0.10:9092,192.168.1.10:9092,192.168.2.10:9092
        zookeeperServers: 192.168.0.20:2181,192.168.1.20:2181,192.168.2.20:2181
    ```
4) Start Humio back up

## Migrating Using Ephemeral Nodes and Bucket Storage

When migrating to the Operator using ephemeral nodes and bucket storage, first install the Operator but bring down the
existing Humio pods prior to creating the `HumioCluster`. Configure the new `HumioCluster` to use the same kafka and 
zookeeper servers as the existing cluster. The Operator will create pods that assume the identity of the existing nodes
and will pull data from bucket storage as needed.

1) Install the Operator according to the 
[installation](https://github.com/humio/humio-operator/tree/master/docs#install-humio-operator) docs.
2) Bring down existing pods by changing the `replicas` of the Humio stateful set to `0`.
3) Create a `HumioCluster` according to the 
[create humio cluster](https://github.com/humio/humio-operator/tree/master/docs#create-humio-cluster) docs. Ensure that 
this resource is configured the same as the existing chart's values.yaml file. See 
[special considerations](#special-considerations). Ensure that TLS is disabled for the `HumioCluster`, see [TLS](#tls). 
Ensure that `autoRebalancePartitions` is set to `false` (default).
4) Validate that the new Humio pods are running with the existing node identities and they show up in the Cluster 
Administration page of the Humio UI.
5) Follow either [Ingress Migration](#ingress-migration) or [Service Migration](#service-migration) depending on whether
   you are using services or ingress to access the Humio cluster.
6) Modify the Humio Helm Chart values.yaml so that it no longer manages Humio. If using fluentbit, ensure es
    autodiscovery is turned off: 
    ```yaml
     humio-core:
       enabled: false
     humio-fluentbit:
       es:
         autodiscovery: false
     ```
     And then run: `helm upgrade -f values.yaml humio humio/humio-helm-charts`. This will continue to keep fluentbit 
     and/or metricbeat if they are enabled. If you do not wish to keep fluentbit and/or metricbeat or they are not 
     enabled, you can uninstall the Humio Helm chart by running `helm delete --purge humio` where `humio` is the name of
     the original Helm Chart. _Be cautious to delete the original Helm Chart and not the Helm Chart used to install the 
     Operator._
7) Enable [TLS](#tls).

## Migrating Using PVCs

When migrating to the Operator using PVCs, install the Operator while the existing cluster is running and 
configure the new `HumioCluster` to use the same kafka and zookeeper servers as the existing cluster. The Operator will
create new nodes as part of the existing cluster. From there, change the partition layout such that they are assigned to
only the new nodes, and then we can uninstall the old helm chart.

1) Install the Operator according to the 
[installation](https://github.com/humio/humio-operator/tree/master/docs#install-humio-operator) docs.
2) Create a `HumioCluster` according to the 
[create humio cluster](https://github.com/humio/humio-operator/tree/master/docs#create-humio-cluster) docs. Ensure that 
this resource is configured the same as the existing chart's values.yaml file. See 
[special considerations](#special-considerations). Ensure that TLS is disabled for the `HumioCluster`, see 
[TLS](#tls). Ensure that `autoRebalancePartitions` is set to `false` (default).
3) Validate that the new Humio pods are running and they show up in the Cluster Administration page of the Humio UI.
4) Manually migrate digest partitions from the old pods created by the Helm Chart to the new pods created by the
Operator.
5) Manually migrate storage partitions from the old pods created by the Helm Chart to the new pods created by the
Operator. After the partitions have been re-assigned, for each of the new nodes, click `Show Options` and then 
`Start Transfers`. This will begin the migration of data.
6) Wait until all new nodes contain all the data and the old nodes contain no data.
7) Follow either [Ingress Migration](#ingress-migration) or [Service Migration](#service-migration) depending on whether
   you are using services or ingress to access the Humio cluster.
8) Modify the Humio Helm Chart values.yaml so that it no longer manages Humio. If using fluentbit, ensure es
    autodiscovery is turned off: 
    ```yaml
     humio-core:
       enabled: false
     humio-fluentbit:
       es:
         autodiscovery: false
     ```
     And then run: `helm upgrade -f values.yaml humio humio/humio-helm-charts`. This will continue to keep fluentbit 
     and/or metricbeat if they are enabled. If you do not wish to keep fluentbit and/or metricbeat or they are not 
     enabled, you can uninstall the Humio Helm chart by running `helm delete --purge humio` where `humio` is the name of
     the original Helm Chart. _Be cautious to delete the original Helm Chart and not the Helm Chart used to install the 
     Operator._
9) Enable [TLS](#tls).

## Service Migration

This section is only applicable if the method of accessing the cluster is via the service resources. If you are using 
ingress, refer to the [Ingress Migration](#ingress-migration).

The Humio Helm Chart manages three services: the `http` service, the `es` service and a `headless` service which is
required by the statefulset. All of these services will be replaced by a single service which is named with the name of
the `HumioCluster`.

After migrating the pods, it will no longer be possible to access the cluster using any of the old services. Ensure
that the new service in the `HumioCluster` is exposed the same way (e.g. `type: LoadBalancer`) and then begin using
the new service to access the cluster.

## Ingress Migration

This section is only applicable if the method of accessing the cluster is via the ingress resources. If you are using 
services, refer to the [Service Migration](#service-migration).

When migrating using ingress, be sure to enable and configure the `HumioCluster` ingress using the same hostnames that
the Helm Chart uses. See [ingress](#ingress). As long as the ingress resources use the same ingress controller, they 
should migrate seamlessly as DNS will resolve to the same nginx controller. The ingress resources managed by the Helm 
Chart will be deleted when the Helm Chart is removed or when `humio-core.enabled` is set to false in the values.yaml. 

If you wish to use the same certificates that were generated for the old ingress resource for the new ingresses, you 
must copy the old secrets to the new name format of `<cluster name>-certificate` and `<cluster name>-es-certificate`. It
is possible to use a custom secret name for the certificates by setting `spec.ingress.secretName` and 
`spec.ingress.esSecretName` on the `HumioCluster` resource, however you cannot simply set this to point to the existing
secrets as they are managed by the Helm Chart and will be deleted when the Helm Chart is removed or when
`humio-core.enabled` is set to false in the values.yaml. 

## Special Considerations

There are many situations that when migrating from the [Humio Helm Chart](https://github.com/humio/humio-helm-charts) to 
the Operator where the configuration does not transfer directly from the values.yaml to the `HumioCluster` resource. 
This section lists some common configurations with the original Helm Chart values.yaml and the replacement 
`HumioCluster` spec configuration. Only the relevant parts of the configuration are present starting from the top-level
key for the subset of the resource.

It is not necessary to migrate every one of the listed configurations, but instead use these as a reference on how to
migrate only the configurations that are relevant to your cluster.

### TLS

The Humio Helm Chart supports TLS for Kafka communication but does not support TLS for Humio-to-Humio communication.
This section refers to Humio-to-Humio TLS. For Kafka, see [extra kafka configs](#extra-kafka-configs).

By default, TLS is enabled when creating a `HumioCluster` resource. This is recommended, however, when performing a
migration from the Helm Chart, TLS should be disabled and then after the migration is complete TLS can be enabled.

#### Humio Helm Chart
*Not supported*

#### HumioCluster
```yaml
spec:
  tls:
    enabled: false
```

### Host Path

The Operator creates Humio pods with a stricter security context than the Humio Helm Charts. To support this
stricter context, it is necessary for the permissions of the `hostPath.path` (i.e. the path on the kubernetes node that 
is mounted into the Humio pods) has a group owner of the `nobody` user which is user id `65534`.

#### Humio Helm Chart
```yaml
humio-core:
  primaryStorage:
    type: hostPath
  hostPath:
    path: /mnt/disks/vol1
    type: Directory
``` 

#### HumioCluster
```yaml
spec:
  dataVolumeSource:
    hostPath:
      path: /mnt/disks/vol1
      type: Directory
```

### Persistent Volumes

By default, the Helm Chart uses persistent volumes for storage of the Humio data volume. This changed in the Operator,
where it is now required to define the storage medium.

#### Humio Helm Chart
```yaml
humio-core:
  storageVolume:
    size: 50Gi
``` 

#### HumioCluster
```yaml
spec:
  dataVolumePersistentVolumeClaimSpecTemplate:
    accessModes: [ReadWriteOnce]
    resources:
      requests:
        storage: 50Gi
```

### Custom Storage Class for Persistent Volumes

#### Humio Helm Chart

Create a storage class:
```yaml
humio-core:
  storageClass:
    provisioner: kubernetes.io/gce-pd
    parameters:
      type: pd-ssd
```

Use a custom storage class:
```yaml
humio-core:
  storageClassName: custom-storage-class-name
```

#### HumioCluster

Creating a storage class is no longer supported. First, create your storage class by following the 
[offical docs](https://kubernetes.io/docs/concepts/storage/storage-classes) and then use the following configuration to 
use it.
```yaml
spec:
  dataVolumePersistentVolumeClaimSpecTemplate:
    storageClassName: my-storage-class
```

### Pod Resources

#### Humio Helm Chart
```yaml
humio-core:
  resources:
    limits:
      cpu: "4"
      memory: 6Gi
    requests:
      cpu: 2
      memory: 4Gi
``` 

#### HumioCluster
```yaml
spec:
  resources:
    limits:
      cpu: "4"
      memory: 6Gi
    requests:
      cpu: 2
      memory: 4Gi
```

### JVM Settings

#### Humio Helm Chart
```yaml
jvm:
  xss: 2m
  xms: 256m
  xmx: 1536m
  maxDirectMemorySize: 1536m
  extraArgs: "-XX:+UseParallelOldGC"
``` 

#### HumioCluster
```yaml
spec:
  environmentVariables:
    - name: HUMIO_JVM_ARGS
      value: "-Xss2m -Xms256m -Xmx1536m -server -XX:MaxDirectMemorySize=1536m -XX:+UseParallelOldGC"
```

### Pod Anti-Affinity

It is highly recommended to have anti-affinity policies in place and required for when using `hostPath` for
storage. 

_Note that the Humio pod labels are different between the Helm Chart and operator. In the Helm Chart, the pod label that
is used for anti-affinity is `app=humio-core`, while the operator is `app.kubernetes.io/name=humio`. If migrating PVCs,
it is important to ensure that the new pods created by the operator are not scheduled on the nodes that run the old pods
created by the Humio Helm Chart. To do this, ensure there is a `matchExpressions` with `DoesNotExist` on the `app` key. 
See below for the example._

#### Humio Helm Chart
```yaml
humio-core:
  affinity:
    podAntiAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
      - labelSelector:
          matchExpressions:
          - key: app
            operator: In
            values:
            - humio-core
        topologyKey: kubernetes.io/hostname
``` 

#### HumioCluster
```yaml
spec:
  affinity:
    podAntiAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
      - labelSelector:
          matchExpressions:
          - key: app.kubernetes.io/name
            operator: In
            values:
            - humio
          - key: app
            operator: DoesNotExist
        topologyKey: kubernetes.io/hostname
```

### Service Type

#### Humio Helm Chart
```yaml
humio-core:
  service:
    type: LoadBalancer
``` 

#### HumioCluster

```yaml
spec:
  humioServiceType: LoadBalancer
```

### Ingress

#### Humio Helm Chart
```yaml
humio-core:
  ingress:
    enabled: true
    config:
      - name: general
        annotations:
          certmanager.k8s.io/acme-challenge-type: http01
          certmanager.k8s.io/cluster-issuer: letsencrypt-prod
          kubernetes.io/ingress.class: nginx
          kubernetes.io/tls-acme: "true"
        hosts:
          - host: my-cluster.example.com
            paths:
              - /
        tls:
          - secretName: my-cluster-crt
            hosts:
              - my-cluster.example.com
      - name: ingest-es
        annotations:
          certmanager.k8s.io/acme-challenge-type: http01
          cert-manager.io/cluster-issuer: letsencrypt-prod
          kubernetes.io/ingress.class: nginx
          kubernetes.io/tls-acme: "true"
        rules:
          - host: my-cluster-es.humio.com
            http:
              paths:
                - path: /
                  backend:
                    serviceName: humio-humio-core-es
                    servicePort: 9200
        tls:
          - secretName: my-cluster-es-crt
            hosts:
              - my-cluster-es.humio.com
      ...
``` 

#### HumioCluster
```yaml
spec:
  hostname: "my-cluster.example.com"
  esHostname: "my-cluster-es.example.com"
  ingress:
    enabled: true
    controller: nginx
    # optional secret names. do not set these to the secrets created by the helm chart as they will be deleted when the
    # helm chart is removed
    # secretName: my-cluster-certificate
    # esSecretName: my-cluster-es-certificate
    annotations:
      use-http01-solver: "true"
      cert-manager.io/cluster-issuer: letsencrypt-prod
      kubernetes.io/ingress.class: nginx
```

### Bucket Storage GCP

#### Humio Helm Chart
```yaml
humio-core:
  bucketStorage:
    backend: gcp
  env:
    - name: GCP_STORAGE_BUCKET
      value: "example-cluster-storage"
    - name: GCP_STORAGE_ENCRYPTION_KEY
      value: "example-random-encryption-string"
    - name: LOCAL_STORAGE_PERCENTAGE
      value: "80"
    - name: LOCAL_STORAGE_MIN_AGE_DAYS
      value: "7"
```

#### HumioCluster

```yaml
spec:
  extraHumioVolumeMounts:
    - name: gcp-storage-account-json-file
      mountPath: /var/lib/humio/gcp-storage-account-json-file
      subPath: gcp-storage-account-json-file
      readOnly: true
  extraVolumes:
    - name: gcp-storage-account-json-file
      secret:
        secretName: gcp-storage-account-json-file
 environmentVariables:
    - name: GCP_STORAGE_ACCOUNT_JSON_FILE
      value: "/var/lib/humio/gcp-storage-account-json-file"
    - name: GCP_STORAGE_BUCKET
      value: "my-cluster-storage"
    - name: GCP_STORAGE_ENCRYPTION_KEY
      value: "my-encryption-key"
    - name: LOCAL_STORAGE_PERCENTAGE
      value: "80"
    - name: LOCAL_STORAGE_MIN_AGE_DAYS
      value: "7"
```

### Bucket Storage S3

The S3 bucket storage configuration is the same, with the exception to how the enivronment variables are set.

#### Humio Helm Chart
```yaml
humio-core:
  env:
    - name: S3_STORAGE_BUCKET
      value: "example-cluster-storage"
    - name: S3_STORAGE_REGION
      value: "us-west-2"
    - name: S3_STORAGE_ENCRYPTION_KEY
      value: "example-random-encryption-string"
    - name: LOCAL_STORAGE_PERCENTAGE
      value: "80"
    - name: LOCAL_STORAGE_MIN_AGE_DAYS
      value: "7"
    - name: S3_STORAGE_PREFERRED_COPY_SOURCE
      value: "true"
```

#### HumioCluster
```yaml
spec:
  environmentVariables:
    - name: S3_STORAGE_BUCKET
      value: "example-cluster-storage"
    - name: S3_STORAGE_REGION
      value: "us-west-2"
    - name: S3_STORAGE_ENCRYPTION_KEY
      value: "example-random-encryption-string"
    - name: LOCAL_STORAGE_PERCENTAGE
      value: "80"
    - name: LOCAL_STORAGE_MIN_AGE_DAYS
      value: "7"
    - name: S3_STORAGE_PREFERRED_COPY_SOURCE
      value: "true"
```

### Ephemeral Nodes and Cluster Identity

There are three main parts to using ephemeral nodes: setting the `USING_EPHEMERAL_DISKS` environment variable,
selecting zookeeper cluster identity and setting [s3](#bucket-storage-s3) or [gcp](#bucket-storage-gcp) bucket storage
(described in the separate linked section). In the Helm Chart, zookeeper identity is explicitly configured, but the 
operator now defaults to using zookeeper for identity regardless of the ephemeral disks setting.

#### Humio Helm Chart
```yaml
humio-core:
  clusterIdentity:
    type: zookeeper
  env:
    - name: ZOOKEEPER_URL_FOR_NODE_UUID
      value: "$(ZOOKEEPER_URL)"
    - name: USING_EPHEMERAL_DISKS
      value: "true"
```

#### HumioCluster
```yaml
spec:
  environmentVariables:
    - name: USING_EPHEMERAL_DISKS
      value: "true"
```

### Cache Configuration

Cache configuration is no longer supported in the Humio operator. It's recommended to use ephemeral nodes and bucket
storage instead.

#### Humio Helm Chart
```yaml
humio-core:
  cache:
    localVolume:
      enabled: true
```

#### HumioCluster
*Not supported*

### Authentication - OAuth Google

#### Humio Helm Chart
```yaml
humio-core:
  authenticationMethod: oauth
  oauthConfig:
    autoCreateUserOnSuccessfulLogin: true
    publicUrl: https://my-cluster.example.com
  env:
    - name: GOOGLE_OAUTH_CLIENT_SECRET
      valueFrom:
        secretKeyRef:
          name: humio-google-oauth-secret
          key: supersecretkey
    - name: GOOGLE_OAUTH_CLIENT_ID
      value: YOURCLIENTID
```

#### HumioCluster
```yaml
spec:
  environmentVariables:
    - name: AUTHENTICATION_METHOD
      value: oauth
    - name: AUTO_CREATE_USER_ON_SUCCESSFUL_LOGIN
      value: "true"
    - name: PUBLIC_URL
      value: https://my-cluster.example.com
    - name: GOOGLE_OAUTH_CLIENT_SECRET
      valueFrom:
        secretKeyRef:
          name: humio-google-oauth-secret
          key: supersecretkey
    - name: GOOGLE_OAUTH_CLIENT_ID
      value: YOURCLIENTID
```

### Authentication - OAuth Github

#### Humio Helm Chart
```yaml
humio-core:
  authenticationMethod: oauth
  env:
    - name: PUBLIC_URL
      value: https://my-cluster.example.com
    - name: GITHUB_OAUTH_CLIENT_ID
      value: client-id-from-github-oauth
    - name: GITHUB_OAUTH_CLIENT_SECRET
      value: client-secret-from-github-oauth
```

#### HumioCluster
```yaml
spec:
  environmentVariables:
    - name: AUTHENTICATION_METHOD
      value: oauth
    - name: AUTO_CREATE_USER_ON_SUCCESSFUL_LOGIN
      value: "true"
    - name: PUBLIC_URL
      value: https://my-cluster.example.com
    - name: GITHUB_OAUTH_CLIENT_ID
      value: client-id-from-github-oauth
    - name: GITHUB_OAUTH_CLIENT_SECRET
      value: client-secret-from-github-oauth
```

### Authentication - OAuth BitBucket

#### Humio Helm Chart
```yaml
humio-core:
  authenticationMethod: oauth
  env:
    - name: PUBLIC_URL
      value: https://my-cluster.example.com
    - name: BITBUCKET_OAUTH_CLIENT_ID
      value: client-id-from-bitbucket-oauth
    - name: BITBUCKET_OAUTH_CLIENT_SECRET
      value: client-secret-from-bitbucket-oauth
```

#### HumioCluster
```yaml
spec:
  environmentVariables:
    - name: AUTHENTICATION_METHOD
      value: oauth
    - name: AUTO_CREATE_USER_ON_SUCCESSFUL_LOGIN
      value: "true"
    - name: BITBUCKET_OAUTH_CLIENT_ID
      value: client-id-from-bitbucket-oauth
    - name: BITBUCKET_OAUTH_CLIENT_SECRET
      value: client-secret-from-bitbucket-oauth
```

### Authentication - SAML

When using SAML, it's necessary to follow the
[SAML instruction](https://docs.humio.com/cluster-management/security/saml) and once the IDP certificate is obtained,
you must create a secret containing that certificate using kubectl. The secret name is slightly different in the
`HumioCluster` vs the Helm Chart as the `HumioCluster` secret must be prefixed with the cluster name.

Creating the secret:

Helm Chart:
```bash
kubectl create secret generic idp-certificate --from-file=idp-certificate=./my-idp-certificate.pem -n <namespace>
```

HumioCluster:
```bash
kubectl create secret generic <cluster-name>-idp-certificate --from-file=idp-certificate.pem=./my-idp-certificate.pem -n <namespace>
```

#### Humio Helm Chart
```yaml
humio-core:
  authenticationMethod: saml
  samlConfig:
    publicUrl: https://my-cluster.example.com
    idpSignOnUrl: https://accounts.google.com/o/saml2/idp?idpid=idptoken
    idpEntityId: https://accounts.google.com/o/saml2/idp?idpid=idptoken
  env:
    - name: GOOGLE_OAUTH_CLIENT_SECRET
      valueFrom:
        secretKeyRef:
          name: humio-google-oauth-secret
          key: supersecretkey
    - name: GOOGLE_OAUTH_CLIENT_ID
      value: YOURCLIENTID
```

#### HumioCluster
```yaml
spec:
  environmentVariables:
    - name: AUTHENTICATION_METHOD
      value: saml
    - name: AUTO_CREATE_USER_ON_SUCCESSFUL_LOGIN
      value: "true"
    - name: PUBLIC_URL
      value: https://my-cluster.example.com
    - name: SAML_IDP_SIGN_ON_URL
      value: https://accounts.google.com/o/saml2/idp?idpid=idptoken
    - name: SAML_IDP_ENTITY_ID
      value: https://accounts.google.com/o/saml2/idp?idpid=idptoken
```

### Authentication - By Proxy

#### Humio Helm Chart
```yaml
humio-core:
  authenticationMethod: byproxy
  authByProxyConfig:
    headerName: name-of-http-header
```

#### HumioCluster
```yaml
spec:
  environmentVariables:
    - name: AUTHENTICATION_METHOD
      value: byproxy
    - name: AUTH_BY_PROXY_HEADER_NAME
      value: name-of-http-header
```

### Authentication - Single User

The Helm Chart generated a password for developer user when using single-user mode. The operator does not do this so you
must supply your own password. This can be done via a plain text environment variable or using a kuberenetes secret that
is referenced by an environment variable. If supplying a secret, you must populate this secret prior to creating the
`HumioCluster` resource otherwise the pods will fail to start.

#### Humio Helm Chart
```yaml
humio-core:
  authenticationMethod: single-user
```

#### HumioCluster
Note that the `AUTHENTICATION_METHOD` defaults to `single-user`.

By setting a password using an environment variable plain text value:
```yaml
spec:
  environmentVariables:
    - name: "SINGLE_USER_PASSWORD"
      value: "MyVeryS3cretPassword"
```

By setting a password using an environment variable secret reference:
```yaml
spec:
  environmentVariables:
    - name: "SINGLE_USER_PASSWORD"
      valueFrom:
        secretKeyRef:
          name: developer-user-password
          key: password
```

### Extra Kafka Configs

#### Humio Helm Chart
```yaml
humio-core:
  extraKafkaConfigs: "security.protocol=SSL"
```

#### HumioCluster

```yaml
spec:
  extraKafkaConfigs: "security.protocol=SSL"
```

### Prometheus

The Humio Helm chart supported setting the `prometheus.io/port` and `prometheus.io/scrape` annotations on the Humio
pods. The Operator no longer supports this.

#### Humio Helm Chart
```yaml
humio-core:
  prometheus:
    enabled: true
```

#### HumioCluster

*Not supported*

### Pod Security Context

#### Humio Helm Chart
```yaml
humio-core:
  podSecurityContext:
    runAsUser: 1000
    runAsGroup: 3000
    fsGroup: 2000
```

#### HumioCluster

```yaml
spec:
  podSecurityContext:
    runAsUser: 1000
    runAsGroup: 3000
    fsGroup: 2000
```

### Container Security Context

#### Humio Helm Chart
```yaml
humio-core:
  containerSecurityContext:
    capabilities:
      add: ["SYS_NICE"]
```

#### HumioCluster

```yaml
spec:
  containerSecurityContext:
    capabilities:
      add: ["SYS_NICE"]
```

### Initial Partitions

The Helm Chart accepted both `ingest.initialPartitionsPerNode` and `storage.initialPartitionsPerNode`. The Operator no
longer supports the per-node setting, so it's up to the administrator to set the initial partitions such that they are
divisible by the node count.

#### Humio Helm Chart
```yaml
humio-core:
  ingest:
    initialPartitionsPerNode: 4
  storage:
    initialPartitionsPerNode: 4
``` 

#### HumioCluster

Assuming a three node cluster:
```yaml
spec:
  environmentVariables:
    - name: "INGEST_QUEUE_INITIAL_PARTITIONS"
      value: "12"
    - name: "DEFAULT_PARTITION_COUNT"
      value: "12"
```

### Log Storage

The Helm Chart supports the use of separate storage for logs. This is not supported in the Operator and instead defaults
to running Humio with the environment variable `LOG4J_CONFIGURATION=log4j2-stdout-json.xml` which outputs to stdout in 
json format.

#### Humio Helm Chart
```yaml
humio-core:
  jvm:
    xss: 2m
    xms: 256m
    xmx: 1536m
    maxDirectMemorySize: 1536m
    extraArgs: "-XX:+UseParallelOldGC"
``` 

#### HumioCluster

*Not supported*