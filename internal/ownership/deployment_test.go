/*
Copyright 2026 The PodHandoff Authors.

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

package ownership

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPodBelongsToDeployment(t *testing.T) {
	const testNamespace = "test"

	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	deploymentUID := types.UID("deployment-uid")
	replicaSetUID := types.UID("replicaset-uid")
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: testNamespace, UID: deploymentUID},
	}
	replicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "target-rs", Namespace: testNamespace, UID: replicaSetUID,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "Deployment",
				Name: deployment.Name, UID: deploymentUID, Controller: ptr.To(true),
			}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "target-pod", Namespace: testNamespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "ReplicaSet",
				Name: replicaSet.Name, UID: replicaSetUID, Controller: ptr.To(true),
			}},
		},
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deployment, replicaSet, pod).Build()

	owned, err := PodBelongsToDeployment(context.Background(), reader, pod, deployment)
	if err != nil {
		t.Fatal(err)
	}
	if !owned {
		t.Fatal("expected Pod -> ReplicaSet -> Deployment owner chain to match")
	}

	other := deployment.DeepCopy()
	other.Name = "other"
	other.UID = types.UID("other-uid")
	owned, err = PodBelongsToDeployment(context.Background(), reader, pod, other)
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Fatal("an overlapping selector or name must not replace owner UID proof")
	}

	bare := pod.DeepCopy()
	bare.Name = "bare"
	bare.OwnerReferences = nil
	owned, err = PodBelongsToDeployment(context.Background(), reader, bare, deployment)
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Fatal("a Pod without a controller-owner chain must not match")
	}
}
