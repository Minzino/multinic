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
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/subnets"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"

	multinicv1alpha1 "github.com/xormsdhkdwk/multinic/api/v1alpha1"
)

const (
	// Database configuration
	dbRetryInterval   = 10 * time.Second
	dbMaxRetries      = 5
	reconcileInterval = 5 * time.Minute
	finalizerName     = "multinic.example.com/finalizer"

	// Database connection configuration
	defaultDBHost     = "mariadb"
	defaultDBPort     = "3306"
	defaultDBUserName = "root"
	defaultDBPassword = "cloud1234"
	defaultDBName     = "multinic"

	// HTTP client configuration
	httpTimeout         = 120 * time.Second
	tlsHandshakeTimeout = 30 * time.Second
	dialTimeout         = 30 * time.Second
	keepAliveTimeout    = 30 * time.Second

	// OpenStack endpoints
	identityEndpointSuffix = "/v3"
	networkEndpointSuffix  = "/v2.0"
	computeEndpointSuffix  = "/v2.1"

	// Database connection pool settings
	maxOpenConns    = 25
	maxIdleConns    = 10
	connMaxLifetime = 30 * time.Minute
	connMaxIdleTime = 5 * time.Minute

	// OpenStack retry settings
	openstackMaxRetries = 3
)

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
}

// OpenstackConfig holds OpenStack configuration
type OpenstackConfig struct {
	AuthURL         string
	Username        string
	Password        string
	ProjectID       string
	DomainName      string
	NetworkEndpoint string
	ComputeEndpoint string
}

// OpenstackConfigReconciler reconciles a OpenstackConfig object
type OpenstackConfigReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	DB          *sql.DB
	DBConfig    *DatabaseConfig
	HTTPClient  *http.Client
	clientMutex sync.RWMutex
}

// +kubebuilder:rbac:groups=multinic.example.com,resources=openstackconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=multinic.example.com,resources=openstackconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=multinic.example.com,resources=openstackconfigs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *OpenstackConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	startTime := time.Now()
	log := log.FromContext(ctx)

	defer func() {
		duration := time.Since(startTime).Seconds()
		OpenstackRequestDuration.WithLabelValues("reconcile", "completed").Observe(duration)
	}()

	log.Info("Starting reconciliation", "openstackconfig", req.NamespacedName)

	// Ensure DB connection with retry
	if err := r.ensureDBConnectionWithRetry(ctx); err != nil {
		log.Error(err, "Failed to ensure database connection after retries")
		ReconcileTotal.WithLabelValues("db_error").Inc()
		return ctrl.Result{RequeueAfter: dbRetryInterval}, err
	}

	// Fetch the OpenstackConfig instance
	openstackConfig := &multinicv1alpha1.OpenstackConfig{}
	if err := r.Client.Get(ctx, req.NamespacedName, openstackConfig); err != nil {
		ReconcileTotal.WithLabelValues("not_found").Inc()
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if openstackConfig.DeletionTimestamp != nil {
		result, err := r.handleDeletion(ctx, openstackConfig)
		if err != nil {
			ReconcileTotal.WithLabelValues("deletion_error").Inc()
		} else {
			ReconcileTotal.WithLabelValues("deleted").Inc()
		}
		return result, err
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(openstackConfig, finalizerName) {
		log.Info("Adding finalizer")
		controllerutil.AddFinalizer(openstackConfig, finalizerName)
		if err := r.Update(ctx, openstackConfig); err != nil {
			log.Error(err, "Failed to add finalizer")
			ReconcileTotal.WithLabelValues("finalizer_error").Inc()
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Continue with normal reconciliation logic
	result, err := r.reconcileNormal(ctx, openstackConfig)
	if err != nil {
		ReconcileTotal.WithLabelValues("reconcile_error").Inc()
	} else {
		ReconcileTotal.WithLabelValues("success").Inc()
	}

	return result, err
}

// ensureDBConnectionWithRetry ensures database connection with retry logic
func (r *OpenstackConfigReconciler) ensureDBConnectionWithRetry(ctx context.Context) error {
	var lastErr error

	for i := 0; i < dbMaxRetries; i++ {
		if err := r.ensureDBConnection(ctx); err != nil {
			lastErr = err
			log := log.FromContext(ctx)
			log.Error(err, "Database connection attempt failed", "attempt", i+1, "max_retries", dbMaxRetries)

			if i < dbMaxRetries-1 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(i+1) * time.Second): // exponential backoff
					continue
				}
			}
		} else {
			return nil // Success
		}
	}

	return fmt.Errorf("failed to establish database connection after %d retries: %w", dbMaxRetries, lastErr)
}

// ensureDBConnection ensures database connection is available
func (r *OpenstackConfigReconciler) ensureDBConnection(ctx context.Context) error {
	if r.DB == nil {
		if err := r.connectToDatabase(ctx); err != nil {
			return err
		}
	}

	// Test connection
	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := r.DB.PingContext(ctxTimeout); err != nil {
		// Connection lost, try to reconnect
		if err := r.connectToDatabase(ctx); err != nil {
			return fmt.Errorf("failed to reconnect to database: %w", err)
		}
	}

	// Update connection metrics
	stats := r.DB.Stats()
	ActiveConnections.Set(float64(stats.OpenConnections))

	return nil
}

// connectToDatabase establishes database connection with optimized settings
func (r *OpenstackConfigReconciler) connectToDatabase(ctx context.Context) error {
	log := log.FromContext(ctx)

	if r.DBConfig == nil {
		r.DBConfig = r.getDatabaseConfig()
	}

	dsn := r.buildDSN()
	log.Info("Connecting to database", "host", r.DBConfig.Host, "port", r.DBConfig.Port, "database", r.DBConfig.DBName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("failed to open database connection: %w", err)
	}

	// Configure connection pool for optimal performance
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)

	// Test connection
	ctxTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctxTimeout); err != nil {
		db.Close()
		return fmt.Errorf("failed to ping database: %w", err)
	}

	// Close existing connection if any
	if r.DB != nil {
		r.DB.Close()
	}

	r.DB = db

	// Initialize database schema if needed
	if err := r.initializeDatabase(); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	log.Info("Successfully connected to database")
	return nil
}

// getHTTPClient returns a cached HTTP client or creates a new one
func (r *OpenstackConfigReconciler) getHTTPClient() *http.Client {
	r.clientMutex.RLock()
	if r.HTTPClient != nil {
		defer r.clientMutex.RUnlock()
		return r.HTTPClient
	}
	r.clientMutex.RUnlock()

	r.clientMutex.Lock()
	defer r.clientMutex.Unlock()

	// Double-check pattern
	if r.HTTPClient != nil {
		return r.HTTPClient
	}

	r.HTTPClient = createOptimizedHTTPClient()
	return r.HTTPClient
}

// createOptimizedHTTPClient creates an HTTP client with optimized settings
func createOptimizedHTTPClient() *http.Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: keepAliveTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	return &http.Client{
		Transport: transport,
		Timeout:   httpTimeout,
	}
}

