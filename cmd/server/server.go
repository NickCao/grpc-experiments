package main

import (
	"context"
	"log"
	"net"
	"sync"

	st "github.com/mangelajo/grpc-experiments/pkg/stream"
	pb "github.com/mangelajo/grpc-experiments/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const CHANNEL_SIZE int = 10

type ForExporterServer struct {
	pb.UnimplementedForExporterServer
}

type ForClientServer struct {
	pb.UnimplementedForClientServer
}

type streamContext struct {
	context.Context
	context.CancelFunc
}

type streamMapping struct {
	data *sync.Map
}

type streamWrapper struct {
	grpc.ServerStream
	id      string
	mapping *streamMapping
}

func (w streamWrapper) Context() context.Context {
	ctx := context.WithValue(w.ServerStream.Context(), "id", w.id)
	ctx = context.WithValue(ctx, "mapping", w.mapping)
	return ctx
}

func main() {
	listen, err := net.Listen("tcp", "127.0.0.1:8000")
	if err != nil {
		log.Fatal(err)
	}

	mapping := &streamMapping{
		data: &sync.Map{},
	}

	s := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context,
		req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "missing metadata")
		}
		if _, ok := md["authorization"]; !ok {
			return nil, status.Errorf(codes.Unauthenticated, "invalid token")
		}
		if id, ok := md["id"]; !ok || len(id) != 1 {
			return nil, status.Errorf(codes.Unauthenticated, "invalid device id")
		} else {
			ctx = context.WithValue(ctx, "id", id[0])
			ctx = context.WithValue(ctx, "mapping", mapping)
			return handler(ctx, req)
		}
	}), grpc.StreamInterceptor(func(srv any,
		ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			return status.Errorf(codes.InvalidArgument, "missing metadata")
		}
		if _, ok := md["authorization"]; !ok {
			return status.Errorf(codes.Unauthenticated, "invalid token")
		}
		if id, ok := md["id"]; !ok || len(id) != 1 {
			return status.Errorf(codes.Unauthenticated, "invalid device id")
		} else {
			return handler(srv, streamWrapper{
				ServerStream: ss,
				id:           id[0],
				mapping:      mapping,
			})
		}
	}))

	pb.RegisterForClientServer(s, &ForClientServer{})
	pb.RegisterForExporterServer(s, &ForExporterServer{})

	err = s.Serve(listen)
	if err != nil {
		log.Fatal(err)
	}
}

func (s *ForExporterServer) Register(ctx context.Context, report *pb.ExporterReport) (*emptypb.Empty, error) {
	id := ctx.Value("id").(string)
	mapping := ctx.Value("mapping").(*streamMapping)
	mapping.data.Store(id, make(chan streamContext, CHANNEL_SIZE))
	return &emptypb.Empty{}, nil
}

func (s *ForExporterServer) DataStream(stream pb.ForExporter_DataStreamServer) error {
	id := stream.Context().Value("id").(string)
	mapping := stream.Context().Value("mapping").(*streamMapping)

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, "stream", stream)

	ch, _ := mapping.data.Load(id)

	select {
	case ch.(chan streamContext) <- streamContext{
		Context:    ctx,
		CancelFunc: cancel,
	}:
		log.Println("new stream queued")
	case <-stream.Context().Done():
		return nil
	}
	select {
	case <-ctx.Done():
		log.Println("stream finished")
		return nil
	}
}

func (s *ForClientServer) DataStream(stream pb.ForClient_DataStreamServer) error {
	log.Println("new stream connecting")
	id := stream.Context().Value("id").(string)
	mapping := stream.Context().Value("mapping").(*streamMapping)
	ch, _ := mapping.data.Load(id)
	select {
	case sctx := <-ch.(chan streamContext):
		estream := sctx.Context.Value("stream").(pb.ForExporter_DataStreamServer)
		log.Println("new stream connected")
		defer sctx.CancelFunc()
		return st.Forward(stream.Context(), stream, estream)
	case <-stream.Context().Done():
		return nil
	}
}
