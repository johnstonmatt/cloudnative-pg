/*
Copyright The CloudNativePG Contributors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/postgres"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/specs"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pod upgrade", func() {
	var cluster apiv1.Cluster

	BeforeEach(func() {
		cluster = apiv1.Cluster{
			Spec: apiv1.ClusterSpec{
				ImageName: "postgres:13.0",
			},
		}
	})

	It("will not require a restart for just created Pods", func() {
		pod := specs.PodWithExistingStorage(cluster, 1)

		needRestart, reason := isPodNeedingRestart(&cluster, postgres.PostgresqlStatus{Pod: pod})
		Expect(needRestart).To(BeFalse())
		Expect(reason).To(BeEmpty())
	})

	It("checks when we are running a different image name", func() {
		pod := specs.PodWithExistingStorage(cluster, 1)
		pod.Spec.Containers[0].Image = "postgres:13.1"
		oldImage, newImage, err := isPodNeedingUpgradedImage(&cluster, *pod)
		Expect(err).NotTo(HaveOccurred())
		Expect(oldImage).NotTo(BeEmpty())
		Expect(newImage).NotTo(BeEmpty())
	})

	It("checks when a restart has been scheduled on the cluster", func() {
		pod := specs.PodWithExistingStorage(cluster, 1)
		clusterRestart := cluster
		clusterRestart.Annotations = make(map[string]string)
		clusterRestart.Annotations[specs.ClusterRestartAnnotationName] = "now"

		needRestart, reason := isPodNeedingRestart(&clusterRestart, postgres.PostgresqlStatus{Pod: pod})
		Expect(needRestart).To(BeTrue())
		Expect(reason).ToNot(BeEmpty())

		needRestart, reason = isPodNeedingRestart(&cluster, postgres.PostgresqlStatus{Pod: pod})
		Expect(needRestart).To(BeFalse())
		Expect(reason).To(BeEmpty())
	})

	It("checks when a restart is being needed by PostgreSQL", func() {
		pod := specs.PodWithExistingStorage(cluster, 1)

		needRestart, reason := isPodNeedingRestart(&cluster, postgres.PostgresqlStatus{Pod: pod})
		Expect(needRestart).To(BeFalse())
		Expect(reason).To(BeEmpty())

		needRestart, reason = isPodNeedingRestart(&cluster,
			postgres.PostgresqlStatus{
				Pod:            pod,
				PendingRestart: true,
			})
		Expect(needRestart).To(BeTrue())
		Expect(reason).ToNot(BeEmpty())
	})

	It("checks when a rollout is being needed for any reason", func() {
		pod := specs.PodWithExistingStorage(cluster, 1)
		status := postgres.PostgresqlStatus{Pod: pod, PendingRestart: true}
		needRollout, inplacePossible, reason := IsPodNeedingRollout(status, &cluster)
		Expect(needRollout).To(BeFalse())
		Expect(inplacePossible).To(BeFalse())
		Expect(reason).To(BeEmpty())

		status.IsPodReady = true
		needRollout, inplacePossible, reason = IsPodNeedingRollout(status, &cluster)
		Expect(needRollout).To(BeTrue())
		Expect(inplacePossible).To(BeFalse())
		Expect(reason).To(BeEmpty())

		status.ExecutableHash = "test_hash"
		needRollout, inplacePossible, reason = IsPodNeedingRollout(status, &cluster)
		Expect(needRollout).To(BeTrue())
		Expect(inplacePossible).To(BeTrue())
		Expect(reason).To(BeEquivalentTo("configuration needs a restart to apply some configuration changes"))
	})

	It("should trigger a rollout when the scheduler changes", func() {
		pod := specs.PodWithExistingStorage(cluster, 1)
		cluster.Spec.SchedulerName = "newScheduler"

		rollout, reason := isPodNeedingUpdatedScheduler(&cluster, *pod)
		Expect(rollout).To(BeTrue())
		Expect(reason).ToNot(BeEmpty())
	})

	When("there's a custom environment variable set", func() {
		It("detects when a new custom environment variable is set", func() {
			pod := specs.PodWithExistingStorage(cluster, 1)

			cluster := cluster.DeepCopy()
			cluster.Spec.Env = []corev1.EnvVar{
				{
					Name:  "TEST",
					Value: "test",
				},
			}

			needRollout, _ := isPodNeedingUpdatedEnvironment(*cluster, *pod)
			Expect(needRollout).To(BeTrue())
		})
	})
})

var _ = Describe("Test isPodNeedingUpdatedTopology", func() {
	var cluster *apiv1.Cluster
	var pod corev1.Pod

	BeforeEach(func() {
		topology := corev1.TopologySpreadConstraint{
			MaxSkew:           1,
			TopologyKey:       "zone",
			WhenUnsatisfiable: corev1.DoNotSchedule,
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test-app"},
			},
		}
		cluster = &apiv1.Cluster{
			Spec: apiv1.ClusterSpec{
				TopologySpreadConstraints: []corev1.TopologySpreadConstraint{topology},
			},
		}
		pod = corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod",
			},
			Spec: corev1.PodSpec{
				TopologySpreadConstraints: []corev1.TopologySpreadConstraint{topology},
			},
		}
	})

	It("should return false when the cluster and pod have the same TopologySpreadConstraints", func() {
		needsUpdate, reason := isPodNeedingUpdatedTopology(cluster, pod)
		Expect(needsUpdate).To(BeFalse())
		Expect(reason).To(BeEmpty())
	})

	It("should return true when the cluster and pod do not have the same TopologySpreadConstraints", func() {
		pod.Spec.TopologySpreadConstraints[0].MaxSkew = 2
		needsUpdate, reason := isPodNeedingUpdatedTopology(cluster, pod)
		Expect(needsUpdate).To(BeTrue())
		Expect(reason).ToNot(BeEmpty())
	})

	It("should return true when the LabelSelector maps are different", func() {
		pod.Spec.TopologySpreadConstraints[0].LabelSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{"app": "different-app"},
		}

		needsUpdate, reason := isPodNeedingUpdatedTopology(cluster, pod)
		Expect(needsUpdate).To(BeTrue())
		Expect(reason).ToNot(BeEmpty())
	})

	It("should return true when TopologySpreadConstraints is nil in one of the objects", func() {
		pod.Spec.TopologySpreadConstraints = nil

		needsUpdate, reason := isPodNeedingUpdatedTopology(cluster, pod)
		Expect(needsUpdate).To(BeTrue())
		Expect(reason).ToNot(BeEmpty())
	})

	It("should return false if both are nil", func() {
		cluster.Spec.TopologySpreadConstraints = nil
		pod.Spec.TopologySpreadConstraints = nil

		needsUpdate, reason := isPodNeedingUpdatedTopology(cluster, pod)
		Expect(needsUpdate).To(BeFalse())
		Expect(reason).To(BeEmpty())
	})
})
