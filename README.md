# connect-sse

Server-Sent Events (SSE) transport layer for
[Connect RPC](https://connectrpc.com).

## Motivation

Modern AI agent workloads have increasingly adopted Server-Sent Events
(SSE) as their preferred transport protocol for streaming responses. SSE
provides a simple, HTTP-based streaming mechanism that works seamlessly
across various network infrastructures, including corporate proxies and
CDNs that may have difficulty with WebSockets or gRPC.

While [Connect RPC](https://connectrpc.com) offers an excellent framework
for building type-safe APIs with protocol buffers, there are scenarios
where you need to bridge between Connect RPC's streaming protocol and SSE:

- **AI Gateway Integration**: When building gateways that need to expose
  Connect RPC services through SSE for compatibility with AI agent
  frameworks
- **Legacy System Support**: When integrating with existing systems that
  expect SSE streams
- **Network Constraints**: When operating in environments where only
  HTTP/1.1 is available or where SSE has better support than other
  streaming protocols

The `connect-sse` package provides this bridge by implementing translation
at the `net/http` layer, allowing Connect RPC services to communicate
using SSE as the wire format while maintaining full type safety and the
ergonomics of Connect RPC.

## Installation

```bash
go get github.com/firetiger-oss/connect-sse
```

## Usage

### Server Example

Expose a Connect RPC handler through an SSE-compatible endpoint:

```go
package main

import (
    "context"
    "log"
    "net/http"

    "connectrpc.com/connect"
    "github.com/firetiger-oss/connect-sse"

    "example.com/gen/greet/v1/greetv1connect"
)

type GreetServer struct{}

func (s *GreetServer) Greet(
    ctx context.Context,
    req *connect.Request[greetv1.GreetRequest],
) (*connect.Response[greetv1.GreetResponse], error) {
    return connect.NewResponse(&greetv1.GreetResponse{
        Greeting: "Hello, " + req.Msg.Name,
    }), nil
}

func (s *GreetServer) GreetStream(
    ctx context.Context,
    req *connect.Request[greetv1.GreetRequest],
    stream *connect.ServerStream[greetv1.GreetResponse],
) error {
    for i := range 5 {
        if err := stream.Send(&greetv1.GreetResponse{
            Greeting: fmt.Sprintf("Hello %s, message %d", req.Msg.Name, i),
        }); err != nil {
            return err
        }
    }
    return nil
}

func main() {
    mux := http.NewServeMux()
    path, handler := greetv1connect.NewGreetServiceHandler(&GreetServer{})
    mux.Handle(path, handler)

    http.Handle("/sse", &connectsse.Server{
        Handler: mux,
    })

    http.ListenAndServe(":8080", nil)
}
```

### Client Example

Connect to an SSE-based Connect RPC service:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "net/http"
    "net/url"

    "connectrpc.com/connect"
    "github.com/firetiger-oss/connect-sse"

    "example.com/gen/greet/v1"
    "example.com/gen/greet/v1/greetv1connect"
)

func main() {
    sseURL, _ := url.Parse("http://localhost:8080/sse")
    transport := &connectsse.Client{
        URL:       sseURL,
        Transport: http.DefaultTransport,
    }

    client := greetv1connect.NewGreetServiceClient(
        &http.Client{Transport: transport},
        "http://localhost:8080",
    )

    ctx := context.Background()
    resp, err := client.Greet(ctx, connect.NewRequest(&greetv1.GreetRequest{
        Name: "World",
    }))
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(resp.Msg.Greeting)

    stream, err := client.GreetStream(ctx, connect.NewRequest(&greetv1.GreetRequest{
        Name: "Stream",
    }))
    if err != nil {
        log.Fatal(err)
    }

    for stream.Receive() {
        fmt.Println(stream.Msg().Greeting)
    }
    if err := stream.Err(); err != nil {
        log.Fatal(err)
    }
}
```

### How It Works

#### Request Flow

The `connect-sse` package translates between Connect RPC's protocol and a
nested HTTP+JSON/SSE format:

**Client → Server**:
1. Connect RPC request is wrapped in a JSON envelope containing method,
   URI, headers, and body
2. Sent as an HTTP POST to the SSE endpoint
3. Server unwraps the envelope and forwards to the Connect RPC handler

**Server → Client**:
- **Unary responses**: Returned as JSON directly
- **Streaming responses**: Connect RPC's enveloped messages are converted
  to SSE events, then converted back to envelopes by the client

#### Protocol Translation

The package operates at the `net/http` layer:

- **Client** implements `http.RoundTripper` to intercept and translate
  requests/responses
- **Server** implements `http.Handler` to unwrap incoming requests and
  wrap outgoing responses

This design allows the package to work transparently with existing Connect
RPC code without modifications to your service implementation.

## License

Apache 2.0
