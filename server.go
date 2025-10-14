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

	contentType := nestedReq.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)

	if mediaType == "application/proto" {
		errWriter.Write(w, r, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("protobuf codec not supported")))
		return
	}

	if strings.HasPrefix(mediaType, "application/connect+") {
		errWriter.Write(w, r, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("client streaming not supported")))
		return
	}

	innerReq, err := s.reconstructRequest(r, &nestedReq)
	if err != nil {
		errWriter.Write(w, r, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("reconstruct request: %w", err)))
		return
	}

	rw := &responseWriter{
		ResponseWriter: w,
		outerHeader:    w.Header(),
	}

	s.Handler.ServeHTTP(rw, innerReq)
}

func (s *Server) reconstructRequest(outerReq *http.Request, nestedReq *Request) (*http.Request, error) {
	reqURL, err := url.Parse(nestedReq.URI)
	if err != nil {
		return nil, fmt.Errorf("parse URI: %w", err)
	}

	if reqURL.Scheme == "" {
		reqURL.Scheme = outerReq.URL.Scheme
	}
	if reqURL.Host == "" {
		reqURL.Host = outerReq.Host
	}

	var body io.ReadCloser
	if len(nestedReq.Body) > 0 {
		body = io.NopCloser(bytes.NewReader(nestedReq.Body))
	}

	innerReq, err := http.NewRequestWithContext(
		outerReq.Context(),
		nestedReq.Method,
		reqURL.String(),
		body,
	)
	if err != nil {
		return nil, fmt.Errorf("create inner request: %w", err)
	}

	maps.Copy(innerReq.Header, outerReq.Header)
	innerReq.Header.Del("Content-Type")
	innerReq.Header.Del("Content-Length")
	maps.Copy(innerReq.Header, nestedReq.Header)

	return innerReq, nil
}

type responseWriter struct {
	http.ResponseWriter
	outerHeader    http.Header
	headerWritten  bool
	statusCode     int
	capturedHeader http.Header
	isStreaming    bool
	buf            bytes.Buffer
}

func (rw *responseWriter) Header() http.Header {
	if rw.capturedHeader == nil {
		rw.capturedHeader = make(http.Header)
	}
	return rw.capturedHeader
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	if rw.headerWritten {
		return
	}
	rw.headerWritten = true
	rw.statusCode = statusCode

	mediaType, _, _ := mime.ParseMediaType(rw.capturedHeader.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "application/connect+") {
		rw.isStreaming = true
		rw.outerHeader.Set("Content-Type", "text/event-stream")
		rw.outerHeader.Set("Cache-Control", "no-cache")
		rw.outerHeader.Set("Connection", "keep-alive")
	} else {
		maps.Copy(rw.outerHeader, rw.capturedHeader)
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

	rw.buf.Write(b)
	bytesWritten := len(b)

	for rw.buf.Len() >= 5 {
		envBuf := rw.buf.Bytes()

		flags := envBuf[0]
		length := binary.BigEndian.Uint32(envBuf[1:5])

		if rw.buf.Len() < int(5+length) {
			break
		}

		rw.buf.Next(5)
		data := rw.buf.Next(int(length))

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
