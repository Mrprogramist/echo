package apierror

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type ApiError struct {
	StatusCode int            `json:"-"`
	Code       string         `json:"code"`
	Detail     string         `json:"detail"` // Для совместимости с FastAPI/React
	Params     map[string]any `json:"params,omitempty"`
}

func (e *ApiError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Detail)
}

func New(statusCode int, code, text string, params ...map[string]any) *ApiError {
	var p map[string]any
	if len(params) > 0 {
		p = params[0]
	}
	return &ApiError{
		StatusCode: statusCode,
		Code:       code,
		Detail:     text,
		Params:     p,
	}
}

// WriteJSON отправляет стандартизированный ответ клиенту
func WriteJSON(w http.ResponseWriter, err error) {
	if apiErr, ok := err.(*ApiError); ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(apiErr.StatusCode)
		_ = json.NewEncoder(w).Encode(apiErr)
		return
	}

	// Необработанная системная ошибка
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":   "internalError",
		"detail": err.Error(),
	})
}

// SocketError форматирует ошибку для подтверждений (ACK) сокетов
func SocketError(code, text string, params ...map[string]any) map[string]any {
	res := map[string]any{
		"ok":    false,
		"code":  code,
		"error": text,
	}
	if len(params) > 0 && params[0] != nil {
		res["params"] = params[0]
	}
	return res
}
