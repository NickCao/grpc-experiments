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

// https://grpc.io/docs/guides/auth/#credential-types
// Call credentials, which are attached to a call (or ClientContext in C++).
type Credential struct{}

func (c *Credential) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer dummy-auth-token",
		"id":            "dummy-device-id",
	}, nil
}

func (c *Credential) RequireTransportSecurity() bool {
	return false
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

func main() {

	g, _ := errgroup.WithContext(context.TODO())

	g.Go(func() error {
		c, err := grpc.NewClient("127.0.0.1:8000",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithPerRPCCredentials(&Credential{}))
		if err != nil {
			return err
		}

		client := pb.NewForExporterClient(c)

		client.Register(context.Background(), nil)
		defer client.Bye(context.Background(), nil)

		for {
			log.Println("new stream prepared")
			stream, err := client.DataStream(context.Background())
			if err != nil {
				return err
			}

			conn, err := net.Dial("tcp", "127.0.0.1:5201") // iperf3
			if err != nil {
				return err
			}

			go func() {
				forward(stream, conn) // TODO: check err
				stream.CloseSend()
			}()
		}
	})

	g.Go(func() error {
		listen, err := net.Listen("tcp", "127.0.0.1:8001")
		if err != nil {
			return err
		}

		c, err := grpc.NewClient("127.0.0.1:8000",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithPerRPCCredentials(&Credential{}))
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

			go func() {
				forward(stream, conn) // TODO: check err
				stream.CloseSend()
			}()
		}
	})

	err := g.Wait()
	if err != nil {
		log.Fatal(err)
	}
}
