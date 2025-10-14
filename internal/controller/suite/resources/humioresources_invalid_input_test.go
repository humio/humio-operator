package resources

import (
	"context"
	"fmt"
	"strings"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humiov1beta1 "github.com/humio/humio-operator/api/v1beta1"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	defaultNamespace string = "default"
)

var _ = Describe("HumioViewTokenCRD", Label("envtest", "dummy", "real"), func() {
	DescribeTable("invalid inputs should be rejected by the constraints in the CRD/API",
		func(expectedOutput string, invalidInput humiov1alpha1.HumioViewToken) {
			err := k8sClient.Create(context.TODO(), &invalidInput)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expectedOutput))
		},
		// Each Entry has a name and the parameters for the function above
		Entry("name not specified", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					//Name:               "",
					TokenSecretName: "test-secret",
					Permissions:     []string{"ReadAccess"},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("name empty value", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("name too long", "spec.name: Too long:", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               strings.Repeat("A", 255),
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("viewNames not specified", "spec.viewNames: Required value", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
				},
				//ViewNames:          []string{""},
			},
		}),
		Entry("viewNames value not set", "spec.viewNames: Invalid value", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
				},
				ViewNames: []string{""},
			},
		}),
		Entry("viewNames name too long", "spec.viewNames: Invalid value", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
				},
				ViewNames: []string{strings.Repeat("A", 255)},
			},
		}),
		Entry("Permissions not set", "spec.permissions: Required value", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					// Permissions: []string{"ReadAccess"},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("Permissions entry is empty", "spec.permissions: Invalid value: \"array\": permissions: each item must be 1-253", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{""},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("Permissions entry too long", "spec.permissions: Invalid value: \"array\": permissions: each item must be 1-253", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{strings.Repeat("A", 255)},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("Permissions are too many", "spec.permissions: Too many", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        strings.Split(strings.Repeat("validName,", 100)+"validName", ","),
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("IPFilterName too long", "spec.ipFilterName: Too long:", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
					IPFilterName:       strings.Repeat("A", 255),
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("TokenSecretName not set", "spec.tokenSecretName: Invalid value: \"\": spec.tokenSecretName in body should be at least 1 chars long", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					//TokenSecretName:    "test-secret",
					Permissions: []string{"ReadAccess"},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("TokenSecretName set empty", "spec.tokenSecretName: Invalid value: \"\": spec.tokenSecretName in body should be at least 1 chars long", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "",
					Permissions:        []string{"ReadAccess"},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("TokenSecretName too long", "spec.tokenSecretName: Too long", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    strings.Repeat("A", 255),
					Permissions:        []string{"ReadAccess"},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("TokenSecretName invalid char", "spec.tokenSecretName: Invalid value", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test.&",
					Permissions:        []string{"ReadAccess"},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("TokenSecretLabel key too long", "spec.tokenSecretLabels: Invalid value: \"object\": tokenSecretLabels keys must be 1-63 characters", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
					TokenSecretLabels:  map[string]string{strings.Repeat("A", 255): ""},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("TokenSecretLabel value too long", "spec.tokenSecretLabels: Invalid value: \"object\": tokenSecretLabels values must be 1-63 characters", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
					TokenSecretLabels:  map[string]string{"key": strings.Repeat("A", 255)},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("TokenSecretLabel too many keys", "spec.tokenSecretLabels: Too many: 64: must have at most 63 items", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
					TokenSecretLabels: func() map[string]string {
						m := make(map[string]string)
						for i := range 64 {
							m[fmt.Sprintf("validName%d", i)] = strings.Repeat("A", 10)
						}
						return m
					}(),
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("TokenSecretAnnotations key too long", "spec.tokenSecretAnnotations: Invalid value: \"object\": tokenSecretAnnotations keys must be 1-63 characters", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName:     "test-cluster",
					Name:                   "test-name",
					TokenSecretName:        "test-secret",
					Permissions:            []string{"ReadAccess"},
					TokenSecretLabels:      map[string]string{"key": "value"},
					TokenSecretAnnotations: map[string]string{strings.Repeat("A", 255): ""},
				},
				ViewNames: []string{"test-view"},
			},
		}),
		Entry("TokenSecretAnnotations too many keys", "spec.tokenSecretAnnotations: Too many: 64: must have at most 63 items", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
					TokenSecretLabels:  map[string]string{"key": "value"},
					TokenSecretAnnotations: func() map[string]string {
						m := make(map[string]string)
						for i := range 64 {
							m[fmt.Sprintf("validName%d", i)] = strings.Repeat("A", 10)
						}
						return m
					}(),
				},
				ViewNames: []string{"test-view"},
			},
		}),
	)
})

