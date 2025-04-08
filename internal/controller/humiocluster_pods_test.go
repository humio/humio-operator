package controller

import (
	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"testing"
)

func TestHumioClusterReconciler_podsMatchUserDefinedFields(t *testing.T) {
	type fields struct {
		BaseLogger logr.Logger
		Log        logr.Logger
	}
	type args struct {
		hnp        *HumioNodePool
		pod        corev1.Pod
		desiredPod corev1.Pod
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name: "pods match",
			fields: fields{
				BaseLogger: logr.Logger{},
				Log:        logr.Logger{},
			},
			args: args{
				hnp:        &HumioNodePool{},
				pod:        corev1.Pod{},
				desiredPod: corev1.Pod{},
			},
			want: true,
		},
		{
			name: "pods match because node spec has not been modified",
			fields: fields{
				BaseLogger: logr.Logger{},
				Log:        logr.Logger{},
			},
			args: args{
				hnp: &HumioNodePool{},
				pod: corev1.Pod{Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc-1",
								},
							},
						},
					},
				}},
				desiredPod: corev1.Pod{Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc-2",
								},
							},
						},
					},
				}},
			},
			want: true,
		},
		{
			name: "pods do not match because node spec has been modified",
			fields: fields{
				BaseLogger: logr.Logger{},
				Log:        logr.Logger{},
			},
			args: args{
				hnp: &HumioNodePool{
					humioNodeSpec: humiov1alpha1.HumioNodeSpec{
						DataVolumePersistentVolumeClaimSpecTemplate: corev1.PersistentVolumeClaimSpec{
							DataSource: &corev1.TypedLocalObjectReference{
								Kind: "PersistentVolumeClaim",
								Name: "pvc-2",
							},
						},
					},
				},
				pod: corev1.Pod{Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: "/data",
									Name:      "data",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc-1",
								},
							},
						},
					},
				}},
				desiredPod: corev1.Pod{Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "pvc-2",
								},
							},
						},
					},
				}},
			},
			want: false,
		},
		{
			name: "pods match affinity",
			fields: fields{
				BaseLogger: logr.Logger{},
				Log:        logr.Logger{},
			},
			args: args{
				hnp: &HumioNodePool{
					humioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Affinity: corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchFields: []corev1.NodeSelectorRequirement{
												{Key: "foo", Operator: corev1.NodeSelectorOpIn, Values: []string{"bar"}},
											},
										},
									},
								},
							},
						},
					}},
				pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchFields: []corev1.NodeSelectorRequirement{
												{Key: "foo", Operator: corev1.NodeSelectorOpIn, Values: []string{"bar"}},
											},
										},
									},
								},
							},
						},
					},
				},
				desiredPod: corev1.Pod{
					Spec: corev1.PodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchFields: []corev1.NodeSelectorRequirement{
												{Key: "foo", Operator: corev1.NodeSelectorOpIn, Values: []string{"bar"}},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "pods do not match affinity",
			fields: fields{
				BaseLogger: logr.Logger{},
				Log:        logr.Logger{},
			},
			args: args{
				hnp: &HumioNodePool{
					humioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Affinity: corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchFields: []corev1.NodeSelectorRequirement{
												{Key: "foo", Operator: corev1.NodeSelectorOpIn, Values: []string{"bar"}},
											},
										},
									},
								},
							},
						},
					}},
				pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchFields: []corev1.NodeSelectorRequirement{
												{Key: "foo", Operator: corev1.NodeSelectorOpIn, Values: []string{"bar2"}},
											},
										},
									},
								},
							},
						},
					},
				},
				desiredPod: corev1.Pod{
					Spec: corev1.PodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchFields: []corev1.NodeSelectorRequirement{
												{Key: "foo", Operator: corev1.NodeSelectorOpIn, Values: []string{"bar"}},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "pods do not match",
			fields: fields{
				BaseLogger: logr.Logger{},
				Log:        logr.Logger{},
			},
			args: args{
				hnp: &HumioNodePool{
					humioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Affinity: corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchFields: []corev1.NodeSelectorRequirement{
												{Key: "foo", Operator: corev1.NodeSelectorOpIn, Values: []string{"bar"}},
											},
										},
									},
								},
							},
						},
					}},
				pod: corev1.Pod{
					Spec: corev1.PodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchFields: []corev1.NodeSelectorRequirement{
												{Key: "foo", Operator: corev1.NodeSelectorOpIn, Values: []string{"bar"}},
											},
										},
									},
								},
							},
						},
					},
				},
				desiredPod: corev1.Pod{},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &HumioClusterReconciler{
				BaseLogger: tt.fields.BaseLogger,
				Log:        tt.fields.Log,
			}
			if got, v := r.podsMatchUserDefinedFields(tt.args.hnp, tt.args.pod, tt.args.desiredPod); got != tt.want {
				t.Errorf("podsMatchUserDefinedFields() = %v, want %v, value %v", got, tt.want, v)
			}
		})
	}
}
