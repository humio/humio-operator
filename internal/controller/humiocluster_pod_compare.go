package controller

import (
	"github.com/google/go-cmp/cmp"
	"github.com/humio/humio-operator/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
)

// PodComparisonType represents different types of pod comparisons
type PodMismatchSeverityType string
type PodMismatchType string

const (
	PodMismatchSeverityCritical PodMismatchSeverityType = "PodMismatchSeverityCritical"
	PodMismatchSeverityWarning  PodMismatchSeverityType = "PodMismatchSeverityWarning"
	PodMismatchVersion          PodMismatchType         = "PodMismatchVersion"
	PodMismatchAnnotation       PodMismatchType         = "PodMismatchAnnotation"
)

// PodComparison holds the pods to compare and comparison results
type PodComparison struct {
	currentPod            *corev1.Pod
	desiredPod            *corev1.Pod
	currentHumioContainer *corev1.Container
	desiredHumioContainer *corev1.Container
	result                PodComparisionResult
}

type VersionMismatch struct {
	To   *HumioVersion
	From *HumioVersion
}

type PodComparisionResult struct {
	diff                             string
	podAnnotationMismatches          []string
	podEnvironmentVariableMismatches []string
	humioContainerMismatch           *VersionMismatch
	mismatchSeverity                 PodMismatchSeverityType
	mismatchType                     PodMismatchType
}

// NewPodComparison creates a new PodComparison instance
func NewPodComparison(hnp *HumioNodePool, current *corev1.Pod, desired *corev1.Pod) (*PodComparison, error) {
	currentPodCopy := current.DeepCopy()
	desiredPodCopy := desired.DeepCopy()

	sanitizedCurrentPod := sanitizePod(hnp, currentPodCopy)
	sanitizedDesiredPod := sanitizePod(hnp, desiredPodCopy)

	pc := &PodComparison{
		currentPod: sanitizedCurrentPod,
		desiredPod: sanitizedDesiredPod,
		result: PodComparisionResult{
			diff:                   cmp.Diff(sanitizedCurrentPod.Spec, sanitizedDesiredPod.Spec),
			humioContainerMismatch: &VersionMismatch{},
		},
	}

	currentHumioContainerIdx, desiredHumioContainerIdx, err := pc.getContainerIndexes()
	if err != nil {
		return pc, err
	}
	pc.currentHumioContainer = &pc.currentPod.Spec.Containers[currentHumioContainerIdx]
	pc.desiredHumioContainer = &pc.desiredPod.Spec.Containers[desiredHumioContainerIdx]

	pc.processAnnotations()
	pc.processEnvironmentVariables()
	pc.processHumioContainerImages()
	return pc, nil
}

func (pc *PodComparison) Matches() bool {
	return !pc.HasCriticalMismatch() && !pc.HasWarningMismatch()
}

func (pc *PodComparison) Diff() string {
	return pc.result.diff
}

func (pc *PodComparison) MismatchedAnnotations() []string {
	return pc.result.podAnnotationMismatches
}

func (pc *PodComparison) HasCriticalMismatch() bool {
	return pc.result.mismatchSeverity == PodMismatchSeverityCritical
}

func (pc *PodComparison) HasWarningMismatch() bool {
	return pc.result.mismatchSeverity == PodMismatchSeverityWarning
}

func (pc *PodComparison) processHumioContainerImages() {
	if pc.currentHumioContainer.Image != pc.desiredHumioContainer.Image {
		pc.setDoesNotMatch(PodMismatchVersion, PodMismatchSeverityCritical)
		pc.setVersionMismatch(
			HumioVersionFromString(pc.currentHumioContainer.Image),
			HumioVersionFromString(pc.desiredHumioContainer.Image),
		)
	}
}

