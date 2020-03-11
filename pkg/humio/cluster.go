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

// // CountNodesRegistered returns how many registered nodes there are in the cluster
func (c *ClusterController) CountNodesRegistered() (int, error) {
	cluster, err := c.client.GetClusters()
	if err != nil {
		return -1, err
	}
	return len(cluster.Nodes), nil
}

// // CanBeSafelyUnregistered returns true if the Humio API indicates that the node can be safely unregistered. This should ensure that the node does not hold any data.
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

// AreStoragePartitionsBalanced ensures four things.
// First, if all storage partitions are consumed by the expected (target replication factor) number of storage nodes.
// Second, all storage nodes must have storage partitions assigned.
// Third, all nodes that are not storage nodes does not have any storage partitions assigned.
// Fourth, the difference in number of partitiones assigned per storage node must be at most 1.
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

	var min, max int
	for i := 0; i < len(cluster.Nodes); i++ {
		if nodeToPartitionCount[i] == 0 {
			log.Infof("node id %d does not contain any partitions", i)
			return false, nil
		}
		if min == 0 {
			min = nodeToPartitionCount[i]
		}
		if max == 0 {
			max = nodeToPartitionCount[i]
		}
		if nodeToPartitionCount[i] > max {
			max = nodeToPartitionCount[i]
		}
		if nodeToPartitionCount[i] < min {
			min = nodeToPartitionCount[i]
		}
	}

	if max-min > 1 {
		log.Infof("the difference in number of partitions assigned per storage node is greater than 1, min=%d, max=%d", min, max)
		return false, nil
	}

	log.Infof("storage partitions are balanced min=%d, max=%d", min, max)
	return true, nil

}

// // RebalanceStoragePartitions will assign storage partitions evenly across registered storage nodes. If replication is not set, we set it to 1.
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
	return nil
}

// // IsIngestPartitionsBalanced ensures four things. First, if all ingest partitions are consumed by the expected (target replication factor) number of digest nodes. Second, all digest nodes must have ingest partitions assigned. Third, all nodes that are not digest nodes does not have any ingest partitions assigned. Forth, the difference in number of partitiones assigned per digest node must be at most 1.
// func (c *ClusterController) IsIngestPartitionsBalanced(hc *clusterv1alpha1.HumioCluster) (bool, error) {
// 	cluster, err := c.client.GetClusters()
// 	if err != nil {
// 		return false, err
// 	}

// 	// get a map that can tell us how many partitions a node has
// 	nodeToPartitionCount := make(map[int]int)
// 	for _, nodeID := range cluster.Nodes {
// 		nodeToPartitionCount[nodeID.Id] = 0
// 	}

// 	for _, partition := range cluster.IngestPartitions {
// 		if len(partition.NodeIds) != hc.Spec.TargetReplicationFactor {
// 			// our target replication factor is not met
// 			return false, nil
// 		}
// 		for _, node := range partition.NodeIds {
// 			nodeToPartitionCount[node]++
// 		}
// 	}

// 	var min, max int
// 	for _, hnp := range hc.Spec.NodePools {
// 		poolShouldDigest := IsDigestNode(hnp)
// 		for i := 0; i < hnp.NodeCount; i++ {
// 			if !poolShouldDigest && nodeToPartitionCount[hnp.FirstNodeID+i] != 0 {
// 				// node should not be digesting, but has ingest partitions assigned
// 				return false, nil
// 			}
// 			if poolShouldDigest {
// 				if nodeToPartitionCount[hnp.FirstNodeID+i] == 0 {
// 					// a node has no partitions
// 					return false, nil
// 				}
// 				if min == 0 {
// 					min = nodeToPartitionCount[hnp.FirstNodeID+i]
// 				}
// 				if max == 0 {
// 					max = nodeToPartitionCount[hnp.FirstNodeID+i]
// 				}
// 				if nodeToPartitionCount[hnp.FirstNodeID+i] > max {
// 					max = nodeToPartitionCount[hnp.FirstNodeID+i]
// 				}
// 				if nodeToPartitionCount[hnp.FirstNodeID+i] < min {
// 					min = nodeToPartitionCount[hnp.FirstNodeID+i]
// 				}
// 			}
// 		}
// 	}

