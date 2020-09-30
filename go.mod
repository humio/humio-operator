module github.com/humio/humio-operator

go 1.15

require (
	github.com/go-logr/logr v0.1.0
	github.com/google/martian v2.1.0+incompatible
	github.com/humio/cli v0.27.0
	github.com/jetstack/cert-manager v0.16.1
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/openshift/api v3.9.0+incompatible
	github.com/prometheus/client_golang v1.0.0
	github.com/shurcooL/graphql v0.0.0-20181231061246-d48a9a75455f
	go.uber.org/zap v1.10.0
	k8s.io/api v0.18.6
	k8s.io/apimachinery v0.18.6
	k8s.io/client-go v0.18.6
	sigs.k8s.io/controller-runtime v0.6.2
)
