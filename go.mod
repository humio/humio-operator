module github.com/humio/humio-operator

go 1.13

require (
	github.com/dgrijalva/jwt-go v3.2.0+incompatible
	github.com/go-logr/logr v0.1.0
	github.com/humio/cli v0.23.1-0.20191104141832-29ce3f399518
	github.com/onsi/ginkgo v1.8.0
	github.com/onsi/gomega v1.5.0
	github.com/prometheus/client_golang v1.2.1
	github.com/prometheus/common v0.7.0
	github.com/shurcooL/graphql v0.0.0-20181231061246-d48a9a75455f
	golang.org/x/oauth2 v0.0.0-20190604053449-0f29369cfe45
	k8s.io/api v0.0.0-20190918195907-bd6ac527cfd2
	k8s.io/apimachinery v0.0.0-20190817020851-f2f3a405f61d
	k8s.io/client-go v0.0.0-20190918200256-06eb1244587a
	sigs.k8s.io/controller-runtime v0.3.0
)
