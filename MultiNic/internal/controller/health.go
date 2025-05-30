package controllers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	healthCheckTimeout = 5 * time.Second
)

var (
	// HealthCheckDuration tracks health check duration
	HealthCheckDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "multinic_health_check_duration_seconds",
			Help:    "Duration of health checks in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"component", "status"},
	)

	// HealthCheckTotal counts health check attempts
	HealthCheckTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "multinic_health_check_total",
			Help: "Total number of health check attempts",
		},
		[]string{"component", "status"},
	)
)

func init() {
	// Register health check metrics
	prometheus.MustRegister(HealthCheckDuration, HealthCheckTotal)
}

// DatabaseHealthChecker checks database connectivity with enhanced monitoring
type DatabaseHealthChecker struct {
	reconciler *OpenstackConfigReconciler
}

// NewDatabaseHealthChecker creates a new database health checker
func NewDatabaseHealthChecker(reconciler *OpenstackConfigReconciler) *DatabaseHealthChecker {
	return &DatabaseHealthChecker{
		reconciler: reconciler,
	}
}

// Check implements healthz.Checker interface with improved error handling
func (c *DatabaseHealthChecker) Check(req *http.Request) error {
	startTime := time.Now()
	component := "database"

	defer func() {
		duration := time.Since(startTime).Seconds()
		HealthCheckDuration.WithLabelValues(component, "completed").Observe(duration)
	}()

	ctx, cancel := context.WithTimeout(req.Context(), healthCheckTimeout)
	defer cancel()

	log := log.FromContext(ctx)

	// Check database connection
	if err := c.reconciler.ensureDBConnection(ctx); err != nil {
		log.Error(err, "Database health check failed - connection error")
		HealthCheckTotal.WithLabelValues(component, "connection_error").Inc()
		return fmt.Errorf("database connection failed: %w", err)
	}

	// Ping database
	if err := c.reconciler.DB.PingContext(ctx); err != nil {
		log.Error(err, "Database health check failed - ping error")
		HealthCheckTotal.WithLabelValues(component, "ping_error").Inc()
		return fmt.Errorf("database ping failed: %w", err)
	}

	// Test query execution
	var result int
	query := "SELECT 1"
	if err := c.reconciler.DB.QueryRowContext(ctx, query).Scan(&result); err != nil {
		log.Error(err, "Database health check failed - query error")
		HealthCheckTotal.WithLabelValues(component, "query_error").Inc()
		return fmt.Errorf("database query test failed: %w", err)
	}

	if result != 1 {
		log.Error(nil, "Database health check failed - unexpected query result", "expected", 1, "got", result)
		HealthCheckTotal.WithLabelValues(component, "result_error").Inc()
		return fmt.Errorf("database query returned unexpected result: expected 1, got %d", result)
	}

	HealthCheckTotal.WithLabelValues(component, "success").Inc()
	log.V(1).Info("Database health check passed")
	return nil
}

// GetHealthChecker returns an enhanced health checker function
func (r *OpenstackConfigReconciler) GetHealthChecker() healthz.Checker {
	return func(req *http.Request) error {
		startTime := time.Now()
		component := "openstackconfig"

		defer func() {
			duration := time.Since(startTime).Seconds()
			HealthCheckDuration.WithLabelValues(component, "completed").Observe(duration)
		}()

		ctx, cancel := context.WithTimeout(req.Context(), healthCheckTimeout)
		defer cancel()

		log := log.FromContext(ctx)

		// Check database connection
		if err := r.ensureDBConnection(ctx); err != nil {
			log.Error(err, "OpenstackConfig health check failed - database connection error")
			HealthCheckTotal.WithLabelValues(component, "db_connection_error").Inc()
			return fmt.Errorf("database connection failed: %w", err)
		}

		// Ping database
		if err := r.DB.PingContext(ctx); err != nil {
			log.Error(err, "OpenstackConfig health check failed - database ping error")
			HealthCheckTotal.WithLabelValues(component, "db_ping_error").Inc()
			return fmt.Errorf("database ping failed: %w", err)
		}

		// Check if database tables exist
		tables := []string{"multi_subnet", "multi_interface", "node_table"}
		for _, table := range tables {
			if err := r.checkTableExists(ctx, table); err != nil {
				log.Error(err, "OpenstackConfig health check failed - table check error", "table", table)
				HealthCheckTotal.WithLabelValues(component, "table_error").Inc()
				return fmt.Errorf("table %s check failed: %w", table, err)
			}
		}

		HealthCheckTotal.WithLabelValues(component, "success").Inc()
		log.V(1).Info("OpenstackConfig health check passed")
		return nil
	}
}

// checkTableExists verifies that a database table exists
func (r *OpenstackConfigReconciler) checkTableExists(ctx context.Context, tableName string) error {
	query := "SHOW TABLES LIKE ?"
	var result string

	if err := r.DB.QueryRowContext(ctx, query, tableName).Scan(&result); err != nil {
		if err.Error() == "sql: no rows in result set" {
			return fmt.Errorf("table %s does not exist", tableName)
		}
		return fmt.Errorf("failed to check table %s: %w", tableName, err)
	}

	if result != tableName {
		return fmt.Errorf("table %s not found", tableName)
	}

	return nil
}

// GetReadinessChecker returns a readiness checker for the OpenstackConfig controller
func (r *OpenstackConfigReconciler) GetReadinessChecker() healthz.Checker {
	return func(req *http.Request) error {
		startTime := time.Now()
		component := "openstackconfig_readiness"

		defer func() {
			duration := time.Since(startTime).Seconds()
			HealthCheckDuration.WithLabelValues(component, "completed").Observe(duration)
		}()

		ctx, cancel := context.WithTimeout(req.Context(), healthCheckTimeout)
		defer cancel()

		log := log.FromContext(ctx)

		// Basic health checks
		if err := r.GetHealthChecker()(req); err != nil {
			HealthCheckTotal.WithLabelValues(component, "health_check_failed").Inc()
			return err
		}

		// Additional readiness checks can be added here
		// For example: check if all required configurations are present

		HealthCheckTotal.WithLabelValues(component, "success").Inc()
		log.V(1).Info("OpenstackConfig readiness check passed")
		return nil
	}
}