// 	if max-min <= 1 {
// 		return true, nil
// 	}

// 	return false, nil
// }

// // RebalanceIngestPartitions will assign ingest partitions evenly across registered digest nodes. If replication is not set, we set it to 1.
// func (c *ClusterController) RebalanceIngestPartitions(hc *clusterv1alpha1.HumioCluster) error {
// 	log.Info("rebalancing ingest partitions")

// 	replication := hc.Spec.TargetReplicationFactor
// 	if hc.Spec.TargetReplicationFactor == 0 {
// 		replication = 1
// 	}

// 	var digestNodeIDs []int
// 	for _, hnp := range hc.Spec.NodePools {
// 		for _, t := range hnp.Types {
// 			// we assign ingest partitions to digest nodes
// 			if clusterv1alpha1.HumioNodeType(t) == clusterv1alpha1.Digest {
// 				for i := 0; i < hnp.NodeCount; i++ {
// 					digestNodeIDs = append(digestNodeIDs, hnp.FirstNodeID+i)
// 				}
// 			}
// 		}
// 	}
// 	partitionAssignment, err := generateIngestPartitionSchemeCandidate(hc, digestNodeIDs, 24, replication)
// 	if err != nil {
// 		return fmt.Errorf("could not generate ingest partition scheme candidate: %v", err)
// 	}

// 	if err := c.client.UpdateIngestPartitionScheme(partitionAssignment); err != nil {
// 		return fmt.Errorf("could not update ingest partition scheme: %v", err)
// 	}
// 	return nil
// }

// // IsStorageNode returns true if node pool lists storage as its type
// func IsStorageNode(hnp clusterv1alpha1.HumioNodePool) bool {
// 	for _, t := range hnp.Types {
// 		if clusterv1alpha1.HumioNodeType(t) == clusterv1alpha1.Storage {
// 			return true
// 		}
// 	}
// 	return false
// }

// // IsDigestNode returns true if node pool lists digest as its type
// func IsDigestNode(hnp clusterv1alpha1.HumioNodePool) bool {
// 	for _, t := range hnp.Types {
// 		if clusterv1alpha1.HumioNodeType(t) == clusterv1alpha1.Digest {
// 			return true
// 		}
// 	}
// 	return false
// }

// // IsIngestNode returns true if node pool lists ingest as its type
// func IsIngestNode(hnp clusterv1alpha1.HumioNodePool) bool {
// 	for _, t := range hnp.Types {
// 		if clusterv1alpha1.HumioNodeType(t) == clusterv1alpha1.Ingest {
// 			return true
// 		}
// 	}
// 	return false
// }

// // StartDataRedistribution notifies the Humio cluster that it should start redistributing data to match current assignments
// func StartDataRedistribution(hc *clusterv1alpha1.HumioCluster) error {
// 	log.Info("starting data redistribution")

// 	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
// 	if err != nil {
// 		log.Info(fmt.Sprintf("could not create humio client: %v", err))
// 	}

// 	if err := humioAPI.Clusters().StartDataRedistribution(); err != nil {
// 		return fmt.Errorf("could not start data redistribution: %v", err)
// 	}
// 	return nil
// }

// // MoveStorageRouteAwayFromNode notifies the Humio cluster that a node ID should be removed from handling any storage partitions
// func MoveStorageRouteAwayFromNode(hc *clusterv1alpha1.HumioCluster, pID int) error {
// 	log.Info(fmt.Sprintf("moving storage route away from node %d", pID))

// 	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
// 	if err != nil {
// 		log.Info(fmt.Sprintf("could not create humio client: %v", err))
// 	}

// 	if err := humioAPI.Clusters().ClusterMoveStorageRouteAwayFromNode(pID); err != nil {
// 		return fmt.Errorf("could not move storage route away from node: %v", err)
// 	}
// 	return nil
// }

