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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// OpenstackConfigSpec defines the desired state of OpenstackConfig.
type OpenstackConfigSpec struct {
	// SubnetName is the name of the OpenStack subnet
	SubnetName string `json:"subnetName"`

	// VMNames is a list of OpenStack VM names to monitor
	VMNames []string `json:"vmNames"`

	// OpenStackCredentials contains the credentials for OpenStack API
	Credentials OpenStackCredentials `json:"credentials"`
}

// OpenStackCredentials defines the credentials for OpenStack API
type OpenStackCredentials struct {
	// AuthURL is the authentication URL for OpenStack
	AuthURL string `json:"authURL"`

	// Username for OpenStack authentication
	Username string `json:"username"`

	// Password for OpenStack authentication
	Password string `json:"password"`

	// ProjectID is the ID of the OpenStack project
	ProjectID string `json:"projectID"`

	// DomainName is the name of the OpenStack domain
	DomainName string `json:"domainName"`

	// NetworkEndpoint is the URL for OpenStack Network service (Neutron)
	// +optional
	NetworkEndpoint string `json:"networkEndpoint,omitempty"`

	// ComputeEndpoint is the URL for OpenStack Compute service (Nova)
	// +optional
	ComputeEndpoint string `json:"computeEndpoint,omitempty"`
}

// OpenstackConfigStatus defines the observed state of OpenstackConfig.
type OpenstackConfigStatus struct {
	// Status represents the current status of the OpenstackConfig
	Status string `json:"status,omitempty"`

	// LastUpdated represents the last time the status was updated
	LastUpdated metav1.Time `json:"lastUpdated,omitempty"`

	// Message provides additional information about the current status
	Message string `json:"message,omitempty"`

	// VMNetworkInfos contains the network information for each VM retrieved from OpenStack
	VMNetworkInfos []VMNetworkInfo `json:"vmNetworkInfos,omitempty"`
}

// VMNetworkInfo contains the network information for a specific VM from OpenStack
type VMNetworkInfo struct {
	// VMName is the name of the VM
	VMName string `json:"vmName"`

	// SubnetID is the ID of the OpenStack subnet
	SubnetID string `json:"subnetID,omitempty"`

	// NetworkID is the ID of the OpenStack network
	NetworkID string `json:"networkID,omitempty"`

	// IPAddress is the IP address assigned to the VM
	IPAddress string `json:"ipAddress,omitempty"`

	// MACAddress is the MAC address of the VM's network interface
	MACAddress string `json:"macAddress,omitempty"`

	// Status represents the status of this specific VM
	Status string `json:"status,omitempty"`

	// Message provides additional information about this VM's status
	Message string `json:"message,omitempty"`
}

// NetworkInfo contains the network information from OpenStack
// +deprecated Use VMNetworkInfo instead for better multi-VM support
type NetworkInfo struct {
	// SubnetID is the ID of the OpenStack subnet
	SubnetID string `json:"subnetID,omitempty"`

	// NetworkID is the ID of the OpenStack network
	NetworkID string `json:"networkID,omitempty"`

	// IPAddress is the IP address assigned to the VM
	IPAddress string `json:"ipAddress,omitempty"`

	// MACAddress is the MAC address of the VM's network interface
	MACAddress string `json:"macAddress,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// OpenstackConfig is the Schema for the openstackconfigs API.
type OpenstackConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OpenstackConfigSpec   `json:"spec,omitempty"`
	Status OpenstackConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OpenstackConfigList contains a list of OpenstackConfig.
type OpenstackConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpenstackConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OpenstackConfig{}, &OpenstackConfigList{})
}
