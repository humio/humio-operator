package humio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	jwt "github.com/dgrijalva/jwt-go"
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/prometheus/common/log"
	"github.com/shurcooL/graphql"
	"golang.org/x/oauth2"
)

// ListPods grabs the list of all pods associated to a an instance of HumioCluster
func ListPods(c client.Client, hc humiov1alpha1.HumioCluster) ([]corev1.Pod, error) {
	var foundPodList corev1.PodList
	matchingLabels := client.MatchingLabels{
		"humio_cr": hc.Name,
	}
	// client.MatchingField also exists?

	err := c.List(context.TODO(), &foundPodList, client.InNamespace(hc.Namespace), matchingLabels)
	if err != nil {
		return nil, err
	}

	return foundPodList.Items, nil
}

// GetHumioBaseURL the first base URL for the first Humio node it can reach
func GetHumioBaseURL(c client.Client, hc humiov1alpha1.HumioCluster) (string, error) {
	allPodsForCluster, err := ListPods(c, hc)
	if err != nil {
		return "", fmt.Errorf("could not list pods for cluster: %v", err)
	}
	for _, p := range allPodsForCluster {
		if p.DeletionTimestamp == nil {
			// only consider pods not being deleted

			if p.Status.PodIP == "" {
				// skip pods with no pod IP
				continue
			}

			// check if we can reach the humio endpoint
			humioBaseURL := "http://" + p.Status.PodIP + ":8080/"
			resp, err := http.Get(humioBaseURL)
			if err != nil {
				log.Info(fmt.Sprintf("Humio API is unavailable: %v", err))
				continue
			}
			defer resp.Body.Close()

			// if request was ok, return the base URL
			if resp.StatusCode == http.StatusOK {
				return humioBaseURL, nil
			}
		}
	}
	return "", fmt.Errorf("did not find a valid base URL")
}

func isTokenExpired(tokenString string) bool {
	var p jwt.Parser

	t, _, err := p.ParseUnverified(tokenString, &jwt.StandardClaims{})
	if err != nil {
		return true
	}

	if err := t.Claims.Valid(); err == nil {
		// valid token
		return false
	}
	return true
}

// GetJWTForSingleUser performs a login to humio with the given credentials and returns a valid JWT token
func GetJWTForSingleUser(hc humiov1alpha1.HumioCluster) (string, error) {
	if hc.Status.JWTToken != "" {
		existing := oauth2.Token{AccessToken: hc.Status.JWTToken}
		if existing.Valid() && !isTokenExpired(hc.Status.JWTToken) {
			return hc.Status.JWTToken, nil
		}
	}

	message := map[string]interface{}{
		"login":    "developer",
		"password": hc.Spec.SingleUserPassword,
	}

	bytesRepresentation, err := json.Marshal(message)
	if err != nil {
		return "", err
	}
	resp, err := http.Post(hc.Status.BaseURL+"api/v1/login", "application/json", bytes.NewBuffer(bytesRepresentation))
	if err != nil {
		return "", fmt.Errorf("could not perform login to obtain jwt token: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("wrong status code when fetching token, expected 200, but got: %v", resp.Status)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)

	return result["token"], nil
}

// AreAllRegisteredNodesAvailable only returns true if all nodes registered with humio are available
func AreAllRegisteredNodesAvailable(c client.Client, hc humiov1alpha1.HumioCluster) (bool, error) {
	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		log.Info(fmt.Sprintf("could not create humio client: %v", err))
	}
	cluster, err := humioAPI.Clusters().Get()

	if err != nil {
		log.Info(fmt.Sprintf("could not get cluster information: %v", err))
		return false, err
	}

	for _, n := range cluster.Nodes {
		if !n.IsAvailable {
			return false, nil
		}
	}
	return true, nil
}

// ContainsNodePoolName returns true if any of the current node pools has the given name
func ContainsNodePoolName(poolName string, hc humiov1alpha1.HumioCluster) bool {
	for _, pool := range hc.Spec.NodePools {
		if pool.Name == poolName {
			return true
		}
	}
	return false
}

// NoDataMissing only returns true if all data are available
func NoDataMissing(c client.Client, hc humiov1alpha1.HumioCluster) (bool, error) {
	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		log.Info(fmt.Sprintf("could not create humio client: %v", err))
	}
	cluster, err := humioAPI.Clusters().Get()

	if err != nil {
		log.Info(fmt.Sprintf("could not get cluster information: %v", err))
		return false, err
	}
	if cluster.MissingSegmentSize == 0 {
		return true, nil
	}
	return false, nil
}

