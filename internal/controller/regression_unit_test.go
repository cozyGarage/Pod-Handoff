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
	"context"
	"testing"
	"time"

	appsv1alpha1 "github.com/cozyGarage/podhandoff/api/v1alpha1"
	"github.com/cozyGarage/podhandoff/internal/signal"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	regressionTarget = "work"
	doomedNode       = "doomed"
	laterNode        = "later"
)

func regressionFixture(t *testing.T, rollout bool) (*PodHandoffReconciler, *appsv1alpha1.PodHandoff, *appsv1.Deployment) {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	cr := &appsv1alpha1.PodHandoff{
		ObjectMeta: metav1.ObjectMeta{Name: "protect", Namespace: "ns", Finalizers: []string{PodHandoffFinalizer}},
		Spec:       appsv1alpha1.PodHandoffSpec{TargetRef: appsv1alpha1.TargetReference{Name: regressionTarget}, ReadinessDeadlineSeconds: ptr.To(int32(1))},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: regressionTarget, Namespace: "ns", UID: "dep", Generation: 1},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(1)), Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": regressionTarget}}},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 0},
	}
	if rollout {
		dep.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue, Reason: deploymentRolloutReason}}
	}
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns", UID: "rs", OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: targetDeploymentKind, Name: regressionTarget, UID: "dep", Controller: ptr.To(true)}}}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "victim", Namespace: "ns", Labels: map[string]string{"app": regressionTarget}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "rs", UID: "rs", Controller: ptr.To(true)}}}, Spec: corev1.PodSpec{NodeName: doomedNode}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(cr, dep).WithObjects(cr, dep, rs, pod).Build()
	reg := signal.NewRegistry()
	reg.Set(signal.NodeDoom{Node: doomedNode, Source: "test", Class: signal.VoluntaryUnbounded})
	return &PodHandoffReconciler{Client: c, Registry: reg}, cr, dep
}

func TestExpiredHoldReleasesDuringRollout(t *testing.T) {
	r, cr, _ := regressionFixture(t, true)
	cr.Status.Phase = appsv1alpha1.PhaseSurging
	cr.Status.BlockedSince = &metav1.Time{Time: time.Now().Add(-time.Minute)}
	if err := r.Status().Update(context.Background(), cr); err != nil {
		t.Fatal(err)
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cr)})
	if err != nil {
		t.Fatal(err)
	}
	var got appsv1alpha1.PodHandoff
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(cr), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != appsv1alpha1.PhaseRelaxed {
		t.Fatalf("expired hold phase = %q, want Relaxed", got.Status.Phase)
	}
}

func TestFinalizerKeepsSharedSurgeForOtherProtector(t *testing.T) {
	r, cr, dep := regressionFixture(t, false)
	dep.Spec.Replicas = ptr.To(int32(2))
	dep.Annotations = map[string]string{AnnotationBaseReplicas: "1"}
	if err := r.Update(context.Background(), dep); err != nil {
		t.Fatal(err)
	}
	other := cr.DeepCopy()
	other.Name = "also-protects"
	other.ResourceVersion = ""
	other.UID = ""
	if err := r.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if err := r.finalize(context.Background(), cr, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cr)}); err != nil {
		t.Fatal(err)
	}
	var got appsv1.Deployment
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(dep), &got); err != nil {
		t.Fatal(err)
	}
	if *got.Spec.Replicas != 2 || got.Annotations[AnnotationBaseReplicas] != "1" {
		t.Fatalf("remaining protector lost surge state: replicas=%d annotations=%v", *got.Spec.Replicas, got.Annotations)
	}
}

func TestAssessmentChoosesEarliestInvoluntaryDeadline(t *testing.T) {
	r, cr, dep := regressionFixture(t, false)
	soon, later := time.Now().Add(30*time.Second), time.Now().Add(5*time.Minute)
	r.Registry.Set(signal.NodeDoom{Node: doomedNode, Source: "soon", Class: signal.Involuntary, Deadline: &soon})
	r.Registry.Set(signal.NodeDoom{Node: laterNode, Source: laterNode, Class: signal.Involuntary, Deadline: &later})
	var pod corev1.Pod
	if err := r.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "victim"}, &pod); err != nil {
		t.Fatal(err)
	}
	pod.Name, pod.Spec.NodeName = "later-victim", laterNode
	pod.ResourceVersion = ""
	pod.UID = ""
	if err := r.Create(context.Background(), &pod); err != nil {
		t.Fatal(err)
	}
	a, err := r.assessPods(context.Background(), cr, dep)
	if err != nil {
		t.Fatal(err)
	}
	if a.worstDoom == nil || a.worstDoom.Deadline == nil || !a.worstDoom.Deadline.Equal(soon) {
		t.Fatalf("selected deadline = %v, want %v", a.worstDoom, soon)
	}
}
