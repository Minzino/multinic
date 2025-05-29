package controllers

import (
	"context"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// DatabaseHealthChecker checks database connectivity
type DatabaseHealthChecker struct {
	reconciler *OpenstackConfigReconciler
}

// NewDatabaseHealthChecker creates a new database health checker
func NewDatabaseHealthChecker(reconciler *OpenstackConfigReconciler) *DatabaseHealthChecker {
	return &DatabaseHealthChecker{
		reconciler: reconciler,
	}
}

// CheckHealth implements healthz.Checker interface
func (c *DatabaseHealthChecker) Check(req *http.Request) error {
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	log := log.FromContext(ctx)

	if err := c.reconciler.ensureDBConnection(ctx); err != nil {
		log.Error(err, "Database health check failed")
		return err
	}

	if err := c.reconciler.DB.PingContext(ctx); err != nil {
		log.Error(err, "Database ping failed")
		return err
	}

	return nil
}

// GetHealthChecker returns a health checker function
func (r *OpenstackConfigReconciler) GetHealthChecker() healthz.Checker {
	return func(req *http.Request) error {
		ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
		defer cancel()

		log := log.FromContext(ctx)

		if err := r.ensureDBConnection(ctx); err != nil {
			log.Error(err, "Database health check failed")
			return err
		}

		if err := r.DB.PingContext(ctx); err != nil {
			log.Error(err, "Database ping failed")
			return err
		}

		return nil
	}
}