// processEnvironmentVariables returns a list of environment variables which do not match. we don't set
// PodMismatchSeverityType here and instead rely on the annotations mismatches. this is because some environment
// variables may be excluded from the pod hash because they are defaults managed by the operator.
// we are only returning environment variables here in case there is specific restart behavior that needs to be
// evaluated for a given environment variable. for example, see env vars defined in
// environmentVariablesRequiringSimultaneousRestartRestart
func (pc *PodComparison) processEnvironmentVariables() {
	currentEnvVars := make(map[string]string)
	desiredEnvVars := make(map[string]string)

	for _, env := range pc.currentHumioContainer.Env {
		currentEnvVars[env.Name] = EnvVarValue(pc.currentHumioContainer.Env, env.Name)
	}

	for _, env := range pc.desiredHumioContainer.Env {
		desiredEnvVars[env.Name] = EnvVarValue(pc.desiredHumioContainer.Env, env.Name)
	}

	for envName, desiredValue := range desiredEnvVars {
		currentValue, exists := currentEnvVars[envName]
		if !exists || currentValue != desiredValue {
			pc.result.podEnvironmentVariableMismatches = append(pc.result.podEnvironmentVariableMismatches, envName)
		}
	}

	for envName := range currentEnvVars {
		if _, exists := desiredEnvVars[envName]; !exists {
			pc.result.podEnvironmentVariableMismatches = append(pc.result.podEnvironmentVariableMismatches, envName)
		}
	}
}

func (pc *PodComparison) getContainerIndexes() (int, int, error) {
	currentHumioContainerIdx, err := kubernetes.GetContainerIndexByName(*pc.currentPod, HumioContainerName)
	if err != nil {
		return -1, -1, err
	}
	desiredHumioContainerIdx, err := kubernetes.GetContainerIndexByName(*pc.desiredPod, HumioContainerName)
	if err != nil {
		return -1, -1, err
	}
	return currentHumioContainerIdx, desiredHumioContainerIdx, nil
}

func (pc *PodComparison) MismatchedEnvironmentVariables() []string {
	return pc.result.podEnvironmentVariableMismatches
}

func (pc *PodComparison) MismatchedHumioVersions() (bool, *VersionMismatch) {
	if pc.result.mismatchType == PodMismatchVersion {
		return true, pc.result.humioContainerMismatch
	}
	return false, pc.result.humioContainerMismatch
}

func (pc *PodComparison) setDoesNotMatch(mismatchType PodMismatchType, mismatchSeverity PodMismatchSeverityType) {
	// Don't downgrade from Critical to Warning
	if pc.result.mismatchSeverity == PodMismatchSeverityCritical && mismatchSeverity == PodMismatchSeverityWarning {
		return
	}

	pc.result.mismatchType = mismatchType
	pc.result.mismatchSeverity = mismatchSeverity
}

func (pc *PodComparison) processAnnotations() {
	for _, annotation := range []string{
		BootstrapTokenHashAnnotation,
		PodHashAnnotation,
		PodRevisionAnnotation,
		BootstrapTokenHashAnnotation,
		EnvVarSourceHashAnnotation,
		CertificateHashAnnotation,
	} {
		if !pc.compareAnnotation(annotation) {
			pc.setDoesNotMatch(PodMismatchAnnotation, PodMismatchSeverityCritical)
			pc.result.podAnnotationMismatches = append(pc.result.podAnnotationMismatches, annotation)
		}
	}
	for _, annotation := range []string{
		PodOperatorManagedFieldsHashAnnotation,
	} {
		if !pc.compareAnnotation(annotation) {
			pc.setDoesNotMatch(PodMismatchAnnotation, PodMismatchSeverityWarning)
			pc.result.podAnnotationMismatches = append(pc.result.podAnnotationMismatches, annotation)
		}
	}
}

func (pc *PodComparison) compareAnnotation(annotation string) bool {
	return pc.currentPod.Annotations[annotation] == pc.desiredPod.Annotations[annotation]
}

func (pc *PodComparison) setVersionMismatch(from, to *HumioVersion) {
	if pc.result.humioContainerMismatch == nil {
		pc.result.humioContainerMismatch = &VersionMismatch{}
	}
	pc.result.humioContainerMismatch.From = from
	pc.result.humioContainerMismatch.To = to
}
