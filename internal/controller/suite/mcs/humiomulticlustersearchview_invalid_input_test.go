package mcs

import (
	"context"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("HumioMultiClusterSearchView", Label("envtest", "dummy", "real"), func() {
	DescribeTable("invalid inputs should be rejected by the constraints in the CRD/API",
		func(expectedOutput string, invalidInput humiov1alpha1.HumioMultiClusterSearchView) {
			err := k8sClient.Create(context.TODO(), &invalidInput)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expectedOutput))
		},
		// Each Entry has a name and the parameters for the function above
		Entry("no connections specified", "spec.connections: Required value", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				// Missing connections field
			},
		}),
		Entry("empty connections slice specified", "spec.connections: Required value", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections:        []humiov1alpha1.HumioMultiClusterSearchViewConnection{},
			},
		}),
		Entry("managedClusterName and externalClusterName are both specified", "Must specify exactly one of managedClusterName or externalClusterName", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName:  "test-cluster",
				ExternalClusterName: "external-cluster",
				Name:                "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "test-repo",
					},
				},
			},
		}),
		Entry("missing type", "spec.connections[0].type: Unsupported value: \"\": supported values: \"Local\", \"Remote\"", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						// Missing type
					},
				},
			},
		}),
		Entry("invalid type", "spec.connections[0].type: Unsupported value", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            "Invalid", // Invalid type
					},
				},
			},
		}),
		Entry("empty cluster identity", "spec.connections[0].clusterIdentity in body should be at least 1 chars long", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "", // Empty cluster identity
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "test-repo",
					},
				},
			},
		}),
		Entry("missing cluster identity", "spec.connections[0].clusterIdentity in body should be at least 1 chars long", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						// Missing cluster identity
						Type:           humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName: "test-repo",
					},
				},
			},
		}),
		Entry("duplicate cluster identity", "spec.connections[1]: Duplicate value: map[string]interface {}{\"clusterIdentity\":\"same-identity\"}", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "same-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "test-repo",
					},
					{
						ClusterIdentity: "same-identity", // Duplicate identity
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote,
						Url:             "https://example.com",
						APITokenSource: &humiov1alpha1.HumioMultiClusterSearchViewConnectionAPITokenSpec{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "secret"},
								Key:                  "token",
							},
						},
					},
				},
			},
		}),
		Entry("missing key for secretKeyRef in apiTokenSource", "SecretKeyRef must have both name and key fields set", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote,
						Url:             "https://example.com",
						APITokenSource: &humiov1alpha1.HumioMultiClusterSearchViewConnectionAPITokenSpec{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "secret"},
								// Missing Key field
							},
						},
					},
				},
			},
		}),
		Entry("missing name for secretKeyRef in apiTokenSource", "SecretKeyRef must have both name and key fields set", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote,
						Url:             "https://example.com",
						APITokenSource: &humiov1alpha1.HumioMultiClusterSearchViewConnectionAPITokenSpec{
							SecretKeyRef: &corev1.SecretKeySelector{
								// Missing Name field
								Key: "token",
							},
						},
					},
				},
			},
		}),
		Entry("missing viewOrRepoName when using type=Local", "When type is Local, viewOrRepoName must be set", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						// Missing ViewOrRepoName
					},
				},
			},
		}),
		Entry("missing url when using type=Remote", "When type is Remote, url/apiTokenSource must be set", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote,
						// Missing URL
						APITokenSource: &humiov1alpha1.HumioMultiClusterSearchViewConnectionAPITokenSpec{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "secret"},
								Key:                  "token",
							},
						},
					},
				},
			},
		}),
		Entry("missing apiTokenSource when using type=Remote", "When type is Remote, url/apiTokenSource must be set", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote,
						Url:             "https://example.com",
						// Missing APITokenSource
					},
				},
			},
		}),
		Entry("url specified when using type=Local", "When type is Local, viewOrRepoName must be set and url/apiTokenSource must not be set", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "test-repo",
						Url:             "https://example.com", // URL not allowed in Local type
					},
				},
			},
		}),
		Entry("apiTokenSource specified when using type=Local", "When type is Local, viewOrRepoName must be set and url/apiTokenSource must not be set", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "test-repo",
						APITokenSource: &humiov1alpha1.HumioMultiClusterSearchViewConnectionAPITokenSpec{ // APITokenSource not allowed in Local type
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "secret"},
								Key:                  "token",
							},
						},
					},
				},
			},
		}),
		Entry("viewOrRepoName specified when using type=Remote", "When type is Remote, url/apiTokenSource must be set and viewOrRepoName must not be set", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote,
						ViewOrRepoName:  "test-repo", // ViewOrRepoName not allowed in Remote type
						Url:             "https://example.com",
						APITokenSource: &humiov1alpha1.HumioMultiClusterSearchViewConnectionAPITokenSpec{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "secret"},
								Key:                  "token",
							},
						},
					},
				},
			},
		}),
		Entry("duplicate key for tag", "spec.connections[0].tags[1]: Duplicate value: map[string]interface {}{\"key\":\"env\"}", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "test-repo",
						Tags: []humiov1alpha1.HumioMultiClusterSearchViewConnectionTag{
							{
								Key:   "env",
								Value: "prod",
							},
							{
								Key:   "env", // Duplicate key
								Value: "test",
							},
						},
					},
				},
			},
		}),
		Entry("empty string key for tag", "spec.connections[0].tags[0].key: Invalid value: \"\": spec.connections[0].tags[0].key in body should be at least 1 chars long", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "test-repo",
						Tags: []humiov1alpha1.HumioMultiClusterSearchViewConnectionTag{
							{
								Key:   "", // Empty key
								Value: "prod",
							},
						},
					},
				},
			},
		}),
		Entry("empty string value for tag", "spec.connections[0].tags[0].value: Invalid value: \"\": spec.connections[0].tags[0].value in body should be at least 1 chars long", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "test-repo",
						Tags: []humiov1alpha1.HumioMultiClusterSearchViewConnectionTag{
							{
								Key:   "env",
								Value: "", // Empty value
							},
						},
					},
				},
			},
		}),
		Entry("empty secretKeyRef for apiTokenSource", "spec.connections[0].apiTokenSource.secretKeyRef: Required value", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote,
						Url:             "https://example.com",
						APITokenSource:  &humiov1alpha1.HumioMultiClusterSearchViewConnectionAPITokenSpec{
							// Missing SecretKeyRef
						},
					},
				},
			},
		}),
		Entry("empty url for type=Remote", "spec.connections[0]: Invalid value: \"object\": When type is Remote, url/apiTokenSource must be set and viewOrRepoName must not be set", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeRemote,
						Url:             "", // Empty URL, should be at least 8 chars
						APITokenSource: &humiov1alpha1.HumioMultiClusterSearchViewConnectionAPITokenSpec{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "secret"},
								Key:                  "token",
							},
						},
					},
				},
			},
		}),
		Entry("multiple connections with type=Local", "Only one connection can have type 'Local'", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "local-1",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "repo-1",
					},
					{
						ClusterIdentity: "local-2",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal, // Second Local connection not allowed
						ViewOrRepoName:  "repo-2",
					},
				},
			},
		}),
		Entry("neither managedClusterName nor externalClusterName specified", "Must specify exactly one of managedClusterName or externalClusterName", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				// Missing both managedClusterName and externalClusterName
				Name: "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "test-repo",
					},
				},
			},
		}),
		Entry("missing name field", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				// Missing Name field
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "test-repo",
					},
				},
			},
		}),
		Entry("empty name field", "spec.name: Invalid value: \"\": spec.name in body should be at least 1 chars long", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "", // Empty Name field
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "test-identity",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "test-repo",
					},
				},
			},
		}),
		Entry("clusteridentity as tag key", "spec.connections[0].tags[0].key: Invalid value: \"string\": The key 'clusteridentity' is reserved and cannot be used", humiov1alpha1.HumioMultiClusterSearchView{
			ObjectMeta: metav1.ObjectMeta{Name: "test-view", Namespace: "default"},
			Spec: humiov1alpha1.HumioMultiClusterSearchViewSpec{
				ManagedClusterName: "test-cluster",
				Name:               "test-view",
				Connections: []humiov1alpha1.HumioMultiClusterSearchViewConnection{
					{
						ClusterIdentity: "local-1",
						Type:            humiov1alpha1.HumioMultiClusterSearchViewConnectionTypeLocal,
						ViewOrRepoName:  "repo-1",
						Tags: []humiov1alpha1.HumioMultiClusterSearchViewConnectionTag{
							{
								Key:   "clusteridentity",
								Value: "test",
							},
						},
					},
				},
			},
		}),
	)
})
