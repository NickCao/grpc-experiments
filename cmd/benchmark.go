package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"

	pb "github.com/mangelajo/grpc-experiments/proto"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const BUF_SIZE int = 8192

type Stream interface {
	Send(*pb.DataChunk) error
	Recv() (*pb.DataChunk, error)
}

func forward[T Stream](stream T, conn net.Conn) error {
	g, _ := errgroup.WithContext(context.TODO())

	g.Go(func() error {
		buffer := make([]byte, BUF_SIZE)
		for seq := uint64(0); ; seq++ {
			n, err := conn.Read(buffer)
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			err = stream.Send(&pb.DataChunk{
				Seq:  seq,
				Data: buffer[:n],
			})
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	})

	g.Go(func() error {
		for {
			chunk, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			_, err = conn.Write(chunk.Data)
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	})

	return g.Wait()
}

type ForClientServer struct {
	pb.UnimplementedForClientServer
}

func (s *ForClientServer) DataStream(stream pb.ForClient_DataStreamServer) error {
	conn, err := net.Dial("tcp", "127.0.0.1:5201") // iperf3
	if err != nil {
		return err
	}

	return forward(stream, conn)
}

func main() {

	g, _ := errgroup.WithContext(context.TODO())

	g.Go(func() error {
		listen, err := net.Listen("tcp", "127.0.0.1:8000")
		if err != nil {
			return err
		}

		s := grpc.NewServer()

		pb.RegisterForClientServer(s, &ForClientServer{})

		return s.Serve(listen)
	})

	g.Go(func() error {
		listen, err := net.Listen("tcp", "127.0.0.1:8001")
		if err != nil {
			return err
		}

		c, err := grpc.NewClient("127.0.0.1:8000", grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return err
		}

		client := pb.NewForClientClient(c)

		for {
			conn, err := listen.Accept()
			if err != nil {
				return err
			}

			stream, err := client.DataStream(context.Background())
			if err != nil {
				return err
			}

			go forward(stream, conn) // TODO: check err
		}
	})

	err := g.Wait()
	if err != nil {
		log.Fatal(err)
	}
}
