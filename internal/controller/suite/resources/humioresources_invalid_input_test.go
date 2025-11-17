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

var _ = Describe("HumioPackageRegistry", Label("envtest", "dummy", "real"), func() {
	processID := GinkgoParallelProcess()
	DescribeTable("invalid inputs should be rejected by the constraints in the CRD/API",
		func(expectedOutput string, invalidInput humiov1alpha1.HumioPackageRegistry) {
			err := k8sClient.Create(context.TODO(), &invalidInput)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expectedOutput))
		},
		// Each Entry has a name and the parameters for the function above
		Entry("registryType not specified", "Unsupported value: \"\": supported values: \"marketplace\", \"gitlab\", \"github\", \"artifactory\", \"aws\", \"gcloud\"", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				//RegistryType: "marketplace",
				Enabled: true,
				Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
					URL: "https://packages.humio.com",
				},
			},
		}),
		Entry("registryType specified but empty", "Unsupported value: \"\": supported values: \"marketplace\", \"gitlab\", \"github\", \"artifactory\", \"aws\", \"gcloud\"", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "",
				Enabled:            true,
				Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
					URL: "https://packages.humio.com",
				},
			},
		}),
		Entry("registryType specified but unsupported value", "Unsupported value: \"badvalue\": supported values: \"marketplace\", \"gitlab\", \"github\", \"artifactory\", \"aws\", \"gcloud\"", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "badvalue",
				Enabled:            true,
				Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
					URL: "https://packages.humio.com",
				},
			},
		}),
		Entry("displayName too long", "spec.displayName: Too long: may not be more than 253 bytes", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        strings.Repeat("A", 255),
				RegistryType:       "marketplace",
				Enabled:            true,
				Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
					URL: "https://packages.humio.com",
				},
			},
		}),
		Entry("type marketplace but no config", "Invalid value: \"object\": marketplace is required when registryType is 'marketplace'", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "marketplace",
				Enabled:            true,
				// Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
				// 	URL: "https://packages.humio.com",
				// },
			},
		}),
		Entry("type marketplace but invalid config missing URL", "is invalid: spec.marketplace.url: Invalid value: \"\"", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "marketplace",
				Enabled:            true,
				Marketplace:        &humiov1alpha1.RegistryConnectionMarketplace{
					//URL: "https://packages.humio.com",
				},
			},
		}),
		Entry("type marketplace but invalid config empty URL", "spec.marketplace.url: Invalid value: \"\": spec.marketplace.url in body should match '^https://.*'", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "marketplace",
				Enabled:            true,
				Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
					URL: "",
				},
			},
		}),
		Entry("type marketplace but invalid config", "spec.marketplace.url: Too long: may not be more than 253 bytes", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "marketplace",
				Enabled:            true,
				Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
					URL: "https://" + strings.Repeat("A", 255),
				},
			},
		}),
		// GitLab tests
		Entry("type gitlab but no config", "Invalid value: \"object\": gitlab is required when registryType is 'gitlab'", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "gitlab",
				Enabled:            true,
				// Gitlab: &humiov1alpha1.RegistryConnectionGitlab{
				//     URL: "https://gitlab.com",
				// },
			},
		}),
		Entry("type gitlab but invalid config", "spec.gitlab.url: Invalid value: \"\": spec.gitlab.url in body should match '^https://.*'",
			humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
				Spec: humiov1alpha1.HumioPackageRegistrySpec{
					ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
					DisplayName:        "test",
					RegistryType:       "gitlab",
					Enabled:            true,
					Gitlab: &humiov1alpha1.RegistryConnectionGitlab{
						URL: "",
					},
				},
			}),
		Entry("type gitlab but invalid config", "spec.gitlab.url: Too long: may not be more than 253 bytes", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "gitlab",
				Enabled:            true,
				Gitlab: &humiov1alpha1.RegistryConnectionGitlab{
					URL: "https://" + strings.Repeat("A", 255),
				},
			},
		}),
		// GitLab missing required fields
		Entry("type gitlab missing token", "spec.gitlab.tokenRef.name: Invalid value", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "gitlab",
				Enabled:            true,
				Gitlab: &humiov1alpha1.RegistryConnectionGitlab{
					URL: "https://gitlab.example.com/api/v4",
					//TokenRef: humiov1alpha1.SecretKeyRef{Name: "gitlab-token", Key: "token"},
					Project: "myproject",
				},
			},
		}),
		Entry("type gitlab missing project", "is invalid: spec.gitlab.project: Invalid value: \"\"", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "gitlab",
				Enabled:            true,
				Gitlab: &humiov1alpha1.RegistryConnectionGitlab{
					URL:      "https://gitlab.example.com/api/v4",
					TokenRef: humiov1alpha1.SecretKeyRef{Name: "gitlab-token", Key: "token"},
					//Project: "myproject",
				},
			},
		}),

		// GitHub tests
		Entry("type github but no config", "Invalid value: \"object\": github is required when registryType is 'github'", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "github",
				Enabled:            true,
				// Github: &humiov1alpha1.RegistryConnectionGithub{
				//     URL: "https://github.com",
				// },
			},
		}),
		Entry("type github but invalid config", "spec.github.url: Invalid value: \"\": spec.github.url in body should match '^https://.*'",
			humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
				Spec: humiov1alpha1.HumioPackageRegistrySpec{
					ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
					DisplayName:        "test",
					RegistryType:       "github",
					Enabled:            true,
					Github: &humiov1alpha1.RegistryConnectionGithub{
						URL: "",
					},
				},
			}),
		Entry("type github but invalid config", "spec.github.url: Too long: may not be more than 253 bytes", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "github",
				Enabled:            true,
				Github: &humiov1alpha1.RegistryConnectionGithub{
					URL: "https://" + strings.Repeat("A", 255),
				},
			},
		}),
		Entry("type github missing token", "spec.github.tokenRef.key: Invalid value: \"\"", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "github",
				Enabled:            true,
				Github: &humiov1alpha1.RegistryConnectionGithub{
					URL: "https://api.github.com",
					//TokenRef: humiov1alpha1.SecretKeyRef{Name: "github-token", Key: "token"},
					Owner: "myorg",
				},
			},
		}),
		Entry("type github missing owner", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid: spec.github.owner: Invalid value", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "github",
				Enabled:            true,
				Github: &humiov1alpha1.RegistryConnectionGithub{
					URL:      "https://api.github.com",
					TokenRef: humiov1alpha1.SecretKeyRef{Name: "github-token", Key: "token"},
					//Owner: "myorg",
				},
			},
		}),
		// SecretKeyRef field validation tests
		Entry("secretKeyRef missing name", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "github",
				Enabled:            true,
				Github: &humiov1alpha1.RegistryConnectionGithub{
					URL: "https://api.github.com",
					TokenRef: humiov1alpha1.SecretKeyRef{
						//Name: "github-token",
						Key: "token"},
					Owner: "myorg",
				},
			},
		}),
		Entry("secretKeyRef missing key", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "github",
				Enabled:            true,
				Github: &humiov1alpha1.RegistryConnectionGithub{
					URL: "https://api.github.com",
					TokenRef: humiov1alpha1.SecretKeyRef{
						Name: "github-token",
						//Key: "token"
					},
					Owner: "myorg",
				},
			},
		}),
		Entry("secretKeyRef name too long", "spec.github.tokenRef.name: Too long: may not be more than 253 bytes", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "github",
				Enabled:            true,
				Github: &humiov1alpha1.RegistryConnectionGithub{
					URL:      "https://api.github.com",
					TokenRef: humiov1alpha1.SecretKeyRef{Name: strings.Repeat("A", 255), Key: "token"},
					Owner:    "myorg",
				},
			},
		}),
		Entry("secretKeyRef key too long", "spec.github.tokenRef.key: Too long: may not be more than 253 bytes", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "github",
				Enabled:            true,
				Github: &humiov1alpha1.RegistryConnectionGithub{
					URL:      "https://api.github.com",
					TokenRef: humiov1alpha1.SecretKeyRef{Name: "github-token", Key: strings.Repeat("A", 255)},
					Owner:    "myorg",
				},
			},
		}),
		// Artifactory tests
		Entry("type artifactory but no config", "Invalid value: \"object\": artifactory is required when registryType is 'artifactory'", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "artifactory",
				Enabled:            true,
				// Artifactory: &humiov1alpha1.RegistryConnectionArtifactory{
				//     URL: "https://mycompany.jfrog.io",
				// },
			},
		}),
		Entry("type artifactory but invalid config", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid",
			humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
				Spec: humiov1alpha1.HumioPackageRegistrySpec{
					ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
					DisplayName:        "test",
					RegistryType:       "artifactory",
					Enabled:            true,
					Artifactory: &humiov1alpha1.RegistryConnectionArtifactory{
						URL: "",
					},
				},
			}),
		Entry("type artifactory but invalid config", "spec.artifactory.url: Too long: may not be more than 253 bytes", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "artifactory",
				Enabled:            true,
				Artifactory: &humiov1alpha1.RegistryConnectionArtifactory{
					URL: "https://" + strings.Repeat("A", 255),
				},
			},
		}),
		Entry("type artifactory missing repository", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "artifactory",
				Enabled:            true,
				Artifactory: &humiov1alpha1.RegistryConnectionArtifactory{
					URL: "https://mycompany.jfrog.io",
					//Repository: "generic-repo",
					TokenRef: humiov1alpha1.SecretKeyRef{Name: "artifactory-token", Key: "token"},
				},
			},
		}),
		Entry("type artifactory missing token", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "artifactory",
				Enabled:            true,
				Artifactory: &humiov1alpha1.RegistryConnectionArtifactory{
					URL:        "https://mycompany.jfrog.io",
					Repository: "generic-repo",
					//TokenRef: humiov1alpha1.SecretKeyRef{Name: "artifactory-token", Key: "token"},
				},
			},
		}),

		// AWS tests
		Entry("type aws but no config", "Invalid value: \"object\": aws is required when registryType is 'aws'", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "aws",
				Enabled:            true,
				// Aws: &humiov1alpha1.RegistryConnectionAWS{
				//     Region: "us-east-1",
				// },
			},
		}),
		Entry("type aws but invalid config", "spec.aws.region: Invalid value: \"\": spec.aws.region in body should be at least 1 chars long",
			humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
				Spec: humiov1alpha1.HumioPackageRegistrySpec{
					ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
					DisplayName:        "test",
					RegistryType:       "aws",
					Enabled:            true,
					Aws: &humiov1alpha1.RegistryConnectionAws{
						Region: "",
					},
				},
			}),
		Entry("type aws but invalid config", "spec.aws.region: Too long: may not be more than 63 bytes", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "aws",
				Enabled:            true,
				Aws: &humiov1alpha1.RegistryConnectionAws{
					Region: strings.Repeat("A", 65),
				},
			},
		}),
		Entry("type aws missing domain", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "aws",
				Enabled:            true,
				Aws: &humiov1alpha1.RegistryConnectionAws{
					Region: "us-west-2",
					//Domain: "my-domain",
					Repository:      "my-repo",
					AccessKeyRef:    humiov1alpha1.SecretKeyRef{Name: "aws-secret", Key: "access-key"},
					AccessSecretRef: humiov1alpha1.SecretKeyRef{Name: "aws-secret", Key: "secret-key"},
				},
			},
		}),
		Entry("type aws missing repository", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "aws",
				Enabled:            true,
				Aws: &humiov1alpha1.RegistryConnectionAws{
					Region: "us-west-2",
					Domain: "my-domain",
					//Repository: "my-repo",
					AccessKeyRef:    humiov1alpha1.SecretKeyRef{Name: "aws-secret", Key: "access-key"},
					AccessSecretRef: humiov1alpha1.SecretKeyRef{Name: "aws-secret", Key: "secret-key"},
				},
			},
		}),
		Entry("type aws missing accessKey", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "aws",
				Enabled:            true,
				Aws: &humiov1alpha1.RegistryConnectionAws{
					Region:     "us-west-2",
					Domain:     "my-domain",
					Repository: "my-repo",
					//AccessKeyRef: humiov1alpha1.SecretKeyRef{Name: "aws-secret", Key: "access-key"},
					AccessSecretRef: humiov1alpha1.SecretKeyRef{Name: "aws-secret", Key: "secret-key"},
				},
			},
		}),
		Entry("type aws missing accessSecret", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "aws",
				Enabled:            true,
				Aws: &humiov1alpha1.RegistryConnectionAws{
					Region:       "us-west-2",
					Domain:       "my-domain",
					Repository:   "my-repo",
					AccessKeyRef: humiov1alpha1.SecretKeyRef{Name: "aws-secret", Key: "access-key"},
					//AccessSecretRef: humiov1alpha1.SecretKeyRef{Name: "aws-secret", Key: "secret-key"},
				},
			},
		}),

		// GCloud tests
		Entry("type gcloud but no config", "Invalid value: \"object\": gcloud is required when registryType is 'gcloud'", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "gcloud",
				Enabled:            true,
				// Gcloud: &humiov1alpha1.RegistryConnectionGcloud{
				//     ProjectID: "my-project-id",
				// },
			},
		}),
		Entry("type gcloud but invalid config", "spec.gcloud.projectId: Invalid value: \"\": spec.gcloud.projectId in body should be at least 1 chars long",
			humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
				Spec: humiov1alpha1.HumioPackageRegistrySpec{
					ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
					DisplayName:        "test",
					RegistryType:       "gcloud",
					Enabled:            true,
					Gcloud: &humiov1alpha1.RegistryConnectionGcloud{
						ProjectID: "",
					},
				},
			}),
		Entry("type gcloud but invalid config", "spec.gcloud.projectId: Too long: may not be more than 253 bytes", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "gcloud",
				Enabled:            true,
				Gcloud: &humiov1alpha1.RegistryConnectionGcloud{
					ProjectID: strings.Repeat("A", 255),
				},
			},
		}),
		Entry("neither managedClusterName nor externalClusterName specified", "Must specify exactly one of managedClusterName or externalClusterName", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				//ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				//ExternalClusterName: "external-cluster",
				DisplayName:  "test",
				RegistryType: "marketplace",
				Enabled:      true,
				Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
					URL: "https://packages.humio.com",
				},
			},
		}),
		Entry("both managedClusterName and externalClusterName specified", "Must specify exactly one of managedClusterName or externalClusterName", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName:  fmt.Sprintf("humiocluster-shared-%d", processID),
				ExternalClusterName: "external-cluster",
				DisplayName:         "test",
				RegistryType:        "marketplace",
				Enabled:             true,
				Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
					URL: "https://packages.humio.com",
				},
			},
		}),
		Entry("type gcloud missing repository", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "gcloud",
				Enabled:            true,
				Gcloud: &humiov1alpha1.RegistryConnectionGcloud{
					URL:       "https://artifactregistry.googleapis.com",
					ProjectID: "my-project",
					//Repository: "my-repo",
					Location:             "us-central1",
					ServiceAccountKeyRef: humiov1alpha1.SecretKeyRef{Name: "gcp-secret", Key: "service-account.json"},
				},
			},
		}),
		Entry("type gcloud missing location", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "gcloud",
				Enabled:            true,
				Gcloud: &humiov1alpha1.RegistryConnectionGcloud{
					URL:        "https://artifactregistry.googleapis.com",
					ProjectID:  "my-project",
					Repository: "my-repo",
					//Location: "us-central1",
					ServiceAccountKeyRef: humiov1alpha1.SecretKeyRef{
						Name: "gcp-secret",
						Key:  "service-account.json",
					},
				},
			},
		}),
		Entry("type gcloud missing serviceAccount", "HumioPackageRegistry.core.humio.com \"test-packageregistry\" is invalid", humiov1alpha1.HumioPackageRegistry{
			ObjectMeta: metav1.ObjectMeta{Name: "test-packageregistry", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				DisplayName:        "test",
				RegistryType:       "gcloud",
				Enabled:            true,
				Gcloud: &humiov1alpha1.RegistryConnectionGcloud{
					URL:        "https://artifactregistry.googleapis.com",
					ProjectID:  "my-project",
					Repository: "my-repo",
					Location:   "us-central1",
					//ServiceAccountKeyRef: humiov1alpha1.SecretKeyRef{Name: "gcp-secret", Key: "service-account.json"},
				},
			},
		}),
	)
})

