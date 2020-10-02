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

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/shurcooL/graphql"
	"go.uber.org/zap"
)

// ClusterController holds our client
type ClusterController struct {
	client Client
	logger *zap.SugaredLogger
}

// NewClusterController returns a ClusterController
func NewClusterController(logger *zap.SugaredLogger, client Client) *ClusterController {
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

// AreStoragePartitionsBalanced ensures three things.
// First, if all storage partitions are consumed by the expected (target replication factor) number of storage nodes.
// Second, all storage nodes must have storage partitions assigned.
// Third, the difference in number of partitiones assigned per storage node must be at most 1.
func (c *ClusterController) AreStoragePartitionsBalanced(hc *humiov1alpha1.HumioCluster) (bool, error) {
	cluster, err := c.client.GetClusters()
	if err != nil {
		return false, err
	}

	nodeToPartitionCount := make(map[int]int)
	for _, nodeID := range cluster.Nodes {
		nodeToPartitionCount[nodeID.Id] = 0
	}

	for _, partition := range cluster.StoragePartitions {
		if len(partition.NodeIds) != hc.Spec.TargetReplicationFactor {
			c.logger.Info("the number of nodes in a partition does not match the replication factor")
			return false, nil
		}
		for _, node := range partition.NodeIds {
			nodeToPartitionCount[node]++
		}
	}

	// TODO: this should be moved to the humio/cli package
	var min, max int
	for i, partitionCount := range nodeToPartitionCount {
		if partitionCount == 0 {
			c.logger.Infof("node id %d does not contain any storage partitions", i)
			return false, nil
		}
		if min == 0 {
			min = partitionCount
		}
		if max == 0 {
			max = partitionCount
		}
		if partitionCount > max {
			max = partitionCount
		}
		if partitionCount < min {
			min = partitionCount
		}
	}

	if max-min > 1 {
		c.logger.Infof("the difference in number of storage partitions assigned per storage node is greater than 1, min=%d, max=%d", min, max)
		return false, nil
	}

	c.logger.Infof("storage partitions are balanced min=%d, max=%d", min, max)
	return true, nil
}

// RebalanceStoragePartitions will assign storage partitions evenly across registered storage nodes. If replication is not set, we set it to 1.
func (c *ClusterController) RebalanceStoragePartitions(hc *humiov1alpha1.HumioCluster) error {
	c.logger.Info("rebalancing storage partitions")

	cluster, err := c.client.GetClusters()
	if err != nil {
		return err
	}

	replication := hc.Spec.TargetReplicationFactor
	if hc.Spec.TargetReplicationFactor == 0 {
		replication = 1
	}

	var storageNodeIDs []int

	for _, node := range cluster.Nodes {
		storageNodeIDs = append(storageNodeIDs, node.Id)
	}

	partitionAssignment, err := generateStoragePartitionSchemeCandidate(storageNodeIDs, hc.Spec.StoragePartitionsCount, replication)
	if err != nil {
		return fmt.Errorf("could not generate storage partition scheme candidate: %s", err)
	}

	if err := c.client.UpdateStoragePartitionScheme(partitionAssignment); err != nil {
		return fmt.Errorf("could not update storage partition scheme: %s", err)
	}
	return nil
}

// AreIngestPartitionsBalanced ensures three things.
// First, if all ingest partitions are consumed by the expected (target replication factor) number of digest nodes.
// Second, all digest nodes must have ingest partitions assigned.
// Third, the difference in number of partitiones assigned per digest node must be at most 1.
func (c *ClusterController) AreIngestPartitionsBalanced(hc *humiov1alpha1.HumioCluster) (bool, error) {
	cluster, err := c.client.GetClusters()
	if err != nil {
		return false, err
	}

	// get a map that can tell us how many partitions a node has
	nodeToPartitionCount := make(map[int]int)
	for _, nodeID := range cluster.Nodes {
		nodeToPartitionCount[nodeID.Id] = 0
	}

	for _, partition := range cluster.IngestPartitions {
		if len(partition.NodeIds) != hc.Spec.TargetReplicationFactor {
			c.logger.Info("the number of nodes in a partition does not match the replication factor")
			return false, nil
		}
		for _, node := range partition.NodeIds {
			nodeToPartitionCount[node]++
		}
	}

	// TODO: this should be moved to the humio/cli package
	var min, max int
	for i, partitionCount := range nodeToPartitionCount {
		if partitionCount == 0 {
			c.logger.Infof("node id %d does not contain any ingest partitions", i)
			return false, nil
		}
		if min == 0 {
			min = partitionCount
		}
		if max == 0 {
			max = partitionCount
		}
		if partitionCount > max {
			max = partitionCount
		}
		if partitionCount < min {
			min = partitionCount
		}
	}

	if max-min > 1 {
		c.logger.Infof("the difference in number of ingest partitions assigned per storage node is greater than 1, min=%d, max=%d", min, max)
		return false, nil
	}

	c.logger.Infof("ingest partitions are balanced min=%d, max=%d", min, max)
	return true, nil
}

// RebalanceIngestPartitions will assign ingest partitions evenly across registered digest nodes. If replication is not set, we set it to 1.
func (c *ClusterController) RebalanceIngestPartitions(hc *humiov1alpha1.HumioCluster) error {
	c.logger.Info("rebalancing ingest partitions")

	cluster, err := c.client.GetClusters()
	if err != nil {
		return err
	}

	replication := hc.Spec.TargetReplicationFactor
	if hc.Spec.TargetReplicationFactor == 0 {
		replication = 1
	}

	var digestNodeIDs []int

	for _, node := range cluster.Nodes {
		digestNodeIDs = append(digestNodeIDs, node.Id)
	}

	partitionAssignment, err := generateIngestPartitionSchemeCandidate(hc, digestNodeIDs, hc.Spec.DigestPartitionsCount, replication)
	if err != nil {
		return fmt.Errorf("could not generate ingest partition scheme candidate: %s", err)
	}

	if err := c.client.UpdateIngestPartitionScheme(partitionAssignment); err != nil {
		return fmt.Errorf("could not update ingest partition scheme: %s", err)
	}
	return nil
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
	c.logger.Infof("moving storage route away from node %d", nodeID)

	if err := c.client.ClusterMoveStorageRouteAwayFromNode(nodeID); err != nil {
		return fmt.Errorf("could not move storage route away from node: %s", err)
	}
	return nil
}

// MoveIngestRoutesAwayFromNode notifies the Humio cluster that a node ID should be removed from handling any ingest partitions
func (c *ClusterController) MoveIngestRoutesAwayFromNode(hc *humiov1alpha1.HumioCluster, nodeID int) error {
	c.logger.Infof("moving ingest routes away from node %d", nodeID)

	if err := c.client.ClusterMoveIngestRoutesAwayFromNode(nodeID); err != nil {
		return fmt.Errorf("could not move ingest routes away from node: %s", err)
	}
	return nil
}

// ClusterUnregisterNode tells the Humio cluster that we want to unregister a node
func (c *ClusterController) ClusterUnregisterNode(hc *humiov1alpha1.HumioCluster, nodeID int) error {
	c.logger.Infof("unregistering node with id %d", nodeID)

	err := c.client.Unregister(nodeID)
	if err != nil {
		return fmt.Errorf("could not unregister node: %s", err)
	}
	return nil
}

func generateStoragePartitionSchemeCandidate(storageNodeIDs []int, partitionCount, targetReplication int) ([]humioapi.StoragePartitionInput, error) {
	replicas := targetReplication
	if targetReplication > len(storageNodeIDs) {
		replicas = len(storageNodeIDs)
	}
	if replicas == 0 {
		return nil, fmt.Errorf("not possible to use replication factor 0")
	}

	var ps []humioapi.StoragePartitionInput

	for p := 0; p < partitionCount; p++ {
		var nodeIds []graphql.Int
		for r := 0; r < replicas; r++ {
			idx := (p + r) % len(storageNodeIDs)
			nodeIds = append(nodeIds, graphql.Int(storageNodeIDs[idx]))
		}
		ps = append(ps, humioapi.StoragePartitionInput{ID: graphql.Int(p), NodeIDs: nodeIds})
	}

	return ps, nil
}

// TODO: move this to the cli
// TODO: perhaps we need to move the zones to groups. e.g. zone a becomes group 1, zone c becomes zone 2 if there is no zone b
func generateIngestPartitionSchemeCandidate(hc *humiov1alpha1.HumioCluster, ingestNodeIDs []int, partitionCount, targetReplication int) ([]humioapi.IngestPartitionInput, error) {
	replicas := targetReplication
	if targetReplication > len(ingestNodeIDs) {
		replicas = len(ingestNodeIDs)
	}
	if replicas == 0 {
		return nil, fmt.Errorf("not possible to use replication factor 0")
	}

	var ps []humioapi.IngestPartitionInput

	for p := 0; p < partitionCount; p++ {
		var nodeIds []graphql.Int
		for r := 0; r < replicas; r++ {
			idx := (p + r) % len(ingestNodeIDs)
			nodeIds = append(nodeIds, graphql.Int(ingestNodeIDs[idx]))
		}
		ps = append(ps, humioapi.IngestPartitionInput{ID: graphql.Int(p), NodeIDs: nodeIds})
	}

	return ps, nil
}
