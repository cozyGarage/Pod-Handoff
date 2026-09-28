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

package controller

import (
	"testing"

	appsv1alpha1 "github.com/cozyGarage/podhandoff/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStatusEqualIncludesObservedGeneration(t *testing.T) {
	a := appsv1alpha1.PodHandoffStatus{Conditions: []metav1.Condition{{Type: "Protected", Status: metav1.ConditionTrue, Reason: "EvictionHold", ObservedGeneration: 1}}}
	b := *a.DeepCopy()
	b.Conditions[0].ObservedGeneration = 2
	if statusEqual(&a, &b) {
		t.Fatal("different observed generations must produce a status update")
	}
}
