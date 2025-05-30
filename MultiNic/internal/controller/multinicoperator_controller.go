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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	multinicv1alpha1 "github.com/xormsdhkdwk/multinic/api/v1alpha1"
)

const (
	operatorFinalizerName = "multinic.example.com/operator-finalizer"
	protectionLabel       = "multinic.example.com/protected"
	operatorLabel         = "multinic.example.com/managed-by-operator"

	// Component names
	databaseComponentName   = "database"
	controllerComponentName = "controller"

	// Default requeue intervals
	defaultRequeueInterval = 30 * time.Second
	errorRequeueInterval   = 1 * time.Minute
	fastRequeueInterval    = 5 * time.Second
)

// ComponentStatus represents the status of a component
type ComponentStatus struct {
	Name      string
	Ready     bool
	Reason    string
	Message   string
	LastCheck time.Time
}

// MultiNicOperatorReconciler reconciles a MultiNicOperator object
type MultiNicOperatorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=multinic.example.com,resources=multinicoperators,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=multinic.example.com,resources=multinicoperators/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=multinic.example.com,resources=multinicoperators/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop
func (r *MultiNicOperatorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	log := log.FromContext(ctx)

	defer func() {
		duration := time.Since(startTime).Seconds()
		OperatorReconcileDuration.WithLabelValues("multinic-operator").Observe(duration)
	}()

	// Fetch the MultiNicOperator instance
	operator := &multinicv1alpha1.MultiNicOperator{}
	if err := r.Get(ctx, req.NamespacedName, operator); err != nil {
		if errors.IsNotFound(err) {
			log.Info("MultiNicOperator resource not found. Object must be deleted")
			ReconcileTotal.WithLabelValues("success").Inc()
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get MultiNicOperator")
		ReconcileTotal.WithLabelValues("error").Inc()
		return ctrl.Result{}, err
	}

	log.Info("Reconciling MultiNicOperator", "name", operator.Name, "namespace", operator.Namespace)

	// Handle deletion
	if operator.DeletionTimestamp != nil {
		result, err := r.handleDeletion(ctx, operator)
		if err != nil {
			ReconcileTotal.WithLabelValues("error").Inc()
		} else {
			ReconcileTotal.WithLabelValues("success").Inc()
		}
		return result, err
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(operator, operatorFinalizerName) {
		log.Info("Adding finalizer to MultiNicOperator")
		controllerutil.AddFinalizer(operator, operatorFinalizerName)
		if err := r.Update(ctx, operator); err != nil {
			log.Error(err, "Failed to add finalizer")
			ReconcileTotal.WithLabelValues("error").Inc()
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Reconcile components with detailed status tracking
	componentStatuses, err := r.reconcileComponents(ctx, operator)
	if err != nil {
		log.Error(err, "Failed to reconcile components")
		r.updateStatusWithComponents(ctx, operator, "Error", err.Error(), componentStatuses)
		ReconcileTotal.WithLabelValues("error").Inc()
		return ctrl.Result{RequeueAfter: errorRequeueInterval}, err
	}

	// Update status with component details
	if r.allComponentsReady(componentStatuses) {
		r.updateStatusWithComponents(ctx, operator, "Running", "All components are running successfully", componentStatuses)
		ReconcileTotal.WithLabelValues("success").Inc()
		// Update component metrics
		r.updateComponentMetrics(operator, componentStatuses)
		return ctrl.Result{RequeueAfter: defaultRequeueInterval}, nil
	} else {
		r.updateStatusWithComponents(ctx, operator, "Pending", "Some components are not ready", componentStatuses)
		ReconcileTotal.WithLabelValues("pending").Inc()
		// Update component metrics
		r.updateComponentMetrics(operator, componentStatuses)
		return ctrl.Result{RequeueAfter: fastRequeueInterval}, nil
	}
}

// reconcileComponents manages all child components with detailed status tracking
func (r *MultiNicOperatorReconciler) reconcileComponents(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) ([]ComponentStatus, error) {
	log := log.FromContext(ctx)
	var componentStatuses []ComponentStatus

	// 1. Reconcile Database if enabled
	if operator.Spec.Database.Enabled {
		status := ComponentStatus{
			Name:      databaseComponentName,
			LastCheck: time.Now(),
		}

		if err := r.reconcileDatabase(ctx, operator); err != nil {
			log.Error(err, "Failed to reconcile database")
			status.Ready = false
			status.Reason = "ReconcileError"
			status.Message = err.Error()
			componentStatuses = append(componentStatuses, status)
			return componentStatuses, fmt.Errorf("database reconciliation failed: %w", err)
		}

		// Check database readiness
		ready, reason, message := r.checkDatabaseReadiness(ctx, operator)
		status.Ready = ready
		status.Reason = reason
		status.Message = message
		componentStatuses = append(componentStatuses, status)
	}

	// 2. Reconcile Controller
	controllerStatus := ComponentStatus{
		Name:      controllerComponentName,
		LastCheck: time.Now(),
	}

	if err := r.reconcileController(ctx, operator); err != nil {
		log.Error(err, "Failed to reconcile controller")
		controllerStatus.Ready = false
		controllerStatus.Reason = "ReconcileError"
		controllerStatus.Message = err.Error()
		componentStatuses = append(componentStatuses, controllerStatus)
		return componentStatuses, fmt.Errorf("controller reconciliation failed: %w", err)
	}

	// Check controller readiness
	ready, reason, message := r.checkControllerReadiness(ctx, operator)
	controllerStatus.Ready = ready
	controllerStatus.Reason = reason
	controllerStatus.Message = message
	componentStatuses = append(componentStatuses, controllerStatus)

	// 3. Apply protection if enabled
	if operator.Spec.Protection.EnableMutationPrevention {
		if err := r.applyProtection(ctx, operator); err != nil {
			log.Error(err, "Failed to apply protection")
			// Protection failure is not critical, just log it
		}
	}

	return componentStatuses, nil
}

// checkDatabaseReadiness checks if the database is ready
func (r *MultiNicOperatorReconciler) checkDatabaseReadiness(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) (bool, string, string) {
	// Check StatefulSet readiness
	statefulset := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: "mariadb", Namespace: operator.Namespace}, statefulset)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, "NotFound", "Database StatefulSet not found"
		}
		return false, "GetError", fmt.Sprintf("Failed to get database StatefulSet: %v", err)
	}

	if statefulset.Status.ReadyReplicas == 0 {
		return false, "NotReady", "Database pods are not ready"
	}

	if statefulset.Status.ReadyReplicas < *statefulset.Spec.Replicas {
		return false, "PartiallyReady", fmt.Sprintf("Database: %d/%d replicas ready",
			statefulset.Status.ReadyReplicas, *statefulset.Spec.Replicas)
	}

	return true, "Ready", "Database is ready"
}

