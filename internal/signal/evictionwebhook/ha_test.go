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

package evictionwebhook

import (
	"context"
	"testing"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	appsv1alpha1 "github.com/cozyGarage/podhandoff/api/v1alpha1"
	"github.com/cozyGarage/podhandoff/internal/signal"
)

const fixtureTarget = "work"

func TestEvictionSignalIsSharedAndDoomedStandInIsRejected(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: fixtureTarget, Namespace: "ns", UID: "dep", Annotations: map[string]string{baseReplicasAnnotation: "1"}},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(2)), Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": fixtureTarget}}},
	}
	cr := &appsv1alpha1.PodHandoff{ObjectMeta: metav1.ObjectMeta{Name: "protect", Namespace: "ns"}, Spec: appsv1alpha1.PodHandoffSpec{TargetRef: appsv1alpha1.TargetReference{Name: fixtureTarget}}}
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns", UID: "rs", OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: fixtureTarget, UID: "dep", Controller: ptr.To(true)}}}}
	victim := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "victim", Namespace: "ns", Labels: map[string]string{"app": fixtureTarget}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "rs", UID: "rs", Controller: ptr.To(true)}}}, Spec: corev1.PodSpec{NodeName: "node1"}}
	standIn := victim.DeepCopy()
	standIn.Name = "stand-in"
	standIn.Spec.NodeName = "node2"
	standIn.Annotations = map[string]string{signal.EvictionUntilAnnotation: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}
	standIn.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dep, cr, rs, victim, standIn).Build()
	standby := &Adapter{reader: reader, registry: signal.NewRegistry()}
	resp := standby.Handle(context.Background(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Name: "victim", Namespace: "ns"}})
	if resp.Allowed {
		t.Fatal("a ready stand-in on a doomed node must not release eviction")
	}
	var updated corev1.Pod
	if err := reader.Get(context.Background(), client.ObjectKeyFromObject(victim), &updated); err != nil {
		t.Fatal(err)
	}
	if _, ok := signal.EvictionDoom(&updated, time.Now()); !ok {
		t.Fatal("eviction signal must be persisted for the leader to observe")
	}
}
