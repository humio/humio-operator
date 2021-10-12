module github.com/humio/humio-operator

go 1.16

require (
	github.com/Masterminds/semver v1.5.0
	github.com/go-logr/logr v0.4.0
	github.com/go-logr/zapr v0.4.0
	github.com/google/go-cmp v0.5.6
	github.com/google/martian v2.1.0+incompatible
	github.com/humio/cli v0.28.9
	github.com/jetstack/cert-manager v1.5.3
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.16.0
	github.com/openshift/api v3.9.0+incompatible
	github.com/prometheus/client_golang v1.11.0
	github.com/shurcooL/graphql v0.0.0-20200928012149-18c5c3165e3a
	go.uber.org/zap v1.19.1
	gopkg.in/square/go-jose.v2 v2.6.0
	k8s.io/api v0.21.3
	k8s.io/apimachinery v0.21.3
	k8s.io/client-go v0.21.3
	sigs.k8s.io/controller-runtime v0.9.2
)
