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
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	multinicv1alpha1 "github.com/xormsdhkdwk/multinic/api/v1alpha1"
)

const (
	// Database configuration
	dbRetryInterval   = 10 * time.Second
	maxRetries        = 3
	reconcileInterval = 5 * time.Minute
	finalizerName     = "multinic.example.com/finalizer"

	// Database connection configuration
	defaultDBHost     = "110.0.0.101"
	defaultDBPort     = "30306"
	defaultDBUser     = "root"
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
	Scheme   *runtime.Scheme
	DB       *sql.DB
	DBConfig *DatabaseConfig
}

// +kubebuilder:rbac:groups=multinic.example.com,resources=openstackconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=multinic.example.com,resources=openstackconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=multinic.example.com,resources=openstackconfigs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the OpenstackConfig object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *OpenstackConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Ensure DB connection
	if err := r.ensureDBConnection(ctx); err != nil {
		log.Error(err, "Failed to ensure database connection")
		return ctrl.Result{RequeueAfter: dbRetryInterval}, err
	}

	// Fetch the OpenstackConfig instance
	openstackConfig := &multinicv1alpha1.OpenstackConfig{}
	if err := r.Client.Get(ctx, req.NamespacedName, openstackConfig); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if openstackConfig.DeletionTimestamp != nil {
		return r.handleDeletion(ctx, openstackConfig)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(openstackConfig, finalizerName) {
		controllerutil.AddFinalizer(openstackConfig, finalizerName)
		if err := r.Update(ctx, openstackConfig); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Continue with normal reconciliation logic...
	return r.reconcileNormal(ctx, openstackConfig)
}

// handleDeletion handles the deletion of OpenstackConfig CR
func (r *OpenstackConfigReconciler) handleDeletion(ctx context.Context, openstackConfig *multinicv1alpha1.OpenstackConfig) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(openstackConfig, finalizerName) {
		// Update deleted_at timestamp in multi_subnet, multi_interface, and node_table
		deletedAt := openstackConfig.DeletionTimestamp.Time

		// Get subnet information first to find which subnet to mark as deleted
		subnet, err := r.getSubnetInfo(ctx, openstackConfig)
		if err != nil {
			log.Error(err, "Failed to get subnet info during deletion")
			// Continue with finalizer removal even if subnet lookup fails
		} else if subnet != nil {
			// Begin transaction for deletion
			tx, err := r.DB.BeginTx(ctx, nil)
			if err != nil {
				log.Error(err, "Failed to begin deletion transaction")
				return ctrl.Result{}, err
			}
			defer tx.Rollback()

			// Update multi_subnet table
			_, err = tx.ExecContext(ctx, `
				UPDATE multi_subnet 
				SET deleted_at = ?, status = 'deleted'
				WHERE id = ?`,
				deletedAt, subnet.ID)
			if err != nil {
				log.Error(err, "Failed to update multi_subnet deleted_at timestamp", "subnet_id", subnet.ID)
				return ctrl.Result{}, err
			}

			// Update multi_interface table for all interfaces in this subnet (before node_table due to foreign key)
			_, err = tx.ExecContext(ctx, `
				UPDATE multi_interface 
				SET deleted_at = ?, status = 'deleted'
				WHERE subnet_id = ?`,
				deletedAt, subnet.ID)
			if err != nil {
				log.Error(err, "Failed to update multi_interface deleted_at timestamp", "subnet_id", subnet.ID)
				return ctrl.Result{}, err
			}

			// Update node_table for all nodes associated with interfaces in this subnet
			_, err = tx.ExecContext(ctx, `
				UPDATE node_table 
				SET deleted_at = ?, status = 'deleted'
				WHERE attached_node_id IN (
					SELECT DISTINCT attached_node_id 
					FROM multi_interface 
					WHERE subnet_id = ? AND attached_node_id IS NOT NULL
				)`,
				deletedAt, subnet.ID)
			if err != nil {
				log.Error(err, "Failed to update node_table deleted_at timestamp", "subnet_id", subnet.ID)
				return ctrl.Result{}, err
			}

			// Commit deletion transaction
			if err := tx.Commit(); err != nil {
				log.Error(err, "Failed to commit deletion transaction")
				return ctrl.Result{}, err
			}

			log.Info("Successfully marked subnet, interfaces, and nodes as deleted",
				"subnet_id", subnet.ID,
				"deleted_at", deletedAt)
		}

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

	log.Info("test-opts", "IdentityEndpoint", opts.IdentityEndpoint)

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
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
	}

	// Create compute client
	computeEndpoint := normalizeEndpoint(openstackConfig.Spec.Credentials.ComputeEndpoint, computeEndpointSuffix)
	computeClient, err := createOpenStackClient(ctx, opts, computeEndpoint, "compute")
	if err != nil {
		log.Error(err, "Failed to create compute client")
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
	}

	log.Info("test-computeClient1", "computeClient", computeClient)
	log.Info("test-computeClient2", "computeClient", computeClient.Endpoint)
	log.Info("test-computeClient3", "computeClient", computeClient.TokenID, "type", computeClient.Type, "identityEndpoint", opts.IdentityEndpoint, "MoreHeaders", computeClient.MoreHeaders)

	// Get VM information
	var targetVM *servers.Server
	log.Info("Starting VM search process", "target_vm", openstackConfig.Spec.VMName)

	if computeClient == nil {
		log.Error(nil, "Compute client is nil")
		openstackConfig.Status.Status = "Error"
		openstackConfig.Status.LastUpdated = metav1.Now()
		openstackConfig.Status.Message = "Failed to create compute client"
		if err := r.Client.Status().Update(ctx, openstackConfig); err != nil {
			log.Error(err, "Failed to update status")
		}
		return ctrl.Result{RequeueAfter: reconcileInterval}, fmt.Errorf("compute client is nil")
	}

	log.Info("Starting server list request with options",
		"computeEndpoint", computeClient.Endpoint,
		"resourceBase", computeClient.ResourceBase,
		"tokenID", computeClient.TokenID != "",
		"serviceType", computeClient.Type)

	// 실제 요청 URL 구성 확인 - 특정 서버 이름으로 필터링
	requestURL := computeClient.Endpoint + "/servers/detail?name=" + openstackConfig.Spec.VMName
	log.Info("Actual request URL that will be used", "url", requestURL, "target_vm", openstackConfig.Spec.VMName)

	// 직접 HTTP 요청으로 서버 목록 조회
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	httpReq, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		log.Error(err, "Failed to create HTTP request")
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
	}

	httpReq.Header.Set("X-Auth-Token", computeClient.TokenID)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		log.Error(err, "Failed to execute HTTP request")
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error(err, "Failed to read response body")
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
	}

	log.Info("HTTP response received", "status", resp.Status, "bodyLength", len(body))

	// JSON 응답 파싱
	var serverResponse struct {
		Servers []servers.Server `json:"servers"`
	}

	err = json.Unmarshal(body, &serverResponse)
	if err != nil {
		log.Error(err, "Failed to unmarshal server response", "body", string(body))
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
	}

	allServers := serverResponse.Servers
	log.Info("Successfully extracted servers", "count", len(allServers))

	// 필터링된 결과에서 대상 VM 찾기
	if len(allServers) == 0 {
		log.Info("Target VM not found", "name", openstackConfig.Spec.VMName)
		openstackConfig.Status.Status = "Error"
		openstackConfig.Status.LastUpdated = metav1.Now()
		openstackConfig.Status.Message = fmt.Sprintf("VM %s not found", openstackConfig.Spec.VMName)
		if err := r.Client.Status().Update(ctx, openstackConfig); err != nil {
			log.Error(err, "Failed to update status")
		}
		return ctrl.Result{RequeueAfter: reconcileInterval}, nil
	}

	// 첫 번째 서버를 대상 VM으로 사용 (이름으로 필터링했으므로)
	targetVM = &allServers[0]
	log.Info("Found target VM",
		"name", targetVM.Name,
		"id", targetVM.ID,
		"status", targetVM.Status)

	// 서버 목록 로깅 (디버깅용)
	for _, server := range allServers {
		log.Info("Found server",
			"name", server.Name,
			"id", server.ID,
			"status", server.Status)
	}

	// Get subnet information
	subnet, err := r.getSubnetInfoWithClients(ctx, openstackConfig, networkClient, client)
	if err != nil {
		log.Error(err, "Failed to get subnet information")
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
	}

	if subnet == nil {
		log.Info("Subnet not found", "name", openstackConfig.Spec.SubnetName)
		openstackConfig.Status.Status = "Error"
		openstackConfig.Status.LastUpdated = metav1.Now()
		openstackConfig.Status.Message = fmt.Sprintf("Subnet %s not found", openstackConfig.Spec.SubnetName)
		if err := r.Client.Status().Update(ctx, openstackConfig); err != nil {
			log.Error(err, "Failed to update status")
		}
		return ctrl.Result{RequeueAfter: reconcileInterval}, nil
	}

	// Begin transaction
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		log.Error(err, "Failed to begin transaction")
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
	}
	defer tx.Rollback()

	// Insert/Update multi_subnet with CR lifecycle timestamps
	createdAt := openstackConfig.CreationTimestamp.Time
	modifiedAt := time.Now()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO multi_subnet (id, subnet_name, cidr, status, created_at, modified_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
		subnet_name=?, cidr=?, status=?, modified_at=?`,
		subnet.ID, subnet.Name, subnet.CIDR, "active", createdAt, modifiedAt,
		subnet.Name, subnet.CIDR, "active", modifiedAt,
	)
	if err != nil {
		log.Error(err, "Failed to insert subnet information")
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
	}

	log.Info("Successfully inserted/updated subnet information",
		"subnet_id", subnet.ID,
		"subnet_name", subnet.Name,
		"cidr", subnet.CIDR,
		"created_at", createdAt,
		"modified_at", modifiedAt)

	// Insert into node_table first (before multi_interface due to foreign key)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO node_table (attached_node_id, attached_node_name, status, created_at, modified_at)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
		attached_node_name=?, status=?, modified_at=?`,
		targetVM.ID, targetVM.Name, "active", createdAt, modifiedAt,
		targetVM.Name, "active", modifiedAt,
	)
	if err != nil {
		log.Error(err, "Failed to insert node information")
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
	}

	log.Info("Successfully inserted/updated node information",
		"node_id", targetVM.ID,
		"node_name", targetVM.Name,
		"created_at", createdAt,
		"modified_at", modifiedAt)

	// Get port information for the server
	for _, addresses := range targetVM.Addresses {
		for _, address := range addresses.([]interface{}) {
			addr := address.(map[string]interface{})

			// type과 mac_addr 필드 체크 (port는 없을 수 있음)
			if addr["OS-EXT-IPS:type"] == nil || addr["OS-EXT-IPS-MAC:mac_addr"] == nil {
				log.Info("Skipping address entry due to missing required fields",
					"address", addr,
					"has_type", addr["OS-EXT-IPS:type"] != nil,
					"has_mac", addr["OS-EXT-IPS-MAC:mac_addr"] != nil)
				continue
			}

			if addr["OS-EXT-IPS:type"].(string) == "fixed" {
				macAddr, ok := addr["OS-EXT-IPS-MAC:mac_addr"].(string)
				if !ok {
					log.Info("MAC address is not a string, skipping", "mac", addr["OS-EXT-IPS-MAC:mac_addr"])
					continue
				}

				var portID string

				// port 필드가 있으면 직접 사용
				if addr["port"] != nil {
					if id, ok := addr["port"].(string); ok {
						portID = id
						log.Info("Using port ID from VM address info", "port_id", portID, "mac_addr", macAddr)
					}
				}

				// port 필드가 없거나 유효하지 않으면 MAC 주소로 포트 조회
				if portID == "" {
					log.Info("Port ID not available, searching by MAC address", "mac_addr", macAddr)

					// MAC 주소로 포트 조회
					foundPortID, err := r.findPortByMACAddress(ctx, openstackConfig, macAddr, subnet.ID)
					if err != nil {
						log.Error(err, "Failed to find port by MAC address", "mac_addr", macAddr)
						continue
					}
					if foundPortID == "" {
						log.Info("No port found for MAC address", "mac_addr", macAddr)
						continue
					}
					portID = foundPortID
					log.Info("Found port ID by MAC address", "port_id", portID, "mac_addr", macAddr)
				}

				// Insert into multi_interface
				_, err = tx.ExecContext(ctx, `
					INSERT INTO multi_interface (id, subnet_id, macaddress, attached_node_id, status, created_at, modified_at)
					VALUES (?, ?, ?, ?, ?, ?, ?)
					ON DUPLICATE KEY UPDATE
					macaddress=?, attached_node_id=?, status=?, modified_at=?`,
					portID, subnet.ID, macAddr, targetVM.ID, "active", createdAt, modifiedAt,
					macAddr, targetVM.ID, "active", modifiedAt,
				)
				if err != nil {
					log.Error(err, "Failed to insert interface information")
					return ctrl.Result{RequeueAfter: reconcileInterval}, err
				}

				log.Info("Successfully inserted interface information",
					"port_id", portID,
					"mac_address", macAddr,
					"vm_id", targetVM.ID,
					"subnet_id", subnet.ID,
					"created_at", createdAt,
					"modified_at", modifiedAt)
			}
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		log.Error(err, "Failed to commit transaction")
		return ctrl.Result{RequeueAfter: reconcileInterval}, err
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
	openstackConfig.Status.NetworkInfo = multinicv1alpha1.NetworkInfo{
		SubnetID:   subnet.ID,
		NetworkID:  subnet.NetworkID,
		IPAddress:  targetVM.AccessIPv4,
		MACAddress: "", // Will be populated when we get network interface details
	}

	if err := r.Client.Status().Update(ctx, openstackConfig); err != nil {
		log.Error(err, "Failed to update OpenstackConfig status")
		return ctrl.Result{RequeueAfter: time.Second * 10}, err
	}

	return ctrl.Result{RequeueAfter: reconcileInterval}, nil
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

	for i := 0; i < maxRetries; i++ {
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

	return nil, fmt.Errorf("subnet %s not found after %d attempts", openstackConfig.Spec.SubnetName, maxRetries)
}

// SetupWithManager sets up the controller with the Manager.
func (r *OpenstackConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Note: Database connection will be established on first reconcile
	return ctrl.NewControllerManagedBy(mgr).
		For(&multinicv1alpha1.OpenstackConfig{}).
		Complete(r)
}

// ensureDBConnection ensures that the database connection is active
func (r *OpenstackConfigReconciler) ensureDBConnection(ctx context.Context) error {
	if r.DB == nil || r.DB.PingContext(ctx) != nil {
		dsn := r.buildDSN()
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return fmt.Errorf("failed to connect to MariaDB: %v", err)
		}

		// Configure connection pool with optimized settings
		db.SetMaxOpenConns(5)                   // 연결 수 줄임 (기존: 10)
		db.SetMaxIdleConns(2)                   // 유휴 연결 수 줄임 (기존: 5)
		db.SetConnMaxLifetime(10 * time.Minute) // 연결 수명 단축 (기존: 1시간)
		db.SetConnMaxIdleTime(5 * time.Minute)  // 유휴 연결 타임아웃 추가

		r.DB = db

		// Test connection
		if err := r.DB.PingContext(ctx); err != nil {
			return fmt.Errorf("failed to ping MariaDB: %v", err)
		}

		// Initialize database tables
		if err := r.initializeDatabase(); err != nil {
			return fmt.Errorf("failed to initialize database: %v", err)
		}
	}
	return nil
}

// initializeDatabase creates/updates database tables
func (r *OpenstackConfigReconciler) initializeDatabase() error {
	// Create/Update tables
	queries := []string{
		`CREATE TABLE IF NOT EXISTS multi_subnet (
			id VARCHAR(36) PRIMARY KEY,
			subnet_name VARCHAR(255) NOT NULL,
			cidr VARCHAR(255) NOT NULL,
			status VARCHAR(50) DEFAULT 'created',
			created_at TIMESTAMP NULL,
			modified_at TIMESTAMP NULL,
			deleted_at TIMESTAMP NULL
		)`,
		`CREATE TABLE IF NOT EXISTS node_table (
			id INT AUTO_INCREMENT PRIMARY KEY,
			attached_node_id VARCHAR(36) NOT NULL UNIQUE,
			attached_node_name VARCHAR(255) NOT NULL,
			status VARCHAR(50) DEFAULT 'created',
			created_at TIMESTAMP NULL,
			modified_at TIMESTAMP NULL,
			deleted_at TIMESTAMP NULL
		)`,
		`CREATE TABLE IF NOT EXISTS multi_interface (
			id VARCHAR(36) PRIMARY KEY,
			subnet_id VARCHAR(36) NOT NULL,
			macaddress VARCHAR(17) NOT NULL,
			attached_node_id VARCHAR(36),
			status VARCHAR(50) DEFAULT 'created',
			created_at TIMESTAMP NULL,
			modified_at TIMESTAMP NULL,
			deleted_at TIMESTAMP NULL,
			FOREIGN KEY (subnet_id) REFERENCES multi_subnet(id),
			FOREIGN KEY (attached_node_id) REFERENCES node_table(attached_node_id)
		)`,
	}

	for _, query := range queries {
		if _, err := r.DB.Exec(query); err != nil {
			return fmt.Errorf("failed to create table: %v", err)
		}
	}

	// Add/Modify columns for existing tables
	alterQueries := []string{
		// multi_subnet 테이블 수정
		`ALTER TABLE multi_subnet MODIFY COLUMN created_at TIMESTAMP NULL`,
		`ALTER TABLE multi_subnet MODIFY COLUMN modified_at TIMESTAMP NULL`,
		`ALTER TABLE multi_subnet ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP NULL`,
		`ALTER TABLE multi_subnet DROP COLUMN IF EXISTS cr_created_at`,
		`ALTER TABLE multi_subnet DROP COLUMN IF EXISTS cr_modified_at`,
		`ALTER TABLE multi_subnet DROP COLUMN IF EXISTS cr_deleted_at`,

		// multi_interface 테이블 수정
		`ALTER TABLE multi_interface MODIFY COLUMN created_at TIMESTAMP NULL`,
		`ALTER TABLE multi_interface MODIFY COLUMN modified_at TIMESTAMP NULL`,
		`ALTER TABLE multi_interface ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP NULL`,
		`ALTER TABLE multi_interface ADD COLUMN IF NOT EXISTS status VARCHAR(50) DEFAULT 'created'`,

		// node_table 테이블 수정
		`ALTER TABLE node_table MODIFY COLUMN created_at TIMESTAMP NULL`,
		`ALTER TABLE node_table MODIFY COLUMN modified_at TIMESTAMP NULL`,
		`ALTER TABLE node_table ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP NULL`,
		`ALTER TABLE node_table ADD COLUMN IF NOT EXISTS status VARCHAR(50) DEFAULT 'created'`,
	}

	for _, query := range alterQueries {
		if _, err := r.DB.Exec(query); err != nil {
			// Log error but don't fail - some MySQL versions don't support IF NOT EXISTS
			fmt.Printf("Warning: Failed to execute alter query: %v\n", err)
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

	for i := 0; i < maxRetries; i++ {
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

	return nil, fmt.Errorf("failed to create OpenStack client after %d attempts", maxRetries)
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
		User:     getEnvOrDefault("DB_USER", defaultDBUser),
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
		log.Info("Port found but not in target subnet",
			"mac_addr", macAddr,
			"port_id", port.ID,
			"port_subnets", port.FixedIPs,
			"target_subnet_id", targetSubnetID)
	}

	log.Info("No ports found in target subnet for MAC address", "mac_addr", macAddr, "target_subnet_id", targetSubnetID)
	return "", nil
}