// IsNodeRegistered returns whether the Humio cluster has a node with the given node id
func IsNodeRegistered(hc humiov1alpha1.HumioCluster, nodeID int) (bool, error) {
	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		log.Info(fmt.Sprintf("could not create humio client: %v", err))
	}
	cluster, err := humioAPI.Clusters().Get()
	if err != nil {
		return false, fmt.Errorf("could not get cluster information: %v", err)
	}
	for _, node := range cluster.Nodes {
		if int(node.Id) == nodeID {
			return true, nil
		}
	}
	return false, nil

}

// CountNodesRegistered returns how many registered nodes there are in the cluster
func CountNodesRegistered(hc humiov1alpha1.HumioCluster) (int, error) {
	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		log.Info(fmt.Sprintf("could not create humio client: %v", err))
	}
	cluster, err := humioAPI.Clusters().Get()

	if err != nil {
		log.Info(fmt.Sprintf("could not get cluster information: %v", err))
		return 0, err
	}
	return len(cluster.Nodes), nil
}

// CanBeSafelyUnregistered returns true if the Humio API indicates that the node can be safely unregistered. This should ensure that the node does not hold any data.
func CanBeSafelyUnregistered(hc humiov1alpha1.HumioCluster, podID int) (bool, error) {
	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		log.Info(fmt.Sprintf("could not create humio client: %v", err))
	}
	cluster, err := humioAPI.Clusters().Get()
	if err != nil {
		return false, fmt.Errorf("could not get cluster information: %v", err)
	}
	for _, node := range cluster.Nodes {
		if int(node.Id) == podID && node.CanBeSafelyUnregistered {
			return true, nil
		}
	}
	return false, nil
}

// IsStoragePartitionsBalanced ensures four things. First, if all storage partitions are consumed by the expected (target replication factor) number of storage nodes. Second, all storage nodes must have storage partitions assigned. Third, all nodes that are not storage nodes does not have any storage partitions assigned. Forth, the difference in number of partitiones assigned per storage node must be at most 1.
func IsStoragePartitionsBalanced(hc humiov1alpha1.HumioCluster) (bool, error) {
	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		return false, fmt.Errorf("could not create humio client: %v", err)
	}
	currentClusterState, err := humioAPI.Clusters().Get()
	if err != nil {
		return false, fmt.Errorf("could not get current humio cluster state: %v", err)
	}
	nodeToPartitionCount := make(map[int]int)
	for _, nodeID := range currentClusterState.Nodes {
		nodeToPartitionCount[nodeID.Id] = 0
	}

	for _, partition := range currentClusterState.StoragePartitions {
		if len(partition.NodeIds) != hc.Spec.TargetReplicationFactor {
			return false, nil
		}
		for _, node := range partition.NodeIds {
			nodeToPartitionCount[node]++
		}
	}

	var min, max int
	for _, hnp := range hc.Spec.NodePools {
		poolShouldStore := IsStorageNode(hnp)
		for i := 0; i < hnp.NodeCount; i++ {
			if !poolShouldStore && nodeToPartitionCount[hnp.FirstNodeID+i] != 0 {
				// node should not be digesting, but has ingest partitions assigned
				return false, nil
			}
			if poolShouldStore {
				if nodeToPartitionCount[hnp.FirstNodeID+i] == 0 {
					// a node has no partitions
					return false, nil
				}
				if min == 0 {
					min = nodeToPartitionCount[hnp.FirstNodeID+i]
				}
				if max == 0 {
					max = nodeToPartitionCount[hnp.FirstNodeID+i]
				}
				if nodeToPartitionCount[hnp.FirstNodeID+i] > max {
					max = nodeToPartitionCount[hnp.FirstNodeID+i]
				}
				if nodeToPartitionCount[hnp.FirstNodeID+i] < min {
					min = nodeToPartitionCount[hnp.FirstNodeID+i]
				}
			}
		}
	}

	if max-min <= 1 {
		return true, nil
	}

	return false, nil
}