// // MoveIngestRoutesAwayFromNode notifies the Humio cluster that a node ID should be removed from handling any ingest partitions
// func MoveIngestRoutesAwayFromNode(hc *clusterv1alpha1.HumioCluster, pID int) error {
// 	log.Info(fmt.Sprintf("moving ingest routes away from node %d", pID))

// 	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
// 	if err != nil {
// 		log.Info(fmt.Sprintf("could not create humio client: %v", err))
// 	}

// 	if err := humioAPI.Clusters().ClusterMoveIngestRoutesAwayFromNode(pID); err != nil {
// 		return fmt.Errorf("could not move ingest routes away from node: %v", err)
// 	}
// 	return nil
// }

// // ClusterUnregisterNode tells the Humio cluster that we want to unregister a node
// func ClusterUnregisterNode(hc *clusterv1alpha1.HumioCluster, pID int) error {
// 	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
// 	if err != nil {
// 		return fmt.Errorf("could not create humio client: %v", err)
// 	}
// 	err = humioAPI.ClusterNodes().Unregister(int64(pID), false)
// 	if err != nil {
// 		return fmt.Errorf("could not unregister node: %v", err)
// 	}
// 	return nil
// }

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

// func generateIngestPartitionSchemeCandidate(hc *clusterv1alpha1.HumioCluster, ingestNodeIDs []int, partitionCount, targetReplication int) ([]humioapi.IngestPartitionInput, error) {
// 	replicas := targetReplication
// 	if targetReplication > len(ingestNodeIDs) {
// 		replicas = len(ingestNodeIDs)
// 	}
// 	if replicas == 0 {
// 		return nil, fmt.Errorf("not possible to use replication factor 0")
// 	}

// 	var ps []humioapi.IngestPartitionInput

// 	for p := 0; p < partitionCount; p++ {
// 		var nodeIds []graphql.Int
// 		for r := 0; r < replicas; r++ {
// 			idx := (p + r) % len(ingestNodeIDs)
// 			nodeIds = append(nodeIds, graphql.Int(ingestNodeIDs[idx]))
// 		}
// 		ps = append(ps, humioapi.IngestPartitionInput{ID: graphql.Int(p), NodeIDs: nodeIds})
// 	}

// 	return ps, nil
// }

// func isTokenExpired(tokenString string) bool {
// 	var p jwt.Parser

// 	t, _, err := p.ParseUnverified(tokenString, &jwt.StandardClaims{})
// 	if err != nil {
// 		return true
// 	}

// 	if err := t.Claims.Valid(); err == nil {
// 		// valid token
// 		return false
// 	}
// 	return true
// }

// // GetJWTForSingleUser performs a login to humio with the given credentials and returns a valid JWT token
// func GetJWTForSingleUser(hc *clusterv1alpha1.HumioCluster) (string, error) {
// 	if hc.Status.JWTToken != "" {
// 		existing := oauth2.Token{AccessToken: hc.Status.JWTToken}
// 		if existing.Valid() && !isTokenExpired(hc.Status.JWTToken) {
// 			return hc.Status.JWTToken, nil
// 		}
// 	}

// 	message := map[string]interface{}{
// 		"login":    "developer",
// 		"password": hc.Spec.SingleUserPassword,
// 	}

// 	bytesRepresentation, err := json.Marshal(message)
// 	if err != nil {
// 		return "", err
// 	}
// 	resp, err := http.Post(hc.Status.BaseURL+"api/v1/login", "application/json", bytes.NewBuffer(bytesRepresentation))
// 	if err != nil {
// 		return "", fmt.Errorf("could not perform login to obtain jwt token: %v", err)
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != 200 {
// 		return "", fmt.Errorf("wrong status code when fetching token, expected 200, but got: %v", resp.Status)
// 	}

// 	var result map[string]string
// 	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
// 		return "", fmt.Errorf("unable to decode response body: %v", err)
// 	}

// 	return result["token"], nil
// }
