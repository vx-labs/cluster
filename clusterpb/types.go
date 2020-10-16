package clusterpb

//go:generate protoc -I ${GOPATH}/src/github.com/vx-labs/cluster/vendor -I ${GOPATH}/src/github.com/vx-labs/cluster/vendor/github.com/gogo/protobuf/ -I ${GOPATH}/src/github.com/vx-labs/cluster/clusterpb/ cluster.proto --gofast_out=plugins=grpc:.
