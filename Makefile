setup:
	poetry install

grpc_experiments/grpc/jumpstarter_pb2_grpc.py: proto/jumpstarter.proto
	poetry run python -m grpc_tools.protoc -I ./proto --pyi_out=grpc_experiments/jgrpc \
	                               --grpc_python_out=grpc_experiments/jgrpc \
								   proto/jumpstarter.proto

proto/jumpstarter.pb.go proto/jumpstarter_grpc.pb.go: proto/jumpstarter.proto
	protoc --go_out=. --go_opt=paths=source_relative \
			--go-grpc_out=. --go-grpc_opt=paths=source_relative \
			proto/jumpstarter.proto

grpc-python: grpc_experiments/grpc/jumpstarter_pb2_grpc.py
grpc-go: proto/jumpstarter.pb.go proto/jumpstarter_grpc.pb.go

.PHONY: grpc-python install