// handleDeletion handles the deletion of OpenstackConfig CR
func (r *OpenstackConfigReconciler) handleDeletion(ctx context.Context, openstackConfig *multinicv1alpha1.OpenstackConfig) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(openstackConfig, finalizerName) {
		// Update deleted_at timestamp in multi_interface only
		deletedAt := openstackConfig.DeletionTimestamp.Time

		// Begin transaction for deletion
		tx, err := r.DB.BeginTx(ctx, nil)
		if err != nil {
			log.Error(err, "Failed to begin deletion transaction")
			return ctrl.Result{}, err
		}
		defer tx.Rollback()

		// Delete multi_interface records for this CR only
		// multi_subnet and node_table are master tables and should not be deleted per CR
		_, err = tx.ExecContext(ctx, `
			DELETE FROM multi_interface 
			WHERE cr_namespace = ? AND cr_name = ?`,
			openstackConfig.Namespace, openstackConfig.Name)
		if err != nil {
			log.Error(err, "Failed to delete multi_interface records", "cr_namespace", openstackConfig.Namespace, "cr_name", openstackConfig.Name)
			return ctrl.Result{}, err
		}

		// Delete CR state record
		_, err = tx.ExecContext(ctx, `
			DELETE FROM cr_state 
			WHERE cr_namespace = ? AND cr_name = ?`,
			openstackConfig.Namespace, openstackConfig.Name)
		if err != nil {
			log.Error(err, "Failed to delete CR state record", "cr_namespace", openstackConfig.Namespace, "cr_name", openstackConfig.Name)
			return ctrl.Result{}, err
		}

		// Commit deletion transaction
		if err := tx.Commit(); err != nil {
			log.Error(err, "Failed to commit deletion transaction")
			return ctrl.Result{}, err
		}

		log.Info("Successfully deleted CR interface records",
			"cr_namespace", openstackConfig.Namespace,
			"cr_name", openstackConfig.Name,
			"deleted_at", deletedAt)

		// Remove finalizer
		controllerutil.RemoveFinalizer(openstackConfig, finalizerName)
		if err := r.Update(ctx, openstackConfig); err != nil {
			log.Error(err, "Failed to remove finalizer")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// reconcileNormal handles normal reconciliation logic
func (r *OpenstackConfigReconciler) reconcileNormal(ctx context.Context, openstackConfig *multinicv1alpha1.OpenstackConfig) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	log.Info("=== RECONCILE NORMAL FUNCTION STARTED ===", "cr_name", openstackConfig.Name, "cr_namespace", openstackConfig.Namespace)

	// Calculate hash of current CR spec to detect changes
	currentHash, err := r.calculateCRHash(openstackConfig)
	if err != nil {
		log.Error(err, "Failed to calculate CR hash")
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
	}

	// Check if CR has actually changed by comparing with stored hash
	hasChanged, err := r.hasCRChanged(ctx, openstackConfig, currentHash)
	if err != nil {
		log.Error(err, "Failed to check CR changes")
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
	}

	// Get MultiNicOperator trigger configuration
	triggerConfig, err := r.getReconcileTriggerConfig(ctx, openstackConfig.Namespace)
	if err != nil {
		log.Error(err, "Failed to get reconcile trigger configuration, using default")
		// Use default behavior if we can't get the config
		triggerConfig = &multinicv1alpha1.ReconcileTriggerConfig{
			Mode:                "manual",
			ImmediateOnCRChange: true,
		}
	}

	log.Info("Trigger configuration retrieved",
		"mode", triggerConfig.Mode,
		"interval", triggerConfig.Interval,
		"immediateOnCRChange", triggerConfig.ImmediateOnCRChange,
		"crHasChanged", hasChanged)

	// Check if we should proceed with reconcile based on trigger configuration
	shouldReconcile, requeueAfter := r.shouldReconcile(ctx, triggerConfig, hasChanged)
	if !shouldReconcile {
		log.Info("Reconcile skipped due to trigger configuration",
			"mode", triggerConfig.Mode,
			"crHasChanged", hasChanged,
			"requeueAfter", requeueAfter)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	log.Info("Proceeding with reconcile",
		"mode", triggerConfig.Mode,
		"crHasChanged", hasChanged,
		"reason", func() string {
			if hasChanged && triggerConfig.ImmediateOnCRChange {
				return "CR changed"
			}
			switch triggerConfig.Mode {
			case "immediate":
				return "immediate mode"
			case "scheduled":
				return "scheduled interval"
			default:
				return "default behavior"
			}
		}())

	// Only proceed with OpenStack API calls if we should reconcile
	log.Info("CR has changed or scheduled reconcile triggered, proceeding with OpenStack API calls",
		"cr_namespace", openstackConfig.Namespace,
		"cr_name", openstackConfig.Name,
		"current_hash", currentHash,
		"crHasChanged", hasChanged)

	// Initialize OpenStack client
	authURL := normalizeEndpoint(openstackConfig.Spec.Credentials.AuthURL, identityEndpointSuffix)
	log.Info("Using auth URL", "url", authURL)

	opts := gophercloud.AuthOptions{
		IdentityEndpoint: authURL,
		Username:         openstackConfig.Spec.Credentials.Username,
		Password:         openstackConfig.Spec.Credentials.Password,
		TenantID:         openstackConfig.Spec.Credentials.ProjectID,
		DomainName:       openstackConfig.Spec.Credentials.DomainName,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectID: openstackConfig.Spec.Credentials.ProjectID,
		},
	}

	log.Info("Creating OpenStack clients", "mode", triggerConfig.Mode)

	// Create network client
	networkEndpoint := normalizeEndpoint(openstackConfig.Spec.Credentials.NetworkEndpoint, networkEndpointSuffix)
	networkClient, err := createOpenStackClient(ctx, opts, networkEndpoint, "network")
	if err != nil {
		log.Error(err, "Failed to create OpenStack network client")
		openstackConfig.Status.Status = "Error"
		openstackConfig.Status.LastUpdated = metav1.Now()
		openstackConfig.Status.Message = fmt.Sprintf("Failed to create OpenStack network client: %v", err)
		if updateErr := r.Client.Status().Update(ctx, openstackConfig); updateErr != nil {
			log.Error(updateErr, "Failed to update status")
		}
		// Return with appropriate requeue interval based on trigger config
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, err
	}

	// Create compute client
	computeEndpoint := normalizeEndpoint(openstackConfig.Spec.Credentials.ComputeEndpoint, computeEndpointSuffix)
	computeClient, err := createOpenStackClient(ctx, opts, computeEndpoint, "compute")
	if err != nil {
		log.Error(err, "Failed to create compute client")
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, err
	}

	log.Info("OpenStack clients created successfully",
		"networkEndpoint", networkClient.Endpoint,
		"computeEndpoint", computeClient.Endpoint,
		"mode", triggerConfig.Mode)

	// Get VM information
	log.Info("Starting VM search process", "target_vms", openstackConfig.Spec.VMNames)

	if computeClient == nil {
		log.Error(nil, "Compute client is nil")
		openstackConfig.Status.Status = "Error"
		openstackConfig.Status.LastUpdated = metav1.Now()
		openstackConfig.Status.Message = "Failed to create compute client"
		if err := r.Client.Status().Update(ctx, openstackConfig); err != nil {
			log.Error(err, "Failed to update status")
		}
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, fmt.Errorf("compute client is nil")
	}

	log.Info("Starting server list request with options",
		"computeEndpoint", computeClient.Endpoint,
		"resourceBase", computeClient.ResourceBase,
		"tokenID", computeClient.TokenID != "",
		"serviceType", computeClient.Type)

	// Get all servers - we'll filter them locally
	requestURL := computeClient.Endpoint + "/servers/detail"
	log.Info("Actual request URL that will be used", "url", requestURL, "target_vms", openstackConfig.Spec.VMNames)

	// 직접 HTTP 요청으로 서버 목록 조회
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	httpReq, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		log.Error(err, "Failed to create HTTP request")
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, err
	}

	httpReq.Header.Set("X-Auth-Token", computeClient.TokenID)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		log.Error(err, "Failed to execute HTTP request")
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error(err, "Failed to read response body")
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, err
	}

	log.Info("HTTP response received", "status", resp.Status, "bodyLength", len(body))

	// JSON 응답 파싱
	var serverResponse struct {
		Servers []servers.Server `json:"servers"`
	}

	err = json.Unmarshal(body, &serverResponse)
	if err != nil {
		log.Error(err, "Failed to unmarshal server response", "body", string(body))
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, err
	}

	allServers := serverResponse.Servers
	log.Info("Successfully extracted servers", "count", len(allServers))

	// Find target VMs by matching names (single pass)
	var targetVMs []servers.Server
	vmNameSet := make(map[string]bool)
	for _, vmName := range openstackConfig.Spec.VMNames {
		vmNameSet[vmName] = true
	}

	for _, server := range allServers {
		if vmNameSet[server.Name] {
			targetVMs = append(targetVMs, server)
			log.Info("Target VM found", "server_name", server.Name, "server_id", server.ID)
		}
	}

	if len(targetVMs) == 0 {
		log.Info("No target VMs found", "requested_vms", openstackConfig.Spec.VMNames)
		openstackConfig.Status.Status = "Error"
		openstackConfig.Status.LastUpdated = metav1.Now()
		openstackConfig.Status.Message = fmt.Sprintf("None of the requested VMs found: %v", openstackConfig.Spec.VMNames)
		if err := r.Client.Status().Update(ctx, openstackConfig); err != nil {
			log.Error(err, "Failed to update status")
		}
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, nil
	}

	log.Info("Target VMs selected", "count", len(targetVMs), "vm_names", openstackConfig.Spec.VMNames)

	// Get subnet information
	subnet, err := r.getSubnetInfoWithClients(ctx, openstackConfig, networkClient, client)
	if err != nil {
		log.Error(err, "Failed to get subnet information")
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, err
	}

	if subnet == nil {
		log.Info("Subnet not found", "name", openstackConfig.Spec.SubnetName)
		openstackConfig.Status.Status = "Error"
		openstackConfig.Status.LastUpdated = metav1.Now()
		openstackConfig.Status.Message = fmt.Sprintf("Subnet %s not found", openstackConfig.Spec.SubnetName)
		if err := r.Client.Status().Update(ctx, openstackConfig); err != nil {
			log.Error(err, "Failed to update status")
		}
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, nil
	}

	// Begin transaction
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		log.Error(err, "Failed to begin transaction")
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, err
	}
	defer tx.Rollback()

	// Clean up existing records for this CR to prevent duplicates
	log.Info("Cleaning up existing records for CR",
		"cr_namespace", openstackConfig.Namespace,
		"cr_name", openstackConfig.Name)

	_, err = tx.ExecContext(ctx, `
		DELETE FROM multi_interface 
		WHERE cr_namespace = ? AND cr_name = ?`,
		openstackConfig.Namespace, openstackConfig.Name)
	if err != nil {
		log.Error(err, "Failed to delete existing multi_interface records")
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, err
	}

	log.Info("Successfully cleaned up existing records for CR")

	// Insert/Update multi_subnet
	createdAt := openstackConfig.CreationTimestamp.Time
	modifiedAt := time.Now()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO multi_subnet (subnet_id, subnet_name, cidr, network_id, status, created_at, modified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
		subnet_name=?, cidr=?, network_id=?, status=?, modified_at=?`,
		subnet.ID, subnet.Name, subnet.CIDR, subnet.NetworkID, "active", createdAt, modifiedAt,
		subnet.Name, subnet.CIDR, subnet.NetworkID, "active", modifiedAt,
	)
	if err != nil {
		log.Error(err, "Failed to insert subnet information")
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, err
	}

	log.Info("Successfully inserted/updated subnet information",
		"subnet_id", subnet.ID,
		"subnet_name", subnet.Name,
		"cidr", subnet.CIDR,
		"network_id", subnet.NetworkID)

	// Batch insert nodes - prepare all node data first
	nodeValues := make([]interface{}, 0, len(targetVMs)*5)
	nodePlaceholders := make([]string, 0, len(targetVMs))

	for _, targetVM := range targetVMs {
		nodePlaceholders = append(nodePlaceholders, "(?, ?, ?, ?, ?)")
		nodeValues = append(nodeValues, targetVM.ID, targetVM.Name, "active", createdAt, modifiedAt)
	}

	// Batch insert all nodes
	if len(nodeValues) > 0 {
		nodeQuery := fmt.Sprintf(`
			INSERT INTO node_table (attached_node_id, attached_node_name, status, created_at, modified_at)
			VALUES %s
			ON DUPLICATE KEY UPDATE
			attached_node_name=VALUES(attached_node_name), status=VALUES(status), modified_at=VALUES(modified_at)`,
			strings.Join(nodePlaceholders, ", "))

		_, err = tx.ExecContext(ctx, nodeQuery, nodeValues...)
		if err != nil {
			log.Error(err, "Failed to batch insert node information")
			nextRequeue := r.getNextReconcileInterval(triggerConfig)
			return ctrl.Result{RequeueAfter: nextRequeue}, err
		}
		log.Info("Successfully batch inserted/updated node information", "count", len(targetVMs))
	}

	// Process network interfaces for all VMs
	interfaceValues := make([]interface{}, 0)
	interfacePlaceholders := make([]string, 0)
	vmNetworkInfos := make([]multinicv1alpha1.VMNetworkInfo, 0, len(targetVMs))

	for _, targetVM := range targetVMs {
		vmNetworkInfo := multinicv1alpha1.VMNetworkInfo{
			VMName:     targetVM.Name,
			SubnetID:   subnet.ID,
			NetworkID:  subnet.NetworkID,
			IPAddress:  targetVM.AccessIPv4,
			MACAddress: "",
			Status:     "active",
			Message:    "Successfully processed",
		}

		// Process network addresses
		for _, addresses := range targetVM.Addresses {
			for _, address := range addresses.([]interface{}) {
				addr := address.(map[string]interface{})

				if addr["OS-EXT-IPS:type"] == nil || addr["OS-EXT-IPS-MAC:mac_addr"] == nil {
					continue
				}

				if addr["OS-EXT-IPS:type"].(string) == "fixed" {
					macAddr, ok := addr["OS-EXT-IPS-MAC:mac_addr"].(string)
					if !ok {
						continue
					}

					var portID string
					if addr["port"] != nil {
						if id, ok := addr["port"].(string); ok {
							portID = id
						}
					}

					if portID == "" {
						foundPortID, err := r.findPortByMACAddress(ctx, openstackConfig, macAddr, subnet.ID)
						if err != nil {
							log.Info("Error finding port for MAC address, skipping", "mac_addr", macAddr, "error", err)
							continue
						}
						if foundPortID == "" {
							// This is normal - VM may have interfaces on multiple networks
							// Only process interfaces that belong to the target subnet
							log.V(1).Info("MAC address not found in target subnet, skipping", "mac_addr", macAddr, "target_subnet_id", subnet.ID)
							continue
						}
						portID = foundPortID
					}

					// Collect interface data for batch insert
					interfacePlaceholders = append(interfacePlaceholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
					interfaceValues = append(interfaceValues,
						portID, subnet.ID, macAddr, targetVM.ID, targetVM.Name,
						openstackConfig.Namespace, openstackConfig.Name, "active", 0, createdAt, modifiedAt)

					// Update VM network info with MAC address
					if vmNetworkInfo.MACAddress == "" {
						vmNetworkInfo.MACAddress = macAddr
					}
				}
			}
		}
		vmNetworkInfos = append(vmNetworkInfos, vmNetworkInfo)
	}

	// Batch insert all interfaces
	if len(interfaceValues) > 0 {
		interfaceQuery := fmt.Sprintf(`
			INSERT INTO multi_interface (port_id, subnet_id, macaddress, attached_node_id, attached_node_name, cr_namespace, cr_name, status, netplan_success, created_at, modified_at)
			VALUES %s
			ON DUPLICATE KEY UPDATE
			macaddress=VALUES(macaddress), attached_node_id=VALUES(attached_node_id), attached_node_name=VALUES(attached_node_name), 
			status=VALUES(status), netplan_success=VALUES(netplan_success), modified_at=VALUES(modified_at)`,
			strings.Join(interfacePlaceholders, ", "))

		_, err = tx.ExecContext(ctx, interfaceQuery, interfaceValues...)
		if err != nil {
			log.Error(err, "Failed to batch insert interface information")
			nextRequeue := r.getNextReconcileInterval(triggerConfig)
			return ctrl.Result{RequeueAfter: nextRequeue}, err
		}
		log.Info("Successfully batch inserted interface information", "count", len(interfacePlaceholders))
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		log.Error(err, "Failed to commit transaction")
		nextRequeue := r.getNextReconcileInterval(triggerConfig)
		return ctrl.Result{RequeueAfter: nextRequeue}, err
	}

	// Ensure DB connection is properly maintained after transaction
	if err := r.DB.PingContext(ctx); err != nil {
		log.Info("DB connection lost after transaction, will reconnect on next reconcile", "error", err)
		r.DB = nil // Force reconnection on next reconcile
	}

	// Update status
	openstackConfig.Status.Status = "Completed"
	openstackConfig.Status.LastUpdated = metav1.Now()
	openstackConfig.Status.Message = "Successfully updated VM and network information"

	// vmNetworkInfos is already created above during interface processing
	openstackConfig.Status.VMNetworkInfos = vmNetworkInfos

	if err := r.Client.Status().Update(ctx, openstackConfig); err != nil {
		log.Error(err, "Failed to update OpenstackConfig status")
		return ctrl.Result{RequeueAfter: time.Second * 10}, err
	}

	// Calculate next reconcile interval based on trigger configuration
	nextRequeue := r.getNextReconcileInterval(triggerConfig)
	log.Info("Reconcile completed successfully",
		"nextRequeue", nextRequeue,
		"triggerMode", triggerConfig.Mode)

	return ctrl.Result{RequeueAfter: nextRequeue}, nil
}

// getSubnetInfo gets subnet information (extracted for reuse)
func (r *OpenstackConfigReconciler) getSubnetInfo(ctx context.Context, openstackConfig *multinicv1alpha1.OpenstackConfig) (*subnets.Subnet, error) {
	// Create network client
	authURL := openstackConfig.Spec.Credentials.AuthURL
	if !strings.HasSuffix(authURL, "/v3") {
		authURL = strings.TrimSuffix(authURL, "/") + "/v3"
	}

	opts := gophercloud.AuthOptions{
		IdentityEndpoint: authURL,
		Username:         openstackConfig.Spec.Credentials.Username,
		Password:         openstackConfig.Spec.Credentials.Password,
		TenantID:         openstackConfig.Spec.Credentials.ProjectID,
		DomainName:       openstackConfig.Spec.Credentials.DomainName,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectID: openstackConfig.Spec.Credentials.ProjectID,
		},
	}

	networkEndpoint := openstackConfig.Spec.Credentials.NetworkEndpoint
	if !strings.HasSuffix(networkEndpoint, "/v2.0") {
		networkEndpoint = strings.TrimSuffix(networkEndpoint, "/")
		networkEndpoint = networkEndpoint + "/v2.0"
	}

	networkClient, err := createOpenStackClient(ctx, opts, networkEndpoint, "network")
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	return r.getSubnetInfoWithClients(ctx, openstackConfig, networkClient, client)
}

// getSubnetInfoWithClients gets subnet information using provided clients
func (r *OpenstackConfigReconciler) getSubnetInfoWithClients(ctx context.Context, openstackConfig *multinicv1alpha1.OpenstackConfig, networkClient *gophercloud.ServiceClient, client *http.Client) (*subnets.Subnet, error) {
	log := log.FromContext(ctx)

	for i := 0; i < openstackMaxRetries; i++ {
		log.Info("Searching for network and subnet", "subnet_name", openstackConfig.Spec.SubnetName)

		// 직접 HTTP 요청으로 네트워크 목록을 가져옵니다
		log.Info("Attempting to list networks using direct HTTP request")
		networkRequestURL := networkClient.Endpoint + "/networks"
		log.Info("Network request URL", "url", networkRequestURL)

		networkReq, err := http.NewRequest("GET", networkRequestURL, nil)
		if err != nil {
			log.Error(err, "Failed to create network HTTP request", "attempt", i+1)
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}

		networkReq.Header.Set("X-Auth-Token", networkClient.TokenID)
		networkReq.Header.Set("Content-Type", "application/json")

		networkResp, err := client.Do(networkReq)
		if err != nil {
			log.Error(err, "Failed to execute network HTTP request", "attempt", i+1)
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}
		defer networkResp.Body.Close()

		networkBody, err := io.ReadAll(networkResp.Body)
		if err != nil {
			log.Error(err, "Failed to read network response body", "attempt", i+1)
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}

		log.Info("Network HTTP response received", "status", networkResp.Status, "bodyLength", len(networkBody))

		// JSON 응답 파싱
		var networkResponse struct {
			Networks []networks.Network `json:"networks"`
		}

		err = json.Unmarshal(networkBody, &networkResponse)
		if err != nil {
			log.Error(err, "Failed to unmarshal network response", "attempt", i+1, "body", string(networkBody))
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}

		networkList := networkResponse.Networks
		log.Info("Successfully retrieved networks", "count", len(networkList))

		// 각 네트워크의 서브넷을 확인합니다
		for _, network := range networkList {
			log.Info("Checking network", "network_name", network.Name, "network_id", network.ID)

			// 직접 HTTP 요청으로 서브넷 목록을 가져옵니다
			subnetRequestURL := networkClient.Endpoint + "/subnets?network_id=" + network.ID
			log.Info("Subnet request URL", "url", subnetRequestURL)

			subnetReq, err := http.NewRequest("GET", subnetRequestURL, nil)
			if err != nil {
				log.Error(err, "Failed to create subnet HTTP request", "network_id", network.ID)
				continue
			}

			subnetReq.Header.Set("X-Auth-Token", networkClient.TokenID)
			subnetReq.Header.Set("Content-Type", "application/json")

			subnetResp, err := client.Do(subnetReq)
			if err != nil {
				log.Error(err, "Failed to execute subnet HTTP request", "network_id", network.ID)
				continue
			}
			defer subnetResp.Body.Close()

			subnetBody, err := io.ReadAll(subnetResp.Body)
			if err != nil {
				log.Error(err, "Failed to read subnet response body", "network_id", network.ID)
				continue
			}

			log.Info("Subnet HTTP response received", "status", subnetResp.Status, "bodyLength", len(subnetBody), "network_id", network.ID)

			// JSON 응답 파싱
			var subnetResponse struct {
				Subnets []subnets.Subnet `json:"subnets"`
			}

			err = json.Unmarshal(subnetBody, &subnetResponse)
			if err != nil {
				log.Error(err, "Failed to unmarshal subnet response", "network_id", network.ID, "body", string(subnetBody))
				continue
			}

			subnetList := subnetResponse.Subnets

			// 서브넷 목록을 순회하면서 이름이 일치하는 서브넷을 찾습니다
			for _, s := range subnetList {
				log.Info("Found subnet in network",
					"network_name", network.Name,
					"subnet_name", s.Name,
					"subnet_id", s.ID,
					"cidr", s.CIDR)

				if s.Name == openstackConfig.Spec.SubnetName {
					log.Info("Found matching subnet",
						"network_name", network.Name,
						"subnet_name", s.Name,
						"subnet_id", s.ID,
						"cidr", s.CIDR)
					return &s, nil
				}
			}
		}

		log.Info("Subnet not found in this attempt", "name", openstackConfig.Spec.SubnetName, "attempt", i+1)
		time.Sleep(time.Second * time.Duration(i+1))
	}

	return nil, fmt.Errorf("subnet %s not found after %d attempts", openstackConfig.Spec.SubnetName, openstackMaxRetries)
}

// SetupWithManager sets up the controller with the Manager.
func (r *OpenstackConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Note: Database connection will be established on first reconcile
	return ctrl.NewControllerManagedBy(mgr).
		For(&multinicv1alpha1.OpenstackConfig{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		WithEventFilter(NewTimerBasedPredicate(mgr.GetClient())).
		Complete(r)
}

// initializeDatabase creates/updates database tables
func (r *OpenstackConfigReconciler) initializeDatabase() error {
	// Create tables with proper schema
	queries := []string{
		`CREATE TABLE IF NOT EXISTS multi_subnet (
			id INT AUTO_INCREMENT PRIMARY KEY,
			subnet_id VARCHAR(36) NOT NULL UNIQUE,
			subnet_name VARCHAR(255) NOT NULL,
			cidr VARCHAR(255) NOT NULL,
			network_id VARCHAR(36) NOT NULL COMMENT 'OpenStack network ID',
			status VARCHAR(50) DEFAULT 'active',
			created_at TIMESTAMP NULL,
			modified_at TIMESTAMP NULL,
			deleted_at TIMESTAMP NULL
		)`,
		`CREATE TABLE IF NOT EXISTS node_table (
			id INT AUTO_INCREMENT PRIMARY KEY,
			attached_node_id VARCHAR(36) NOT NULL UNIQUE,
			attached_node_name VARCHAR(255) NOT NULL UNIQUE,
			status VARCHAR(50) DEFAULT 'active',
			created_at TIMESTAMP NULL,
			modified_at TIMESTAMP NULL,
			deleted_at TIMESTAMP NULL
		)`,
		`CREATE TABLE IF NOT EXISTS multi_interface (
			id INT AUTO_INCREMENT PRIMARY KEY,
			port_id VARCHAR(36) NOT NULL UNIQUE,
			subnet_id VARCHAR(36) NOT NULL,
			macaddress VARCHAR(17) NOT NULL,
			attached_node_id VARCHAR(36),
			attached_node_name VARCHAR(255) NULL,
			cr_namespace VARCHAR(255) NOT NULL COMMENT 'OpenstackConfig CR namespace',
			cr_name VARCHAR(255) NOT NULL COMMENT 'OpenstackConfig CR name',
			status VARCHAR(50) DEFAULT 'active',
			netplan_success TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'Netplan apply success (0: fail/not applied, 1: success)',
			created_at TIMESTAMP NULL,
			modified_at TIMESTAMP NULL,
			deleted_at TIMESTAMP NULL,
			FOREIGN KEY (subnet_id) REFERENCES multi_subnet(subnet_id),
			FOREIGN KEY (attached_node_id) REFERENCES node_table(attached_node_id),
			FOREIGN KEY (attached_node_name) REFERENCES node_table(attached_node_name),
			UNIQUE KEY unique_cr_interface (cr_namespace, cr_name, port_id)
		)`,
		`CREATE TABLE IF NOT EXISTS cr_state (
			id INT AUTO_INCREMENT PRIMARY KEY,
			cr_namespace VARCHAR(255) NOT NULL,
			cr_name VARCHAR(255) NOT NULL,
			spec_hash VARCHAR(64) NOT NULL COMMENT 'SHA256 hash of CR spec',
			last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY unique_cr (cr_namespace, cr_name)
		)`,
	}

	for _, query := range queries {
		if _, err := r.DB.Exec(query); err != nil {
			return fmt.Errorf("failed to create table: %v", err)
		}
	}

	return nil
}

// createOpenStackClient creates a new OpenStack client with retry logic
func createOpenStackClient(ctx context.Context, opts gophercloud.AuthOptions, endpoint, clientType string) (*gophercloud.ServiceClient, error) {
	var client *gophercloud.ServiceClient
	log := log.FromContext(ctx)

	// 디버그 로그 활성화
	if err := os.Setenv("OS_DEBUG", "1"); err != nil {
		log.Error(err, "Failed to set OS_DEBUG environment variable")
	}

	log.Info("Creating OpenStack client",
		"endpoint", endpoint,
		"clientType", clientType,
		"authURL", opts.IdentityEndpoint,
		"username", opts.Username,
		"projectID", opts.TenantID,
		"domainName", opts.DomainName,
		"scope", opts.Scope)

	for i := 0; i < openstackMaxRetries; i++ {
		log.Info("Attempting to create provider client", "attempt", i+1)

		// gophercloud provider 옵션 설정
		providerClient, err := openstack.NewClient(opts.IdentityEndpoint)
		if err != nil {
			log.Error(err, "Failed to create OpenStack provider",
				"attempt", i+1,
				"error_type", fmt.Sprintf("%T", err),
				"error_details", fmt.Sprintf("%+v", err))
			continue
		}

		// Use optimized HTTP client
		providerClient.HTTPClient = *createHTTPClient()

		// 인증 시도
		log.Info("Attempting authentication",
			"attempt", i+1,
			"auth_url", opts.IdentityEndpoint,
			"transport_settings", map[string]interface{}{
				"insecure_skip_verify":  true,
				"tls_min_version":       tls.VersionTLS12,
				"tls_handshake_timeout": tlsHandshakeTimeout,
				"total_timeout":         httpTimeout,
				"keep_alive":            keepAliveTimeout,
				"dial_timeout":          dialTimeout,
			})

		err = openstack.Authenticate(providerClient, opts)
		if err != nil {
			log.Error(err, "Authentication failed",
				"attempt", i+1,
				"error_type", fmt.Sprintf("%T", err),
				"error_details", fmt.Sprintf("%+v", err))

			// 자세한 에러 정보 출력
			if gopherErr, ok := err.(gophercloud.ErrDefault401); ok {
				log.Error(err, "Authentication error (401)", "body", string(gopherErr.Body))
			} else if gopherErr, ok := err.(gophercloud.ErrDefault404); ok {
				log.Error(err, "Resource not found (404)", "body", string(gopherErr.Body))
			} else if gopherErr, ok := err.(gophercloud.ErrDefault500); ok {
				log.Error(err, "OpenStack server error (500)", "body", string(gopherErr.Body))
			}

			continue
		}

		log.Info("Authentication successful", "token_id", providerClient.TokenID)

		// 서비스 클라이언트 생성
		switch clientType {
		case "network":
			log.Info("Creating network client")
			client, err = openstack.NewNetworkV2(providerClient, gophercloud.EndpointOpts{})
		case "compute":
			log.Info("Creating compute client")
			client, err = openstack.NewComputeV2(providerClient, gophercloud.EndpointOpts{})
		default:
			return nil, fmt.Errorf("unsupported client type: %s", clientType)
		}

		if err != nil {
			log.Error(err, "Failed to create service client",
				"type", clientType,
				"error_type", fmt.Sprintf("%T", err),
				"error_details", fmt.Sprintf("%+v", err))
			continue
		}

		if endpoint != "" {
			log.Info("Setting custom endpoint", "endpoint", endpoint)
			client.Endpoint = endpoint
		}

		log.Info("Successfully created service client",
			"type", clientType,
			"endpoint", client.Endpoint,
			"microversion", client.Microversion)

		return client, nil
	}

	return nil, fmt.Errorf("failed to create OpenStack client after %d attempts", openstackMaxRetries)
}

// 맵의 키들을 가져오는 헬퍼 함수
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// getDatabaseConfig returns database configuration from environment or defaults
func (r *OpenstackConfigReconciler) getDatabaseConfig() *DatabaseConfig {
	if r.DBConfig != nil {
		return r.DBConfig
	}

	r.DBConfig = &DatabaseConfig{
		Host:     getEnvOrDefault("DB_HOST", defaultDBHost),
		Port:     getEnvOrDefault("DB_PORT", defaultDBPort),
		User:     getEnvOrDefault("DB_USER", defaultDBUserName),
		Password: getEnvOrDefault("DB_PASSWORD", defaultDBPassword),
		DBName:   getEnvOrDefault("DB_NAME", defaultDBName),
	}

	return r.DBConfig
}

// getEnvOrDefault returns environment variable value or default
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// buildDSN builds database connection string
func (r *OpenstackConfigReconciler) buildDSN() string {
	config := r.getDatabaseConfig()
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?tls=false&timeout=30s&readTimeout=30s&writeTimeout=30s&parseTime=true&loc=Local&charset=utf8mb4&collation=utf8mb4_unicode_ci",
		config.User, config.Password, config.Host, config.Port, config.DBName)
}

// createHTTPClient creates optimized HTTP client
func createHTTPClient() *http.Client {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	}

	transport := &http.Transport{
		TLSClientConfig:     tlsConfig,
		TLSHandshakeTimeout: tlsHandshakeTimeout,
		Proxy:               http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: keepAliveTimeout,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   httpTimeout,
	}
}

// normalizeEndpoint ensures endpoint has the correct suffix
func normalizeEndpoint(endpoint, suffix string) string {
	if !strings.HasSuffix(endpoint, suffix) {
		endpoint = strings.TrimSuffix(endpoint, "/") + suffix
	}
	return endpoint
}

// findPortByMACAddress finds a port ID based on a MAC address and validates it belongs to the target subnet
func (r *OpenstackConfigReconciler) findPortByMACAddress(ctx context.Context, openstackConfig *multinicv1alpha1.OpenstackConfig, macAddr string, targetSubnetID string) (string, error) {
	log := log.FromContext(ctx)

	// Create network client
	authURL := openstackConfig.Spec.Credentials.AuthURL
	if !strings.HasSuffix(authURL, "/v3") {
		authURL = strings.TrimSuffix(authURL, "/") + "/v3"
	}

	opts := gophercloud.AuthOptions{
		IdentityEndpoint: authURL,
		Username:         openstackConfig.Spec.Credentials.Username,
		Password:         openstackConfig.Spec.Credentials.Password,
		TenantID:         openstackConfig.Spec.Credentials.ProjectID,
		DomainName:       openstackConfig.Spec.Credentials.DomainName,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectID: openstackConfig.Spec.Credentials.ProjectID,
		},
	}

	networkEndpoint := openstackConfig.Spec.Credentials.NetworkEndpoint
	if !strings.HasSuffix(networkEndpoint, "/v2.0") {
		networkEndpoint = strings.TrimSuffix(networkEndpoint, "/")
		networkEndpoint = networkEndpoint + "/v2.0"
	}

	networkClient, err := createOpenStackClient(ctx, opts, networkEndpoint, "network")
	if err != nil {
		return "", err
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Query ports by MAC address
	portRequestURL := networkClient.Endpoint + "/ports?mac_address=" + macAddr
	log.Info("Searching for port by MAC address", "url", portRequestURL, "mac_addr", macAddr, "target_subnet_id", targetSubnetID)

	portReq, err := http.NewRequest("GET", portRequestURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create port HTTP request: %v", err)
	}

	portReq.Header.Set("X-Auth-Token", networkClient.TokenID)
	portReq.Header.Set("Content-Type", "application/json")

	portResp, err := client.Do(portReq)
	if err != nil {
		return "", fmt.Errorf("failed to execute port HTTP request: %v", err)
	}
	defer portResp.Body.Close()

	portBody, err := io.ReadAll(portResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read port response body: %v", err)
	}

	log.Info("Port HTTP response received", "status", portResp.Status, "bodyLength", len(portBody))

	if portResp.StatusCode != 200 {
		return "", fmt.Errorf("port query failed with status %d: %s", portResp.StatusCode, string(portBody))
	}

	// Parse JSON response
	var portResponse struct {
		Ports []struct {
			ID         string `json:"id"`
			MACAddress string `json:"mac_address"`
			FixedIPs   []struct {
				SubnetID  string `json:"subnet_id"`
				IPAddress string `json:"ip_address"`
			} `json:"fixed_ips"`
		} `json:"ports"`
	}

	err = json.Unmarshal(portBody, &portResponse)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal port response: %v", err)
	}

	if len(portResponse.Ports) == 0 {
		log.Info("No ports found for MAC address", "mac_addr", macAddr)
		return "", nil
	}

	// Find port that belongs to the target subnet
	for _, port := range portResponse.Ports {
		for _, fixedIP := range port.FixedIPs {
			if fixedIP.SubnetID == targetSubnetID {
				log.Info("Found port in target subnet",
					"mac_addr", macAddr,
					"port_id", port.ID,
					"target_subnet_id", targetSubnetID,
					"ip_address", fixedIP.IPAddress)
				return port.ID, nil
			}
		}
		log.V(1).Info("Port found but not in target subnet",
			"mac_addr", macAddr,
			"port_id", port.ID,
			"port_subnets", port.FixedIPs,
			"target_subnet_id", targetSubnetID)
	}

	log.V(1).Info("No ports found in target subnet for MAC address", "mac_addr", macAddr, "target_subnet_id", targetSubnetID)
	return "", nil
}

// calculateCRHash calculates a hash of the current CR spec
func (r *OpenstackConfigReconciler) calculateCRHash(openstackConfig *multinicv1alpha1.OpenstackConfig) (string, error) {
	// Create a structure that contains only the fields that matter for reconciliation
	specData := struct {
		AuthURL         string   `json:"auth_url"`
		Username        string   `json:"username"`
		Password        string   `json:"password"`
		ProjectID       string   `json:"project_id"`
		DomainName      string   `json:"domain_name"`
		NetworkEndpoint string   `json:"network_endpoint"`
		ComputeEndpoint string   `json:"compute_endpoint"`
		VMNames         []string `json:"vm_names"`
		SubnetName      string   `json:"subnet_name"`
	}{
		AuthURL:         openstackConfig.Spec.Credentials.AuthURL,
		Username:        openstackConfig.Spec.Credentials.Username,
		Password:        openstackConfig.Spec.Credentials.Password,
		ProjectID:       openstackConfig.Spec.Credentials.ProjectID,
		DomainName:      openstackConfig.Spec.Credentials.DomainName,
		NetworkEndpoint: openstackConfig.Spec.Credentials.NetworkEndpoint,
		ComputeEndpoint: openstackConfig.Spec.Credentials.ComputeEndpoint,
		VMNames:         openstackConfig.Spec.VMNames,
		SubnetName:      openstackConfig.Spec.SubnetName,
	}

	// Convert to JSON and calculate SHA256 hash
	jsonData, err := json.Marshal(specData)
	if err != nil {
		return "", fmt.Errorf("failed to marshal spec data: %v", err)
	}

	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:]), nil
}

// hasCRChanged checks if the CR has actually changed
func (r *OpenstackConfigReconciler) hasCRChanged(ctx context.Context, openstackConfig *multinicv1alpha1.OpenstackConfig, currentHash string) (bool, error) {
	log := log.FromContext(ctx)

	// Query the database to get the last known hash for this CR
	var storedHash string
	err := r.DB.QueryRowContext(ctx, `
		SELECT spec_hash FROM cr_state 
		WHERE cr_namespace = ? AND cr_name = ?`,
		openstackConfig.Namespace, openstackConfig.Name).Scan(&storedHash)

	if err != nil {
		if err == sql.ErrNoRows {
			// No previous state found, this is a new or first-time CR
			// Store the hash for future comparisons
			_, insertErr := r.DB.ExecContext(ctx, `
				INSERT INTO cr_state (cr_namespace, cr_name, spec_hash, last_updated)
				VALUES (?, ?, ?, NOW())`,
				openstackConfig.Namespace, openstackConfig.Name, currentHash)
			if insertErr != nil {
				log.Error(insertErr, "Failed to store initial CR state")
				return false, fmt.Errorf("failed to store initial CR state: %v", insertErr)
			}
			log.Info("Stored initial CR state",
				"cr_namespace", openstackConfig.Namespace,
				"cr_name", openstackConfig.Name,
				"hash", currentHash)
			return true, nil
		}
		return false, fmt.Errorf("failed to query CR state: %v", err)
	}

	// Compare hashes
	hasChanged := storedHash != currentHash

	log.Info("CR change comparison",
		"cr_namespace", openstackConfig.Namespace,
		"cr_name", openstackConfig.Name,
		"stored_hash", storedHash,
		"current_hash", currentHash,
		"has_changed", hasChanged)

	// Update the stored hash if it has changed
	if hasChanged {
		_, err = r.DB.ExecContext(ctx, `
			UPDATE cr_state 
			SET spec_hash = ?, last_updated = NOW()
			WHERE cr_namespace = ? AND cr_name = ?`,
			currentHash, openstackConfig.Namespace, openstackConfig.Name)
		if err != nil {
			log.Error(err, "Failed to update CR state")
			return false, fmt.Errorf("failed to update CR state: %v", err)
		}
		log.Info("Updated CR state",
			"cr_namespace", openstackConfig.Namespace,
			"cr_name", openstackConfig.Name,
			"new_hash", currentHash)
	}

	return hasChanged, nil
}

// getReconcileTriggerConfig retrieves the reconcile trigger configuration from MultiNicOperator
func (r *OpenstackConfigReconciler) getReconcileTriggerConfig(ctx context.Context, namespace string) (*multinicv1alpha1.ReconcileTriggerConfig, error) {
	log := log.FromContext(ctx)

	// List all MultiNicOperator CRs in the namespace
	multinicOperatorList := &multinicv1alpha1.MultiNicOperatorList{}
	err := r.Client.List(ctx, multinicOperatorList, client.InNamespace(namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list MultiNicOperator CRs: %v", err)
	}

	// If no MultiNicOperator found, use default configuration
	if len(multinicOperatorList.Items) == 0 {
		log.Info("No MultiNicOperator found, using default trigger configuration")
		return &multinicv1alpha1.ReconcileTriggerConfig{
			Mode:                "immediate",
			ImmediateOnCRChange: true,
		}, nil
	}

	// Use the first MultiNicOperator's configuration
	// In practice, there should be only one per namespace
	operator := multinicOperatorList.Items[0]
	if len(multinicOperatorList.Items) > 1 {
		log.Info("Multiple MultiNicOperator CRs found, using the first one",
			"count", len(multinicOperatorList.Items),
			"selected", operator.Name)
	}

	// Set defaults if not specified
	config := operator.Spec.ReconcileTrigger
	if config.Mode == "" {
		config.Mode = "immediate"
	}
	if config.Mode == "immediate" {
		config.ImmediateOnCRChange = true
	}

	return &config, nil
}

// shouldReconcile determines if the reconcile should proceed based on the trigger configuration
func (r *OpenstackConfigReconciler) shouldReconcile(ctx context.Context, triggerConfig *multinicv1alpha1.ReconcileTriggerConfig, hasChanged bool) (bool, time.Duration) {
	log := log.FromContext(ctx)

	// Always reconcile immediately on CR changes if ImmediateOnCRChange is true
	if hasChanged && triggerConfig.ImmediateOnCRChange {
		log.Info("Immediate reconcile triggered by CR change event")
		return true, 0
	}

	switch triggerConfig.Mode {
	case "manual":
		// Only reconcile on CR changes, not on periodic reconciles
		if hasChanged {
			log.Info("Manual mode: reconciling due to CR change")
			return true, 0
		}
		log.Info("Manual mode: skipping periodic reconcile")
		return false, time.Hour * 24 // Check again in 24 hours

	case "scheduled":
		if hasChanged {
			// CR change event - reconcile immediately if ImmediateOnCRChange is true
			shouldReconcileNow := triggerConfig.ImmediateOnCRChange
			if shouldReconcileNow {
				log.Info("Scheduled mode: immediate reconcile due to CR change")
				return true, 0
			} else {
				log.Info("Scheduled mode: CR changed but immediateOnCRChange is false, will wait for scheduled interval")
				interval := r.parseInterval(triggerConfig.Interval)
				if interval > 0 {
					return false, interval
				}
				return false, reconcileInterval
			}
		}

		// For scheduled mode, always reconcile on periodic intervals
		// This ensures regular sync with OpenStack at the specified interval
		interval := r.parseInterval(triggerConfig.Interval)
		if interval > 0 {
			log.Info("Scheduled mode: periodic reconcile triggered", "interval", interval)
			return true, interval
		}

		// Fallback to default interval if parsing fails
		log.Info("Scheduled mode: using default interval")
		return true, reconcileInterval

	default:
		// Default behavior: treat unknown modes (including legacy "immediate" and "webhook") as manual mode
		log.Info("Unknown or legacy trigger mode, treating as manual mode", "mode", triggerConfig.Mode)
		if hasChanged {
			return true, 0
		}
		return false, time.Hour * 24
	}
}

// getNextReconcileInterval retrieves the next reconcile interval based on the trigger configuration
func (r *OpenstackConfigReconciler) getNextReconcileInterval(triggerConfig *multinicv1alpha1.ReconcileTriggerConfig) time.Duration {
	switch triggerConfig.Mode {
	case "scheduled":
		interval := r.parseInterval(triggerConfig.Interval)
		if interval > 0 {
			return interval
		}
		return reconcileInterval

	case "manual":
		return time.Hour * 24 // Check infrequently for manual mode

	default:
		return reconcileInterval
	}
}

// parseInterval parses interval string (e.g., "5m", "1h", "30s")
func (r *OpenstackConfigReconciler) parseInterval(interval string) time.Duration {
	if interval == "" {
		return 0
	}

	duration, err := time.ParseDuration(interval)
	if err != nil {
		log.Log.Error(err, "Failed to parse interval", "interval", interval)
		return 0
	}

	// Ensure minimum interval of 1 minute
	if duration < time.Minute {
		return time.Minute
	}

	return duration
}

// TimerBasedPredicate는 MultiNicOperator CR의 트리거 설정에 따라 reconcile을 제어합니다
type TimerBasedPredicate struct {
	client            client.Client
	lastReconcileTime sync.Map // namespace -> time.Time
}

func NewTimerBasedPredicate(client client.Client) *TimerBasedPredicate {
	return &TimerBasedPredicate{
		client: client,
	}
}

func (p *TimerBasedPredicate) Create(e event.CreateEvent) bool {
	// Creation 이벤트는 항상 허용
	return true
}

func (p *TimerBasedPredicate) Delete(e event.DeleteEvent) bool {
	// Deletion 이벤트는 항상 허용
	return true
}

func (p *TimerBasedPredicate) Update(e event.UpdateEvent) bool {
	// Spec이 변경된 경우 항상 허용
	if e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() {
		return true
	}

	// Status update로 인한 reconcile인 경우 시간 기반 제어
	return p.shouldReconcileBasedOnTimer(e.ObjectNew)
}

func (p *TimerBasedPredicate) Generic(e event.GenericEvent) bool {
	// Generic 이벤트는 시간 기반으로 제어
	return p.shouldReconcileBasedOnTimer(e.Object)
}

func (p *TimerBasedPredicate) shouldReconcileBasedOnTimer(obj client.Object) bool {
	ctx := context.Background()

	// MultiNicOperator CR에서 트리거 설정 가져오기
	triggerConfig, err := p.getReconcileTriggerConfig(ctx, obj.GetNamespace())
	if err != nil {
		// 오류 시 기본적으로 허용
		return true
	}

	// Manual 모드는 spec 변경 시에만 허용
	if triggerConfig.Mode == "manual" {
		return false
	}

	// Legacy immediate, webhook 모드는 manual 모드로 처리
	if triggerConfig.Mode == "immediate" || triggerConfig.Mode == "webhook" || triggerConfig.Mode == "" {
		return false
	}

	// Scheduled 모드의 경우 시간 체크
	if triggerConfig.Mode == "scheduled" {
		key := obj.GetNamespace() + "/" + obj.GetName()
		lastTime, exists := p.lastReconcileTime.Load(key)

		if !exists {
			// 첫 번째 reconcile인 경우 허용
			p.lastReconcileTime.Store(key, time.Now())
			return true
		}

		interval := p.parseInterval(triggerConfig.Interval)
		if interval == 0 {
			interval = time.Minute // 기본값
		}

		if time.Since(lastTime.(time.Time)) >= interval {
			p.lastReconcileTime.Store(key, time.Now())
			return true
		}

		return false
	}

	// 기본적으로 허용
	return true
}

func (p *TimerBasedPredicate) getReconcileTriggerConfig(ctx context.Context, namespace string) (*multinicv1alpha1.ReconcileTriggerConfig, error) {
	multinicOperatorList := &multinicv1alpha1.MultiNicOperatorList{}
	err := p.client.List(ctx, multinicOperatorList, client.InNamespace(namespace))
	if err != nil {
		return nil, err
	}

	if len(multinicOperatorList.Items) == 0 {
		return &multinicv1alpha1.ReconcileTriggerConfig{
			Mode:                "immediate",
			ImmediateOnCRChange: true,
		}, nil
	}

	config := multinicOperatorList.Items[0].Spec.ReconcileTrigger
	if config.Mode == "" {
		config.Mode = "immediate"
	}
	if config.Mode == "immediate" {
		config.ImmediateOnCRChange = true
	}

	return &config, nil
}

func (p *TimerBasedPredicate) parseInterval(interval string) time.Duration {
	if interval == "" {
		return 0
	}

	duration, err := time.ParseDuration(interval)
	if err != nil {
		return 0
	}

	if duration < time.Minute {
		return time.Minute
	}

	return duration
}