// RebalanceStoragePartitions will assign storage partitions evenly across registered storage nodes. If replication is not set, we set it to 1.
func RebalanceStoragePartitions(hc humiov1alpha1.HumioCluster) error {
	log.Info("rebalancing storage partitions")

	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		log.Info(fmt.Sprintf("could not create humio client: %v", err))
	}
	replication := hc.Spec.TargetReplicationFactor
	if hc.Spec.TargetReplicationFactor == 0 {
		replication = 1
	}

	var storageNodeIDs []int
	for _, hnp := range hc.Spec.NodePools {
		for _, t := range hnp.Types {
			if humiov1alpha1.HumioNodeType(t) == humiov1alpha1.Storage {
				for i := 0; i < hnp.NodeCount; i++ {
					storageNodeIDs = append(storageNodeIDs, hnp.FirstNodeID+i)
				}
			}
		}
	}
	partitionAssignment, err := generateStoragePartitionSchemeCandidate(hc, storageNodeIDs, 24, replication)
	if err != nil {
		return fmt.Errorf("could not generate storage partition scheme candidate: %v", err)
	}

	if err := humioAPI.Clusters().UpdateStoragePartitionScheme(partitionAssignment); err != nil {
		return fmt.Errorf("could not update storage partition scheme: %v", err)
	}
	return nil
}

// IsIngestPartitionsBalanced ensures four things. First, if all ingest partitions are consumed by the expected (target replication factor) number of digest nodes. Second, all digest nodes must have ingest partitions assigned. Third, all nodes that are not digest nodes does not have any ingest partitions assigned. Forth, the difference in number of partitiones assigned per digest node must be at most 1.
func IsIngestPartitionsBalanced(hc humiov1alpha1.HumioCluster) (bool, error) {
	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		return false, fmt.Errorf("could not create humio client: %v", err)
	}
	currentClusterState, err := humioAPI.Clusters().Get()
	if err != nil {
		return false, fmt.Errorf("could not get current humio cluster state: %v", err)
	}

	// get a map that can tell us how many partitions a node has
	nodeToPartitionCount := make(map[int]int)
	for _, nodeID := range currentClusterState.Nodes {
		nodeToPartitionCount[nodeID.Id] = 0
	}

	for _, partition := range currentClusterState.IngestPartitions {
		if len(partition.NodeIds) != hc.Spec.TargetReplicationFactor {
			// our target replication factor is not met
			return false, nil
		}
		for _, node := range partition.NodeIds {
			nodeToPartitionCount[node]++
		}
	}

	var min, max int
	for _, hnp := range hc.Spec.NodePools {
		poolShouldDigest := IsDigestNode(hnp)
		for i := 0; i < hnp.NodeCount; i++ {
			if !poolShouldDigest && nodeToPartitionCount[hnp.FirstNodeID+i] != 0 {
				// node should not be digesting, but has ingest partitions assigned
				return false, nil
			}
			if poolShouldDigest {
				if nodeToPartitionCount[hnp.FirstNodeID+i] == 0 {
					// a node has no partitions
					return false, nil
				}
				if min == 0 {
					min = nodeToPartitionCount[hnp.FirstNodeID+i]
				}
				if max == 0 {
					max = nodeToPartitionCount[hnp.FirstNodeID+i]
				}
				if nodeToPartitionCount[hnp.FirstNodeID+i] > max {
					max = nodeToPartitionCount[hnp.FirstNodeID+i]
				}
				if nodeToPartitionCount[hnp.FirstNodeID+i] < min {
					min = nodeToPartitionCount[hnp.FirstNodeID+i]
				}
			}
		}
	}

	if max-min <= 1 {
		return true, nil
	}

	return false, nil
}

// RebalanceIngestPartitions will assign ingest partitions evenly across registered digest nodes. If replication is not set, we set it to 1.
func RebalanceIngestPartitions(hc humiov1alpha1.HumioCluster) error {
	log.Info("rebalancing ingest partitions")

	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		log.Info(fmt.Sprintf("could not create humio client: %v", err))
	}
	replication := hc.Spec.TargetReplicationFactor
	if hc.Spec.TargetReplicationFactor == 0 {
		replication = 1
	}

	var digestNodeIDs []int
	for _, hnp := range hc.Spec.NodePools {
		for _, t := range hnp.Types {
			// we assign ingest partitions to digest nodes
			if humiov1alpha1.HumioNodeType(t) == humiov1alpha1.Digest {
				for i := 0; i < hnp.NodeCount; i++ {
					digestNodeIDs = append(digestNodeIDs, hnp.FirstNodeID+i)
				}
			}
		}
	}
	partitionAssignment, err := generateIngestPartitionSchemeCandidate(hc, digestNodeIDs, 24, replication)
	if err != nil {
		return fmt.Errorf("could not generate ingest partition scheme candidate: %v", err)
	}

	if err := humioAPI.Clusters().UpdateIngestPartitionScheme(partitionAssignment); err != nil {
		return fmt.Errorf("could not update ingest partition scheme: %v", err)
	}
	return nil
}

