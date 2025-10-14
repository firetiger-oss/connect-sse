package connectsse_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"connectrpc.com/connect"
	connectsse "github.com/firetiger-oss/connect-sse"
	greetv1 "github.com/firetiger-oss/connect-sse/proto/go/example/greet/v1"
	"github.com/firetiger-oss/connect-sse/proto/go/example/greet/v1/greetv1connect"
)

func TestCompressionRejected(t *testing.T) {
	tests := []struct {
		name          string
		handlerOpts   []connect.HandlerOption
		wantErrString string
	}{
		{
			name:          "gzip compression enabled",
			handlerOpts:   []connect.HandlerOption{}, // Default includes gzip
			wantErrString: "compression not supported",
		},
		{
			name:          "explicitly send compressed",
			handlerOpts:   []connect.HandlerOption{connect.WithCompressMinBytes(1)},
			wantErrString: "compression not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a Connect RPC handler with compression enabled
			mux := http.NewServeMux()
			path, handler := greetv1connect.NewGreetServiceHandler(
				&greetServer{},
				tt.handlerOpts...,
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

			// Make streaming call - should fail due to compression
			ctx := context.Background()
			stream, err := client.GreetStream(ctx, connect.NewRequest(&greetv1.GreetRequest{
				Name: "Test",
			}))
			if err != nil {
				// Error on initial call is fine
				if !strings.Contains(err.Error(), tt.wantErrString) {
					t.Fatalf("expected error containing %q, got: %v", tt.wantErrString, err)
				}
				return
			}

			// Try to receive - should fail
			gotMessage := false
			for stream.Receive() {
				gotMessage = true
			}

			err = stream.Err()
			if err == nil {
				t.Fatal("expected error due to compression, got nil")
			}

			if !strings.Contains(err.Error(), tt.wantErrString) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErrString, err)
			}

			if gotMessage {
				t.Error("should not have received any messages with compression enabled")
			}
		})
	}
}

func TestCompressionDisabled(t *testing.T) {
	// Create a Connect RPC handler with compression explicitly disabled
	mux := http.NewServeMux()
	path, handler := greetv1connect.NewGreetServiceHandler(
		&greetServer{},
		connect.WithCompression("gzip", nil, nil), // Disable gzip
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

	// Make streaming call - should succeed
	ctx := context.Background()
	stream, err := client.GreetStream(ctx, connect.NewRequest(&greetv1.GreetRequest{
		Name: "Test",
	}))
	if err != nil {
		t.Fatalf("client.GreetStream: %v", err)
	}

	// Receive all messages
	count := 0
	for stream.Receive() {
		count++
	}

	if err := stream.Err(); err != nil {
		t.Fatalf("stream.Err: %v", err)
	}

	if count != 3 {
		t.Errorf("expected 3 messages, got %d", count)
	}
}
