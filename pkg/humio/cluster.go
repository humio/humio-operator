/*
Copyright 2020 Humio https://humio.com

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

package humio

import (
	"fmt"
	"github.com/go-logr/logr"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

// ClusterController holds our client
type ClusterController struct {
	client Client
	logger logr.Logger
}

// NewClusterController returns a ClusterController
func NewClusterController(logger logr.Logger, client Client) *ClusterController {
	return &ClusterController{
		client: client,
		logger: logger,
	}
}

// AreAllRegisteredNodesAvailable only returns true if all nodes registered with humio are available
func (c *ClusterController) AreAllRegisteredNodesAvailable() (bool, error) {
	cluster, err := c.client.GetClusters()
	if err != nil {
		return false, err
	}

	for _, n := range cluster.Nodes {
		if !n.IsAvailable {
			return false, nil
		}
	}
	return true, nil
}

// NoDataMissing only returns true if all data are available
func (c *ClusterController) NoDataMissing() (bool, error) {
	cluster, err := c.client.GetClusters()
	if err != nil {
		return false, err
	}
	if cluster.MissingSegmentSize == 0 {
		return true, nil
	}
	return false, nil
}

// IsNodeRegistered returns whether the Humio cluster has a node with the given node id
func (c *ClusterController) IsNodeRegistered(nodeID int) (bool, error) {
	cluster, err := c.client.GetClusters()
	if err != nil {
		return false, err
	}

	for _, node := range cluster.Nodes {
		if int(node.Id) == nodeID {
			return true, nil
		}
	}
	return false, nil
}

// CountNodesRegistered returns how many registered nodes there are in the cluster
func (c *ClusterController) CountNodesRegistered() (int, error) {
	cluster, err := c.client.GetClusters()
	if err != nil {
		return -1, err
	}
	return len(cluster.Nodes), nil
}

// CanBeSafelyUnregistered returns true if the Humio API indicates that the node can be safely unregistered. This should ensure that the node does not hold any data.
func (c *ClusterController) CanBeSafelyUnregistered(podID int) (bool, error) {
	cluster, err := c.client.GetClusters()
	if err != nil {
		return false, err
	}

	for _, node := range cluster.Nodes {
		if int(node.Id) == podID && node.CanBeSafelyUnregistered {
			return true, nil
		}
	}
	return false, nil
}

// StartDataRedistribution notifies the Humio cluster that it should start redistributing data to match current assignments
// TODO: how often, or when do we run this? Is it necessary for storage and digest? Is it necessary for MoveStorageRouteAwayFromNode
// and MoveIngestRoutesAwayFromNode?
func (c *ClusterController) StartDataRedistribution(hc *humiov1alpha1.HumioCluster) error {
	c.logger.Info("starting data redistribution")

	if err := c.client.StartDataRedistribution(); err != nil {
		return fmt.Errorf("could not start data redistribution: %s", err)
	}
	return nil
}

// MoveStorageRouteAwayFromNode notifies the Humio cluster that a node ID should be removed from handling any storage partitions
func (c *ClusterController) MoveStorageRouteAwayFromNode(hc *humiov1alpha1.HumioCluster, nodeID int) error {
	c.logger.Info(fmt.Sprintf("moving storage route away from node %d", nodeID))

	if err := c.client.ClusterMoveStorageRouteAwayFromNode(nodeID); err != nil {
		return fmt.Errorf("could not move storage route away from node: %s", err)
	}
	return nil
}

// MoveIngestRoutesAwayFromNode notifies the Humio cluster that a node ID should be removed from handling any ingest partitions
func (c *ClusterController) MoveIngestRoutesAwayFromNode(hc *humiov1alpha1.HumioCluster, nodeID int) error {
	c.logger.Info(fmt.Sprintf("moving ingest routes away from node %d", nodeID))

	if err := c.client.ClusterMoveIngestRoutesAwayFromNode(nodeID); err != nil {
		return fmt.Errorf("could not move ingest routes away from node: %s", err)
	}
	return nil
}

// ClusterUnregisterNode tells the Humio cluster that we want to unregister a node
func (c *ClusterController) ClusterUnregisterNode(hc *humiov1alpha1.HumioCluster, nodeID int) error {
	c.logger.Info(fmt.Sprintf("unregistering node with id %d", nodeID))

	err := c.client.Unregister(nodeID)
	if err != nil {
		return fmt.Errorf("could not unregister node: %s", err)
	}
	return nil
}
