/*
Copyright 2026 The Fluid Authors.

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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DataUnloadSpec defines the desired state of DataUnload
type DataUnloadSpec struct {
	// Dataset defines the target dataset of the DataUnload
	Dataset TargetDataset `json:"dataset,omitempty"`

	// Target defines target paths that needs to be unloaded.
	// If empty, the whole dataset cache will be unloaded.
	Target []TargetPath `json:"target,omitempty"`

	// Options specifies the extra dataunload properties for runtime
	Options map[string]string `json:"options,omitempty"`

	// PodMetadata defines labels and annotations that will be propagated to DataUnload pods
	PodMetadata PodMetadata `json:"podMetadata,omitempty"`

	// +optional
	// Affinity defines affinity for DataUnload pod
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// +optional
	// Tolerations defines tolerations for DataUnload pod
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// +optional
	// NodeSelector defines node selector for DataUnload pod
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// +optional
	// SchedulerName sets the scheduler to be used for DataUnload pod
	SchedulerName string `json:"schedulerName,omitempty"`

	//+kubebuilder:default:=Once
	//+kubebuilder:validation:Enum=Once;Cron
	// including Once, Cron
	// +optional
	Policy Policy `json:"policy,omitempty"`

	// The schedule in Cron format, only set when policy is cron, see https://en.wikipedia.org/wiki/Cron.
	// +optional
	Schedule string `json:"schedule,omitempty"`

	// Specifies that the preceding operation in a workflow
	// +optional
	RunAfter *OperationRef `json:"runAfter,omitempty"`

	// TTLSecondsAfterFinished is the time second to clean up data operations after finished or failed
	// +optional
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// Resources that will be requested by the DataUnload job. <br>
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// +kubebuilder:printcolumn:name="Dataset",type="string",JSONPath=`.spec.dataset.name`
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Duration",type="string",JSONPath=`.status.duration`
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:resource:categories={fluid},shortName=unload
// +genclient

// DataUnload is the Schema for the dataunloads API
type DataUnload struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DataUnloadSpec  `json:"spec,omitempty"`
	Status OperationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced

// DataUnloadList contains a list of DataUnload
type DataUnloadList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DataUnload `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DataUnload{}, &DataUnloadList{})
}
