package apperror

import "net/http"

type Error struct {
	Status  int
	Code    string
	Message string
	Details any
}

func New(status int, code, message string, details any) Error {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	return Error{Status: status, Code: code, Message: message, Details: details}
}

func (e Error) Error() string {
	return e.Message
}

func (e Error) Body() map[string]any {
	return map[string]any{"code": e.Code, "message": e.Message, "details": e.Details}
}
