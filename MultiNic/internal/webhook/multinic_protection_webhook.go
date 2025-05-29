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

package webhook

import (
	"context"
	"fmt"
	"net/http"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	protectionLabel = "multinic.example.com/protected"
	operatorLabel   = "multinic.example.com/managed-by-operator"
)

// +kubebuilder:webhook:path=/validate-multinic-protection,mutating=false,failurePolicy=fail,sideEffects=None,groups=apps;core,resources=deployments;statefulsets;services,verbs=update;delete,versions=v1,name=vprotection.multinic.example.com,admissionReviewVersions=v1

// MultiNicProtectionValidator validates operations on protected resources
type MultiNicProtectionValidator struct {
	Client  client.Client
	decoder *admission.Decoder
}

// Handle validates admission requests for protected MultiNic resources
func (v *MultiNicProtectionValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	// Skip validation for system namespaces
	if isSystemNamespace(req.Namespace) {
		return admission.Allowed("System namespace operations are allowed")
	}

	// Check if this is a protected resource
	isProtected, operatorName, err := v.isResourceProtected(ctx, req)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	if !isProtected {
		return admission.Allowed("Resource is not protected")
	}

	// Allow operations from the operator itself
	if v.isOperatorRequest(req) {
		return admission.Allowed("Operation from MultiNic Operator is allowed")
	}

	// Block direct modifications to protected resources
	message := fmt.Sprintf("Direct modification of MultiNic protected resource is not allowed. "+
		"This resource is managed by MultiNicOperator '%s'. "+
		"Please modify the MultiNicOperator configuration instead.", operatorName)

	return admission.Denied(message)
}

// isResourceProtected checks if the resource has protection labels
func (v *MultiNicProtectionValidator) isResourceProtected(ctx context.Context, req admission.Request) (bool, string, error) {
	var labels map[string]string
	var err error

	switch req.Kind.Kind {
	case "Deployment":
		obj := &appsv1.Deployment{}
		err = v.Client.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, obj)
		if err != nil {
			return false, "", err
		}
		labels = obj.Labels

	case "StatefulSet":
		obj := &appsv1.StatefulSet{}
		err = v.Client.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, obj)
		if err != nil {
			return false, "", err
		}
		labels = obj.Labels

	case "Service":
		obj := &corev1.Service{}
		err = v.Client.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, obj)
		if err != nil {
			return false, "", err
		}
		labels = obj.Labels

	default:
		return false, "", nil
	}

	if labels == nil {
		return false, "", nil
	}

	protected, hasProtection := labels[protectionLabel]
	operatorName, _ := labels[operatorLabel]

	return hasProtection && protected == "true", operatorName, nil
}

// isOperatorRequest checks if the request is coming from the MultiNic Operator
func (v *MultiNicProtectionValidator) isOperatorRequest(req admission.Request) bool {
	// Check user info for operator service account
	userInfo := req.UserInfo
	if userInfo.Username == "system:serviceaccount:multinic-system:multinic-operator" {
		return true
	}

	// Check for operator annotations or labels in the request
	// This could be enhanced to check for specific operator tokens or certificates
	return false
}

// isSystemNamespace checks if the namespace is a system namespace
func isSystemNamespace(namespace string) bool {
	systemNamespaces := []string{
		"kube-system",
		"kube-public",
		"kube-node-lease",
		"default",
	}

	for _, ns := range systemNamespaces {
		if namespace == ns {
			return true
		}
	}

	return false
}

// InjectDecoder injects the decoder
func (v *MultiNicProtectionValidator) InjectDecoder(d *admission.Decoder) error {
	v.decoder = d
	return nil
}