var _ = Describe("HumioPackage", Label("envtest", "dummy", "real"), func() {
	processID := GinkgoParallelProcess()
	DescribeTable("invalid inputs should be rejected by the constraints in the CRD/API",
		func(expectedOutput string, invalidInput humiov1alpha1.HumioPackage) {
			err := k8sClient.Create(context.TODO(), &invalidInput)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expectedOutput))
		},
		// Required field validation tests
		Entry("packageName not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.packageName: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				//PackageName:        "crowdstrike/fdr",
				PackageVersion:  "1.0.0",
				PackageChecksum: "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		Entry("packageVersion not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.packageVersion: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				//PackageVersion: "1.0.0",
				PackageChecksum: "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		Entry("packageChecksum not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.packageChecksum: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				//PackageChecksum: "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		Entry("registryRef not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.registryRef.name: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				//RegistryRef: humiov1alpha1.RegistryReference{Name: "test-registry"},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		Entry("packageInstallTargets not specified", "spec.packageInstallTargets: Required value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				//PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{{ViewNames: []string{"test-view"}}},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		// Cluster name validation (exactly one required)
		Entry("neither managedClusterName nor externalClusterName specified", "Invalid value: \"object\": Must specify exactly one of managedClusterName or externalClusterName", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				//ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				//ExternalClusterName: "external-cluster",
				PackageName:     "crowdstrike/fdr",
				PackageVersion:  "1.0.0",
				PackageChecksum: "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		Entry("both managedClusterName and externalClusterName specified", "Invalid value: \"object\": Must specify exactly one of managedClusterName or externalClusterName", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName:  fmt.Sprintf("humiocluster-shared-%d", processID),
				ExternalClusterName: "external-cluster",
				PackageName:         "crowdstrike/fdr",
				PackageVersion:      "1.0.0",
				PackageChecksum:     "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		// Registry configuration validation (exactly one required)
		Entry("no registry configuration specified", "Invalid value: \"object\": Must specify exactly one of marketplace, gitlab, github, aws, artifactory, or gcloud configuration", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				// No registry config specified
			},
		}),
		// GitLab specific validation tests
		Entry("gitlab packageName not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.gitlab.package: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					//PackageName: "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		Entry("gitlab assetName not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.gitlab.assetName: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package: "test-package",
					//AssetName: "test.tar.gz",
				},
			},
		}),
		// GitHub specific validation tests
		Entry("github repository not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.github.repository: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Github: &humiov1alpha1.GithubPackageInfo{
					//Repository: "myorg/myrepo",
					Tag: "v1.0.0",
				},
			},
		}),
		Entry("github tag not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.github.tag: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Github: &humiov1alpha1.GithubPackageInfo{
					Repository: "myorg/myrepo",
					//Tag: "v1.0.0",
				},
			},
		}),
		// Artifactory specific validation tests
		Entry("artifactory filePath not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.artifactory.filePath: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Artifactory: &humiov1alpha1.ArtifactoryPackageInfo{
					//FilePath: "path/to/package.tar.gz",
				},
			},
		}),
		// AWS specific validation tests
		Entry("aws namespace not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.aws.namespace: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Aws: &humiov1alpha1.AwsPackageInfo{
					//Namespace: "my-namespace",
					Package:  "my-package",
					Filename: "package.tar.gz",
				},
			},
		}),
		Entry("aws packageName not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.aws.package: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Aws: &humiov1alpha1.AwsPackageInfo{
					Namespace: "my-namespace",
					//PackageName: "my-package",
					Filename: "package.tar.gz",
				},
			},
		}),
		Entry("aws filename not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.aws.filename: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Aws: &humiov1alpha1.AwsPackageInfo{
					Namespace: "my-namespace",
					Package:   "my-package",
					//Filename: "package.tar.gz",
				},
			},
		}),
		// GCloud specific validation tests
		Entry("gcloud packageName not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.gcloud.package: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Gcloud: &humiov1alpha1.GcloudPackageInfo{
					//PackageName: "my-package",
					Filename: "package.tar.gz",
				},
			},
		}),
		Entry("gcloud filename not specified", "HumioPackage.core.humio.com \"test-package\" is invalid", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					Name: "test-registry",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Gcloud: &humiov1alpha1.GcloudPackageInfo{
					Package: "my-package",
					//Filename: "package.tar.gz",
				},
			},
		}),
		// PackageInstallTargets validation
		Entry("packageInstallTargets empty array", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.packageInstallTargets: Invalid value: 0", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName:    fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:           "crowdstrike/fdr",
				PackageVersion:        "1.0.0",
				PackageChecksum:       "abc123",
				RegistryRef:           humiov1alpha1.RegistryReference{Name: "test-registry"},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{}, // Empty array
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		Entry("packageInstallTarget with both viewNames and viewRef", "Invalid value: \"object\": Must specify exactly one of viewNames or viewRef", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef:        humiov1alpha1.RegistryReference{Name: "test-registry"},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{
						ViewNames: []string{"test-view"}, // Both specified - should fail
						ViewRef:   &humiov1alpha1.HumioViewReference{Name: "test-view-ref"},
					},
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		Entry("packageInstallTarget with neither viewNames nor viewRef", "Invalid value: \"object\": Must specify exactly one of viewNames or viewRef", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef:        humiov1alpha1.RegistryReference{Name: "test-registry"},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{
						// Neither viewNames nor viewRef specified - should fail
					},
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		Entry("viewNames empty array", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.packageInstallTargets[0]: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef:        humiov1alpha1.RegistryReference{Name: "test-registry"},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{}}, // Empty array - should fail
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		Entry("viewRef name not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.packageInstallTargets[0].viewRef.name", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef:        humiov1alpha1.RegistryReference{Name: "test-registry"},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewRef: &humiov1alpha1.HumioViewReference{
						//Name: "test-view-ref", // Name missing - should fail
						Namespace: "test-ns",
					}},
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
		Entry("registryRef name not specified", "HumioPackage.core.humio.com \"test-package\" is invalid: spec.registryRef.name: Invalid value", humiov1alpha1.HumioPackage{
			ObjectMeta: metav1.ObjectMeta{Name: "test-package", Namespace: fmt.Sprintf("e2e-resources-%d", processID)},
			Spec: humiov1alpha1.HumioPackageSpec{
				ManagedClusterName: fmt.Sprintf("humiocluster-shared-%d", processID),
				PackageName:        "crowdstrike/fdr",
				PackageVersion:     "1.0.0",
				PackageChecksum:    "abc123",
				RegistryRef: humiov1alpha1.RegistryReference{
					//Name: "test-registry", // Name missing - should fail
					Namespace: "test-ns",
				},
				PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
					{ViewNames: []string{"test-view"}},
				},
				Gitlab: &humiov1alpha1.GitlabPackageInfo{
					Package:   "test-package",
					AssetName: "test.tar.gz",
				},
			},
		}),
	)
})
