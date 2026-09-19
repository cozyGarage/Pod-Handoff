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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodBelongsToDeployment proves the normal Pod -> ReplicaSet -> Deployment
// controller-owner chain. Matching labels alone are deliberately insufficient:
// selectors can overlap, and an unrelated Pod must never affect readiness or
// eviction decisions for a protected Deployment.
func PodBelongsToDeployment(
	ctx context.Context,
	reader client.Reader,
	pod *corev1.Pod,
	deployment *appsv1.Deployment,
) (bool, error) {
	podOwner := metav1.GetControllerOf(pod)
	if podOwner == nil || podOwner.APIVersion != appsv1.SchemeGroupVersion.String() || podOwner.Kind != "ReplicaSet" {
		return false, nil
	}

	var replicaSet appsv1.ReplicaSet
	if err := reader.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: podOwner.Name}, &replicaSet); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if podOwner.UID != "" && replicaSet.UID != podOwner.UID {
		return false, nil
	}

	replicaSetOwner := metav1.GetControllerOf(&replicaSet)
	if replicaSetOwner == nil || replicaSetOwner.APIVersion != appsv1.SchemeGroupVersion.String() ||
		replicaSetOwner.Kind != "Deployment" || replicaSetOwner.Name != deployment.Name {
		return false, nil
	}
	if deployment.UID != "" && replicaSetOwner.UID != deployment.UID {
		return false, nil
	}
	return true, nil
}
