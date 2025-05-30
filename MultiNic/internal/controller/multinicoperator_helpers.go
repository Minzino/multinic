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

package controllers

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"

	multinicv1alpha1 "github.com/xormsdhkdwk/multinic/api/v1alpha1"
)

const (
	// Default database configuration
	defaultDBImage       = "mariadb:10.11"
	defaultDBStorageSize = "10Gi"
	defaultDBRootPwd     = "cloud1234"
	defaultDatabaseName  = "multinic"
	defaultDatabaseUser  = "multinic"
	defaultDBUserPwd     = "cloud1234"

	// Default controller configuration
	defaultControllerImage    = "multinic:v1alpha1"
	defaultControllerReplicas = 1

	// Resource defaults
	defaultDBCPURequest    = "100m"
	defaultDBCPULimit      = "500m"
	defaultDBMemoryRequest = "128Mi"
	defaultDBMemoryLimit   = "512Mi"

	defaultControllerCPURequest    = "10m"
	defaultControllerCPULimit      = "500m"
	defaultControllerMemoryRequest = "64Mi"
	defaultControllerMemoryLimit   = "128Mi"
)

// buildDatabaseStatefulSet creates a StatefulSet for MariaDB with optimized configuration
func (r *MultiNicOperatorReconciler) buildDatabaseStatefulSet(operator *multinicv1alpha1.MultiNicOperator) *appsv1.StatefulSet {
	labels := map[string]string{
		"app":           "mariadb",
		"component":     "database",
		operatorLabel:   operator.Name,
		protectionLabel: "true",
	}

	// Image configuration
	image := defaultDBImage
	if operator.Spec.Database.Image != "" {
		image = operator.Spec.Database.Image
	}

	// Storage configuration
	storageSize := defaultDBStorageSize
	if operator.Spec.Database.StorageSize != "" {
		storageSize = operator.Spec.Database.StorageSize
	}

	// Database credentials with fallbacks
	env := r.buildDatabaseEnvVars(operator)

	replicas := int32(1)
	runAsNonRoot := true
	allowPrivilegeEscalation := false
	readOnlyRootFilesystem := false
	runAsUser := int64(999) // mysql user

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mariadb",
			Namespace: operator.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"multinic.example.com/managed-by": operator.Name,
			},
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: "mariadb",
			Replicas:    &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "mariadb",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &runAsNonRoot,
						RunAsUser:    &runAsUser,
						FSGroup:      &runAsUser,
					},
					Containers: []corev1.Container{
						{
							Name:            "mariadb",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env:             env,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 3306,
									Name:          "mysql",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "mysql-storage",
									MountPath: "/var/lib/mysql",
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &allowPrivilegeEscalation,
								ReadOnlyRootFilesystem:   &readOnlyRootFilesystem,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
									Add:  []corev1.Capability{"CHOWN", "DAC_OVERRIDE", "SETGID", "SETUID"},
								},
							},
							Resources: r.getDatabaseResourceRequirements(operator.Spec.Database.Resources),
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"mysqladmin", "ping", "-h", "localhost", "-u", "root", "-p" + r.getDatabasePassword(operator, "root")},
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
								FailureThreshold:    3,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"mysql", "-h", "localhost", "-u", "root", "-p" + r.getDatabasePassword(operator, "root"), "-e", "SELECT 1"},
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
								TimeoutSeconds:      3,
								FailureThreshold:    3,
							},
							StartupProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"mysqladmin", "ping", "-h", "localhost", "-u", "root", "-p" + r.getDatabasePassword(operator, "root")},
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       5,
								TimeoutSeconds:      3,
								FailureThreshold:    30,
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "mysql-storage",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.ReadWriteOnce,
						},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse(storageSize),
							},
						},
					},
				},
			},
		},
	}
}

// buildDatabaseEnvVars creates environment variables for database container
func (r *MultiNicOperatorReconciler) buildDatabaseEnvVars(operator *multinicv1alpha1.MultiNicOperator) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "MYSQL_ROOT_PASSWORD", Value: r.getDatabasePassword(operator, "root")},
		{Name: "MYSQL_DATABASE", Value: r.getDatabaseName(operator)},
		{Name: "MYSQL_USER", Value: r.getDatabaseUser(operator)},
		{Name: "MYSQL_PASSWORD", Value: r.getDatabasePassword(operator, "user")},
	}

	return env
}

