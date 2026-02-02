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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ProjectBudgetSpec defines the desired state of ProjectBudget
type ProjectBudgetSpec struct {
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinLength=1
    // TeamName is the name of the namespace/label to govern (e.g., "team-alpha")
    TeamName string `json:"teamName"`

    // +kubebuilder:validation:Required
    // +kubebuilder:validation:Pattern=`^\d+(m|)$`
    // MaxCpuLimit is the maximum total CPU allowed for the namespace (e.g., "2000m" = 2 Cores)
    MaxCpuLimit string `json:"maxCpuLimit"`

    // +kubebuilder:validation:Optional
    // +kubebuilder:validation:Pattern=`^\d+(Mi|Gi)$`
    // MaxMemoryLimit is the maximum total Memory allowed (e.g., "4Gi")
    MaxMemoryLimit string `json:"maxMemoryLimit,omitempty"`
}

// ProjectBudgetStatus defines the observed state of ProjectBudget.
type ProjectBudgetStatus struct {
    // CurrentCpuUsage shows the total CPU requests found in the namespace
    CurrentCpuUsage string `json:"currentCpuUsage,omitempty"`
    
    // LastCheckTime is the timestamp of the last reconciliation
    LastCheckTime string `json:"lastCheckTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ProjectBudget is the Schema for the projectbudgets API
type ProjectBudget struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ProjectBudget
	// +required
	Spec ProjectBudgetSpec `json:"spec"`

	// status defines the observed state of ProjectBudget
	// +optional
	Status ProjectBudgetStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ProjectBudgetList contains a list of ProjectBudget
type ProjectBudgetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ProjectBudget `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProjectBudget{}, &ProjectBudgetList{})
}
