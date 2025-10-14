package connectsse_test

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"

	"connectrpc.com/connect"
	connectsse "github.com/firetiger-oss/connect-sse"
	greetv1 "github.com/firetiger-oss/connect-sse/proto/go/example/greet/v1"
	"github.com/firetiger-oss/connect-sse/proto/go/example/greet/v1/greetv1connect"
)

// greetServer implements the GreetService.
type greetServer struct {
	greetv1connect.UnimplementedGreetServiceHandler
}

func (s *greetServer) Greet(
	ctx context.Context,
	req *connect.Request[greetv1.GreetRequest],
) (*connect.Response[greetv1.GreetResponse], error) {
	return connect.NewResponse(&greetv1.GreetResponse{
		Greeting: fmt.Sprintf("Hello, %s!", req.Msg.Name),
	}), nil
}

func (s *greetServer) GreetStream(
	ctx context.Context,
	req *connect.Request[greetv1.GreetRequest],
	stream *connect.ServerStream[greetv1.GreetResponse],
) error {
	for i := range 3 {
		if err := stream.Send(&greetv1.GreetResponse{
			Greeting: fmt.Sprintf("Hello, %s! (message %d)", req.Msg.Name, i+1),
		}); err != nil {
			return err
		}
	}
	return nil
}

func Example_unary() {
	// Create a Connect RPC handler
	mux := http.NewServeMux()
	path, handler := greetv1connect.NewGreetServiceHandler(
		&greetServer{},
		connect.WithCompression("gzip", nil, nil), // Disable gzip compression
	)
	mux.Handle(path, handler)

	// Wrap with SSE server
	server := httptest.NewServer(&connectsse.Server{
		Handler: mux,
	})
	defer server.Close()

	// Create SSE client
	serverURL, _ := url.Parse(server.URL)
	transport := &connectsse.Client{
		URL: &url.URL{Path: "/"},
	}

	// Create Connect RPC client
	client := greetv1connect.NewGreetServiceClient(
		&http.Client{Transport: transport},
		serverURL.String(),
		connect.WithProtoJSON(),
	)

	// Make unary call
	ctx := context.Background()
	resp, err := client.Greet(ctx, connect.NewRequest(&greetv1.GreetRequest{
		Name: "World",
	}))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(resp.Msg.Greeting)
	// Output: Hello, World!
}

func Example_streaming() {
	// Create a Connect RPC handler
	mux := http.NewServeMux()
	path, handler := greetv1connect.NewGreetServiceHandler(
		&greetServer{},
		connect.WithCompression("gzip", nil, nil), // Disable gzip compression
	)
	mux.Handle(path, handler)

	// Wrap with SSE server
	server := httptest.NewServer(&connectsse.Server{
		Handler: mux,
	})
	defer server.Close()

	// Create SSE client
	serverURL, _ := url.Parse(server.URL)
	transport := &connectsse.Client{
		URL: &url.URL{Path: "/"},
	}

	// Create Connect RPC client using Connect protocol with JSON
	client := greetv1connect.NewGreetServiceClient(
		&http.Client{Transport: transport},
		serverURL.String(),
		connect.WithProtoJSON(),
	)

	// Make streaming call
	ctx := context.Background()
	stream, err := client.GreetStream(ctx, connect.NewRequest(&greetv1.GreetRequest{
		Name: "Streaming",
	}))
	if err != nil {
		log.Fatal(err)
	}

	// Receive streamed messages
	for stream.Receive() {
		fmt.Println(stream.Msg().Greeting)
	}
	if err := stream.Err(); err != nil {
		log.Fatal(err)
	}

	// Output:
	// Hello, Streaming! (message 1)
	// Hello, Streaming! (message 2)
	// Hello, Streaming! (message 3)
}
