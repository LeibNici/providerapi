package protocol

import "fmt"

type ProviderError struct {
	Message           string `json:"message"`
	Type              string `json:"type"`
	Code              string `json:"code"`
	Param             any    `json:"param"`
	HTTPStatus        int    `json:"-"`
	ProviderStatus    int    `json:"provider_status,omitempty"`
	ProviderErrorCode string `json:"provider_error_code,omitempty"`
	ProviderRequestID string `json:"provider_request_id,omitempty"`
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func NewProviderError(httpStatus int, code, message string) *ProviderError {
	return &ProviderError{
		Message:    message,
		Type:       "provider_error",
		Code:       code,
		Param:      nil,
		HTTPStatus: httpStatus,
	}
}

func InvalidRequest(message string) *ProviderError {
	return &ProviderError{
		Message:    message,
		Type:       "invalid_request_error",
		Code:       "invalid_request",
		HTTPStatus: 400,
	}
}

func ModelNotFound(model string) *ProviderError {
	return &ProviderError{
		Message:    fmt.Sprintf("The model `%s` does not exist", model),
		Type:       "invalid_request_error",
		Code:       "model_not_found",
		HTTPStatus: 404,
	}
}

func Timeout(message string) *ProviderError {
	if message == "" {
		message = "provider request timed out"
	}
	return NewProviderError(504, "timeout", message)
}

func PluginCrash(message string) *ProviderError {
	if message == "" {
		message = "plugin process crashed"
	}
	return NewProviderError(502, "plugin_crash", message)
}

func MapProviderStatus(status int, message, providerCode, providerReqID string) *ProviderError {
	httpStatus := 502
	code := "provider_error"
	switch status {
	case 400:
		httpStatus, code = 400, "invalid_request"
	case 401:
		httpStatus, code = 401, "unauthorized"
	case 403:
		httpStatus, code = 403, "forbidden"
	case 404:
		httpStatus, code = 404, "not_found"
	case 429:
		httpStatus, code = 429, "rate_limited"
	case 503:
		httpStatus, code = 503, "unavailable"
	}
	if message == "" {
		message = fmt.Sprintf("upstream returned HTTP %d", status)
	}
	return &ProviderError{
		Message:           message,
		Type:              "provider_error",
		Code:              code,
		HTTPStatus:        httpStatus,
		ProviderStatus:    status,
		ProviderErrorCode: providerCode,
		ProviderRequestID: providerReqID,
	}
}

func AsProviderError(err error) *ProviderError {
	if err == nil {
		return nil
	}
	if pe, ok := err.(*ProviderError); ok {
		return pe
	}
	return NewProviderError(502, "provider_error", err.Error())
}