var _ = Describe("HumioSystemTokenCRD", Label("envtest", "dummy", "real"), func() {
	DescribeTable("invalid inputs should be rejected by the constraints in the CRD/API",
		func(expectedOutput string, invalidInput humiov1alpha1.HumioSystemToken) {
			err := k8sClient.Create(context.TODO(), &invalidInput)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expectedOutput))
		},
		// Each Entry has a name and the parameters for the function above
		Entry("name not specified", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					//Name:               "",
					TokenSecretName: "test-secret",
					Permissions:     []string{"ReadAccess"},
				},
			},
		}),
		Entry("name empty value", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
				},
			},
		}),
		Entry("name too long", "spec.name: Too long:", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               strings.Repeat("A", 255),
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
				},
			},
		}),
		Entry("Permissions not set", "spec.permissions: Required value", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					// Permissions: []string{"ReadAccess"},
				},
			},
		}),
		Entry("Permissions entry is empty", "spec.permissions: Invalid value: \"array\": permissions: each item must be 1-253", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{""},
				},
			},
		}),
		Entry("Permissions entry too long", "spec.permissions: Invalid value: \"array\": permissions: each item must be 1-253", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{strings.Repeat("A", 255)},
				},
			},
		}),
		Entry("Permissions are too many", "spec.permissions: Too many", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        strings.Split(strings.Repeat("validName,", 100)+"validName", ","),
				},
			},
		}),
		Entry("IPFilterName too long", "spec.ipFilterName: Too long:", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
					IPFilterName:       strings.Repeat("A", 255),
				},
			},
		}),
		Entry("TokenSecretName not set", "spec.tokenSecretName: Invalid value: \"\": spec.tokenSecretName in body should be at least 1 chars long", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					//TokenSecretName:    "test-secret",
					Permissions: []string{"ReadAccess"},
				},
			},
		}),
		Entry("TokenSecretName set empty", "spec.tokenSecretName: Invalid value: \"\": spec.tokenSecretName in body should be at least 1 chars long", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "",
					Permissions:        []string{"ReadAccess"},
				},
			},
		}),
		Entry("TokenSecretName too long", "spec.tokenSecretName: Too long", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    strings.Repeat("A", 255),
					Permissions:        []string{"ReadAccess"},
				},
			},
		}),
		Entry("TokenSecretName invalid char", "spec.tokenSecretName: Invalid value", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test.&",
					Permissions:        []string{"ReadAccess"},
				},
			},
		}),
		Entry("TokenSecretLabel key too long", "spec.tokenSecretLabels: Invalid value: \"object\": tokenSecretLabels keys must be 1-63 characters", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
					TokenSecretLabels:  map[string]string{strings.Repeat("A", 255): ""},
				},
			},
		}),
		Entry("TokenSecretLabel value too long", "spec.tokenSecretLabels: Invalid value: \"object\": tokenSecretLabels values must be 1-63 characters", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
					TokenSecretLabels:  map[string]string{"key": strings.Repeat("A", 255)},
				},
			},
		}),
		Entry("TokenSecretLabel too many keys", "spec.tokenSecretLabels: Too many: 64: must have at most 63 items", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
					TokenSecretLabels: func() map[string]string {
						m := make(map[string]string)
						for i := range 64 {
							m[fmt.Sprintf("validName%d", i)] = strings.Repeat("A", 10)
						}
						return m
					}(),
				},
			},
		}),
		Entry("TokenSecretAnnotations key too long", "spec.tokenSecretAnnotations: Invalid value: \"object\": tokenSecretAnnotations keys must be 1-63 characters", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName:     "test-cluster",
					Name:                   "test-name",
					TokenSecretName:        "test-secret",
					Permissions:            []string{"ReadAccess"},
					TokenSecretLabels:      map[string]string{"key": "value"},
					TokenSecretAnnotations: map[string]string{strings.Repeat("A", 255): ""},
				},
			},
		}),
		Entry("TokenSecretAnnotations too many keys", "spec.tokenSecretAnnotations: Too many: 64: must have at most 63 items", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ReadAccess"},
					TokenSecretLabels:  map[string]string{"key": "value"},
					TokenSecretAnnotations: func() map[string]string {
						m := make(map[string]string)
						for i := range 64 {
							m[fmt.Sprintf("validName%d", i)] = strings.Repeat("A", 10)
						}
						return m
					}(),
				},
			},
		}),
	)
})

