package connectsse

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"connectrpc.com/connect"
)

// Server implements http.Handler and translates HTTP+JSON requests with nested
// request structure to Connect RPC requests, and translates Connect RPC responses
// back to JSON or SSE format.
type Server struct {
	Handler http.Handler
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	errWriter := connect.NewErrorWriter()

	var nestedReq Request
	if err := json.NewDecoder(r.Body).Decode(&nestedReq); err != nil {
		errWriter.Write(w, r, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("decode nested request: %w", err)))
		return
	}

	if len(nestedReq.Message) == 0 {
		errWriter.Write(w, r, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("message is required")))
		return
	}

	contentType := nestedReq.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)

	if mediaType == "application/proto" {
		errWriter.Write(w, r, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("protobuf codec not supported: %s", mediaType)))
		return
	}

	if strings.HasPrefix(mediaType, "application/connect+") {
		errWriter.Write(w, r, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("client streaming not supported: %s", mediaType)))
		return
	}

	innerReq, err := s.reconstructRequest(r, &nestedReq)
	if err != nil {
		errWriter.Write(w, r, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("reconstruct request: %w", err)))
		return
	}

	s.Handler.ServeHTTP(&responseWriter{ResponseWriter: w}, innerReq)
}

func (s *Server) reconstructRequest(outerReq *http.Request, nestedReq *Request) (*http.Request, error) {
	reqURL, err := url.Parse(nestedReq.Procedure)
	if err != nil {
		return nil, fmt.Errorf("parse procedure: %w", err)
	}

	if reqURL.Scheme == "" {
		reqURL.Scheme = outerReq.URL.Scheme
	}
	if reqURL.Host == "" {
		reqURL.Host = outerReq.Host
	}

	innerReq, err := http.NewRequestWithContext(
		outerReq.Context(),
		http.MethodPost,
		reqURL.String(),
		io.NopCloser(bytes.NewReader(nestedReq.Message)),
	)
	if err != nil {
		return nil, fmt.Errorf("create inner request: %w", err)
	}

	maps.Copy(innerReq.Header, outerReq.Header)
	innerReq.Header.Del("Content-Type")
	innerReq.Header.Del("Content-Length")
	maps.Copy(innerReq.Header, nestedReq.Header)
	innerReq.Header.Set("Content-Type", "application/json")

	return innerReq, nil
}

type responseWriter struct {
	http.ResponseWriter
	headerWritten bool
	isStreaming   bool
	buffer        bytes.Buffer
}

func (rw *responseWriter) Header() http.Header {
	return rw.ResponseWriter.Header()
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	if rw.headerWritten {
		return
	}
	rw.headerWritten = true

	header := rw.ResponseWriter.Header()
	mediaType, _, _ := mime.ParseMediaType(header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "application/connect+") {
		rw.isStreaming = true
		header.Set("Content-Type", "text/event-stream")
		header.Set("Cache-Control", "no-cache")
		header.Set("Connection", "keep-alive")
	}

	rw.ResponseWriter.WriteHeader(statusCode)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.headerWritten {
		rw.WriteHeader(http.StatusOK)
	}

	if !rw.isStreaming {
		return rw.ResponseWriter.Write(b)
	}

	rw.buffer.Write(b)
	bytesWritten := len(b)

	for rw.buffer.Len() >= 5 {
		envBuf := rw.buffer.Bytes()

		flags := envBuf[0]
		length := binary.BigEndian.Uint32(envBuf[1:5])

		if rw.buffer.Len() < int(5+length) {
			break
		}

		rw.buffer.Next(5)
		data := rw.buffer.Next(int(length))

		if err := rw.writeSSEEvent(data, flags); err != nil {
			return bytesWritten, err
		}
	}

	return bytesWritten, nil
}

func (rw *responseWriter) writeSSEEvent(data []byte, flags byte) error {
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		if _, err := io.WriteString(rw.ResponseWriter, "data: "); err != nil {
			return err
		}
		if _, err := rw.ResponseWriter.Write(line); err != nil {
			return err
		}
		if _, err := io.WriteString(rw.ResponseWriter, "\n"); err != nil {
			return err
		}
	}

	if _, err := io.WriteString(rw.ResponseWriter, "\n"); err != nil {
		return err
	}

	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}

	return nil
}
