package controller

import (
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCommon(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Common Suite")
}

var _ = Describe("ShouldForceFinalize", func() {
	Context("Nil safety", func() {
		It("should return false when annotations map is nil", func() {
			obj := &humiov1alpha1.HumioSavedQuery{}
			Expect(ShouldForceFinalize(obj)).To(BeFalse(), "Should handle nil annotations map safely")
		})

		It("should return false when annotations map is empty", func() {
			obj := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			}
			Expect(ShouldForceFinalize(obj)).To(BeFalse(), "Should return false for empty annotations")
		})
	})

	Context("Annotation value checking", func() {
		It("should return true when force-finalize is set to 'true'", func() {
			obj := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ForceFinalizerAnnotation: "true",
					},
				},
			}
			Expect(ShouldForceFinalize(obj)).To(BeTrue(), "Should detect force-finalize='true'")
		})

		It("should return false when force-finalize is set to 'false'", func() {
			obj := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ForceFinalizerAnnotation: "false",
					},
				},
			}
			Expect(ShouldForceFinalize(obj)).To(BeFalse(), "Should reject force-finalize='false'")
		})

		It("should return false when force-finalize is set to 'True' (wrong case)", func() {
			obj := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ForceFinalizerAnnotation: "True",
					},
				},
			}
			Expect(ShouldForceFinalize(obj)).To(BeFalse(), "Should be case-sensitive (lowercase 'true' only)")
		})

		It("should return false when force-finalize is set to '1'", func() {
			obj := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ForceFinalizerAnnotation: "1",
					},
				},
			}
			Expect(ShouldForceFinalize(obj)).To(BeFalse(), "Should only accept string 'true', not '1'")
		})

		It("should return false when force-finalize is set to empty string", func() {
			obj := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ForceFinalizerAnnotation: "",
					},
				},
			}
			Expect(ShouldForceFinalize(obj)).To(BeFalse(), "Should reject empty string value")
		})

		It("should return false when annotation key is missing", func() {
			obj := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"other-annotation": "true",
					},
				},
			}
			Expect(ShouldForceFinalize(obj)).To(BeFalse(), "Should return false when annotation key doesn't exist")
		})
	})

	Context("Multiple annotations", func() {
		It("should find force-finalize among other annotations", func() {
			obj := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"annotation-1":           "value1",
						ForceFinalizerAnnotation: "true",
						"annotation-2":           "value2",
					},
				},
			}
			Expect(ShouldForceFinalize(obj)).To(BeTrue(), "Should find annotation among others")
		})
	})

	Context("Different resource types", func() {
		It("should work with HumioRepository", func() {
			obj := &humiov1alpha1.HumioRepository{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ForceFinalizerAnnotation: "true",
					},
				},
			}
			Expect(ShouldForceFinalize(obj)).To(BeTrue(), "Should work with HumioRepository")
		})

		It("should work with HumioParser", func() {
			obj := &humiov1alpha1.HumioParser{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ForceFinalizerAnnotation: "true",
					},
				},
			}
			Expect(ShouldForceFinalize(obj)).To(BeTrue(), "Should work with HumioParser")
		})
	})
})

var _ = Describe("ShouldSkipFinalizer", func() {
	Context("Global EnableFinalizers flag", func() {
		It("should return true when EnableFinalizers is false regardless of annotation", func() {
			config := CommonConfig{EnableFinalizers: boolPtr(false)}
			obj := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-action",
					Namespace: "default",
				},
			}
			Expect(ShouldSkipFinalizer(config, obj)).To(BeTrue())
		})

		It("should return true when EnableFinalizers is false even with annotation false", func() {
			config := CommonConfig{EnableFinalizers: boolPtr(false)}
			obj := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-action",
					Namespace: "default",
					Annotations: map[string]string{
						ForceFinalizerAnnotation: "false",
					},
				},
			}
			Expect(ShouldSkipFinalizer(config, obj)).To(BeTrue())
		})
	})

	Context("Per-resource annotation fallback", func() {
		It("should return true when force-finalize annotation is set", func() {
			config := CommonConfig{EnableFinalizers: boolPtr(true)}
			obj := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-action",
					Namespace: "default",
					Annotations: map[string]string{
						ForceFinalizerAnnotation: "true",
					},
				},
			}
			Expect(ShouldSkipFinalizer(config, obj)).To(BeTrue())
		})

		It("should return false when neither flag nor annotation is set", func() {
			config := CommonConfig{EnableFinalizers: boolPtr(true)}
			obj := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-action",
					Namespace: "default",
				},
			}
			Expect(ShouldSkipFinalizer(config, obj)).To(BeFalse())
		})

		It("should return false when annotation value is not 'true'", func() {
			config := CommonConfig{EnableFinalizers: boolPtr(true)}
			obj := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-action",
					Namespace: "default",
					Annotations: map[string]string{
						ForceFinalizerAnnotation: "yes",
					},
				},
			}
			Expect(ShouldSkipFinalizer(config, obj)).To(BeFalse())
		})
	})

	Context("Nil safety", func() {
		It("should handle nil annotations safely with EnableFinalizers true", func() {
			config := CommonConfig{EnableFinalizers: boolPtr(true)}
			obj := &humiov1alpha1.HumioRepository{}
			Expect(ShouldSkipFinalizer(config, obj)).To(BeFalse())
		})

		It("should handle nil annotations safely with EnableFinalizers false", func() {
			config := CommonConfig{EnableFinalizers: boolPtr(false)}
			obj := &humiov1alpha1.HumioRepository{}
			Expect(ShouldSkipFinalizer(config, obj)).To(BeTrue())
		})
	})

	Context("Different resource types", func() {
		It("should work with HumioParser", func() {
			config := CommonConfig{EnableFinalizers: boolPtr(false)}
			obj := &humiov1alpha1.HumioParser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-parser",
					Namespace: "default",
				},
			}
			Expect(ShouldSkipFinalizer(config, obj)).To(BeTrue())
		})

		It("should work with HumioView", func() {
			config := CommonConfig{EnableFinalizers: boolPtr(true)}
			obj := &humiov1alpha1.HumioView{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-view",
					Namespace: "default",
					Annotations: map[string]string{
						ForceFinalizerAnnotation: "true",
					},
				},
			}
			Expect(ShouldSkipFinalizer(config, obj)).To(BeTrue())
		})
	})
})
