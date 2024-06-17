package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"sync"

	pb "github.com/mangelajo/grpc-experiments/proto"
	"golang.org/x/sync/errgroup"
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
	mutex *sync.RWMutex
	data  map[string]chan streamContext
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
		mutex: &sync.RWMutex{},
		data:  make(map[string]chan streamContext),
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
	mapping.mutex.Lock()
	defer mapping.mutex.Unlock()
	mapping.data[id] = make(chan streamContext, CHANNEL_SIZE)
	return &emptypb.Empty{}, nil
}

func (s *ForExporterServer) DataStream(stream pb.ForExporter_DataStreamServer) error {
	log.Println("new stream")
	id := stream.Context().Value("id").(string)
	mapping := stream.Context().Value("mapping").(*streamMapping)
	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, "stream", stream)
	mapping.mutex.RLock()
	mapping.data[id] <- streamContext{
		Context:    ctx,
		CancelFunc: cancel,
	}
	mapping.mutex.RUnlock()
	log.Println("new stream")
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
	mapping.mutex.RLock()
	sctx := <-mapping.data[id]
	mapping.mutex.RUnlock()
	estream := sctx.Context.Value("stream").(pb.ForExporter_DataStreamServer)

	log.Println("new stream connected")
	defer sctx.CancelFunc()
	return forward(stream.Context(), stream, estream)
}

type Stream[T any] interface {
	Send(T) error
	Recv() (T, error)
}

func pipe[T any, A Stream[T], B Stream[T]](a A, b B) error {
	for {
		chunk, err := a.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		err = b.Send(chunk)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func forward[T any, A Stream[T], B Stream[T]](ctx context.Context, a A, b B) error {
	g, _ := errgroup.WithContext(ctx)
	g.Go(func() error { return pipe(a, b) })
	g.Go(func() error { return pipe(b, a) })
	return g.Wait()
}
