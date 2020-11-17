package membership

import "encoding/json"

type MemberMetadata struct {
	RPCAddress  string `json:"raft_address"`
	ID          uint64 `json:"id"`
	ClusterName string `json:"cluster_id"`
	Version     string `json:"version"`
}

func DecodeMD(buf []byte) (MemberMetadata, error) {
	md := MemberMetadata{}
	return md, json.Unmarshal(buf, &md)
}
func EncodeMD(id uint64, clusterName string, rpcAddress string, version string) []byte {
	md := MemberMetadata{
		ID:          id,
		RPCAddress:  rpcAddress,
		ClusterName: clusterName,
		Version:     version,
	}
	p, _ := json.Marshal(md)
	return p
}
