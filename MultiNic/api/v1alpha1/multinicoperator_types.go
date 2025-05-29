/*
Copyright 2025.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MultiNicOperatorSpec defines the desired state of MultiNicOperator
type MultiNicOperatorSpec struct {
	// Controller configuration
	Controller ControllerConfig `json:"controller"`

	// Database configuration
	Database DatabaseConfig `json:"database"`

	// OpenStack configuration
	OpenStack OpenStackConfig `json:"openstack,omitempty"`

	// Protection settings
	Protection ProtectionConfig `json:"protection,omitempty"`
}

// ControllerConfig defines controller settings
type ControllerConfig struct {
	// Image repository and tag
	Image string `json:"image"`

	// Number of replicas
	Replicas int32 `json:"replicas,omitempty"`

	// Resource requirements
	Resources ResourceConfig `json:"resources,omitempty"`

	// Health probe configuration
	HealthProbe HealthProbeConfig `json:"healthProbe,omitempty"`
}

// DatabaseConfig defines database settings
type DatabaseConfig struct {
	// Whether to deploy database internally
	Enabled bool `json:"enabled"`

	// Database image
	Image string `json:"image,omitempty"`

	// Storage size
	StorageSize string `json:"storageSize,omitempty"`

	// Resource requirements
	Resources ResourceConfig `json:"resources,omitempty"`

	// Database credentials
	Credentials DBCredentials `json:"credentials,omitempty"`
}

// OpenStackConfig defines OpenStack endpoints
type OpenStackConfig struct {
	// Identity endpoint
	IdentityEndpoint string `json:"identityEndpoint"`

	// Network endpoint
	NetworkEndpoint string `json:"networkEndpoint"`

	// Compute endpoint
	ComputeEndpoint string `json:"computeEndpoint"`
}

// ProtectionConfig defines protection settings
type ProtectionConfig struct {
	// Enable protection against direct modifications
	EnableMutationPrevention bool `json:"enableMutationPrevention,omitempty"`

	// Enable automatic recovery
	EnableAutoRecovery bool `json:"enableAutoRecovery,omitempty"`

	// Allowed operations
	AllowedOperations []string `json:"allowedOperations,omitempty"`
}

// ResourceConfig defines resource requirements
type ResourceConfig struct {
	// CPU requests and limits
	CPU string `json:"cpu,omitempty"`

	// Memory requests and limits
	Memory string `json:"memory,omitempty"`
}

// HealthProbeConfig defines health probe settings
type HealthProbeConfig struct {
	// Port for health probes
	Port int32 `json:"port,omitempty"`

	// Path for health checks
	Path string `json:"path,omitempty"`
}

// DBCredentials defines database credentials
type DBCredentials struct {
	// Root password
	RootPassword string `json:"rootPassword,omitempty"`

	// Database name
	Database string `json:"database,omitempty"`

	// Database user
	User string `json:"user,omitempty"`

	// User password
	Password string `json:"password,omitempty"`
}

// MultiNicOperatorStatus defines the observed state of MultiNicOperator
type MultiNicOperatorStatus struct {
	// Overall status
	Phase string `json:"phase,omitempty"`

	// Status message
	Message string `json:"message,omitempty"`

	// Last update time
	LastUpdated metav1.Time `json:"lastUpdated,omitempty"`

	// Controller status
	ControllerStatus ComponentStatus `json:"controllerStatus,omitempty"`

	// Database status
	DatabaseStatus ComponentStatus `json:"databaseStatus,omitempty"`

	// Protection status
	ProtectionStatus ProtectionStatus `json:"protectionStatus,omitempty"`

	// Conditions
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ComponentStatus defines the status of a component
type ComponentStatus struct {
	// Component phase
	Phase string `json:"phase,omitempty"`

	// Ready replicas
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Desired replicas
	DesiredReplicas int32 `json:"desiredReplicas,omitempty"`

	// Last update time
	LastUpdated metav1.Time `json:"lastUpdated,omitempty"`
}

// ProtectionStatus defines protection status
type ProtectionStatus struct {
	// Whether protection is active
	Active bool `json:"active,omitempty"`

	// Number of prevented mutations
	PreventedMutations int32 `json:"preventedMutations,omitempty"`

	// Last protection event
	LastProtectionEvent metav1.Time `json:"lastProtectionEvent,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Controller",type="string",JSONPath=".status.controllerStatus.phase"
// +kubebuilder:printcolumn:name="Database",type="string",JSONPath=".status.databaseStatus.phase"
// +kubebuilder:printcolumn:name="Protection",type="boolean",JSONPath=".status.protectionStatus.active"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// MultiNicOperator is the Schema for the multinicoperators API
type MultiNicOperator struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MultiNicOperatorSpec   `json:"spec,omitempty"`
	Status MultiNicOperatorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MultiNicOperatorList contains a list of MultiNicOperator
type MultiNicOperatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MultiNicOperator `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MultiNicOperator{}, &MultiNicOperatorList{})
}