// getDatabasePassword returns database password with fallback
func (r *MultiNicOperatorReconciler) getDatabasePassword(operator *multinicv1alpha1.MultiNicOperator, passwordType string) string {
	switch passwordType {
	case "root":
		if operator.Spec.Database.Credentials.RootPassword != "" {
			return operator.Spec.Database.Credentials.RootPassword
		}
		return defaultDBRootPwd
	case "user":
		if operator.Spec.Database.Credentials.Password != "" {
			return operator.Spec.Database.Credentials.Password
		}
		return defaultDBUserPwd
	default:
		return defaultDBUserPwd
	}
}

// getDatabaseName returns database name with fallback
func (r *MultiNicOperatorReconciler) getDatabaseName(operator *multinicv1alpha1.MultiNicOperator) string {
	if operator.Spec.Database.Credentials.Database != "" {
		return operator.Spec.Database.Credentials.Database
	}
	return defaultDatabaseName
}

// getDatabaseUser returns database user with fallback
func (r *MultiNicOperatorReconciler) getDatabaseUser(operator *multinicv1alpha1.MultiNicOperator) string {
	if operator.Spec.Database.Credentials.User != "" {
		return operator.Spec.Database.Credentials.User
	}
	return defaultDatabaseUser
}

// buildDatabaseService creates a Service for MariaDB
func (r *MultiNicOperatorReconciler) buildDatabaseService(operator *multinicv1alpha1.MultiNicOperator) *corev1.Service {
	labels := map[string]string{
		"app":           "mariadb",
		"component":     "database",
		operatorLabel:   operator.Name,
		protectionLabel: "true",
	}

	// Default service configuration
	serviceType := corev1.ServiceTypeClusterIP
	servicePort := int32(3306)
	var nodePort int32

	// Apply service configuration from CR if specified
	if operator.Spec.Database.Service.Type != "" {
		switch operator.Spec.Database.Service.Type {
		case "NodePort":
			serviceType = corev1.ServiceTypeNodePort
		case "LoadBalancer":
			serviceType = corev1.ServiceTypeLoadBalancer
		case "ClusterIP":
			serviceType = corev1.ServiceTypeClusterIP
		default:
			serviceType = corev1.ServiceTypeClusterIP
		}
	}

	if operator.Spec.Database.Service.Port > 0 {
		servicePort = operator.Spec.Database.Service.Port
	}

	if operator.Spec.Database.Service.NodePort > 0 && serviceType == corev1.ServiceTypeNodePort {
		nodePort = operator.Spec.Database.Service.NodePort
	}

	serviceSpec := corev1.ServiceSpec{
		Selector: map[string]string{
			"app": "mariadb",
		},
		Ports: []corev1.ServicePort{
			{
				Name:       "mysql",
				Port:       servicePort,
				TargetPort: intstr.FromInt(3306),
				Protocol:   corev1.ProtocolTCP,
			},
		},
		Type: serviceType,
	}

	// Set NodePort if specified and service type is NodePort
	if serviceType == corev1.ServiceTypeNodePort && nodePort > 0 {
		serviceSpec.Ports[0].NodePort = nodePort
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mariadb",
			Namespace: operator.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"multinic.example.com/managed-by": operator.Name,
			},
		},
		Spec: serviceSpec,
	}
}

