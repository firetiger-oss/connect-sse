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

	"connectrpc.com/connect"
)

// Request represents a nested Connect RPC request that will be encoded in the
// body of the outer HTTP POST request sent by the Client.
type Request struct {
	Procedure string          `json:"procedure"`
	Header    http.Header     `json:"header,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
}

// Client implements connect.HTTPClient and translates
// Connect RPC requests to HTTP+JSON requests with nested request structure,
// and translates JSON or SSE responses back to the Connect RPC format.
type Client struct {
	URL       *url.URL
	Transport connect.HTTPClient
}

// Do implements connect.HTTPClient.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	httpClient := c.Transport
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	outerReq, err := c.newRequest(req)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := httpClient.Do(outerReq)
	if err != nil {
		return nil, err
	}

	return c.newResponse(resp, req), nil
}

func (c *Client) newRequest(req *http.Request) (*http.Request, error) {
	nestedReq := &Request{
		Procedure: req.URL.Path,
		Header:    make(http.Header),
	}

	contentType := req.Header.Get("Content-Type")
	if contentType != "" {
		nestedReq.Header.Set("Content-Type", contentType)
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)

	if req.Body != nil && req.Body != http.NoBody {
		defer req.Body.Close()

		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}

		// For Connect enveloped requests, unwrap the envelope to get the JSON payload
		// but keep the Content-Type as application/connect+json
		if mediaType == "application/connect+json" && len(bodyBytes) >= 5 {
			// Skip 5-byte envelope header (1 byte flags + 4 bytes big-endian length)
			length := binary.BigEndian.Uint32(bodyBytes[1:5])
			if len(bodyBytes) >= int(5+length) {
				bodyBytes = bodyBytes[5 : 5+length]
			}
		}

		if mediaType == "application/json" || mediaType == "application/connect+json" {
			nestedReq.Message = json.RawMessage(bodyBytes)
		}
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
		data, flags, err := r.readSSEEvent()
		if err == io.EOF {
			r.finished = true
			break
		}
		if err != nil {
			return 0, err
		}

		r.data = r.newEnvelopeReader(data, flags)
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

func (r *sseToEnvelopeReader) readSSEEvent() (data []string, flags byte, err error) {
	flags = 0

	for r.scanner.Scan() {
		line := r.scanner.Text()

		if line == "" {
			if len(data) > 0 {
				return data, flags, nil
			}
			continue
		}

		if strings.HasPrefix(line, ":") {
			// Parse flags from SSE comment
			if rest := strings.TrimPrefix(line, ":flags "); rest != line {
				if n, scanErr := fmt.Sscanf(rest, "%d", &flags); scanErr == nil && n == 1 {
					// Successfully parsed flags
				}
			}
			continue
		}

		field, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}

		value = strings.TrimPrefix(value, " ")

		if field == "data" {
			data = append(data, value)
		}
	}

	if err := r.scanner.Err(); err != nil {
		return nil, 0, err
	}

	if len(data) > 0 {
		return data, flags, nil
	}

	return nil, 0, io.EOF
}

func (r *sseToEnvelopeReader) newEnvelopeReader(dataLines []string, flags byte) io.Reader {
	// Check if compression flag is set (bit 0)
	if flags&1 != 0 {
		// Return an error reader that will fail on first read
		return &errorReader{err: fmt.Errorf("compression not supported: envelope has compression flag set")}
	}

	totalLen := 0
	for i, line := range dataLines {
		totalLen += len(line)
		if i < len(dataLines)-1 {
			totalLen++
		}
	}

	header := make([]byte, 5)
	header[0] = flags
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

// errorReader is a reader that always returns an error
type errorReader struct {
	err error
}

func (r *errorReader) Read(p []byte) (int, error) {
	return 0, r.err
}
