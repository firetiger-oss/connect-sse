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

func TestServerServeHTTP(t *testing.T) {
	tests := []struct {
		name               string
		nestedReq          *Request
		handler            http.Handler
		wantInnerMethod    string
		wantInnerURI       string
		wantInnerHeader    http.Header
		wantInnerBody      string
		wantResponseStatus int
		wantResponseBody   string
	}{
		{
			name: "basic json request and response",
			nestedReq: &Request{
				Procedure: "/my.service/Method",
				Message:   json.RawMessage(`{"name":"test"}`),
			},
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method: want POST, got %s", r.Method)
				}
				if r.URL.Path != "/my.service/Method" {
					t.Errorf("path: want /my.service/Method, got %s", r.URL.Path)
				}
				if ct := r.Header.Get("Content-Type"); ct != "application/json" {
					t.Errorf("Content-Type: want application/json, got %s", ct)
				}

				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read body: %v", err)
				}
				if string(body) != `{"name":"test"}` {
					t.Errorf("body: want %s, got %s", `{"name":"test"}`, string(body))
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"result":"ok"}`))
			}),
			wantResponseStatus: http.StatusOK,
			wantResponseBody:   `{"result":"ok"}`,
		},
		{
			name: "request with query parameters",
			nestedReq: &Request{
				Procedure: "/my.service/Method?param1=value1&param2=value2",
				Message:   json.RawMessage(`{}`),
			},
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method: want POST, got %s", r.Method)
				}
				if r.URL.Path != "/my.service/Method" {
					t.Errorf("path: want /my.service/Method, got %s", r.URL.Path)
				}
				if r.URL.Query().Get("param1") != "value1" {
					t.Errorf("param1: want value1, got %s", r.URL.Query().Get("param1"))
				}
				if r.URL.Query().Get("param2") != "value2" {
					t.Errorf("param2: want value2, got %s", r.URL.Query().Get("param2"))
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			}),
			wantResponseStatus: http.StatusOK,
			wantResponseBody:   `{}`,
		},
		{
			name: "header propagation",
			nestedReq: &Request{
				Procedure: "/my.service/Method",
				Header: http.Header{
					"X-Custom": []string{"nested-value"},
				},
				Message: json.RawMessage(`{}`),
			},
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if ct := r.Header.Get("Content-Type"); ct != "application/json" {
					t.Errorf("Content-Type: want application/json, got %s", ct)
				}
				if custom := r.Header.Get("X-Custom"); custom != "nested-value" {
					t.Errorf("X-Custom: want nested-value, got %s", custom)
				}

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Response-Header", "response-value")
				w.WriteHeader(http.StatusCreated)
				w.Write([]byte(`{}`))
			}),
			wantResponseStatus: http.StatusCreated,
			wantResponseBody:   `{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &Server{Handler: tt.handler}

			nestedReqBody, err := json.Marshal(tt.nestedReq)
			if err != nil {
				t.Fatalf("marshal nested request: %v", err)
			}

			req := httptest.NewRequest(http.MethodPost, "/gateway", bytes.NewReader(nestedReqBody))
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)

			if rec.Code != tt.wantResponseStatus {
				t.Errorf("status: want %d, got %d", tt.wantResponseStatus, rec.Code)
			}

			body := rec.Body.String()
			if body != tt.wantResponseBody {
				t.Errorf("body: want %s, got %s", tt.wantResponseBody, body)
			}
		})
	}
}

