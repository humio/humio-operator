package humio

import (
	"fmt"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/prometheus/common/log"
	"github.com/shurcooL/graphql"
)

// ClusterController holds our client
type ClusterController struct {
	client Client
}

// NewClusterController returns a ClusterController
func NewClusterController(client Client) *ClusterController {
	return &ClusterController{client: client}
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

// // IsNodeRegistered returns whether the Humio cluster has a node with the given node id
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
func (c *ClusterController) AreStoragePartitionsBalanced(hc *corev1alpha1.HumioCluster) (bool, error) {
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
			log.Info("the number of nodes in a partition does not match the replication factor")
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
			log.Infof("node id %d does not contain any storage partitions", i)
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
		log.Infof("the difference in number of storage partitions assigned per storage node is greater than 1, min=%d, max=%d", min, max)
		return false, nil
	}

	log.Infof("storage partitions are balanced min=%d, max=%d", min, max)
	return true, nil

}

// RebalanceStoragePartitions will assign storage partitions evenly across registered storage nodes. If replication is not set, we set it to 1.
func (c *ClusterController) RebalanceStoragePartitions(hc *corev1alpha1.HumioCluster) error {
	log.Info("rebalancing storage partitions")

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
		return fmt.Errorf("could not generate storage partition scheme candidate: %v", err)
	}

	if err := c.client.UpdateStoragePartitionScheme(partitionAssignment); err != nil {
		return fmt.Errorf("could not update storage partition scheme: %v", err)
	}

	partitions, _ := c.client.GetStoragePartitions()
	log.Infof("balanced storage partitions: %v, assignment: %v", partitions, partitionAssignment)
	return nil
}

// AreIngestPartitionsBalanced ensures three things.
// First, if all ingest partitions are consumed by the expected (target replication factor) number of digest nodes.
// Second, all digest nodes must have ingest partitions assigned.
// Third, the difference in number of partitiones assigned per digest node must be at most 1.
func (c *ClusterController) AreIngestPartitionsBalanced(hc *corev1alpha1.HumioCluster) (bool, error) {
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
			log.Info("the number of nodes in a partition does not match the replication factor")
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
			log.Infof("node id %d does not contain any ingest partitions", i)
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
		log.Infof("the difference in number of ingest partitions assigned per storage node is greater than 1, min=%d, max=%d", min, max)
		return false, nil
	}

	log.Infof("ingest partitions are balanced min=%d, max=%d", min, max)
	return true, nil
}

// RebalanceIngestPartitions will assign ingest partitions evenly across registered digest nodes. If replication is not set, we set it to 1.
func (c *ClusterController) RebalanceIngestPartitions(hc *corev1alpha1.HumioCluster) error {
	log.Info("rebalancing ingest partitions")

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
		return fmt.Errorf("could not generate ingest partition scheme candidate: %v", err)
	}

	if err := c.client.UpdateIngestPartitionScheme(partitionAssignment); err != nil {
		return fmt.Errorf("could not update ingest partition scheme: %v", err)
	}
	return nil
}

// StartDataRedistribution notifies the Humio cluster that it should start redistributing data to match current assignments
// TODO: how often, or when do we run this? Is it necessary for storage and digest? Is it necessary for MoveStorageRouteAwayFromNode
// and MoveIngestRoutesAwayFromNode?
func (c *ClusterController) StartDataRedistribution(hc *corev1alpha1.HumioCluster) error {
	log.Info("starting data redistribution")

	if err := c.client.StartDataRedistribution(); err != nil {
		return fmt.Errorf("could not start data redistribution: %v", err)
	}
	return nil
}

// MoveStorageRouteAwayFromNode notifies the Humio cluster that a node ID should be removed from handling any storage partitions
func (c *ClusterController) MoveStorageRouteAwayFromNode(hc *corev1alpha1.HumioCluster, pID int) error {
	log.Info(fmt.Sprintf("moving storage route away from node %d", pID))

	if err := c.client.ClusterMoveStorageRouteAwayFromNode(pID); err != nil {
		return fmt.Errorf("could not move storage route away from node: %v", err)
	}
	return nil
}

// MoveIngestRoutesAwayFromNode notifies the Humio cluster that a node ID should be removed from handling any ingest partitions
func (c *ClusterController) MoveIngestRoutesAwayFromNode(hc *corev1alpha1.HumioCluster, pID int) error {
	log.Info(fmt.Sprintf("moving ingest routes away from node %d", pID))

	if err := c.client.ClusterMoveIngestRoutesAwayFromNode(pID); err != nil {
		return fmt.Errorf("could not move ingest routes away from node: %v", err)
	}
	return nil
}

// ClusterUnregisterNode tells the Humio cluster that we want to unregister a node
func (c *ClusterController) ClusterUnregisterNode(hc *corev1alpha1.HumioCluster, pID int) error {
	log.Info(fmt.Sprintf("unregistering node with id %d", pID))

	err := c.client.Unregister(pID)
	if err != nil {
		return fmt.Errorf("could not unregister node: %v", err)
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
func generateIngestPartitionSchemeCandidate(hc *corev1alpha1.HumioCluster, ingestNodeIDs []int, partitionCount, targetReplication int) ([]humioapi.IngestPartitionInput, error) {
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
