package api

import (
	"fmt"
	"io"
	"net/http"
)

type Severity string

const (
	INFO    Severity = "info"
	SUCCESS Severity = "success"
	WARNING Severity = "warning"
	ERROR   Severity = "error"
)

// StatusMessage is a group of human-readable messages sharing one severity.
type StatusMessage struct {
	Message  string   `json:"message"`
	Severity Severity `json:"severity"`
}

// Response is the envelope returned by every endpoint. Code and Header control
// the HTTP response and are not serialized.
//
// For binary payloads (images, files), set Raw or Stream with ContentType.
// When set, the JSON envelope is skipped and bytes are written directly.
type Response struct {
	Status []StatusMessage `json:"status,omitempty"`
	Data   any             `json:"data"`

	// Code defaults to 200 when zero.
	Code int `json:"-"`
	// Header holds extra response headers, e.g. Set-Cookie or Location.
	Header http.Header `json:"-"`
	// Raw, when non-nil, bypasses the JSON envelope and is written as-is.
	Raw []byte `json:"-"`
	// Stream, when non-nil, is copied to the response body. Takes precedence over Raw.
	Stream io.Reader `json:"-"`
	// ContentType for Raw/Stream responses. Defaults to DetectContentType (Raw)
	// or application/octet-stream (Stream) when empty.
	ContentType string `json:"-"`
}

// NewResponse creates a new Response with the given data and a default HTTP status code of 200 OK.
func NewResponse(data any, opts ...Option) (*Response, error) {
	r := &Response{
		Data: data,
		Code: http.StatusOK,
	}
	if err := applyOptions(r, opts); err != nil {
		return nil, err
	}
	return r, nil
}

// NewRawResponse creates a Response that writes raw bytes instead of the JSON envelope.
func NewRawResponse(raw []byte, contentType string, opts ...Option) (*Response, error) {
	r := &Response{
		Raw:         raw,
		ContentType: contentType,
		Code:        http.StatusOK,
	}
	if err := applyOptions(r, opts); err != nil {
		return nil, err
	}
	return r, nil
}

// NewStreamResponse creates a Response that streams from reader instead of the JSON envelope.
func NewStreamResponse(stream io.Reader, contentType string, opts ...Option) (*Response, error) {
	r := &Response{
		Stream:      stream,
		ContentType: contentType,
		Code:        http.StatusOK,
	}
	if err := applyOptions(r, opts); err != nil {
		return nil, err
	}
	return r, nil
}

func applyOptions(r *Response, opts []Option) error {
	for _, o := range opts {
		if err := o(r); err != nil {
			return err
		}
	}
	if r.Stream != nil && r.Raw != nil {
		return fmt.Errorf("response cannot set both Stream and Raw")
	}
	return nil
}

// Option mutates a Response. Returns an error on invalid values.
type Option func(*Response) error

// WithCode sets the HTTP status code for the response.
func WithCode(code int) Option {
	return func(r *Response) error {
		if code < 100 || code > 599 {
			return fmt.Errorf("invalid status code %d", code)
		}
		r.Code = code
		return nil
	}
}

// WithStatus appends a status message to the response.
func WithStatus(sev Severity, message string) Option {
	return func(r *Response) error {
		switch sev {
		case INFO, SUCCESS, WARNING, ERROR:
		default:
			return fmt.Errorf("unknown severity %q", string(sev))
		}
		r.Status = append(r.Status, StatusMessage{Message: message, Severity: sev})
		return nil
	}
}

// WithHeader appends response header values, e.g. Set-Cookie.
func WithHeader(key string, values ...string) Option {
	return func(r *Response) error {
		if r.Header == nil {
			r.Header = http.Header{}
		}
		for _, v := range values {
			r.Header.Add(key, v)
		}
		return nil
	}
}

// WithRaw sets a raw body that bypasses the JSON envelope.
func WithRaw(raw []byte, contentType string) Option {
	return func(r *Response) error {
		r.Raw = raw
		r.ContentType = contentType
		return nil
	}
}

// WithStream sets a streaming body that bypasses the JSON envelope.
func WithStream(stream io.Reader, contentType string) Option {
	return func(r *Response) error {
		r.Stream = stream
		r.ContentType = contentType
		return nil
	}
}