var _ = Describe("HumioOrganizationTokenCRD", Label("envtest", "dummy", "real"), func() {
	DescribeTable("invalid inputs should be rejected by the constraints in the CRD/API",
		func(expectedOutput string, invalidInput humiov1alpha1.HumioOrganizationToken) {
			err := k8sClient.Create(context.TODO(), &invalidInput)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expectedOutput))
		},
		// Each Entry has a name and the parameters for the function above
		Entry("name not specified", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					//Name:               "",
					TokenSecretName: "test-secret",
					Permissions:     []string{"ManageUsers"},
				},
			},
		}),
		Entry("name empty value", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ManageUsers"},
				},
			},
		}),
		Entry("name too long", "spec.name: Too long:", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               strings.Repeat("A", 255),
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ManageUsers"},
				},
			},
		}),
		Entry("Permissions not set", "spec.permissions: Required value", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					// Permissions: []string{"ManageUsers"},
				},
			},
		}),
		Entry("Permissions entry is empty", "spec.permissions: Invalid value: \"array\": permissions: each item must be 1-253", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{""},
				},
			},
		}),
		Entry("Permissions entry too long", "spec.permissions: Invalid value: \"array\": permissions: each item must be 1-253", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{strings.Repeat("A", 255)},
				},
			},
		}),
		Entry("Permissions are too many", "spec.permissions: Too many", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        strings.Split(strings.Repeat("validName,", 100)+"validName", ","),
				},
			},
		}),
		Entry("IPFilterName too long", "spec.ipFilterName: Too long:", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ManageUsers"},
					IPFilterName:       strings.Repeat("A", 255),
				},
			},
		}),
		Entry("TokenSecretName not set", "spec.tokenSecretName: Invalid value: \"\": spec.tokenSecretName in body should be at least 1 chars long", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					//TokenSecretName:    "test-secret",
					Permissions: []string{"ManageUsers"},
				},
			},
		}),
		Entry("TokenSecretName set empty", "spec.tokenSecretName: Invalid value: \"\": spec.tokenSecretName in body should be at least 1 chars long", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "",
					Permissions:        []string{"ManageUsers"},
				},
			},
		}),
		Entry("TokenSecretName too long", "spec.tokenSecretName: Too long", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    strings.Repeat("A", 255),
					Permissions:        []string{"ManageUsers"},
				},
			},
		}),
		Entry("TokenSecretName invalid char", "spec.tokenSecretName: Invalid value", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test.&",
					Permissions:        []string{"ManageUsers"},
				},
			},
		}),
		Entry("TokenSecretLabel key too long", "spec.tokenSecretLabels: Invalid value: \"object\": tokenSecretLabels keys must be 1-63 characters", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ManageUsers"},
					TokenSecretLabels:  map[string]string{strings.Repeat("A", 255): ""},
				},
			},
		}),
		Entry("TokenSecretLabel value too long", "spec.tokenSecretLabels: Invalid value: \"object\": tokenSecretLabels values must be 1-63 characters", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ManageUsers"},
					TokenSecretLabels:  map[string]string{"key": strings.Repeat("A", 255)},
				},
			},
		}),
		Entry("TokenSecretLabel too many keys", "spec.tokenSecretLabels: Too many: 64: must have at most 63 items", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ManageUsers"},
					TokenSecretLabels: func() map[string]string {
						m := make(map[string]string)
						for i := range 64 {
							m[fmt.Sprintf("validName%d", i)] = strings.Repeat("A", 10)
						}
						return m
					}(),
				},
			},
		}),
		Entry("TokenSecretAnnotations key too long", "spec.tokenSecretAnnotations: Invalid value: \"object\": tokenSecretAnnotations keys must be 1-63 characters", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName:     "test-cluster",
					Name:                   "test-name",
					TokenSecretName:        "test-secret",
					Permissions:            []string{"ManageUsers"},
					TokenSecretLabels:      map[string]string{"key": "value"},
					TokenSecretAnnotations: map[string]string{strings.Repeat("A", 255): ""},
				},
			},
		}),
		Entry("TokenSecretAnnotations too many keys", "spec.tokenSecretAnnotations: Too many: 64: must have at most 63 items", humiov1alpha1.HumioOrganizationToken{
			ObjectMeta: metav1.ObjectMeta{Name: "organization-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: "test-cluster",
					Name:               "test-name",
					TokenSecretName:    "test-secret",
					Permissions:        []string{"ManageUsers"},
					TokenSecretLabels:  map[string]string{"key": "value"},
					TokenSecretAnnotations: func() map[string]string {
						m := make(map[string]string)
						for i := range 64 {
							m[fmt.Sprintf("validName%d", i)] = strings.Repeat("A", 10)
						}
						return m
					}(),
				},
			},
		}),
	)
})

