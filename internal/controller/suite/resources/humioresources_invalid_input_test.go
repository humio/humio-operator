package resources

import (
	"context"
	"fmt"
	"strings"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
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
				ManagedClusterName: "test-cluster",
				//Name:               "",
				TokenSecretName: "test-secret",
				ViewNames:       []string{"test-view"},
				Permissions:     []string{"ReadAccess"},
			},
		}),
		Entry("name empty value", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "",
				TokenSecretName:    "test-secret",
				ViewNames:          []string{"test-view"},
				Permissions:        []string{"ReadAccess"},
			},
		}),
		Entry("name too long", "spec.name: Too long:", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               strings.Repeat("A", 255),
				TokenSecretName:    "test-secret",
				ViewNames:          []string{"test-view"},
				Permissions:        []string{"ReadAccess"},
			},
		}),
		Entry("viewNames not specified", "spec.viewNames: Required value", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				//ViewNames:          []string{""},
				Permissions: []string{"ReadAccess"},
			},
		}),
		Entry("viewNames value not set", "spec.viewNames: Invalid value", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				ViewNames:          []string{""},
				Permissions:        []string{"ReadAccess"},
			},
		}),
		Entry("viewNames name too long", "spec.viewNames: Invalid value", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				ViewNames:          []string{strings.Repeat("A", 255)},
				Permissions:        []string{"ReadAccess"},
			},
		}),
		Entry("Permissions not set", "spec.permissions: Required value", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				ViewNames:          []string{"test-view"},
				// Permissions: []string{"ReadAccess"},
			},
		}),
		Entry("Permissions entry is empty", "spec.permissions: Invalid value: \"array\": permissions: each item must be 1-253", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				ViewNames:          []string{"test-view"},
				Permissions:        []string{""},
			},
		}),
		Entry("Permissions entry too long", "spec.permissions: Invalid value: \"array\": permissions: each item must be 1-253", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				ViewNames:          []string{"test-view"},
				Permissions:        []string{strings.Repeat("A", 255)},
			},
		}),
		Entry("Permissions are too many", "spec.permissions: Too many", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				ViewNames:          []string{"test-view"},
				Permissions:        strings.Split(strings.Repeat("validName,", 100)+"validName", ","),
			},
		}),
		Entry("IPFilterName too long", "spec.ipFilterName: Too long:", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				ViewNames:          []string{"test-view"},
				Permissions:        []string{"ReadAccess"},
				IPFilterName:       strings.Repeat("A", 255),
			},
		}),
		Entry("TokenSecretName not set", "spec.tokenSecretName: Invalid value: \"\": spec.tokenSecretName in body should be at least 1 chars long", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				//TokenSecretName:    "test-secret",
				ViewNames:   []string{"test-view"},
				Permissions: []string{"ReadAccess"},
			},
		}),
		Entry("TokenSecretName set empty", "spec.tokenSecretName: Invalid value: \"\": spec.tokenSecretName in body should be at least 1 chars long", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "",
				ViewNames:          []string{"test-view"},
				Permissions:        []string{"ReadAccess"},
			},
		}),
		Entry("TokenSecretName too long", "spec.tokenSecretName: Too long", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    strings.Repeat("A", 255),
				ViewNames:          []string{"test-view"},
				Permissions:        []string{"ReadAccess"},
			},
		}),
		Entry("TokenSecretName invalid char", "spec.tokenSecretName: Invalid value", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test.&",
				ViewNames:          []string{"test-view"},
				Permissions:        []string{"ReadAccess"},
			},
		}),
		Entry("TokenSecretLabel key too long", "spec.tokenSecretLabels: Invalid value: \"object\": tokenSecretLabels keys must be 1-63 characters", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				ViewNames:          []string{"test-view"},
				Permissions:        []string{"ReadAccess"},
				TokenSecretLabels:  map[string]string{strings.Repeat("A", 255): ""},
			},
		}),
		Entry("TokenSecretLabel value too long", "spec.tokenSecretLabels: Invalid value: \"object\": tokenSecretLabels values must be 1-63 characters", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				ViewNames:          []string{"test-view"},
				Permissions:        []string{"ReadAccess"},
				TokenSecretLabels:  map[string]string{"key": strings.Repeat("A", 255)},
			},
		}),
		Entry("TokenSecretLabel too many keys", "spec.tokenSecretLabels: Too many: 64: must have at most 63 items", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				ViewNames:          []string{"test-view"},
				Permissions:        []string{"ReadAccess"},
				TokenSecretLabels: func() map[string]string {
					m := make(map[string]string)
					for i := range 64 {
						m[fmt.Sprintf("validName%d", i)] = strings.Repeat("A", 10)
					}
					return m
				}(),
			},
		}),
		Entry("TokenSecretAnnotations key too long", "spec.tokenSecretAnnotations: Invalid value: \"object\": tokenSecretAnnotations keys must be 1-63 characters", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName:     "test-cluster",
				Name:                   "test-name",
				TokenSecretName:        "test-secret",
				ViewNames:              []string{"test-view"},
				Permissions:            []string{"ReadAccess"},
				TokenSecretLabels:      map[string]string{"key": "value"},
				TokenSecretAnnotations: map[string]string{strings.Repeat("A", 255): ""},
			},
		}),
		Entry("TokenSecretAnnotations too many keys", "spec.tokenSecretAnnotations: Too many: 64: must have at most 63 items", humiov1alpha1.HumioViewToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioViewTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				ViewNames:          []string{"test-view"},
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
				ManagedClusterName: "test-cluster",
				//Name:               "",
				TokenSecretName: "test-secret",
				Permissions:     []string{"ReadAccess"},
			},
		}),
		Entry("name empty value", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "",
				TokenSecretName:    "test-secret",
				Permissions:        []string{"ReadAccess"},
			},
		}),
		Entry("name too long", "spec.name: Too long:", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               strings.Repeat("A", 255),
				TokenSecretName:    "test-secret",
				Permissions:        []string{"ReadAccess"},
			},
		}),
		Entry("Permissions not set", "spec.permissions: Required value", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				// Permissions: []string{"ReadAccess"},
			},
		}),
		Entry("Permissions entry is empty", "spec.permissions: Invalid value: \"array\": permissions: each item must be 1-253", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				Permissions:        []string{""},
			},
		}),
		Entry("Permissions entry too long", "spec.permissions: Invalid value: \"array\": permissions: each item must be 1-253", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				Permissions:        []string{strings.Repeat("A", 255)},
			},
		}),
		Entry("Permissions are too many", "spec.permissions: Too many", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				Permissions:        strings.Split(strings.Repeat("validName,", 100)+"validName", ","),
			},
		}),
		Entry("IPFilterName too long", "spec.ipFilterName: Too long:", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				Permissions:        []string{"ReadAccess"},
				IPFilterName:       strings.Repeat("A", 255),
			},
		}),
		Entry("TokenSecretName not set", "spec.tokenSecretName: Invalid value: \"\": spec.tokenSecretName in body should be at least 1 chars long", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				//TokenSecretName:    "test-secret",
				Permissions: []string{"ReadAccess"},
			},
		}),
		Entry("TokenSecretName set empty", "spec.tokenSecretName: Invalid value: \"\": spec.tokenSecretName in body should be at least 1 chars long", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "",
				Permissions:        []string{"ReadAccess"},
			},
		}),
		Entry("TokenSecretName too long", "spec.tokenSecretName: Too long", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    strings.Repeat("A", 255),
				Permissions:        []string{"ReadAccess"},
			},
		}),
		Entry("TokenSecretName invalid char", "spec.tokenSecretName: Invalid value", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test.&",
				Permissions:        []string{"ReadAccess"},
			},
		}),
		Entry("TokenSecretLabel key too long", "spec.tokenSecretLabels: Invalid value: \"object\": tokenSecretLabels keys must be 1-63 characters", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				Permissions:        []string{"ReadAccess"},
				TokenSecretLabels:  map[string]string{strings.Repeat("A", 255): ""},
			},
		}),
		Entry("TokenSecretLabel value too long", "spec.tokenSecretLabels: Invalid value: \"object\": tokenSecretLabels values must be 1-63 characters", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-name",
				TokenSecretName:    "test-secret",
				Permissions:        []string{"ReadAccess"},
				TokenSecretLabels:  map[string]string{"key": strings.Repeat("A", 255)},
			},
		}),
		Entry("TokenSecretLabel too many keys", "spec.tokenSecretLabels: Too many: 64: must have at most 63 items", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
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
		}),
		Entry("TokenSecretAnnotations key too long", "spec.tokenSecretAnnotations: Invalid value: \"object\": tokenSecretAnnotations keys must be 1-63 characters", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
				ManagedClusterName:     "test-cluster",
				Name:                   "test-name",
				TokenSecretName:        "test-secret",
				Permissions:            []string{"ReadAccess"},
				TokenSecretLabels:      map[string]string{"key": "value"},
				TokenSecretAnnotations: map[string]string{strings.Repeat("A", 255): ""},
			},
		}),
		Entry("TokenSecretAnnotations too many keys", "spec.tokenSecretAnnotations: Too many: 64: must have at most 63 items", humiov1alpha1.HumioSystemToken{
			ObjectMeta: metav1.ObjectMeta{Name: "view-token", Namespace: defaultNamespace},
			Spec: humiov1alpha1.HumioSystemTokenSpec{
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
