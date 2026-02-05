package v1

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	finopsv1 "github.com/AlejandroCasa/k8s-governance-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// log is for logging in this package.
var podlog = logf.Log.WithName("pod-resource")

// --- METRICS DEFINITION START ---
var (
	// Metric 1: Counter of rejections by namespace
	rejectedPods = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "finops_rejected_pods_total",
			Help: "Total number of pods rejected by the FinOps operator due to budget overflow",
		},
		[]string{"team_namespace"},
	)

	// Metric 2: Counter of CPU millicores saved by preventing pod creation
	savedCpu = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "finops_saved_cpu_millicores_total",
			Help: "Total CPU millicores saved/prevented from being provisioned",
		},
		[]string{"team_namespace"},
	)
)

func init() {
	// Register the metrics in the global registry of controller-runtime
	metrics.Registry.MustRegister(rejectedPods, savedCpu)
}

// SetupPodWebhookWithManager registers the webhook for Pod in the manager.
func SetupPodWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&corev1.Pod{}).
		WithValidator(&PodCustomValidator{
			Client: mgr.GetClient(),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/validate--v1-pod,mutating=false,failurePolicy=fail,sideEffects=None,groups=core,resources=pods,verbs=create;update,versions=v1,name=vpod.kb.io,admissionReviewVersions=v1

// PodCustomValidator struct
type PodCustomValidator struct {
	Client client.Client // Necesitamos el cliente para leer ProjectBudgets
}

var _ webhook.CustomValidator = &PodCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Pod.
// +kubebuilder:rbac:groups=finops.acasa.acme,resources=projectbudgets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
func (v *PodCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil, fmt.Errorf("expected a Pod but got a %T", obj)
	}

	podlog.Info("Validating Pod creation for Financial Compliance", "name", pod.Name, "namespace", pod.Namespace)

	// 1. Search for a budget for this namespace
	var budgetList finopsv1.ProjectBudgetList
	if err := v.Client.List(ctx, &budgetList); err != nil {
		podlog.Error(err, "Failed to list budgets, allowing pod safely")
		return nil, nil // Fail-open
	}

	var activeBudget *finopsv1.ProjectBudget
	for _, b := range budgetList.Items {
		if b.Spec.TeamName == pod.Namespace {
			activeBudget = &b
			break
		}
	}

	// If no budget is found, we allow everything (fail-open)
	if activeBudget == nil {
		return nil, nil
	}

	// 2. Calculate the cost of the new Pod based on CPU limits (you can expand this to include memory or other resources)
	var newPodCost int64 = 0
	for _, container := range pod.Spec.Containers {
		cpu := container.Resources.Limits.Cpu()
		if cpu != nil {
			newPodCost += cpu.MilliValue()
		}
	}

	// 3. Calculate current usage of the Namespace by summing CPU limits of all existing Pods in the same namespace
	var existingPods corev1.PodList
	if err := v.Client.List(ctx, &existingPods, client.InNamespace(pod.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list existing pods: %v", err)
	}

	var currentUsage int64 = 0
	for _, p := range existingPods.Items {
		for _, c := range p.Spec.Containers {
			cpu := c.Resources.Limits.Cpu()
			if cpu != nil {
				currentUsage += cpu.MilliValue()
			}
		}
	}

	// 4. Business Decision (Enforcement)
	limitQuantity, _ := resource.ParseQuantity(activeBudget.Spec.MaxCpuLimit)
	limitMilli := limitQuantity.MilliValue()

	totalAfterDeployment := currentUsage + newPodCost

	if totalAfterDeployment > limitMilli {
		violationMsg := fmt.Sprintf("DENIED by FinOps: Budget exceeded for team '%s'. Used: %dm, Limit: %dm, Request: %dm",
			pod.Namespace, currentUsage, limitMilli, newPodCost)

		podlog.Info(violationMsg)

		// Increase the counter of rejections for this team
		rejectedPods.WithLabelValues(pod.Namespace).Inc()

		// Sume the cost we just prevented (CPU saved)
		savedCpu.WithLabelValues(pod.Namespace).Add(float64(newPodCost))

		// Return an error here to block the pod creation. The message will be shown to the user.
		return nil, fmt.Errorf("%s", violationMsg)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator.
func (v *PodCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	// Opcional: Implementar lógica similar si alguien escala verticalmente un pod existente
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator.
func (v *PodCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