var _ = Describe("HumioIPFilterCRD", Label("envtest", "dummy", "real"), func() {
	DescribeTable("invalid inputs should be rejected by the constraints in the CRD/API",
		func(expectedOutput string, invalidInput humiov1alpha1.HumioIPFilter) {
			err := k8sClient.Create(context.TODO(), &invalidInput)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expectedOutput))
		},
		// Each Entry has a name and the parameters for the function above
		Entry("name not specified", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioIPFilter{
			ObjectMeta: metav1.ObjectMeta{Name: "ip-filter", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioIPFilterSpec{
				ManagedClusterName: "test-cluster",
				//Name:               "test-ip-filter",
				IPFilter: []humiov1alpha1.FirewallRule{
					{Action: "allow", Address: "127.0.0.1"},
					{Action: "allow", Address: "10.0.0.0/8"},
					{Action: "allow", Address: "all"}},
			},
		}),
		Entry("name empty value", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioIPFilter{
			ObjectMeta: metav1.ObjectMeta{Name: "ip-filter", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioIPFilterSpec{
				ManagedClusterName: "test-cluster",
				Name:               "",
				IPFilter: []humiov1alpha1.FirewallRule{
					{Action: "allow", Address: "127.0.0.1"},
					{Action: "allow", Address: "10.0.0.0/8"},
					{Action: "allow", Address: "all"}},
			},
		}),
		Entry("name too long", "spec.name: Too long:", humiov1alpha1.HumioIPFilter{
			ObjectMeta: metav1.ObjectMeta{Name: "ip-filter", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioIPFilterSpec{
				ManagedClusterName: "test-cluster",
				Name:               strings.Repeat("A", 255),
				IPFilter: []humiov1alpha1.FirewallRule{
					{Action: "allow", Address: "127.0.0.1"},
					{Action: "allow", Address: "10.0.0.0/8"},
					{Action: "allow", Address: "all"}},
			},
		}),
		Entry("ipFilter not specified", "spec.ipFilter: Required value", humiov1alpha1.HumioIPFilter{
			ObjectMeta: metav1.ObjectMeta{Name: "ip-filter", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioIPFilterSpec{
				ManagedClusterName: "test-cluster",
				Name:               "ip-filter",
				// IPFilter: []humiov1alpha1.FirewallRule{
				// 		{Action: "allow", Address: "127.0.0.1"},
				// 		{Action: "allow", Address: "10.0.0.0/8"},
				// 		{Action: "allow", Address: "all"}},
			},
		}),
		Entry("ipFilter empty list", "spec.ipFilter: Invalid value: 0: spec.ipFilter in body should have at least 1 items", humiov1alpha1.HumioIPFilter{
			ObjectMeta: metav1.ObjectMeta{Name: "ip-filter", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioIPFilterSpec{
				ManagedClusterName: "test-cluster",
				Name:               "ip-filter",
				IPFilter:           []humiov1alpha1.FirewallRule{},
			},
		}),
		Entry("ipFilter empty address", "address: Invalid value", humiov1alpha1.HumioIPFilter{
			ObjectMeta: metav1.ObjectMeta{Name: "ip-filter", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioIPFilterSpec{
				ManagedClusterName: "test-cluster",
				Name:               "ip-filter",
				IPFilter: []humiov1alpha1.FirewallRule{
					{Action: "allow", Address: ""},
					{Action: "allow", Address: "10.0.0.0/8"},
					{Action: "allow", Address: "all"}},
			},
		}),
		Entry("ipFilter invalid address", "address: Invalid value", humiov1alpha1.HumioIPFilter{
			ObjectMeta: metav1.ObjectMeta{Name: "ip-filter", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioIPFilterSpec{
				ManagedClusterName: "test-cluster",
				Name:               "ip-filter",
				IPFilter: []humiov1alpha1.FirewallRule{
					{Action: "allow", Address: "0.0.0"},
					{Action: "allow", Address: "10.0.0.0/8"},
					{Action: "allow", Address: "all"}},
			},
		}),
		Entry("ipFilter empty action", "action: Unsupported value", humiov1alpha1.HumioIPFilter{
			ObjectMeta: metav1.ObjectMeta{Name: "ip-filter", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioIPFilterSpec{
				ManagedClusterName: "test-cluster",
				Name:               "ip-filter",
				IPFilter: []humiov1alpha1.FirewallRule{
					{Action: "", Address: "0.0.0.0/0"},
					{Action: "allow", Address: "10.0.0.0/8"},
					{Action: "allow", Address: "all"}},
			},
		}),
		Entry("ipFilter unsupported action", "action: Unsupported value", humiov1alpha1.HumioIPFilter{
			ObjectMeta: metav1.ObjectMeta{Name: "ip-filter", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioIPFilterSpec{
				ManagedClusterName: "test-cluster",
				Name:               "ip-filter",
				IPFilter: []humiov1alpha1.FirewallRule{
					{Action: "reject", Address: "0.0.0"},
					{Action: "allow", Address: "10.0.0.0/8"},
					{Action: "allow", Address: "all"}},
			},
		}),
	)
})

var _ = Describe("HumioScheduledSearchv1beta1", Label("envtest", "dummy", "real"), func() {
	DescribeTable("invalid inputs should be rejected by the constraints in the CRD/API",
		func(expectedOutput string, invalidInput humiov1beta1.HumioScheduledSearch) {
			err := k8sClient.Create(context.TODO(), &invalidInput)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expectedOutput))
		},
		// Each Entry has a name and the parameters for the function above
		Entry("name not specified", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName: "test-cluster",
				//Name:                        "",
				ViewName:              "humio",
				QueryString:           "*",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * * * *",
				TimeZone: "UTC",
				//BackfillLimit:               5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{"test-action"},
				Labels:  []string{"test-label"},
			},
		}),
		Entry("name empty value", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:    "test-cluster",
				Name:                  "",
				ViewName:              "humio",
				QueryString:           "*",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * * * *",
				TimeZone: "UTC",
				//BackfillLimit:               5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{"test-action"},
				Labels:  []string{"test-label"},
			},
		}),
		Entry("name too long", "spec.name: Too long: may not be more than 253 bytes", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:    "test-cluster",
				Name:                  strings.Repeat("A", 255),
				ViewName:              "humio",
				QueryString:           "*",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * * * *",
				TimeZone: "UTC",
				//BackfillLimit:               5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{"test-action"},
				Labels:  []string{"test-label"},
			},
		}),
		Entry("viewName not specified", "spec.viewName: Invalid value: \"\": spec.viewName in body should be at least 1 chars long", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName: "test-cluster",
				Name:               "name",
				//ViewName:                    "humio",
				QueryString:           "*",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * * * *",
				TimeZone: "UTC",
				//BackfillLimit: 5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{"test-action"},
				Labels:  []string{"test-label"},
			},
		}),
		Entry("viewName empty value", "spec.viewName: Invalid value: \"\": spec.viewName in body should be at least 1 chars long", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:    "test-cluster",
				Name:                  "name",
				ViewName:              "",
				QueryString:           "*",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * * * *",
				TimeZone: "UTC",
				//BackfillLimit: 5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{"test-action"},
				Labels:  []string{"test-label"},
			},
		}),
		Entry("viewName too long", "spec.viewName: Too long: may not be more than 253 bytes", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:    "test-cluster",
				Name:                  "name",
				ViewName:              strings.Repeat("A", 255),
				QueryString:           "*",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * * * *",
				TimeZone: "UTC",
				//BackfillLimit: 5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{"test-action"},
				Labels:  []string{"test-label"},
			},
		}),
		Entry("queryString not specified", "spec.queryString: Invalid value: \"\": spec.queryString in body should be at least 1 chars long", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName: "test-cluster",
				Name:               "name",
				ViewName:           "humio",
				//QueryString:                 "*",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * * * *",
				TimeZone: "UTC",
				//BackfillLimit: 5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{"test-action"},
				Labels:  []string{"test-label"},
			},
		}),
		Entry("queryString empty value", "spec.queryString: Invalid value: \"\": spec.queryString in body should be at least 1 chars long", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:    "test-cluster",
				Name:                  "name",
				ViewName:              "humio",
				QueryString:           "",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * * * *",
				TimeZone: "UTC",
				//BackfillLimit: 5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{"test-action"},
				Labels:  []string{"test-label"},
			},
		}),
		Entry("maxWaitTimeSeconds empty value", "maxWaitTimeSeconds is required when QueryTimestampType is IngestTimestamp", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName: "test-cluster",
				Name:               "name",
				ViewName:           "humio",
				QueryString:        "*",
				Description:        "test description",
				//MaxWaitTimeSeconds:          60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * * * *",
				TimeZone: "UTC",
				//BackfillLimit: 5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{"test-action"},
				Labels:  []string{"test-label"},
			},
		}),
		Entry("searchIntervalOffsetSeconds present", "searchIntervalOffsetSeconds is accepted only when queryTimestampType is set to 'EventTimestamp'", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:          "test-cluster",
				Name:                        "name",
				ViewName:                    "humio",
				QueryString:                 "*",
				Description:                 "test description",
				MaxWaitTimeSeconds:          60,
				QueryTimestampType:          humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds:       120,
				SearchIntervalOffsetSeconds: helpers.Int64Ptr(int64(60)), // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule:                    "30 * * * *",
				TimeZone:                    "UTC",
				//BackfillLimit:               5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{"test-action"},
				Labels:  []string{"test-label"},
			},
		}),
		Entry("schedule invalid", "schedule must be a valid cron expression with 5 fields", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:    "test-cluster",
				Name:                  "name",
				ViewName:              "humio",
				QueryString:           "*",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * *",
				TimeZone: "UTC",
				//BackfillLimit: 5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{"test-action"},
				Labels:  []string{"test-label"},
			},
		}),
		Entry("timezone invalid", "timeZone must be 'UTC' or a UTC offset like 'UTC-01'", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:    "test-cluster",
				Name:                  "name",
				ViewName:              "humio",
				QueryString:           "*",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * * * *",
				TimeZone: "UTC+A",
				//BackfillLimit: 5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{"test-action"},
				Labels:  []string{"test-label"},
			},
		}),
		Entry("backfillLimit set wrongfully", "backfillLimit is accepted only when queryTimestampType is set to 'EventTimestamp'", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:    "test-cluster",
				Name:                  "name",
				ViewName:              "humio",
				QueryString:           "*",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule:      "30 * * * *",
				TimeZone:      "UTC+01",
				BackfillLimit: helpers.IntPtr(int(5)), // Only allowed when queryTimestamp is EventTimestamp
				Enabled:       true,
				Actions:       []string{"test-action"},
				Labels:        []string{"test-label"},
			},
		}),
		Entry("actions not set", "spec.actions: Required value", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:    "test-cluster",
				Name:                  "name",
				ViewName:              "humio",
				QueryString:           "*",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * * * *",
				TimeZone: "UTC+01",
				//BackfillLimit: 5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				//Actions:       []string{"test-action"},
				Labels: []string{"test-label"},
			},
		}),
		Entry("actions set empty", "spec.actions: Invalid value", humiov1beta1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1beta1.HumioScheduledSearchSpec{
				ManagedClusterName:    "test-cluster",
				Name:                  "name",
				ViewName:              "humio",
				QueryString:           "*",
				Description:           "test description",
				MaxWaitTimeSeconds:    60,
				QueryTimestampType:    humiographql.QueryTimestampTypeIngesttimestamp,
				SearchIntervalSeconds: 120,
				//SearchIntervalOffsetSeconds: 60, // Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
				Schedule: "30 * * * *",
				TimeZone: "UTC+01",
				//BackfillLimit: 5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled: true,
				Actions: []string{""},
				Labels:  []string{"test-label"},
			},
		}),
	)
})

