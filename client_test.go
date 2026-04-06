package connectsse

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClientDo(t *testing.T) {
	tests := []struct {
		name                 string
		clientURL            string
		incomingMethod       string
		incomingURL          string
		incomingHeader       http.Header
		incomingBody         string
		wantServerMethod     string
		wantServerPath       string
		wantNestedProcedure  string
		wantNestedHeader     http.Header
		wantNestedMessage    string
		wantOuterHeader      http.Header
		serverResponseStatus int
		serverResponseHeader http.Header
		serverResponseBody   string
		wantStatus           int
		wantBody             string
		wantErr              bool
	}{
		{
			name:                 "basic json request and response",
			clientURL:            "http://gateway.example.com/rpc",
			incomingMethod:       http.MethodPost,
			incomingURL:          "http://service.example.com/my.service/Method",
			incomingHeader:       http.Header{"Content-Type": []string{"application/json"}, "User-Agent": []string{"test-client"}},
			incomingBody:         `{"name":"test"}`,
			wantServerMethod:     http.MethodPost,
			wantServerPath:       "/rpc",
			wantNestedProcedure:  "/my.service/Method",
			wantNestedMessage:    `{"name":"test"}`,
			wantOuterHeader:      http.Header{"User-Agent": []string{"test-client"}},
			serverResponseStatus: http.StatusOK,
			serverResponseHeader: http.Header{"Content-Type": []string{"application/json"}},
			serverResponseBody:   `{"result":"ok"}`,
			wantStatus:           http.StatusOK,
			wantBody:             `{"result":"ok"}`,
		},
		{
			name:                 "url merging with empty client url parts",
			clientURL:            "/rpc",
			incomingMethod:       http.MethodPost,
			incomingURL:          "https://service.example.com/my.service/Method",
			incomingHeader:       http.Header{"Content-Type": []string{"application/json"}},
			incomingBody:         `{}`,
			wantServerPath:       "/rpc",
			serverResponseStatus: http.StatusOK,
			serverResponseHeader: http.Header{"Content-Type": []string{"application/json"}},
			serverResponseBody:   `{}`,
			wantStatus:           http.StatusOK,
			wantBody:             `{}`,
		},
		{
			name:                 "non-json content type",
			clientURL:            "http://gateway.example.com/rpc",
			incomingMethod:       http.MethodPost,
			incomingURL:          "http://service.example.com/my.service/Method",
			incomingHeader:       http.Header{"Content-Type": []string{"application/connect+proto"}},
			incomingBody:         "binary-data",
			wantNestedMessage:    "",
			serverResponseStatus: http.StatusOK,
			serverResponseHeader: http.Header{"Content-Type": []string{"application/json"}},
			serverResponseBody:   `{}`,
			wantStatus:           http.StatusOK,
			wantBody:             `{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.wantServerMethod != "" && r.Method != tt.wantServerMethod {
					t.Errorf("server method: want %s, got %s", tt.wantServerMethod, r.Method)
				}
				if tt.wantServerPath != "" && r.URL.Path != tt.wantServerPath {
					t.Errorf("server path: want %s, got %s", tt.wantServerPath, r.URL.Path)
				}

				if tt.wantNestedProcedure != "" || len(tt.wantNestedHeader) > 0 {
					var nestedReq Request
					if err := json.NewDecoder(r.Body).Decode(&nestedReq); err != nil {
						t.Fatalf("decode nested request: %v", err)
					}

					if tt.wantNestedProcedure != "" && nestedReq.Procedure != tt.wantNestedProcedure {
						t.Errorf("nested procedure: want %s, got %s", tt.wantNestedProcedure, nestedReq.Procedure)
					}
					for k, want := range tt.wantNestedHeader {
						if got := nestedReq.Header.Get(k); got != want[0] {
							t.Errorf("nested header %s: want %s, got %s", k, want[0], got)
						}
					}
					if tt.wantNestedMessage != "" && string(nestedReq.Message) != tt.wantNestedMessage {
						t.Errorf("nested message: want %s, got %s", tt.wantNestedMessage, string(nestedReq.Message))
					}
					if tt.wantNestedMessage == "" && len(nestedReq.Message) != 0 {
						t.Errorf("nested message: want empty, got %s", string(nestedReq.Message))
					}
				}

				for k, want := range tt.wantOuterHeader {
					if got := r.Header.Get(k); got != want[0] {
						t.Errorf("outer header %s: want %s, got %s", k, want[0], got)
					}
				}

				for k, v := range tt.serverResponseHeader {
					w.Header()[k] = v
				}
				w.WriteHeader(tt.serverResponseStatus)
				w.Write([]byte(tt.serverResponseBody))
			}))
			defer server.Close()

			clientURL, err := url.Parse(tt.clientURL)
			if err != nil {
				t.Fatalf("parse client URL: %v", err)
			}

			if !strings.HasPrefix(tt.clientURL, "http") {
				serverURL, _ := url.Parse(server.URL)
				clientURL.Scheme = serverURL.Scheme
				clientURL.Host = serverURL.Host
			} else {
				clientURL.Host = strings.TrimPrefix(server.URL, "http://")
			}

			client := &Client{
				URL:       clientURL,
				HTTPClient: http.DefaultClient,
			}

			reqURL, err := url.Parse(tt.incomingURL)
			if err != nil {
				t.Fatalf("parse incoming URL: %v", err)
			}

			var body io.Reader
			if tt.incomingBody != "" {
				body = strings.NewReader(tt.incomingBody)
			}

			req, err := http.NewRequest(tt.incomingMethod, reqURL.String(), body)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			req.Header = tt.incomingHeader

			resp, err := client.Do(req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Do() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status code: want %d, got %d", tt.wantStatus, resp.StatusCode)
			}

			respBody, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatalf("read response body: %v", err)
			}

			if string(respBody) != tt.wantBody {
				t.Errorf("response body: want %s, got %s", tt.wantBody, string(respBody))
			}
		})
	}
}

func TestClientNewTargetURL(t *testing.T) {
	tests := []struct {
		name      string
		clientURL string
		reqURL    string
		wantURL   string
	}{
		{
			name:      "full client URL",
			clientURL: "https://gateway.example.com/rpc",
			reqURL:    "http://service.example.com/my.service/Method",
			wantURL:   "https://gateway.example.com/rpc",
		},
		{
			name:      "client URL with only path",
			clientURL: "/rpc",
			reqURL:    "https://service.example.com/my.service/Method",
			wantURL:   "https://service.example.com/rpc",
		},
		{
			name:      "client URL with path and scheme",
			clientURL: "https:///rpc",
			reqURL:    "http://service.example.com/my.service/Method",
			wantURL:   "https://service.example.com/rpc",
		},
		{
			name:      "preserve user info",
			clientURL: "/rpc",
			reqURL:    "https://user:pass@service.example.com/my.service/Method",
			wantURL:   "https://user:pass@service.example.com/rpc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientURL, err := url.Parse(tt.clientURL)
			if err != nil {
				t.Fatalf("parse client URL: %v", err)
			}

			reqURL, err := url.Parse(tt.reqURL)
			if err != nil {
				t.Fatalf("parse request URL: %v", err)
			}

			client := &Client{URL: clientURL}
			gotURL := client.newTargetURL(reqURL)

			if gotURL.String() != tt.wantURL {
				t.Errorf("newTargetURL() = %s, want %s", gotURL.String(), tt.wantURL)
			}
		})
	}
}

func TestClientNewRequest(t *testing.T) {
	tests := []struct {
		name          string
		clientURL     string
		reqMethod     string
		reqURL        string
		reqHeader     http.Header
		reqBody       string
		wantNestedReq *Request
		wantOuterURL  string
		wantOuterHdrs http.Header
		wantErr       bool
	}{
		{
			name:      "json request with headers",
			clientURL: "https://gateway/rpc",
			reqMethod: http.MethodPost,
			reqURL:    "https://svc/my.svc/Method",
			reqHeader: http.Header{
				"Content-Type":  []string{"application/json"},
				"Authorization": []string{"Bearer token"},
				"X-Custom":      []string{"value"},
			},
			reqBody: `{"field":"value"}`,
			wantNestedReq: &Request{
				Procedure: "/my.svc/Method",
				Message:   json.RawMessage(`{"field":"value"}`),
			},
			wantOuterURL: "https://gateway/rpc",
			wantOuterHdrs: http.Header{
				"Content-Type":  []string{"application/json"},
				"Authorization": []string{"Bearer token"},
				"X-Custom":      []string{"value"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientURL, err := url.Parse(tt.clientURL)
			if err != nil {
				t.Fatalf("parse client URL: %v", err)
			}

			reqURL, err := url.Parse(tt.reqURL)
			if err != nil {
				t.Fatalf("parse request URL: %v", err)
			}

			var body io.Reader
			if tt.reqBody != "" {
				body = bytes.NewReader([]byte(tt.reqBody))
			}

			req, err := http.NewRequest(tt.reqMethod, reqURL.String(), body)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			req.Header = tt.reqHeader

			client := &Client{URL: clientURL}
			outerReq, err := client.newRequest(req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("newRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			var nestedReq Request
			bodyBytes, err := io.ReadAll(outerReq.Body)
			if err != nil {
				t.Fatalf("read outer request body: %v", err)
			}
			if err := json.Unmarshal(bodyBytes, &nestedReq); err != nil {
				t.Fatalf("unmarshal nested request: %v", err)
			}

			if nestedReq.Procedure != tt.wantNestedReq.Procedure {
				t.Errorf("nested procedure: want %s, got %s", tt.wantNestedReq.Procedure, nestedReq.Procedure)
			}
			if string(nestedReq.Message) != string(tt.wantNestedReq.Message) {
				t.Errorf("nested message: want %s, got %s", string(tt.wantNestedReq.Message), string(nestedReq.Message))
			}

			if outerReq.URL.String() != tt.wantOuterURL {
				t.Errorf("outer URL: want %s, got %s", tt.wantOuterURL, outerReq.URL.String())
			}

			for k, want := range tt.wantOuterHdrs {
				if k == "Content-Type" {
					continue
				}
				got := outerReq.Header.Get(k)
				if got != want[0] {
					t.Errorf("outer header %s: want %s, got %s", k, want[0], got)
				}
			}
		})
	}
}

func TestClientSSEResponse(t *testing.T) {
	tests := []struct {
		name            string
		sseResponse     string
		wantEnvelopes   []string
		wantContentType string
	}{
		{
			name:            "single sse event",
			sseResponse:     "data: {\"message\":\"hello\"}\n\n",
			wantEnvelopes:   []string{`{"message":"hello"}`},
			wantContentType: "application/connect+json",
		},
		{
			name:            "multiple sse events",
			sseResponse:     "data: {\"message\":\"first\"}\n\ndata: {\"message\":\"second\"}\n\n",
			wantEnvelopes:   []string{`{"message":"first"}`, `{"message":"second"}`},
			wantContentType: "application/connect+json",
		},
		{
			name: "multiline sse data",
			sseResponse: "data: {\"message\":\n" +
				"data: \"split across lines\"}\n\n",
			wantEnvelopes:   []string{"{\"message\":\n\"split across lines\"}"},
			wantContentType: "application/connect+json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(tt.sseResponse))
			}))
			defer server.Close()

			clientURL, _ := url.Parse(server.URL + "/rpc")
			client := &Client{
				URL:       clientURL,
				HTTPClient: http.DefaultClient,
			}

			reqURL, _ := url.Parse("http://service.example.com/my.service/Method")
			req, _ := http.NewRequest(http.MethodPost, reqURL.String(), nil)

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Do() error = %v", err)
			}
			defer resp.Body.Close()

			if ct := resp.Header.Get("Content-Type"); ct != tt.wantContentType {
				t.Errorf("Content-Type: want %s, got %s", tt.wantContentType, ct)
			}

			for i, wantData := range tt.wantEnvelopes {
				var envelope [5]byte
				if _, err := io.ReadFull(resp.Body, envelope[:]); err != nil {
					t.Fatalf("read envelope %d header: %v", i, err)
				}

				flags := envelope[0]
				length := binary.BigEndian.Uint32(envelope[1:5])

				data := make([]byte, length)
				if _, err := io.ReadFull(resp.Body, data); err != nil {
					t.Fatalf("read envelope %d data: %v", i, err)
				}

				if flags != 0 {
					t.Errorf("envelope %d flags: want 0, got %d", i, flags)
				}

				if string(data) != wantData {
					t.Errorf("envelope %d data: want %s, got %s", i, wantData, string(data))
				}
			}
		})
	}
}