// buildControllerDeployment creates a Deployment for MultiNic Controller with optimized configuration
func (r *MultiNicOperatorReconciler) buildControllerDeployment(operator *multinicv1alpha1.MultiNicOperator) *appsv1.Deployment {
	labels := map[string]string{
		"app":           "multinic-controller",
		"component":     "controller",
		operatorLabel:   operator.Name,
		protectionLabel: "true",
	}

	// Image configuration
	image := defaultControllerImage
	if operator.Spec.Controller.Image != "" {
		image = operator.Spec.Controller.Image
	}

	// Replica configuration
	replicas := int32(defaultControllerReplicas)
	if operator.Spec.Controller.Replicas > 0 {
		replicas = operator.Spec.Controller.Replicas
	}

	// Environment variables with optimized database connection
	env := r.buildControllerEnvVars(operator)

	runAsNonRoot := true
	allowPrivilegeEscalation := false
	readOnlyRootFilesystem := true

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multinic-controller-manager",
			Namespace: operator.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"multinic.example.com/managed-by": operator.Name,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "multinic-controller",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "multinic-operator",
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &runAsNonRoot,
					},
					Containers: []corev1.Container{
						{
							Name:            "manager",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"/manager"},
							Args: []string{
								"--health-probe-bind-address=:8082",
								"--metrics-bind-address=:8080",
								"--leader-elect",
							},
							Env: env,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8080,
									Name:          "metrics",
									Protocol:      corev1.ProtocolTCP,
								},
								{
									ContainerPort: 8082,
									Name:          "health",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &allowPrivilegeEscalation,
								ReadOnlyRootFilesystem:   &readOnlyRootFilesystem,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "tmp",
									MountPath: "/tmp",
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/healthz",
										Port:   intstr.FromInt(8082),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       20,
								TimeoutSeconds:      5,
								FailureThreshold:    3,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/readyz",
										Port:   intstr.FromInt(8082),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
								TimeoutSeconds:      3,
								FailureThreshold:    3,
							},
							Resources: r.getControllerResourceRequirements(operator.Spec.Controller.Resources),
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "tmp",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}
}

// buildControllerEnvVars creates environment variables for controller container
func (r *MultiNicOperatorReconciler) buildControllerEnvVars(operator *multinicv1alpha1.MultiNicOperator) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "CONTROLLER_MODE", Value: "true"},
		{Name: "DB_HOST", Value: fmt.Sprintf("mariadb.%s.svc.cluster.local", operator.Namespace)},
		{Name: "DB_PORT", Value: "3306"},
		{Name: "DB_USER", Value: r.getDatabaseUser(operator)},
		{Name: "DB_PASSWORD", Value: r.getDatabasePassword(operator, "user")},
		{Name: "DB_NAME", Value: r.getDatabaseName(operator)},
	}

	// Add OpenStack environment variables if configured
	if operator.Spec.OpenStack.IdentityEndpoint != "" {
		env = append(env, corev1.EnvVar{
			Name:  "OPENSTACK_AUTH_URL",
			Value: operator.Spec.OpenStack.IdentityEndpoint,
		})
	}
	if operator.Spec.OpenStack.NetworkEndpoint != "" {
		env = append(env, corev1.EnvVar{
			Name:  "OPENSTACK_NETWORK_ENDPOINT",
			Value: operator.Spec.OpenStack.NetworkEndpoint,
		})
	}
	if operator.Spec.OpenStack.ComputeEndpoint != "" {
		env = append(env, corev1.EnvVar{
			Name:  "OPENSTACK_COMPUTE_ENDPOINT",
			Value: operator.Spec.OpenStack.ComputeEndpoint,
		})
	}

	return env
}

// getDatabaseResourceRequirements converts ResourceConfig to Kubernetes ResourceRequirements for database
func (r *MultiNicOperatorReconciler) getDatabaseResourceRequirements(config multinicv1alpha1.ResourceConfig) corev1.ResourceRequirements {
	requirements := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}

	// CPU configuration
	if config.CPU != "" {
		requirements.Requests[corev1.ResourceCPU] = resource.MustParse(config.CPU)
		requirements.Limits[corev1.ResourceCPU] = resource.MustParse(config.CPU)
	} else {
		requirements.Requests[corev1.ResourceCPU] = resource.MustParse(defaultDBCPURequest)
		requirements.Limits[corev1.ResourceCPU] = resource.MustParse(defaultDBCPULimit)
	}

	// Memory configuration
	if config.Memory != "" {
		requirements.Requests[corev1.ResourceMemory] = resource.MustParse(config.Memory)
		requirements.Limits[corev1.ResourceMemory] = resource.MustParse(config.Memory)
	} else {
		requirements.Requests[corev1.ResourceMemory] = resource.MustParse(defaultDBMemoryRequest)
		requirements.Limits[corev1.ResourceMemory] = resource.MustParse(defaultDBMemoryLimit)
	}

	return requirements
}