// since HumioScheduledSearchv1alpha1 automatically migrated to HumioScheduledSearchv1beta1 we expected the validation applied to be from humiov1beta1.HumioScheduledSearch
var _ = Describe("HumioScheduledSearchv1alpha1", Label("envtest", "dummy", "real"), func() {
	processID := GinkgoParallelProcess()
	DescribeTable("invalid inputs should be rejected by the constraints in the CRD/API",
		func(expectedOutput string, invalidInput humiov1alpha1.HumioScheduledSearch) {
			err := k8sClient.Create(context.TODO(), &invalidInput)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expectedOutput))
		},
		// Each Entry has a name and the parameters for the function above
		Entry("name not specified", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioScheduledSearchSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				//Name:               "test-1",
				ViewName:      "humio",
				QueryString:   "*",
				Description:   "test description",
				QueryStart:    "1h",
				QueryEnd:      "now",
				Schedule:      "30 * * * *",
				TimeZone:      "UTC",
				BackfillLimit: 5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled:       true,
				Actions:       []string{"test-action"},
				Labels:        []string{"test-label"},
			},
		}),
		Entry("name empty value", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioScheduledSearchSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				Name:               "",
				ViewName:           "humio",
				QueryString:        "*",
				Description:        "test description",
				QueryStart:         "1h",
				QueryEnd:           "now",
				Schedule:           "30 * * * *",
				TimeZone:           "UTC",
				BackfillLimit:      5, // Only allowed when queryTimestamp is EventTimestamp, default for humiov1alpha1.HumioScheduledSearch
				Enabled:            true,
				Actions:            []string{"test-action"},
				Labels:             []string{"test-label"},
			},
		}),
		Entry("viewName not specified", "spec.viewName: Invalid value: \"\": spec.viewName in body should be at least 1 chars long", humiov1alpha1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioScheduledSearchSpec{
				ManagedClusterName: "test-cluster",
				Name:               "name",
				//ViewName:                    "humio",
				QueryString:   "*",
				Description:   "test description",
				QueryStart:    "1h",
				QueryEnd:      "now",
				Schedule:      "30 * * * *",
				TimeZone:      "UTC",
				BackfillLimit: 5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled:       true,
				Actions:       []string{"test-action"},
				Labels:        []string{"test-label"},
			},
		}),
		Entry("viewName empty value", "spec.viewName: Invalid value: \"\": spec.viewName in body should be at least 1 chars long", humiov1alpha1.HumioScheduledSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "test-humioscheduledsearch", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioScheduledSearchSpec{
				ManagedClusterName: "test-cluster",
				Name:               "name",
				ViewName:           "",
				QueryString:        "*",
				Description:        "test description",
				QueryStart:         "1h",
				QueryEnd:           "now",
				Schedule:           "30 * * * *",
				TimeZone:           "UTC",
				BackfillLimit:      5, // Only allowed when queryTimestamp is EventTimestamp
				Enabled:            true,
				Actions:            []string{"test-action"},
				Labels:             []string{"test-label"},
			},
		}),
	)
})
