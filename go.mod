module github.com/humio/humio-operator

go 1.15

require (
	github.com/Masterminds/semver v1.5.0
	github.com/go-logr/logr v0.3.0
	github.com/go-logr/zapr v0.3.0
	github.com/google/martian v2.1.0+incompatible
	github.com/humio/cli v0.28.5
	github.com/jetstack/cert-manager v1.3.1
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/openshift/api v3.9.0+incompatible
	github.com/prometheus/client_golang v1.7.1
	github.com/shurcooL/graphql v0.0.0-20200928012149-18c5c3165e3a
	go.uber.org/zap v1.15.0
	gopkg.in/square/go-jose.v2 v2.3.1
	k8s.io/api v0.20.1
	k8s.io/apimachinery v0.20.1
	k8s.io/client-go v0.20.1
	sigs.k8s.io/controller-runtime v0.7.2
)
