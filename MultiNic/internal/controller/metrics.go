package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// OpenstackRequestDuration measures the duration of OpenStack API requests
	OpenstackRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "multinic_openstack_request_duration_seconds",
			Help:    "Duration of OpenStack API requests in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation", "status"},
	)

	// DatabaseOperationDuration measures the duration of database operations
	DatabaseOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "multinic_database_operation_duration_seconds",
			Help:    "Duration of database operations in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation", "status"},
	)

	// ReconcileTotal counts the total number of reconcile operations
	ReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "multinic_reconcile_total",
			Help: "Total number of reconcile operations",
		},
		[]string{"status"},
	)

	// OperatorReconcileDuration measures the duration of operator reconcile operations
	OperatorReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "multinic_operator_reconcile_duration_seconds",
			Help:    "Duration of operator reconcile operations in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operator"},
	)

	// ActiveConnections tracks the number of active database connections
	ActiveConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "multinic_active_db_connections",
			Help: "Number of active database connections",
		},
	)

	// ComponentStatusMetric tracks the status of MultiNic components
	ComponentStatusMetric = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "multinic_component_status",
			Help: "Status of MultiNic components (1=ready, 0=not ready)",
		},
		[]string{"component", "namespace", "operator"},
	)
)

func init() {
	// Register metrics with the controller-runtime metrics registry
	metrics.Registry.MustRegister(
		OpenstackRequestDuration,
		DatabaseOperationDuration,
		ReconcileTotal,
		OperatorReconcileDuration,
		ActiveConnections,
		ComponentStatusMetric,
	)
}