// checkControllerReadiness checks if the controller is ready
func (r *MultiNicOperatorReconciler) checkControllerReadiness(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) (bool, string, string) {
	// Check Deployment readiness
	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: "multinic-controller-manager", Namespace: operator.Namespace}, deployment)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, "NotFound", "Controller Deployment not found"
		}
		return false, "GetError", fmt.Sprintf("Failed to get controller Deployment: %v", err)
	}

	if deployment.Status.ReadyReplicas == 0 {
		return false, "NotReady", "Controller pods are not ready"
	}

	if deployment.Status.ReadyReplicas < *deployment.Spec.Replicas {
		return false, "PartiallyReady", fmt.Sprintf("Controller: %d/%d replicas ready",
			deployment.Status.ReadyReplicas, *deployment.Spec.Replicas)
	}

	return true, "Ready", "Controller is ready"
}

// allComponentsReady checks if all components are ready
func (r *MultiNicOperatorReconciler) allComponentsReady(statuses []ComponentStatus) bool {
	for _, status := range statuses {
		if !status.Ready {
			return false
		}
	}
	return len(statuses) > 0 // At least one component should exist
}

// reconcileDatabase manages the MariaDB StatefulSet with improved error handling
func (r *MultiNicOperatorReconciler) reconcileDatabase(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) error {
	log := log.FromContext(ctx)

	// Create MariaDB StatefulSet
	statefulset := r.buildDatabaseStatefulSet(operator)
	if err := controllerutil.SetControllerReference(operator, statefulset, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference for database StatefulSet: %w", err)
	}

	found := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: statefulset.Name, Namespace: statefulset.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating database StatefulSet",
			"namespace", statefulset.Namespace,
			"name", statefulset.Name)
		if err := r.Create(ctx, statefulset); err != nil {
			return fmt.Errorf("failed to create database StatefulSet: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get database StatefulSet: %w", err)
	} else {
		// StatefulSet exists, check if update is needed
		if r.needsStatefulSetUpdate(found, statefulset) {
			log.Info("Updating database StatefulSet",
				"namespace", statefulset.Namespace,
				"name", statefulset.Name)
			found.Spec = statefulset.Spec
			if err := r.Update(ctx, found); err != nil {
				return fmt.Errorf("failed to update database StatefulSet: %w", err)
			}
		}
	}

	// Create MariaDB Service
	service := r.buildDatabaseService(operator)
	if err := controllerutil.SetControllerReference(operator, service, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference for database Service: %w", err)
	}

	foundSvc := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, foundSvc)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating database Service",
			"namespace", service.Namespace,
			"name", service.Name,
			"type", service.Spec.Type,
			"nodePort", func() int32 {
				if len(service.Spec.Ports) > 0 {
					return service.Spec.Ports[0].NodePort
				}
				return 0
			}())
		if err := r.Create(ctx, service); err != nil {
			return fmt.Errorf("failed to create database Service: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get database Service: %w", err)
	} else {
		// Service exists, check if update is needed
		if r.needsServiceUpdate(foundSvc, service) {
			log.Info("Updating database Service",
				"namespace", service.Namespace,
				"name", service.Name,
				"oldType", foundSvc.Spec.Type,
				"newType", service.Spec.Type,
				"nodePort", func() int32 {
					if len(service.Spec.Ports) > 0 {
						return service.Spec.Ports[0].NodePort
					}
					return 0
				}())
			// Preserve ClusterIP for existing service
			service.Spec.ClusterIP = foundSvc.Spec.ClusterIP
			foundSvc.Spec = service.Spec
			if err := r.Update(ctx, foundSvc); err != nil {
				return fmt.Errorf("failed to update database Service: %w", err)
			}
		}
	}

	return nil
}

