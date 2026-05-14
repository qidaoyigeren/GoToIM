package api

//go:generate protoc -I. --go_out=. --go_opt=paths=source_relative api/protocol/protocol.proto
//go:generate protoc -I. --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative api/comet/comet.proto
//go:generate protoc -I. --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative api/logic/logic.proto
