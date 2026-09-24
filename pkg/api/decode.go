package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"
)

const maxBodyBytes = 1 << 20 // 1MB

var validate = validator.New(validator.WithRequiredStructEnabled())

func init() {
	_ = validate.RegisterValidation("notfuture", func(fl validator.FieldLevel) bool {
		t, ok := fl.Field().Interface().(time.Time)
		if !ok {
			return false
		}
		return !t.After(time.Now())
	})
}

func Decode(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return &badRequestError{message: "invalid request body", cause: err}
	}
	if int64(len(body)) > maxBodyBytes {
		return &badRequestError{message: "request body too large"}
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &badRequestError{message: "invalid request body", cause: err}
	}
	if err := dec.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return &badRequestError{message: "unexpected trailing data"}
	}

	if err := validate.Struct(v); err != nil {
		return &badRequestError{message: err.Error()}
	}

	return nil
}