// getControllerResourceRequirements converts ResourceConfig to Kubernetes ResourceRequirements for controller
func (r *MultiNicOperatorReconciler) getControllerResourceRequirements(config multinicv1alpha1.ResourceConfig) corev1.ResourceRequirements {
	requirements := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}

	// CPU configuration
	if config.CPU != "" {
		requirements.Requests[corev1.ResourceCPU] = resource.MustParse(config.CPU)
		requirements.Limits[corev1.ResourceCPU] = resource.MustParse(config.CPU)
	} else {
		requirements.Requests[corev1.ResourceCPU] = resource.MustParse(defaultControllerCPURequest)
		requirements.Limits[corev1.ResourceCPU] = resource.MustParse(defaultControllerCPULimit)
	}

	// Memory configuration
	if config.Memory != "" {
		requirements.Requests[corev1.ResourceMemory] = resource.MustParse(config.Memory)
		requirements.Limits[corev1.ResourceMemory] = resource.MustParse(config.Memory)
	} else {
		requirements.Requests[corev1.ResourceMemory] = resource.MustParse(defaultControllerMemoryRequest)
		requirements.Limits[corev1.ResourceMemory] = resource.MustParse(defaultControllerMemoryLimit)
	}

	return requirements
}

// addProtectionLabels adds protection labels to resources with retry logic
func (r *MultiNicOperatorReconciler) addProtectionLabels(ctx context.Context, kind, name, namespace string, labels map[string]string) error {
	return wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		switch kind {
		case "StatefulSet":
			obj := &appsv1.StatefulSet{}
			if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
				if errors.IsNotFound(err) {
					return false, nil // Not found, retry
				}
				return false, err
			}
			if obj.Labels == nil {
				obj.Labels = make(map[string]string)
			}
			for k, v := range labels {
				obj.Labels[k] = v
			}
			if err := r.Update(ctx, obj); err != nil {
				if errors.IsConflict(err) {
					return false, nil // Conflict, retry
				}
				return false, err
			}
			return true, nil

		case "Deployment":
			obj := &appsv1.Deployment{}
			if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
				if errors.IsNotFound(err) {
					return false, nil // Not found, retry
				}
				return false, err
			}
			if obj.Labels == nil {
				obj.Labels = make(map[string]string)
			}
			for k, v := range labels {
				obj.Labels[k] = v
			}
			if err := r.Update(ctx, obj); err != nil {
				if errors.IsConflict(err) {
					return false, nil // Conflict, retry
				}
				return false, err
			}
			return true, nil

		case "Service":
			obj := &corev1.Service{}
			if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
				if errors.IsNotFound(err) {
					return false, nil // Not found, retry
				}
				return false, err
			}
			if obj.Labels == nil {
				obj.Labels = make(map[string]string)
			}
			for k, v := range labels {
				obj.Labels[k] = v
			}
			if err := r.Update(ctx, obj); err != nil {
				if errors.IsConflict(err) {
					return false, nil // Conflict, retry
				}
				return false, err
			}
			return true, nil
		}

		return true, nil
	})
}

// removeProtection removes protection labels from managed resources
func (r *MultiNicOperatorReconciler) removeProtection(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) error {
	// Remove protection labels from database resources
	if operator.Spec.Database.Enabled {
		r.removeProtectionLabels(ctx, "StatefulSet", "mariadb", operator.Namespace)
		r.removeProtectionLabels(ctx, "Service", "mariadb", operator.Namespace)
	}

	// Remove protection labels from controller resources
	r.removeProtectionLabels(ctx, "Deployment", "multinic-controller", operator.Namespace)

	return nil
}

// removeProtectionLabels removes protection labels from a resource
func (r *MultiNicOperatorReconciler) removeProtectionLabels(ctx context.Context, kind, name, namespace string) error {
	switch kind {
	case "StatefulSet":
		obj := &appsv1.StatefulSet{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
			return err
		}
		if obj.Labels != nil {
			delete(obj.Labels, protectionLabel)
			delete(obj.Labels, operatorLabel)
		}
		return r.Update(ctx, obj)

	case "Deployment":
		obj := &appsv1.Deployment{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
			return err
		}
		if obj.Labels != nil {
			delete(obj.Labels, protectionLabel)
			delete(obj.Labels, operatorLabel)
		}
		return r.Update(ctx, obj)

	case "Service":
		obj := &corev1.Service{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
			return err
		}
		if obj.Labels != nil {
			delete(obj.Labels, protectionLabel)
			delete(obj.Labels, operatorLabel)
		}
		return r.Update(ctx, obj)
	}

	return nil
}
