/*
Copyright 2026.

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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	finopsv1 "github.com/AlejandroCasa/k8s-governance-operator/api/v1"
)

// ProjectBudgetReconciler reconciles a ProjectBudget object
type ProjectBudgetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups="",resources=projectbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=projectbudgets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=projectbudgets/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ProjectBudget object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
func (r *ProjectBudgetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Get the ProjectBudget instance that triggered this event
	var projectBudget finopsv1.ProjectBudget
	if err := r.Get(ctx, req.NamespacedName, &projectBudget); err != nil {
		// If not found, return. Created objects are automatically garbage collected.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. List all Pods in the budget namespace
	var podList corev1.PodList
	// Using the namespace defined in the spec (e.g., "team-beta")
	targetNamespace := projectBudget.Spec.TeamName

	if err := r.List(ctx, &podList, client.InNamespace(targetNamespace)); err != nil {
		logger.Error(err, "Failed to list pods in namespace", "namespace", targetNamespace)
		return ctrl.Result{}, err
	}

	// 3. Calculate current CPU usage
	var totalCpuUsage int64 = 0
	for _, pod := range podList.Items {
		// Sum the limits of all containers in the pod
		for _, container := range pod.Spec.Containers {
			cpuLimit := container.Resources.Limits.Cpu()
			if cpuLimit != nil {
				// MilliValue returns CPU in millicores (1 Core = 1000m)
				totalCpuUsage += cpuLimit.MilliValue()
			}
		}
	}

	// 4. Compare with the defined limit
	// Parse the limit from the CRD (e.g., "1500m")
	maxCpuLimitQuantity, err := resource.ParseQuantity(projectBudget.Spec.MaxCpuLimit)
	if err != nil {
		logger.Error(err, "Invalid MaxCpuLimit format in CRD")
		return ctrl.Result{}, nil // Does not retry if the format is invalid

	}
	maxCpuMilli := maxCpuLimitQuantity.MilliValue()

	// 5. Decision Logic (Governance)

	if totalCpuUsage > maxCpuMilli {
		logger.Info("VIOLATION DETECTED", "Namespace", targetNamespace, "Current", totalCpuUsage, "Limit", maxCpuMilli)

		// HERE is where in the future we would delete pods or block deployments.
		// But for now, we just log.

	} else {
		logger.Info("Budget OK", "Namespace", targetNamespace, "Usage", totalCpuUsage)
	}

	// 6. Update the ProjectBudget status (visual feedback for the user)
	projectBudget.Status.CurrentCpuUsage = fmt.Sprintf("%dm", totalCpuUsage)
	projectBudget.Status.LastCheckTime = "Just Now"

	if err := r.Status().Update(ctx, &projectBudget); err != nil {
		logger.Error(err, "Failed to update ProjectBudget status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectBudgetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&finopsv1.ProjectBudget{}).
		Named("projectbudget").
		Complete(r)
}
