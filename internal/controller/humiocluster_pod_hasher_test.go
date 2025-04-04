package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("PodHasher", func() {
	Context("When calculating pod hashes", func() {
		Context("With PodHashOnlyManagedFields", func() {
			It("Should only consider managed fields for image", func() {
				pod1 := &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "container1",
								Image: "image1",
							},
						},
					},
				}

				pod2 := &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:            "container1",
								Image:           "image1",
								ImagePullPolicy: corev1.PullNever,
							},
						},
					},
				}

				managedFieldsPod := &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "container1",
								Image: "image1",
							},
						},
					},
				}

				h1 := &PodHasher{
					pod:              pod1,
					managedFieldsPod: managedFieldsPod,
				}
				h2 := &PodHasher{
					pod:              pod2,
					managedFieldsPod: managedFieldsPod,
				}

				pod1Hash, err := h1.PodHashOnlyManagedFields()
				Expect(err).NotTo(HaveOccurred())

				pod2Hash, err := h2.PodHashOnlyManagedFields()
				Expect(err).NotTo(HaveOccurred())

				Expect(pod1Hash).To(Equal(pod2Hash))
			})

			It("Should only consider managed fields for env vars", func() {
				pod1 := &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "container1",
								Env: []corev1.EnvVar{
									{
										Name:  "foo",
										Value: "bar",
									},
								},
							},
						},
					},
				}

				pod2 := &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "container1",
								Env: []corev1.EnvVar{
									{
										Name:  "foo",
										Value: "bar",
									},
									{
										Name:  "foo2",
										Value: "bar2",
									},
								},
							},
						},
					},
				}

				managedFieldsPod := &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "container1",
								Env: []corev1.EnvVar{
									{
										Name:  "foo",
										Value: "bar",
									},
								},
							},
						},
					},
				}

				h1 := &PodHasher{
					pod:              pod1,
					managedFieldsPod: managedFieldsPod,
				}
				h2 := &PodHasher{
					pod:              pod2,
					managedFieldsPod: managedFieldsPod,
				}

				pod1Hash, err := h1.PodHashOnlyManagedFields()
				Expect(err).NotTo(HaveOccurred())

				pod2Hash, err := h2.PodHashOnlyManagedFields()
				Expect(err).NotTo(HaveOccurred())

				Expect(pod1Hash).To(Equal(pod2Hash))
			})
		})

		Context("With PodHashMinusManagedFields", func() {
			It("Should only contain unmanaged fields for image", func() {
				pod1 := &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:            "container1",
								Image:           "image1",
								ImagePullPolicy: corev1.PullNever,
							},
						},
					},
				}

				pod2 := &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:            "container1",
								Image:           "image2",
								ImagePullPolicy: corev1.PullNever,
							},
						},
					},
				}

				managedFieldsPod := &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "container1",
								Image: "image1",
							},
						},
					},
				}

				h1 := &PodHasher{
					pod:              pod1,
					managedFieldsPod: managedFieldsPod,
				}
				h2 := &PodHasher{
					pod:              pod2,
					managedFieldsPod: managedFieldsPod,
				}

				pod1Hash, err := h1.PodHashMinusManagedFields()
				Expect(err).NotTo(HaveOccurred())

				pod2Hash, err := h2.PodHashMinusManagedFields()
				Expect(err).NotTo(HaveOccurred())

				Expect(pod1Hash).To(Equal(pod2Hash))
			})

			It("Should only contain unmanaged fields for env vars", func() {
				pod1 := &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "container1",
								Env: []corev1.EnvVar{
									{
										Name:  "foo",
										Value: "bar",
									},
									{
										Name:  "foo2",
										Value: "bar2",
									},
								},
							},
						},
					},
				}

				pod2 := &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "container1",
								Env: []corev1.EnvVar{
									{
										Name:  "foo",
										Value: "differentBar",
									},
									{
										Name:  "foo2",
										Value: "bar2",
									},
								},
							},
						},
					},
				}

				managedFieldsPod := &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "container1",
								Env: []corev1.EnvVar{
									{
										Name:  "foo",
										Value: "bar",
									},
								},
							},
						},
					},
				}

				h1 := &PodHasher{
					pod:              pod1,
					managedFieldsPod: managedFieldsPod,
				}
				h2 := &PodHasher{
					pod:              pod2,
					managedFieldsPod: managedFieldsPod,
				}

				pod1Hash, err := h1.PodHashMinusManagedFields()
				Expect(err).NotTo(HaveOccurred())

				pod2Hash, err := h2.PodHashMinusManagedFields()
				Expect(err).NotTo(HaveOccurred())

				Expect(pod1Hash).To(Equal(pod2Hash))
			})
		})
	})
})