// reconcileController manages the MultiNic Controller Deployment with improved error handling
func (r *MultiNicOperatorReconciler) reconcileController(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) error {
	log := log.FromContext(ctx)

	// Create Controller Deployment
	deployment := r.buildControllerDeployment(operator)
	if err := controllerutil.SetControllerReference(operator, deployment, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference for controller Deployment: %w", err)
	}

	found := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating controller Deployment",
			"namespace", deployment.Namespace,
			"name", deployment.Name)
		if err := r.Create(ctx, deployment); err != nil {
			return fmt.Errorf("failed to create controller Deployment: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get controller Deployment: %w", err)
	} else {
		// Deployment exists, check if update is needed
		if r.needsDeploymentUpdate(found, deployment) {
			log.Info("Updating controller Deployment",
				"namespace", deployment.Namespace,
				"name", deployment.Name)
			found.Spec = deployment.Spec
			if err := r.Update(ctx, found); err != nil {
				return fmt.Errorf("failed to update controller Deployment: %w", err)
			}
		}
	}

	return nil
}

// needsStatefulSetUpdate checks if StatefulSet needs to be updated
func (r *MultiNicOperatorReconciler) needsStatefulSetUpdate(existing, desired *appsv1.StatefulSet) bool {
	// Compare key fields that might change
	if existing.Spec.Replicas == nil || desired.Spec.Replicas == nil {
		return existing.Spec.Replicas != desired.Spec.Replicas
	}

	if *existing.Spec.Replicas != *desired.Spec.Replicas {
		return true
	}

	// Compare container image
	if len(existing.Spec.Template.Spec.Containers) > 0 && len(desired.Spec.Template.Spec.Containers) > 0 {
		existingImage := existing.Spec.Template.Spec.Containers[0].Image
		desiredImage := desired.Spec.Template.Spec.Containers[0].Image
		if existingImage != desiredImage {
			return true
		}
	}

	return false
}

// needsDeploymentUpdate checks if Deployment needs to be updated
func (r *MultiNicOperatorReconciler) needsDeploymentUpdate(existing, desired *appsv1.Deployment) bool {
	// Compare key fields that might change
	if existing.Spec.Replicas == nil || desired.Spec.Replicas == nil {
		return existing.Spec.Replicas != desired.Spec.Replicas
	}

	if *existing.Spec.Replicas != *desired.Spec.Replicas {
		return true
	}

	// Compare container image
	if len(existing.Spec.Template.Spec.Containers) > 0 && len(desired.Spec.Template.Spec.Containers) > 0 {
		existingImage := existing.Spec.Template.Spec.Containers[0].Image
		desiredImage := desired.Spec.Template.Spec.Containers[0].Image
		if existingImage != desiredImage {
			return true
		}
	}

	return false
}

