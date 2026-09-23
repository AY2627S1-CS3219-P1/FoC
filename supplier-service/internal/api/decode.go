package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/exterrors/errs"
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
		return errs.WrapBadRequestError(err, "invalid request body")
	}
	if int64(len(body)) > maxBodyBytes {
		return errs.NewBadRequestError("request body too large")
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errs.WrapBadRequestError(err, "invalid request body")
	}
	if err := dec.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return errs.NewBadRequestError("unexpected trailing data")
	}

	if err := validate.Struct(v); err != nil {
		return errs.NewBadRequestError(err.Error())
	}

	return nil
}
