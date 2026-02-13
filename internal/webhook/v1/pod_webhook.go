package v1

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
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
			Client:   mgr.GetClient(),
			Decoder:  admission.NewDecoder(mgr.GetScheme()),
			Recorder: mgr.GetEventRecorderFor("finops-webhook"),
		}).
		WithDefaulter(&PodCustomValidator{
			Client:   mgr.GetClient(),
			Recorder: mgr.GetEventRecorderFor("finops-webhook"),
		}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate--v1-pod,mutating=true,failurePolicy=fail,sideEffects=None,groups="",resources=pods,verbs=create;update,versions=v1,name=mpod.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate--v1-pod,mutating=false,failurePolicy=fail,sideEffects=None,groups="",resources=pods,verbs=create;update,versions=v1,name=vpod.kb.io,admissionReviewVersions=v1

// PodCustomValidator struct
type PodCustomValidator struct {
	Client   client.Client
	Decoder  admission.Decoder
	Recorder record.EventRecorder
}

var _ webhook.CustomValidator = &PodCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Pod.
// +kubebuilder:rbac:groups=finops.acasa.acme,resources=projectbudgets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Default implements admission.CustomDefaulter.
// This function is called BEFORE validation. It allows us to modify the Pod on the fly.
func (v *PodCustomValidator) Default(ctx context.Context, obj runtime.Object) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("expected a Pod but got a %T", obj)
	}

	// 1. Safety Check: Only mutate if the user explicitly asks for it via Annotation.
	// We don't want to surprise users by shrinking their databases silently.
	if pod.Annotations["finops.acasa.acme/auto-resize"] != "true" {
		return nil
	}

	podlog.Info("Mutating Pod: Checking for auto-sizing opportunities", "name", pod.Name)

	// 2. Find the Budget
	var budgetList finopsv1.ProjectBudgetList
	if err := v.Client.List(ctx, &budgetList); err != nil {
		return nil // If we can't list budgets, we don't touch anything
	}

	var activeBudget *finopsv1.ProjectBudget
	for _, b := range budgetList.Items {
		if b.Spec.TeamName == pod.Namespace {
			activeBudget = &b
			break
		}
	}
	if activeBudget == nil {
		return nil
	}

	// 3. Calculate Remaining Budget
	currentCpu, _, err := v.calculateCurrentUsage(ctx, pod.Namespace)
	if err != nil {
		return nil
	}

	limitCpuQuantity, _ := resource.ParseQuantity(activeBudget.Spec.MaxCpuLimit)
	limitCpuMilli := limitCpuQuantity.MilliValue()
	remainingCpu := limitCpuMilli - currentCpu

	// If there is no budget left, we can't do anything (Validation will fail later)
	if remainingCpu <= 0 {
		return nil
	}

	// 4. Check if the Pod fits. If not, Resize it.
	// NOTE: For simplicity, we only resize the FIRST container.
	// Complex logic would distribute the cut across all containers.
	if len(pod.Spec.Containers) > 0 {
		container := &pod.Spec.Containers[0]
		requestCpu := container.Resources.Limits.Cpu()

		if requestCpu != nil && requestCpu.MilliValue() > remainingCpu {
			oldCpu := requestCpu.MilliValue()

			// MUTATION HAPPENS HERE: We overwrite the requested limit with the remaining budget
			newLimit := resource.NewMilliQuantity(remainingCpu, resource.DecimalSI)
			container.Resources.Limits[corev1.ResourceCPU] = *newLimit

			msg := fmt.Sprintf("Auto-Sized Pod CPU from %dm to %dm to fit budget", oldCpu, remainingCpu)
			podlog.Info(msg)

			// Add an annotation so the user knows we touched it
			if pod.Annotations == nil {
				pod.Annotations = make(map[string]string)
			}
			pod.Annotations["finops.acasa.acme/resized"] = "true"

			// Record event
			v.Recorder.Event(activeBudget, "Normal", "PodAutoSized", msg)
		}
	}

	return nil
}

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

	// 2. Calculate the cost of the NEW Pod (CPU & Memory)
	var newPodCpuCost int64 = 0
	var newPodMemCost int64 = 0

	for _, container := range pod.Spec.Containers {
		// CPU Calculation
		if cpu := container.Resources.Limits.Cpu(); cpu != nil {
			newPodCpuCost += cpu.MilliValue()
		}
		// Memory Calculation
		if mem := container.Resources.Limits.Memory(); mem != nil {
			newPodMemCost += mem.Value()
		}
	}

	// 3. Calculate CURRENT usage of the Namespace (CPU & Memory)
	var existingPods corev1.PodList
	if err := v.Client.List(ctx, &existingPods, client.InNamespace(pod.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list existing pods: %v", err)
	}

	var currentCpuUsage int64 = 0
	var currentMemUsage int64 = 0

	for _, p := range existingPods.Items {
		// Only count running or pending pods (ignore completed/failed ones)
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}

		for _, c := range p.Spec.Containers {
			// CPU Sum
			if cpu := c.Resources.Limits.Cpu(); cpu != nil {
				currentCpuUsage += cpu.MilliValue()
			}
			// Memory Sum
			if mem := c.Resources.Limits.Memory(); mem != nil {
				currentMemUsage += mem.Value()
			}
		}
	}

	// 4. Enforcement Logic: CPU Check
	limitCpuQuantity, _ := resource.ParseQuantity(activeBudget.Spec.MaxCpuLimit)
	limitCpuMilli := limitCpuQuantity.MilliValue()
	totalCpuAfter := currentCpuUsage + newPodCpuCost

	if totalCpuAfter > limitCpuMilli {
		violationMsg := fmt.Sprintf("DENIED by FinOps: CPU Budget exceeded for team '%s'. Used: %dm, Limit: %dm, Request: %dm",
			pod.Namespace, currentCpuUsage, limitCpuMilli, newPodCpuCost)

		if activeBudget.Spec.ValidationMode == finopsv1.DryRunMode {
			dryRunMsg := fmt.Sprintf("[DRY-RUN] Violation detected but allowed: %s", violationMsg)
			podlog.Info(dryRunMsg)

			// We emit a specific event so the admin knows it WOULD have failed
			v.Recorder.Event(activeBudget, "Warning", "DryRunViolation", dryRunMsg)

			// Metrics: We can still count it as rejected in metrics, or create a new metric "potential_savings"
			// For now, let's keep counting it to see the impact
			rejectedPods.WithLabelValues(pod.Namespace).Inc()

			// CRITICAL: Return nil means "ALLOW"
			return nil, nil
		}

		podlog.Info(violationMsg)

		// Record the event in the ProjectBudget CRD
		v.Recorder.Event(activeBudget, "Warning", "BudgetExceeded", violationMsg)

		// Metrics
		rejectedPods.WithLabelValues(pod.Namespace).Inc()
		savedCpu.WithLabelValues(pod.Namespace).Add(float64(newPodCpuCost))

		return nil, fmt.Errorf("%s", violationMsg)
	}

	// 5. Enforcement Logic: Memory Check (New Feature)
	if activeBudget.Spec.MaxMemoryLimit != "" {
		limitMemQuantity, err := resource.ParseQuantity(activeBudget.Spec.MaxMemoryLimit)
		if err != nil {
			podlog.Error(err, "Invalid memory limit format in ProjectBudget", "budget", activeBudget.Name)
			// We don't block if the budget is malformed, just log error (Fail-Open behavior)
		} else {
			limitMemBytes := limitMemQuantity.Value()
			totalMemAfter := currentMemUsage + newPodMemCost

			if totalMemAfter > limitMemBytes {
				violationMsg := fmt.Sprintf("DENIED by FinOps: RAM Budget exceeded for team '%s'. Used: %d bytes, Limit: %d bytes, Request: %d bytes",
					pod.Namespace, currentMemUsage, limitMemBytes, newPodMemCost)

				if activeBudget.Spec.ValidationMode == finopsv1.DryRunMode {
					dryRunMsg := fmt.Sprintf("[DRY-RUN] Violation detected but allowed: %s", violationMsg)
					podlog.Info(dryRunMsg)

					// We emit a specific event so the admin knows it WOULD have failed
					v.Recorder.Event(activeBudget, "Warning", "DryRunViolation", dryRunMsg)

					// Metrics: We can still count it as rejected in metrics, or create a new metric "potential_savings"
					// For now, let's keep counting it to see the impact
					rejectedPods.WithLabelValues(pod.Namespace).Inc()

					// CRITICAL: Return nil means "ALLOW"
					return nil, nil
				}

				podlog.Info(violationMsg)

				// Record the event in the ProjectBudget CRD
				v.Recorder.Event(activeBudget, "Warning", "BudgetExceeded", violationMsg)

				// Note: We could add a 'savedMemory' metric here in the future
				return nil, fmt.Errorf("%s", violationMsg)
			}
		}
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

// calculateCurrentUsage sums up the CPU and Memory limits of all active Pods in the namespace.
// Returns: (cpuMillis, memoryBytes, error)
func (v *PodCustomValidator) calculateCurrentUsage(ctx context.Context, namespace string) (int64, int64, error) {
	var existingPods corev1.PodList
	if err := v.Client.List(ctx, &existingPods, client.InNamespace(namespace)); err != nil {
		return 0, 0, err
	}

	var currentCpuUsage int64 = 0
	var currentMemUsage int64 = 0

	for _, p := range existingPods.Items {
		// Only count running or pending pods (ignore completed/failed ones)
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}

		for _, c := range p.Spec.Containers {
			if cpu := c.Resources.Limits.Cpu(); cpu != nil {
				currentCpuUsage += cpu.MilliValue()
			}
			if mem := c.Resources.Limits.Memory(); mem != nil {
				currentMemUsage += mem.Value()
			}
		}
	}
	return currentCpuUsage, currentMemUsage, nil
}