func TestServerReconstructRequest(t *testing.T) {
	tests := []struct {
		name        string
		outerReq    *http.Request
		nestedReq   *Request
		wantMethod  string
		wantURI     string
		wantHeaders http.Header
		wantBody    string
		wantErr     bool
	}{
		{
			name: "basic request reconstruction",
			outerReq: &http.Request{
				Host: "gateway.example.com",
				URL:  &url.URL{Scheme: "https", Host: "gateway.example.com", Path: "/rpc"},
				Header: http.Header{
					"Authorization": []string{"Bearer token"},
					"User-Agent":    []string{"client/1.0"},
				},
			},
			nestedReq: &Request{
				Procedure: "/my.service/Method",
				Message:   json.RawMessage(`{"field":"value"}`),
			},
			wantMethod: http.MethodPost,
			wantURI:    "/my.service/Method",
			wantHeaders: http.Header{
				"Content-Type":  []string{"application/json"},
				"Authorization": []string{"Bearer token"},
				"User-Agent":    []string{"client/1.0"},
			},
			wantBody: `{"field":"value"}`,
		},
		{
			name: "request with query string",
			outerReq: &http.Request{
				Host: "gateway.example.com",
				URL:  &url.URL{Scheme: "https", Host: "gateway.example.com"},
			},
			nestedReq: &Request{
				Procedure: "/my.service/Method?key=value&foo=bar",
				Header:    http.Header{},
			},
			wantMethod:  http.MethodPost,
			wantURI:     "/my.service/Method?key=value&foo=bar",
			wantHeaders: http.Header{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &Server{}
			innerReq, err := server.reconstructRequest(tt.outerReq, tt.nestedReq)
			if (err != nil) != tt.wantErr {
				t.Fatalf("reconstructRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if innerReq.Method != tt.wantMethod {
				t.Errorf("method: want %s, got %s", tt.wantMethod, innerReq.Method)
			}

			if innerReq.URL.RequestURI() != tt.wantURI {
				t.Errorf("URI: want %s, got %s", tt.wantURI, innerReq.URL.RequestURI())
			}

			for k, want := range tt.wantHeaders {
				got := innerReq.Header.Get(k)
				if got != want[0] {
					t.Errorf("header %s: want %s, got %s", k, want[0], got)
				}
			}

			if tt.wantBody != "" {
				body, err := io.ReadAll(innerReq.Body)
				if err != nil {
					t.Fatalf("read body: %v", err)
				}
				if string(body) != tt.wantBody {
					t.Errorf("body: want %s, got %s", tt.wantBody, string(body))
				}
			}
		})
	}
}

func TestServerInvalidJSON(t *testing.T) {
	server := &Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("handler should not be called")
		}),
	}

	req := httptest.NewRequest(http.MethodPost, "/gateway", strings.NewReader("invalid json"))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestServerEmptyMessage(t *testing.T) {
	server := &Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("handler should not be called")
		}),
	}

	nestedReq := &Request{
		Procedure: "/my.service/Method",
	}

	nestedReqBody, _ := json.Marshal(nestedReq)
	req := httptest.NewRequest(http.MethodPost, "/gateway", bytes.NewReader(nestedReqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestServerInvalidMessageJSON(t *testing.T) {
	server := &Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("handler should not be called")
		}),
	}

	nestedReq := &Request{
		Procedure: "/my.service/Method",
		Message:   json.RawMessage(`{"invalid":`),
	}

	nestedReqBody, _ := json.Marshal(nestedReq)
	req := httptest.NewRequest(http.MethodPost, "/gateway", bytes.NewReader(nestedReqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestServerProtobufNotSupported(t *testing.T) {
	server := &Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("handler should not be called")
		}),
	}

	nestedReq := &Request{
		Procedure: "/my.service/Method",
		Header: http.Header{
			"Content-Type": []string{"application/proto"},
		},
		Message: json.RawMessage(`{}`),
	}

	nestedReqBody, _ := json.Marshal(nestedReq)
	req := httptest.NewRequest(http.MethodPost, "/gateway", bytes.NewReader(nestedReqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status: want %d, got %d", http.StatusNotImplemented, rec.Code)
	}
}

func TestServerProtobufStreamingNotSupported(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
	}{
		{
			name:        "connect+proto streaming",
			contentType: "application/connect+proto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &Server{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Fatal("handler should not be called")
				}),
			}

			nestedReq := &Request{
				Procedure: "/my.service/Method",
				Header: http.Header{
					"Content-Type": []string{tt.contentType},
				},
				Message: json.RawMessage(`{}`),
			}

			nestedReqBody, _ := json.Marshal(nestedReq)
			req := httptest.NewRequest(http.MethodPost, "/gateway", bytes.NewReader(nestedReqBody))
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotImplemented {
				t.Errorf("status: want %d, got %d", http.StatusNotImplemented, rec.Code)
			}
		})
	}
}

func TestServerSSEStreaming(t *testing.T) {
	tests := []struct {
		name               string
		handlerResponse    []byte
		handlerContentType string
		wantContentType    string
		wantSSEEvents      []string
	}{
		{
			name:               "streaming response with envelopes",
			handlerResponse:    makeEnvelope(0, []byte(`{"message":"first"}`)),
			handlerContentType: "application/connect+json",
			wantContentType:    "text/event-stream",
			wantSSEEvents:      []string{"data: {\"message\":\"first\"}\n\n"},
		},
		{
			name: "multiple streaming messages",
			handlerResponse: append(
				makeEnvelope(0, []byte(`{"message":"first"}`)),
				makeEnvelope(0, []byte(`{"message":"second"}`))...,
			),
			handlerContentType: "application/connect+json",
			wantContentType:    "text/event-stream",
			wantSSEEvents: []string{
				"data: {\"message\":\"first\"}\n\n",
				"data: {\"message\":\"second\"}\n\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.handlerContentType)
				w.WriteHeader(http.StatusOK)
				w.Write(tt.handlerResponse)
			})

			server := &Server{Handler: handler}

			nestedReq := &Request{
				Procedure: "/my.service/Method",
				Message:   json.RawMessage(`{}`),
			}

			nestedReqBody, _ := json.Marshal(nestedReq)
			req := httptest.NewRequest(http.MethodPost, "/gateway", bytes.NewReader(nestedReqBody))

			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)

			if ct := rec.Header().Get("Content-Type"); ct != tt.wantContentType {
				t.Errorf("Content-Type: want %s, got %s", tt.wantContentType, ct)
			}

			body := rec.Body.String()
			for i, wantEvent := range tt.wantSSEEvents {
				if !strings.Contains(body, wantEvent) {
					t.Errorf("event %d: want %s in response, got %s", i, wantEvent, body)
				}
			}
		})
	}
}

func makeEnvelope(flags byte, data []byte) []byte {
	buf := make([]byte, 5+len(data))
	buf[0] = flags
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(data)))
	copy(buf[5:], data)
	return buf
}
