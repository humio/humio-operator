package cacheconfig

import (
	"fmt"
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func setEnvs(t *testing.T, envs map[string]string) {
	t.Helper()
	for k, v := range envs {
		t.Setenv(k, v)
	}
}

func TestGetCacheOptionsWithWatchNamespace_NeitherSet(t *testing.T) {
	// Neither WATCH_NAMESPACE nor WATCH_LABEL_SELECTOR set → empty cache options, no error
	t.Setenv("WATCH_NAMESPACE", "")
	t.Setenv("WATCH_LABEL_SELECTOR", "")

	opts, err := GetCacheOptionsWithWatchNamespace()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(opts.DefaultNamespaces) != 0 {
		t.Errorf("expected empty DefaultNamespaces, got %v", opts.DefaultNamespaces)
	}
	if len(opts.ByObject) != 0 {
		t.Error("expected empty ByObject")
	}
}

func TestGetCacheOptionsWithWatchNamespace_NamespaceMode(t *testing.T) {
	setEnvs(t, map[string]string{
		"WATCH_NAMESPACE":      "ns-a, ns-b",
		"WATCH_LABEL_SELECTOR": "",
	})

	opts, err := GetCacheOptionsWithWatchNamespace()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(opts.DefaultNamespaces) != 2 {
		t.Fatalf("expected 2 namespaces, got %d", len(opts.DefaultNamespaces))
	}
	if _, ok := opts.DefaultNamespaces["ns-a"]; !ok {
		t.Error("expected ns-a in DefaultNamespaces")
	}
	if _, ok := opts.DefaultNamespaces["ns-b"]; !ok {
		t.Error("expected ns-b in DefaultNamespaces")
	}
}

func TestGetCacheOptionsWithWatchNamespace_LabelSelectorMode(t *testing.T) {
	setEnvs(t, map[string]string{
		"WATCH_NAMESPACE":      "",
		"WATCH_LABEL_SELECTOR": "app.kubernetes.io/managed-by=logscale-ops-operator",
	})

	opts, err := GetCacheOptionsWithWatchNamespace()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(opts.DefaultNamespaces) != 1 {
		t.Fatalf("expected 1 entry in DefaultNamespaces (cache.AllNamespaces), got %d", len(opts.DefaultNamespaces))
	}
	allNsCfg, ok := opts.DefaultNamespaces[cache.AllNamespaces]
	if !ok {
		t.Fatal("expected cache.AllNamespaces key in DefaultNamespaces")
	}
	if allNsCfg.LabelSelector == nil {
		t.Fatal("expected non-nil LabelSelector in AllNamespaces config")
	}
	if allNsCfg.LabelSelector.String() != "app.kubernetes.io/managed-by=logscale-ops-operator" {
		t.Errorf("unexpected selector: %s", allNsCfg.LabelSelector.String())
	}
	if len(opts.ByObject) == 0 {
		t.Error("expected ByObject to contain native type exemptions")
	}

	// Verify all internally-created Humio CRDs are exempted
	internalCRDs := []client.Object{
		&humiov1alpha1.HumioBootstrapToken{},
		&humiov1alpha1.HumioTelemetryCollection{},
		&humiov1alpha1.HumioTelemetryExport{},
		&humiov1alpha1.HumioDependencyCheck{},
	}
	for _, obj := range internalCRDs {
		found := false
		for key := range opts.ByObject {
			if fmt.Sprintf("%T", key) == fmt.Sprintf("%T", obj) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %T in ByObject exemptions for internally-created CRD", obj)
		}
	}
}

func TestGetCacheOptionsWithWatchNamespace_BothSet(t *testing.T) {
	setEnvs(t, map[string]string{
		"WATCH_NAMESPACE":      "ns-a",
		"WATCH_LABEL_SELECTOR": "app=test",
	})

	_, err := GetCacheOptionsWithWatchNamespace()
	if err == nil {
		t.Fatal("expected error when both WATCH_NAMESPACE and WATCH_LABEL_SELECTOR are set")
	}
}

func TestGetCacheOptionsWithWatchNamespace_InvalidLabelSelector(t *testing.T) {
	setEnvs(t, map[string]string{
		"WATCH_NAMESPACE":      "",
		"WATCH_LABEL_SELECTOR": "!!!invalid",
	})

	_, err := GetCacheOptionsWithWatchNamespace()
	if err == nil {
		t.Fatal("expected error for invalid label selector")
	}
}
