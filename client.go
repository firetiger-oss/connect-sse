package connectsse

import (
	"bufio"
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
)

// Request represents a nested HTTP request that will be encoded in the body
// of the outer HTTP POST request sent by the Client.
type Request struct {
	Method string          `json:"method"`
	URI    string          `json:"uri"`
	Header http.Header     `json:"header,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// Client implements http.RoundTripper and translates Connect RPC requests to
// HTTP+JSON requests with nested request structure, and translates JSON or SSE
// responses back to the Connect RPC format.
type Client struct {
	URL       *url.URL
	Transport http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (c *Client) RoundTrip(req *http.Request) (*http.Response, error) {
	transport := c.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	outerReq, err := c.newRequest(req)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := transport.RoundTrip(outerReq)
	if err != nil {
		return nil, err
	}

	return c.newResponse(resp, req), nil
}

func (c *Client) newRequest(req *http.Request) (*http.Request, error) {
	nestedReq := &Request{
		Method: req.Method,
		URI:    req.URL.RequestURI(),
	}

	contentType := req.Header.Get("Content-Type")
	if contentType != "" {
		nestedReq.Header = make(http.Header)
		nestedReq.Header.Set("Content-Type", contentType)
	}

	if contentType == "application/json" && req.Body != nil && req.Body != http.NoBody {
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		req.Body.Close()
		nestedReq.Body = json.RawMessage(bodyBytes)
	}

	nestedReqBody, err := json.Marshal(nestedReq)
	if err != nil {
		return nil, fmt.Errorf("marshal nested request: %w", err)
	}

	targetURL := c.newTargetURL(req.URL)
	outerReq, err := http.NewRequestWithContext(
		req.Context(),
		http.MethodPost,
		targetURL.String(),
		bytes.NewReader(nestedReqBody),
	)
	if err != nil {
		return nil, fmt.Errorf("create outer request: %w", err)
	}

	outerReq.Header = maps.Clone(req.Header)
	outerReq.Header.Set("Content-Type", "application/json")
	return outerReq, nil
}

func (c *Client) newTargetURL(reqURL *url.URL) *url.URL {
	targetURL := new(url.URL)
	*targetURL = *c.URL

	if targetURL.Scheme == "" {
		targetURL.Scheme = reqURL.Scheme
	}
	if targetURL.Host == "" {
		targetURL.Host = reqURL.Host
	}
	if targetURL.User == nil {
		targetURL.User = reqURL.User
	}

	return targetURL
}

func (c *Client) newResponse(resp *http.Response, originalReq *http.Request) *http.Response {
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))

	if mediaType == "application/json" || mediaType == "application/connect+json" {
		return resp
	}

	if mediaType == "text/event-stream" {
		resp.Body = &sseToEnvelopeReader{
			scanner: bufio.NewScanner(resp.Body),
			closer:  resp.Body,
		}
		resp.Header.Set("Content-Type", "application/connect+json")
		return resp
	}

	return resp
}

type sseToEnvelopeReader struct {
	scanner  *bufio.Scanner
	closer   io.Closer
	data     io.Reader
	finished bool
}

func (r *sseToEnvelopeReader) Read(p []byte) (int, error) {
	for r.data == nil && !r.finished {
		dataLines, err := r.readSSEEvent()
		if err == io.EOF {
			r.finished = true
			break
		}
		if err != nil {
			return 0, err
		}

		r.data = r.newEnvelopeReader(dataLines)
	}

	if r.data == nil {
		return 0, io.EOF
	}

	n, err := r.data.Read(p)
	if err == io.EOF {
		r.data = nil
		err = nil
	}
	return n, err
}

func (r *sseToEnvelopeReader) Close() error {
	return r.closer.Close()
}

func (r *sseToEnvelopeReader) readSSEEvent() ([]string, error) {
	var dataLines []string

	for r.scanner.Scan() {
		line := r.scanner.Text()

		if line == "" {
			if len(dataLines) > 0 {
				return dataLines, nil
			}
			continue
		}

		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}

		value = strings.TrimPrefix(value, " ")

		if field == "data" {
			dataLines = append(dataLines, value)
		}
	}

	if err := r.scanner.Err(); err != nil {
		return nil, err
	}

	if len(dataLines) > 0 {
		return dataLines, nil
	}

	return nil, io.EOF
}

func (r *sseToEnvelopeReader) newEnvelopeReader(dataLines []string) io.Reader {
	totalLen := 0
	for i, line := range dataLines {
		totalLen += len(line)
		if i < len(dataLines)-1 {
			totalLen++
		}
	}

	header := make([]byte, 5)
	header[0] = 0
	binary.BigEndian.PutUint32(header[1:5], uint32(totalLen))

	readers := make([]io.Reader, 0, 1+len(dataLines)*2)
	readers = append(readers, bytes.NewReader(header))

	for i, line := range dataLines {
		readers = append(readers, strings.NewReader(line))
		if i < len(dataLines)-1 {
			readers = append(readers, strings.NewReader("\n"))
		}
	}

	return io.MultiReader(readers...)
}