// IsStorageNode returns true if node pool lists storage as its type
func IsStorageNode(hnp humiov1alpha1.HumioNodePool) bool {
	for _, t := range hnp.Types {
		if humiov1alpha1.HumioNodeType(t) == humiov1alpha1.Storage {
			return true
		}
	}
	return false
}

// IsDigestNode returns true if node pool lists digest as its type
func IsDigestNode(hnp humiov1alpha1.HumioNodePool) bool {
	for _, t := range hnp.Types {
		if humiov1alpha1.HumioNodeType(t) == humiov1alpha1.Digest {
			return true
		}
	}
	return false
}

// IsIngestNode returns true if node pool lists ingest as its type
func IsIngestNode(hnp humiov1alpha1.HumioNodePool) bool {
	for _, t := range hnp.Types {
		if humiov1alpha1.HumioNodeType(t) == humiov1alpha1.Ingest {
			return true
		}
	}
	return false
}

// StartDataRedistribution notifies the Humio cluster that it should start redistributing data to match current assignments
func StartDataRedistribution(hc humiov1alpha1.HumioCluster) error {
	log.Info("starting data redistribution")

	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		log.Info(fmt.Sprintf("could not create humio client: %v", err))
	}

	if err := humioAPI.Clusters().StartDataRedistribution(); err != nil {
		return fmt.Errorf("could not start data redistribution: %v", err)
	}
	return nil
}

// MoveStorageRouteAwayFromNode notifies the Humio cluster that a node ID should be removed from handling any storage partitions
func MoveStorageRouteAwayFromNode(hc humiov1alpha1.HumioCluster, pID int) error {
	log.Info(fmt.Sprintf("moving storage route away from node %d", pID))

	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		log.Info(fmt.Sprintf("could not create humio client: %v", err))
	}

	if err := humioAPI.Clusters().ClusterMoveStorageRouteAwayFromNode(pID); err != nil {
		return fmt.Errorf("could not move storage route away from node: %v", err)
	}
	return nil
}

// MoveIngestRoutesAwayFromNode notifies the Humio cluster that a node ID should be removed from handling any ingest partitions
func MoveIngestRoutesAwayFromNode(hc humiov1alpha1.HumioCluster, pID int) error {
	log.Info(fmt.Sprintf("moving ingest routes away from node %d", pID))

	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		log.Info(fmt.Sprintf("could not create humio client: %v", err))
	}

	if err := humioAPI.Clusters().ClusterMoveIngestRoutesAwayFromNode(pID); err != nil {
		return fmt.Errorf("could not move ingest routes away from node: %v", err)
	}
	return nil
}

// ClusterUnregisterNode tells the Humio cluster that we want to unregister a node
func ClusterUnregisterNode(hc humiov1alpha1.HumioCluster, pID int) error {
	humioAPI, err := humioapi.NewClient(humioapi.Config{Address: hc.Status.BaseURL, Token: hc.Status.JWTToken})
	if err != nil {
		return fmt.Errorf("could not create humio client: %v", err)
	}
	err = humioAPI.Nodes().Unregister(int64(pID), false)
	if err != nil {
		return fmt.Errorf("could not unregister node: %v", err)
	}
	return nil
}

func generateStoragePartitionSchemeCandidate(hc humiov1alpha1.HumioCluster, storageNodeIDs []int, partitionCount, targetReplication int) ([]humioapi.StoragePartitionInput, error) {
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

func generateIngestPartitionSchemeCandidate(hc humiov1alpha1.HumioCluster, ingestNodeIDs []int, partitionCount, targetReplication int) ([]humioapi.IngestPartitionInput, error) {
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
