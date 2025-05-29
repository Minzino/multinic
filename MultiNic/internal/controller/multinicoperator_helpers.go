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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	multinicv1alpha1 "github.com/xormsdhkdwk/multinic/api/v1alpha1"
)

// buildDatabaseStatefulSet creates a StatefulSet for MariaDB
func (r *MultiNicOperatorReconciler) buildDatabaseStatefulSet(operator *multinicv1alpha1.MultiNicOperator) *appsv1.StatefulSet {
	labels := map[string]string{
		"app":           "mariadb",
		operatorLabel:   operator.Name,
		protectionLabel: "true",
	}

	// Default values
	image := "mariadb:10.11"
	if operator.Spec.Database.Image != "" {
		image = operator.Spec.Database.Image
	}

	storageSize := "10Gi"
	if operator.Spec.Database.StorageSize != "" {
		storageSize = operator.Spec.Database.StorageSize
	}

	env := []corev1.EnvVar{
		{Name: "MYSQL_ROOT_PASSWORD", Value: "cloud1234"},
		{Name: "MYSQL_DATABASE", Value: "multinic"},
		{Name: "MYSQL_USER", Value: "multinic"},
		{Name: "MYSQL_PASSWORD", Value: "cloud1234"},
	}

	// Override with custom credentials if provided
	if operator.Spec.Database.Credentials.RootPassword != "" {
		env[0].Value = operator.Spec.Database.Credentials.RootPassword
	}
	if operator.Spec.Database.Credentials.Database != "" {
		env[1].Value = operator.Spec.Database.Credentials.Database
	}
	if operator.Spec.Database.Credentials.User != "" {
		env[2].Value = operator.Spec.Database.Credentials.User
	}
	if operator.Spec.Database.Credentials.Password != "" {
		env[3].Value = operator.Spec.Database.Credentials.Password
	}

	replicas := int32(1)

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mariadb",
			Namespace: operator.Namespace,
			Labels:    labels,
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
					Containers: []corev1.Container{
						{
							Name:  "mariadb",
							Image: image,
							Env:   env,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 3306,
									Name:          "mysql",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "mysql-storage",
									MountPath: "/var/lib/mysql",
								},
							},
							Resources: r.getResourceRequirements(operator.Spec.Database.Resources),
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"mysqladmin", "ping", "-h", "localhost"},
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"mysql", "-h", "localhost", "-u", "root", "-pcloud1234", "-e", "SELECT 1"},
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
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

// buildDatabaseService creates a Service for MariaDB
func (r *MultiNicOperatorReconciler) buildDatabaseService(operator *multinicv1alpha1.MultiNicOperator) *corev1.Service {
	labels := map[string]string{
		"app":           "mariadb",
		operatorLabel:   operator.Name,
		protectionLabel: "true",
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mariadb",
			Namespace: operator.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": "mariadb",
			},
			Ports: []corev1.ServicePort{
				{
					Port:       3306,
					TargetPort: intstr.FromInt(3306),
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}
}

// buildControllerDeployment creates a Deployment for MultiNic Controller
func (r *MultiNicOperatorReconciler) buildControllerDeployment(operator *multinicv1alpha1.MultiNicOperator) *appsv1.Deployment {
	labels := map[string]string{
		"app":           "multinic-controller",
		operatorLabel:   operator.Name,
		protectionLabel: "true",
	}

	// Default values
	image := "multinic:latest"
	if operator.Spec.Controller.Image != "" {
		image = operator.Spec.Controller.Image
	}

	replicas := int32(1)
	if operator.Spec.Controller.Replicas > 0 {
		replicas = operator.Spec.Controller.Replicas
	}

	// Database connection environment variables
	env := []corev1.EnvVar{
		{Name: "DB_HOST", Value: "mariadb.multinic-system.svc.cluster.local"},
		{Name: "DB_PORT", Value: "3306"},
		{Name: "DB_USER", Value: "root"},
		{Name: "DB_PASSWORD", Value: "cloud1234"},
		{Name: "DB_NAME", Value: "multinic"},
	}

	// Add OpenStack environment variables
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

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multinic-controller",
			Namespace: operator.Namespace,
			Labels:    labels,
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
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{true}[0],
					},
					Containers: []corev1.Container{
						{
							Name:    "manager",
							Image:   image,
							Command: []string{"/manager"},
							Args: []string{
								"--leader-elect",
								"--health-probe-bind-address=:8082",
							},
							Env: env,
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &[]bool{false}[0],
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(8082),
									},
								},
								InitialDelaySeconds: 15,
								PeriodSeconds:       20,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/readyz",
										Port: intstr.FromInt(8082),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
							Resources: r.getResourceRequirements(operator.Spec.Controller.Resources),
						},
					},
				},
			},
		},
	}
}

// getResourceRequirements converts ResourceConfig to Kubernetes ResourceRequirements
func (r *MultiNicOperatorReconciler) getResourceRequirements(config multinicv1alpha1.ResourceConfig) corev1.ResourceRequirements {
	requirements := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}

	// Set defaults
	if config.CPU == "" {
		requirements.Requests[corev1.ResourceCPU] = resource.MustParse("10m")
		requirements.Limits[corev1.ResourceCPU] = resource.MustParse("500m")
	} else {
		requirements.Requests[corev1.ResourceCPU] = resource.MustParse(config.CPU)
		requirements.Limits[corev1.ResourceCPU] = resource.MustParse(config.CPU)
	}

	if config.Memory == "" {
		requirements.Requests[corev1.ResourceMemory] = resource.MustParse("64Mi")
		requirements.Limits[corev1.ResourceMemory] = resource.MustParse("128Mi")
	} else {
		requirements.Requests[corev1.ResourceMemory] = resource.MustParse(config.Memory)
		requirements.Limits[corev1.ResourceMemory] = resource.MustParse(config.Memory)
	}

	return requirements
}

// addProtectionLabels adds protection labels to resources
func (r *MultiNicOperatorReconciler) addProtectionLabels(ctx context.Context, kind, name, namespace string, labels map[string]string) error {
	switch kind {
	case "StatefulSet":
		obj := &appsv1.StatefulSet{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
			return err
		}
		if obj.Labels == nil {
			obj.Labels = make(map[string]string)
		}
		for k, v := range labels {
			obj.Labels[k] = v
		}
		return r.Update(ctx, obj)

	case "Deployment":
		obj := &appsv1.Deployment{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
			return err
		}
		if obj.Labels == nil {
			obj.Labels = make(map[string]string)
		}
		for k, v := range labels {
			obj.Labels[k] = v
		}
		return r.Update(ctx, obj)

	case "Service":
		obj := &corev1.Service{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj); err != nil {
			return err
		}
		if obj.Labels == nil {
			obj.Labels = make(map[string]string)
		}
		for k, v := range labels {
			obj.Labels[k] = v
		}
		return r.Update(ctx, obj)
	}

	return nil
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
