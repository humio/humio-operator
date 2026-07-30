package controller

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

func TestEnvtestSetup(t *testing.T) {
	ctx := context.Background()

	cfg, k8sClient, env, err := setupEnvtest()
	if err != nil {
		t.Fatalf("Failed to setup envtest: %v", err)
	}
	if env != nil {
		defer func() { _ = teardownEnvtest(env) }()
	}

	if cfg == nil {
		t.Error("Expected non-nil rest.Config, got nil")
	}

	if k8sClient == nil {
		t.Error("Expected non-nil k8s client, got nil")
		return
	}

	hc := &humiov1alpha1.HumioCluster{}
	err = k8sClient.Get(ctx, client.ObjectKey{
		Namespace: "default",
		Name:      "test-cluster",
	}, hc)

	if err != nil && client.IgnoreNotFound(err) != nil {
		t.Errorf("Expected CRDs to be loaded, but got error: %v", err)
	}
}

func setupEnvtest() (*rest.Config, client.Client, *envtest.Environment, error) {
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := env.Start()
	if err != nil {
		return nil, nil, nil, err
	}

	err = humiov1alpha1.AddToScheme(scheme.Scheme)
	if err != nil {
		return nil, nil, nil, err
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, nil, nil, err
	}

	return cfg, k8sClient, env, nil
}

func teardownEnvtest(env *envtest.Environment) error {
	if env != nil {
		return env.Stop()
	}
	return nil
}

func init() {
	_ = humiov1alpha1.AddToScheme(scheme.Scheme)
}

func TestShadowReadFailure_SafeDegradation(t *testing.T) {
	ctx := context.Background()

	cfg, k8sClient, env, err := setupEnvtest()
	if err != nil {
		t.Fatalf("Failed to setup envtest: %v", err)
	}
	if env != nil {
		defer func() { _ = teardownEnvtest(env) }()
	}

	if k8sClient == nil {
		t.Fatal("Expected non-nil k8s client, got nil")
	}
	if cfg == nil {
		t.Fatal("Expected non-nil rest.Config, got nil")
	}

	cluster := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster-degradation", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "main",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: nil,
						Autoscaling: &humiov1alpha1.AutoscalingSpec{
							MinReplicas: ptr.To(int32(3)),
							MaxReplicas: 10,
						},
					},
				},
			},
		},
		Status: humiov1alpha1.HumioClusterStatus{
			NodePoolStatus: []humiov1alpha1.HumioNodePoolStatus{{Name: "main", DesiredReplicas: 8}},
		},
	}

	err = k8sClient.Create(ctx, cluster)
	assert.NoError(t, err)

	shadow := &humiov1alpha1.HumioNodePool{}
	shadowKey := types.NamespacedName{Name: "main", Namespace: "default"}
	assert.Eventually(t, func() bool {
		err := k8sClient.Get(ctx, shadowKey, shadow)
		return err == nil
	}, 10*time.Second, 500*time.Millisecond, "shadow not created")

	err = k8sClient.Delete(ctx, shadow)
	assert.NoError(t, err)

	updated := &humiov1alpha1.HumioCluster{}
	err = k8sClient.Get(ctx, types.NamespacedName{Name: "test-cluster-degradation", Namespace: "default"}, updated)
	assert.NoError(t, err)

	for _, poolStatus := range updated.Status.NodePoolStatus {
		if poolStatus.Name == "main" {
			assert.Equal(t, int32(8), poolStatus.DesiredReplicas, "status.desiredReplicas should retain last-known value")
		}
	}

	_ = k8sClient.Delete(ctx, cluster)
}