// needsServiceUpdate checks if Service needs to be updated
func (r *MultiNicOperatorReconciler) needsServiceUpdate(existing, desired *corev1.Service) bool {
	// Compare service type
	if existing.Spec.Type != desired.Spec.Type {
		return true
	}

	// Compare ports
	if len(existing.Spec.Ports) != len(desired.Spec.Ports) {
		return true
	}

	for i, existingPort := range existing.Spec.Ports {
		if i >= len(desired.Spec.Ports) {
			return true
		}
		desiredPort := desired.Spec.Ports[i]

		if existingPort.Port != desiredPort.Port ||
			existingPort.TargetPort != desiredPort.TargetPort ||
			existingPort.NodePort != desiredPort.NodePort ||
			existingPort.Protocol != desiredPort.Protocol {
			return true
		}
	}

	return false
}

// applyProtection adds protection labels and annotations
func (r *MultiNicOperatorReconciler) applyProtection(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) error {
	// Apply protection labels to managed resources
	labels := map[string]string{
		protectionLabel: "true",
		operatorLabel:   operator.Name,
	}

	// Apply to StatefulSets
	if err := r.addProtectionLabels(ctx, "StatefulSet", "mariadb", operator.Namespace, labels); err != nil {
		return fmt.Errorf("failed to apply protection to StatefulSet: %w", err)
	}

	// Apply to Deployments
	if err := r.addProtectionLabels(ctx, "Deployment", "multinic-controller-manager", operator.Namespace, labels); err != nil {
		return fmt.Errorf("failed to apply protection to Deployment: %w", err)
	}

	// Apply to Services
	if err := r.addProtectionLabels(ctx, "Service", "mariadb", operator.Namespace, labels); err != nil {
		return fmt.Errorf("failed to apply protection to Service: %w", err)
	}

	return nil
}

// handleDeletion handles the cleanup when MultiNicOperator is deleted
func (r *MultiNicOperatorReconciler) handleDeletion(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(operator, operatorFinalizerName) {
		log.Info("Performing cleanup for MultiNicOperator", "name", operator.Name)

		// Remove protection from managed resources
		if err := r.removeProtection(ctx, operator); err != nil {
			log.Error(err, "Failed to remove protection during deletion")
			// Continue with cleanup even if protection removal fails
		}

		// Clean up managed resources if needed
		// Note: Resources with controller references will be automatically cleaned up by Kubernetes

		// Remove finalizer
		controllerutil.RemoveFinalizer(operator, operatorFinalizerName)
		if err := r.Update(ctx, operator); err != nil {
			log.Error(err, "Failed to remove finalizer")
			return ctrl.Result{}, err
		}

		log.Info("Finalizer removed, MultiNicOperator cleanup completed")
	}

	return ctrl.Result{}, nil
}

// updateStatusWithComponents updates the MultiNicOperator status with component details
func (r *MultiNicOperatorReconciler) updateStatusWithComponents(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator, phase, message string, componentStatuses []ComponentStatus) {
	log := log.FromContext(ctx)

	// Prepare conditions based on component statuses
	var conditions []metav1.Condition
	now := metav1.NewTime(time.Now())

	// Overall condition
	overallCondition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "ComponentsNotReady",
		Message:            message,
		LastTransitionTime: now,
	}

	if r.allComponentsReady(componentStatuses) {
		overallCondition.Status = metav1.ConditionTrue
		overallCondition.Reason = "AllComponentsReady"
	}

	conditions = append(conditions, overallCondition)

	// Individual component conditions
	for _, status := range componentStatuses {
		condition := metav1.Condition{
			Type:               fmt.Sprintf("%sReady", status.Name),
			Status:             metav1.ConditionFalse,
			Reason:             status.Reason,
			Message:            status.Message,
			LastTransitionTime: metav1.NewTime(status.LastCheck),
		}
		if status.Ready {
			condition.Status = metav1.ConditionTrue
		}
		conditions = append(conditions, condition)
	}

	// Update status
	operator.Status.Phase = phase
	operator.Status.Message = message
	operator.Status.Conditions = conditions
	operator.Status.ObservedGeneration = operator.Generation

	if err := r.Status().Update(ctx, operator); err != nil {
		log.Error(err, "Failed to update MultiNicOperator status")
	}
}

// updateComponentMetrics updates component status metrics
func (r *MultiNicOperatorReconciler) updateComponentMetrics(operator *multinicv1alpha1.MultiNicOperator, statuses []ComponentStatus) {
	for _, status := range statuses {
		value := 0.0
		if status.Ready {
			value = 1.0
		}
		ComponentStatusMetric.WithLabelValues(status.Name, operator.Namespace, operator.Name).Set(value)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *MultiNicOperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&multinicv1alpha1.MultiNicOperator{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
