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
)

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
	log := log.FromContext(ctx)

	// Fetch the MultiNicOperator instance
	operator := &multinicv1alpha1.MultiNicOperator{}
	if err := r.Get(ctx, req.NamespacedName, operator); err != nil {
		if errors.IsNotFound(err) {
			log.Info("MultiNicOperator resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get MultiNicOperator")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if operator.DeletionTimestamp != nil {
		return r.handleDeletion(ctx, operator)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(operator, operatorFinalizerName) {
		controllerutil.AddFinalizer(operator, operatorFinalizerName)
		if err := r.Update(ctx, operator); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Reconcile components
	if err := r.reconcileComponents(ctx, operator); err != nil {
		log.Error(err, "Failed to reconcile components")
		r.updateStatus(ctx, operator, "Error", err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}

	// Update status
	r.updateStatus(ctx, operator, "Running", "All components are running successfully")

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// reconcileComponents manages all child components
func (r *MultiNicOperatorReconciler) reconcileComponents(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) error {
	log := log.FromContext(ctx)

	// 1. Reconcile Database if enabled
	if operator.Spec.Database.Enabled {
		if err := r.reconcileDatabase(ctx, operator); err != nil {
			log.Error(err, "Failed to reconcile database")
			return err
		}
	}

	// 2. Reconcile Controller
	if err := r.reconcileController(ctx, operator); err != nil {
		log.Error(err, "Failed to reconcile controller")
		return err
	}

	// 3. Apply protection if enabled
	if operator.Spec.Protection.EnableMutationPrevention {
		if err := r.applyProtection(ctx, operator); err != nil {
			log.Error(err, "Failed to apply protection")
			return err
		}
	}

	return nil
}

// reconcileDatabase manages the MariaDB StatefulSet
func (r *MultiNicOperatorReconciler) reconcileDatabase(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) error {
	log := log.FromContext(ctx)

	// Create MariaDB StatefulSet
	statefulset := r.buildDatabaseStatefulSet(operator)
	if err := controllerutil.SetControllerReference(operator, statefulset, r.Scheme); err != nil {
		return err
	}

	found := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: statefulset.Name, Namespace: statefulset.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating a new Database StatefulSet", "StatefulSet.Namespace", statefulset.Namespace, "StatefulSet.Name", statefulset.Name)
		if err := r.Create(ctx, statefulset); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// Create MariaDB Service
	service := r.buildDatabaseService(operator)
	if err := controllerutil.SetControllerReference(operator, service, r.Scheme); err != nil {
		return err
	}

	foundSvc := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, foundSvc)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating a new Database Service", "Service.Namespace", service.Namespace, "Service.Name", service.Name)
		if err := r.Create(ctx, service); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	return nil
}

// reconcileController manages the MultiNic Controller Deployment
func (r *MultiNicOperatorReconciler) reconcileController(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) error {
	log := log.FromContext(ctx)

	// Create Controller Deployment
	deployment := r.buildControllerDeployment(operator)
	if err := controllerutil.SetControllerReference(operator, deployment, r.Scheme); err != nil {
		return err
	}

	found := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating a new Controller Deployment", "Deployment.Namespace", deployment.Namespace, "Deployment.Name", deployment.Name)
		if err := r.Create(ctx, deployment); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	return nil
}

// applyProtection adds protection labels and annotations
func (r *MultiNicOperatorReconciler) applyProtection(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) error {
	// Apply protection labels to managed resources
	labels := map[string]string{
		protectionLabel: "true",
		operatorLabel:   operator.Name,
	}

	// Update database resources with protection labels
	if operator.Spec.Database.Enabled {
		if err := r.addProtectionLabels(ctx, "StatefulSet", "mariadb", operator.Namespace, labels); err != nil {
			return err
		}
		if err := r.addProtectionLabels(ctx, "Service", "mariadb", operator.Namespace, labels); err != nil {
			return err
		}
	}

	// Update controller resources with protection labels
	if err := r.addProtectionLabels(ctx, "Deployment", "multinic-controller", operator.Namespace, labels); err != nil {
		return err
	}

	return nil
}

// handleDeletion handles the deletion of MultiNicOperator
func (r *MultiNicOperatorReconciler) handleDeletion(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(operator, operatorFinalizerName) {
		log.Info("Performing cleanup for MultiNicOperator", "Name", operator.Name)

		// Remove protection labels before deletion
		if err := r.removeProtection(ctx, operator); err != nil {
			log.Error(err, "Failed to remove protection")
			return ctrl.Result{}, err
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(operator, operatorFinalizerName)
		if err := r.Update(ctx, operator); err != nil {
			log.Error(err, "Failed to remove finalizer")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// updateStatus updates the MultiNicOperator status
func (r *MultiNicOperatorReconciler) updateStatus(ctx context.Context, operator *multinicv1alpha1.MultiNicOperator, phase, message string) {
	operator.Status.Phase = phase
	operator.Status.Message = message
	operator.Status.LastUpdated = metav1.Now()

	if err := r.Status().Update(ctx, operator); err != nil {
		log := log.FromContext(ctx)
		log.Error(err, "Failed to update MultiNicOperator status")
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *MultiNicOperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&multinicv1alpha1.MultiNicOperator{}).
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
