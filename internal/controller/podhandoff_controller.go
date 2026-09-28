/*
Copyright 2026 The Understudy Authors.
Modifications Copyright 2026 The PodHandoff Authors.

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
	"fmt"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	appsv1alpha1 "github.com/cozyGarage/podhandoff/api/v1alpha1"
	"github.com/cozyGarage/podhandoff/internal/metrics"
	"github.com/cozyGarage/podhandoff/internal/ownership"
	"github.com/cozyGarage/podhandoff/internal/signal"
)

const (
	AnnotationBaseReplicas         = "podhandoff.io/base-replicas"
	AnnotationPreviousDeletionCost = "podhandoff.io/previous-pod-deletion-cost"
	LabelOwned                     = "podhandoff.io/owned"
	PodDeletionCostAnnotation      = "controller.kubernetes.io/pod-deletion-cost"
	PodHandoffFinalizer            = "apps.podhandoff.io/cleanup"
	doomedPodDeletionCost          = "-1000"

	relaxReasonTTL      = "ttl"
	relaxReasonDeadline = "deadline"
	relaxReasonMode     = "mode"

	activeRequeue = 5 * time.Second
)

type PodHandoffReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
	Registry *signal.Registry

	OperatorNamespace  string
	OperatorDeployment string
}

type podAssessment struct {
	doomed    []*corev1.Pod
	worstDoom *signal.NodeDoom
	viable    int
	all       []corev1.Pod
}

// +kubebuilder:rbac:groups=apps.podhandoff.io,resources=podhandoffs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.podhandoff.io,resources=podhandoffs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.podhandoff.io,resources=podhandoffs/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

func (r *PodHandoffReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr appsv1alpha1.PodHandoff
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			metrics.UntrackBlocked(req.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !cr.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.finalize(ctx, &cr, req)
	}
	if !controllerutil.ContainsFinalizer(&cr, PodHandoffFinalizer) {
		patched := cr.DeepCopy()
		controllerutil.AddFinalizer(patched, PodHandoffFinalizer)
		return ctrl.Result{}, r.Patch(ctx, patched, client.MergeFrom(&cr))
	}

	status := *cr.Status.DeepCopy()

	if kind := cr.Spec.TargetRef.Kind; kind != "" && kind != "Deployment" {
		return r.markUnsupported(ctx, &cr, status,
			fmt.Sprintf("target kind %q is not supported (only Deployment); skipping", kind))
	}
	if cr.Namespace == r.OperatorNamespace && cr.Spec.TargetRef.Name == r.OperatorDeployment && r.OperatorDeployment != "" {
		return r.markUnsupported(ctx, &cr, status,
			"refusing to manage the PodHandoff operator itself (self-exclusion fail-safe)")
	}

	var dep appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: cr.Namespace, Name: cr.Spec.TargetRef.Name}, &dep); err != nil {
		if apierrors.IsNotFound(err) {
			setCond(&status, &cr, appsv1alpha1.ConditionTargetSupported, metav1.ConditionFalse,
				"TargetNotFound", fmt.Sprintf("Deployment %q not found", cr.Spec.TargetRef.Name))
			status.Phase = appsv1alpha1.PhaseIdle
			return ctrl.Result{}, r.patchStatus(ctx, &cr, status)
		}
		return ctrl.Result{}, err
	}
	setCond(&status, &cr, appsv1alpha1.ConditionTargetSupported, metav1.ConditionTrue, "Deployment", "target resolved")

	surgeActive := false
	base := int32(1)
	if dep.Spec.Replicas != nil {
		base = *dep.Spec.Replicas
	}
	if v, ok := dep.Annotations[AnnotationBaseReplicas]; ok {
		if parsed, err := strconv.ParseInt(v, 10, 32); err == nil {
			base = int32(parsed)
			surgeActive = true
		}
	}

	r.setStrategyCondition(&status, &cr, &dep, base)

	assessment, err := r.assessPods(ctx, &cr, &dep)
	if err != nil {
		return ctrl.Result{}, err
	}
	surgeReady := assessment.viable >= int(base)

	mode := cr.HoldModeOrDefault()
	if status.Mode == "" && cr.UsesDeprecatedHoldMode() {
		r.event(&cr, corev1.EventTypeWarning, "DeprecatedField", "Configure",
			"spec.hostageMode is deprecated and will be removed; rename it to spec.holdMode")
	}
	status.Mode = mode
	relaxReason, ttlExpired := r.relaxDecision(&cr, mode, assessment, surgeReady, status.Phase, status.BlockedSince)
	setProtectedCondition(&status, &cr, mode, relaxReason)

	switch {
	case len(assessment.doomed) == 0 && !surgeActive:
		status.Phase = appsv1alpha1.PhaseIdle
		status.BlockedSince = nil
		metrics.UntrackBlocked(req.String())
		setCond(&status, &cr, appsv1alpha1.ConditionSurgeReady, boolToStatus(surgeReady), "Steady", "no disruption in sight")
		return ctrl.Result{}, r.patchStatus(ctx, &cr, status)

	case len(assessment.doomed) == 0 && surgeActive:
		return r.reconcileScaleBack(ctx, &cr, &dep, status, assessment, base, req)

	default:
		return r.reconcileDoomed(ctx, &cr, &dep, status, assessment, base, surgeActive, surgeReady, relaxReason, ttlExpired, req)
	}
}

func (r *PodHandoffReconciler) finalize(ctx context.Context, cr *appsv1alpha1.PodHandoff, req ctrl.Request) error {
	if !controllerutil.ContainsFinalizer(cr, PodHandoffFinalizer) {
		return nil
	}

	remaining, err := r.hasOtherProtector(ctx, cr)
	if err != nil {
		return err
	}
	if !remaining && (cr.Spec.TargetRef.Kind == "" || cr.Spec.TargetRef.Kind == "Deployment") {
		var dep appsv1.Deployment
		err := r.Get(ctx, types.NamespacedName{Namespace: cr.Namespace, Name: cr.Spec.TargetRef.Name}, &dep)
		switch {
		case err == nil:
			assessment, assessErr := r.assessPods(ctx, cr, &dep)
			if assessErr != nil {
				return assessErr
			}
			if clearErr := r.clearDeletionCosts(ctx, assessment.all); clearErr != nil {
				return clearErr
			}
			if rawBase, surged := dep.Annotations[AnnotationBaseReplicas]; surged {
				base, parseErr := strconv.ParseInt(rawBase, 10, 32)
				if parseErr != nil {
					return fmt.Errorf("restore Deployment %s/%s: invalid %s annotation %q: %w",
						dep.Namespace, dep.Name, AnnotationBaseReplicas, rawBase, parseErr)
				}
				if scaleErr := r.scaleTo(ctx, &dep, int32(base)); scaleErr != nil {
					return scaleErr
				}
			}
		case apierrors.IsNotFound(err):
			// The target is already gone, so there is no workload state left to restore.
		default:
			return err
		}
	}

	metrics.UntrackBlocked(req.String())
	patched := cr.DeepCopy()
	controllerutil.RemoveFinalizer(patched, PodHandoffFinalizer)
	return r.Patch(ctx, patched, client.MergeFrom(cr))
}

func (r *PodHandoffReconciler) hasOtherProtector(ctx context.Context, cr *appsv1alpha1.PodHandoff) (bool, error) {
	var list appsv1alpha1.PodHandoffList
	if err := r.List(ctx, &list, client.InNamespace(cr.Namespace)); err != nil {
		return false, err
	}
	for i := range list.Items {
		other := &list.Items[i]
		if other.Name != cr.Name && other.DeletionTimestamp.IsZero() && other.Spec.TargetRef.Name == cr.Spec.TargetRef.Name &&
			(other.Spec.TargetRef.Kind == "" || other.Spec.TargetRef.Kind == "Deployment") {
			return true, nil
		}
	}
	return false, nil
}

func (r *PodHandoffReconciler) assessPods(ctx context.Context, cr *appsv1alpha1.PodHandoff, dep *appsv1.Deployment) (podAssessment, error) {
	var a podAssessment
	selector, err := metav1.LabelSelectorAsSelector(dep.Spec.Selector)
	if err != nil {
		return a, err
	}
	var podList corev1.PodList
	if err := r.List(ctx, &podList, client.InNamespace(cr.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return a, err
	}
	for i := range podList.Items {
		pod := &podList.Items[i]
		owned, err := ownership.PodBelongsToDeployment(ctx, r.Client, pod, dep)
		if err != nil {
			return a, err
		}
		if !owned {
			continue
		}
		a.all = append(a.all, *pod)
		if pod.Spec.NodeName == "" || pod.DeletionTimestamp != nil {
			continue
		}
		if doom, doomed := r.Registry.Lookup(pod.Spec.NodeName); doomed {
			a.doomed = append(a.doomed, pod)
			if a.worstDoom == nil || signal.MoreUrgent(doom, *a.worstDoom) {
				d := doom
				a.worstDoom = &d
			}
			continue
		}
		if doom, doomed := signal.EvictionDoom(pod, time.Now()); doomed {
			a.doomed = append(a.doomed, pod)
			if a.worstDoom == nil || signal.MoreUrgent(doom, *a.worstDoom) {
				d := doom
				a.worstDoom = &d
			}
			continue
		}
		if podReady(pod) {
			a.viable++
		}
	}
	return a, nil
}

func (r *PodHandoffReconciler) relaxDecision(cr *appsv1alpha1.PodHandoff, mode appsv1alpha1.HoldMode,
	a podAssessment, surgeReady bool, previousPhase appsv1alpha1.Phase, blockedSince *metav1.Time) (string, bool) {
	relaxReason := ""
	if a.worstDoom != nil && a.worstDoom.Class == signal.Involuntary {
		if mode == appsv1alpha1.HoldVoluntaryOnly {
			relaxReason = relaxReasonMode
		} else if a.worstDoom.Deadline != nil && !surgeReady &&
			time.Until(*a.worstDoom.Deadline) < time.Duration(cr.MinSurgeTimeOrDefault())*time.Second {
			relaxReason = relaxReasonDeadline
		}
	}
	ttlExpired := false
	if len(a.doomed) > 0 && !surgeReady && blockedSince != nil {
		deadline := blockedSince.Add(time.Duration(cr.ReadinessDeadlineOrDefault()) * time.Second)
		if time.Now().After(deadline) {
			ttlExpired = true
			if relaxReason == "" {
				relaxReason = relaxReasonTTL
			}
		}
	}
	// Once a TTL release is published, keep it sticky for the lifetime of the
	// same doom signal. blockedSince is intentionally cleared because admission
	// is no longer blocked, so PhaseRelaxed carries this state across reconciles.
	if relaxReason == "" && previousPhase == appsv1alpha1.PhaseRelaxed && len(a.doomed) > 0 && !surgeReady {
		relaxReason = relaxReasonTTL
		ttlExpired = true
	}
	return relaxReason, ttlExpired
}

func (r *PodHandoffReconciler) reconcileScaleBack(ctx context.Context, cr *appsv1alpha1.PodHandoff,
	dep *appsv1.Deployment, status appsv1alpha1.PodHandoffStatus, a podAssessment, base int32,
	req ctrl.Request) (ctrl.Result, error) {
	if err := r.clearDeletionCosts(ctx, a.all); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.scaleTo(ctx, dep, base); err != nil {
		return ctrl.Result{}, err
	}
	now := metav1.Now()
	status.LastReleaseTime = &now
	status.BlockedSince = nil
	status.Phase = appsv1alpha1.PhaseIdle
	metrics.UntrackBlocked(req.String())
	r.event(cr, corev1.EventTypeNormal, "ScaledBack", "Release",
		fmt.Sprintf("predicament over; scaled %s back to %d replicas", dep.Name, base))
	logf.FromContext(ctx).Info("scaled back", "deployment", dep.Name, "replicas", base)
	return ctrl.Result{}, r.patchStatus(ctx, cr, status)
}

func (r *PodHandoffReconciler) reconcileDoomed(ctx context.Context, cr *appsv1alpha1.PodHandoff,
	dep *appsv1.Deployment, status appsv1alpha1.PodHandoffStatus, a podAssessment, base int32,
	surgeActive, surgeReady bool, relaxReason string, ttlExpired bool, req ctrl.Request) (ctrl.Result, error) {

	holding := cr.HoldModeOrDefault() != appsv1alpha1.HoldOff && !surgeReady && relaxReason == "" && !ttlExpired
	if holding {
		if status.BlockedSince == nil {
			now := metav1.Now()
			status.BlockedSince = &now
		}
		metrics.TrackBlocked(req.String(), status.BlockedSince.Time)
	} else {
		status.BlockedSince = nil
		metrics.UntrackBlocked(req.String())
	}

	if !surgeActive && rolloutInProgress(dep, base) {
		if relaxReason != "" || ttlExpired {
			r.markHoldRelaxed(cr, &status, relaxReason)
		} else {
			status.Phase = appsv1alpha1.PhaseIdle
			setCond(&status, cr, appsv1alpha1.ConditionSurgeReady, metav1.ConditionFalse,
				"RolloutInProgress", "target is mid-rollout; standing down (the Deployment is already surging)")
		}
		r.eventOnce(cr, &status, corev1.EventTypeNormal, "RolloutStandDown", "Skip",
			fmt.Sprintf("doom signal for %s ignored: rollout in progress", dep.Name))
		return ctrl.Result{RequeueAfter: activeRequeue}, r.patchStatus(ctx, cr, status)
	}

	if !surgeActive {
		if err := r.startSurge(ctx, cr, dep, base); err != nil {
			return ctrl.Result{}, err
		}
		now := metav1.Now()
		status.LastSurgeTime = &now
		class := signal.VoluntaryUnbounded
		if a.worstDoom != nil {
			class = a.worstDoom.Class
		}
		metrics.SurgesTotal.WithLabelValues(string(class)).Inc()
		r.event(cr, corev1.EventTypeNormal, "SurgeStarted", "Surge",
			fmt.Sprintf("node doom (%s) for pod(s) of %s; surging %d -> %d",
				doomSource(a.worstDoom), dep.Name, base, base+cr.SurgeReplicasOrDefault()))
	}

	if err := r.stampDeletionCosts(ctx, a.doomed); err != nil {
		return ctrl.Result{}, err
	}

	switch {
	case surgeReady:
		status.Phase = appsv1alpha1.PhaseReleasing
		setCond(&status, cr, appsv1alpha1.ConditionSurgeReady, metav1.ConditionTrue,
			"StandInReady", "stand-in Pod is traffic-ready; eviction may proceed")
		r.eventOnce(cr, &status, corev1.EventTypeNormal, "SurgeReady", "Release",
			fmt.Sprintf("stand-in for %s is traffic-ready; releasing the eviction", dep.Name))
	case relaxReason != "" || ttlExpired:
		r.markHoldRelaxed(cr, &status, relaxReason)
	default:
		status.Phase = appsv1alpha1.PhaseSurging
		setCond(&status, cr, appsv1alpha1.ConditionSurgeReady, metav1.ConditionFalse,
			"AwaitingReadiness", fmt.Sprintf("waiting for %d ready replica(s) on healthy nodes (have %d)", base, a.viable))
	}
	return ctrl.Result{RequeueAfter: activeRequeue}, r.patchStatus(ctx, cr, status)
}

func (r *PodHandoffReconciler) markHoldRelaxed(cr *appsv1alpha1.PodHandoff, status *appsv1alpha1.PodHandoffStatus, reason string) {
	previousPhase := status.Phase
	status.Phase = appsv1alpha1.PhaseRelaxed
	setCond(status, cr, appsv1alpha1.ConditionSurgeReady, metav1.ConditionFalse,
		"Relaxed", "eviction released without a ready stand-in Pod ("+reason+")")
	if status.LastReleaseTime == nil || status.LastSurgeTime != nil && status.LastReleaseTime.Before(status.LastSurgeTime) {
		now := metav1.Now()
		status.LastReleaseTime = &now
	}
	if previousPhase != appsv1alpha1.PhaseRelaxed {
		metrics.RelaxedTotal.WithLabelValues(reason).Inc()
	}
	r.eventOnce(cr, status, corev1.EventTypeWarning, "HoldRelaxed", "Relax",
		fmt.Sprintf("eviction hold relaxed (%s); eviction can proceed without a ready stand-in", reason))
}

func (r *PodHandoffReconciler) startSurge(ctx context.Context, cr *appsv1alpha1.PodHandoff, dep *appsv1.Deployment, base int32) error {
	patched := dep.DeepCopy()
	if patched.Annotations == nil {
		patched.Annotations = map[string]string{}
	}
	patched.Annotations[AnnotationBaseReplicas] = strconv.FormatInt(int64(base), 10)
	target := base + cr.SurgeReplicasOrDefault()
	patched.Spec.Replicas = &target
	return r.Patch(ctx, patched, client.MergeFrom(dep))
}

func (r *PodHandoffReconciler) scaleTo(ctx context.Context, dep *appsv1.Deployment, replicas int32) error {
	patched := dep.DeepCopy()
	patched.Spec.Replicas = &replicas
	delete(patched.Annotations, AnnotationBaseReplicas)
	return r.Patch(ctx, patched, client.MergeFrom(dep))
}

func (r *PodHandoffReconciler) stampDeletionCosts(ctx context.Context, pods []*corev1.Pod) error {
	for _, pod := range pods {
		current, hasCurrent := pod.Annotations[PodDeletionCostAnnotation]
		_, managed := pod.Annotations[AnnotationPreviousDeletionCost]
		if managed {
			continue
		}
		// The exact steering value already existed before PodHandoff saw the
		// Pod. Leave it untouched and, crucially, do not claim ownership of it.
		if hasCurrent && current == doomedPodDeletionCost {
			continue
		}
		patched := pod.DeepCopy()
		if patched.Annotations == nil {
			patched.Annotations = map[string]string{}
		}
		if hasCurrent {
			patched.Annotations[AnnotationPreviousDeletionCost] = "present:" + current
		} else {
			patched.Annotations[AnnotationPreviousDeletionCost] = "absent"
		}
		patched.Annotations[PodDeletionCostAnnotation] = doomedPodDeletionCost
		if err := r.Patch(ctx, patched, client.MergeFrom(pod)); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *PodHandoffReconciler) clearDeletionCosts(ctx context.Context, pods []corev1.Pod) error {
	for i := range pods {
		pod := &pods[i]
		previous, managed := pod.Annotations[AnnotationPreviousDeletionCost]
		if !managed {
			continue
		}
		patched := pod.DeepCopy()
		if patched.Annotations[PodDeletionCostAnnotation] == doomedPodDeletionCost {
			switch {
			case previous == "absent":
				delete(patched.Annotations, PodDeletionCostAnnotation)
			case len(previous) >= len("present:") && previous[:len("present:")] == "present:":
				patched.Annotations[PodDeletionCostAnnotation] = previous[len("present:"):]
			}
		}
		delete(patched.Annotations, AnnotationPreviousDeletionCost)
		if err := r.Patch(ctx, patched, client.MergeFrom(pod)); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *PodHandoffReconciler) markUnsupported(ctx context.Context, cr *appsv1alpha1.PodHandoff,
	status appsv1alpha1.PodHandoffStatus, msg string) (ctrl.Result, error) {
	alreadyMarked := status.Phase == appsv1alpha1.PhaseUnsupported
	status.Phase = appsv1alpha1.PhaseUnsupported
	setCond(&status, cr, appsv1alpha1.ConditionTargetSupported, metav1.ConditionFalse, "UnsupportedTarget", msg)
	if !alreadyMarked {
		r.event(cr, corev1.EventTypeWarning, "UnsupportedTarget", "Skip", msg)
	}
	return ctrl.Result{}, r.patchStatus(ctx, cr, status)
}

func (r *PodHandoffReconciler) setStrategyCondition(status *appsv1alpha1.PodHandoffStatus,
	cr *appsv1alpha1.PodHandoff, dep *appsv1.Deployment, base int32) {
	if dep.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType {
		setCond(status, cr, appsv1alpha1.ConditionZeroDowntimeStrategy, metav1.ConditionFalse,
			"Recreate", "strategy Recreate drops all traffic on every deploy")
		return
	}
	maxUnavailable, maxSurge := 0, 1
	if ru := dep.Spec.Strategy.RollingUpdate; ru != nil {
		if ru.MaxUnavailable != nil {
			maxUnavailable, _ = intstr.GetScaledValueFromIntOrPercent(ru.MaxUnavailable, int(base), false)
		}
		if ru.MaxSurge != nil {
			maxSurge, _ = intstr.GetScaledValueFromIntOrPercent(ru.MaxSurge, int(base), true)
		}
	}
	if maxUnavailable > 0 || maxSurge < 1 {
		setCond(status, cr, appsv1alpha1.ConditionZeroDowntimeStrategy, metav1.ConditionFalse,
			"LossyStrategy",
			fmt.Sprintf("resolved maxUnavailable=%d, maxSurge=%d: deploys can drop traffic; set maxUnavailable: 0, maxSurge: 1",
				maxUnavailable, maxSurge))
		return
	}
	setCond(status, cr, appsv1alpha1.ConditionZeroDowntimeStrategy, metav1.ConditionTrue, "SurgingStrategy",
		"rolling updates keep capacity during deploys")
}

func setProtectedCondition(status *appsv1alpha1.PodHandoffStatus, cr *appsv1alpha1.PodHandoff,
	mode appsv1alpha1.HoldMode, relaxReason string) {
	reason, msg := "HoldOff", "holdMode is off; evictions are admitted immediately"
	if mode != appsv1alpha1.HoldOff {
		reason, msg = "EvictionHold", "evictions are held at admission while a stand-in is prepared"
		if relaxReason != "" {
			reason, msg = "Relaxed", "eviction hold relaxed ("+relaxReason+"); evictions may proceed"
		}
	}
	setCond(status, cr, appsv1alpha1.ConditionProtected, boolToStatus(mode != appsv1alpha1.HoldOff && relaxReason == ""), reason, msg)
}

func (r *PodHandoffReconciler) patchStatus(ctx context.Context, cr *appsv1alpha1.PodHandoff, status appsv1alpha1.PodHandoffStatus) error {
	if statusEqual(&cr.Status, &status) {
		return nil
	}
	patched := cr.DeepCopy()
	patched.Status = status
	return r.Status().Patch(ctx, patched, client.MergeFrom(cr))
}

func (r *PodHandoffReconciler) event(cr *appsv1alpha1.PodHandoff, eventType, reason, action, note string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(cr, nil, eventType, reason, action, "%s", note)
}

func (r *PodHandoffReconciler) eventOnce(cr *appsv1alpha1.PodHandoff, status *appsv1alpha1.PodHandoffStatus,
	eventType, reason, action, note string) {
	prev := meta.FindStatusCondition(cr.Status.Conditions, appsv1alpha1.ConditionSurgeReady)
	next := meta.FindStatusCondition(status.Conditions, appsv1alpha1.ConditionSurgeReady)
	if prev != nil && next != nil && prev.Status == next.Status && prev.Reason == next.Reason {
		return
	}
	r.event(cr, eventType, reason, action, note)
}

func (r *PodHandoffReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mapDeployment := func(ctx context.Context, obj client.Object) []ctrl.Request {
		return r.requestsForTarget(ctx, obj.GetNamespace(), obj.GetName())
	}
	mapPod := func(ctx context.Context, obj client.Object) []ctrl.Request {
		return r.requestsInNamespace(ctx, obj.GetNamespace())
	}
	mapDoom := func(ctx context.Context, _ client.Object) []ctrl.Request {
		return r.requestsInNamespace(ctx, "")
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1alpha1.PodHandoff{}).
		Watches(&appsv1.Deployment{}, handler.EnqueueRequestsFromMapFunc(mapDeployment)).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(mapPod)).
		WatchesRawSource(source.Channel(r.Registry.Subscribe(64), handler.EnqueueRequestsFromMapFunc(mapDoom))).
		Named("podhandoff").
		Complete(r)
}

func (r *PodHandoffReconciler) requestsForTarget(ctx context.Context, namespace, name string) []ctrl.Request {
	var list appsv1alpha1.PodHandoffList
	if err := r.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil
	}
	var reqs []ctrl.Request
	for i := range list.Items {
		if list.Items[i].Spec.TargetRef.Name == name {
			reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
	}
	return reqs
}

func (r *PodHandoffReconciler) requestsInNamespace(ctx context.Context, namespace string) []ctrl.Request {
	var list appsv1alpha1.PodHandoffList
	var opts []client.ListOption
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := r.List(ctx, &list, opts...); err != nil {
		return nil
	}
	var reqs []ctrl.Request
	for i := range list.Items {
		reqs = append(reqs, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return reqs
}

func podReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func rolloutInProgress(dep *appsv1.Deployment, base int32) bool {
	if dep.Status.ObservedGeneration < dep.Generation {
		return true
	}
	for _, c := range dep.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionTrue &&
			c.Reason != "NewReplicaSetAvailable" {
			return true
		}
	}
	return dep.Status.UpdatedReplicas < base
}

func setCond(status *appsv1alpha1.PodHandoffStatus, cr *appsv1alpha1.PodHandoff,
	condType string, condStatus metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             condStatus,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: cr.Generation,
	})
}

func boolToStatus(b bool) metav1.ConditionStatus {
	if b {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

func statusEqual(a, b *appsv1alpha1.PodHandoffStatus) bool {
	return apiequality.Semantic.DeepEqual(a, b)
}

func doomSource(d *signal.NodeDoom) string {
	if d == nil {
		return "unknown"
	}
	return d.Source
}
