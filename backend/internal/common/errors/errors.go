package errors

import "net/http"

// 这些常量是项目统一错误码。
// 以后所有接口都应该尽量复用这套错误码，而不是每个接口自己随便定义。
const (
	CodeSuccess             = 0
	CodeInvalidParams       = 1001
	CodeUnauthorized        = 1002
	CodeForbidden           = 1003
	CodeNotFound            = 1004
	CodeUsernameExists      = 2001
	CodeInvalidAuth         = 2002
	CodeOldPasswordWrong    = 2003
	CodeCommentNotFound     = 4003
	CodeCannotDeleteComment = 4004
	CodeCannotFollowSelf    = 4005
	CodeInternalError       = 5000
	CodeServiceUnusable     = 5001
)

// 这些是启动阶段常见的基础设施错误。
// 提前定义好以后，调用方就能统一处理。
var (
	ErrDBInit              = New(http.StatusInternalServerError, CodeInternalError, "database initialization failed")
	ErrMigration           = New(http.StatusInternalServerError, CodeInternalError, "database migration failed")
	ErrRedisInit           = New(http.StatusInternalServerError, CodeInternalError, "redis initialization failed")
	ErrRabbitMQInit        = New(http.StatusInternalServerError, CodeInternalError, "rabbitmq initialization failed")
	ErrStorageInit         = New(http.StatusInternalServerError, CodeInternalError, "storage initialization failed")
	ErrInvalidParams       = New(http.StatusBadRequest, CodeInvalidParams, "invalid params")
	ErrUnauthorized        = New(http.StatusUnauthorized, CodeUnauthorized, "unauthorized")
	ErrForbidden           = New(http.StatusForbidden, CodeForbidden, "forbidden")
	ErrNotFound            = New(http.StatusNotFound, CodeNotFound, "not found")
	ErrUserNotFound        = New(http.StatusNotFound, CodeNotFound, "user not found")
	ErrVideoNotFound       = New(http.StatusNotFound, CodeNotFound, "video not found")
	ErrCommentNotFound     = New(http.StatusNotFound, CodeCommentNotFound, "comment not found")
	ErrCannotDeleteComment = New(http.StatusForbidden, CodeCannotDeleteComment, "cannot delete others comment")
	ErrCannotFollowSelf    = New(http.StatusBadRequest, CodeCannotFollowSelf, "cannot follow yourself")
	ErrUsernameExists      = New(http.StatusConflict, CodeUsernameExists, "username already exists")
	ErrInvalidCredentials  = New(http.StatusUnauthorized, CodeInvalidAuth, "username or password invalid")
	ErrOldPasswordWrong    = New(http.StatusBadRequest, CodeOldPasswordWrong, "old password invalid")
)

// AppError 是项目自定义错误类型。
// 它比普通 error 多了三个接口层常用字段：
// 1. HTTPStatus：HTTP 状态码
// 2. Code：业务错误码
// 3. Message：对外返回的错误消息
type AppError struct {
	HTTPStatus int
	Code       int
	Message    string
	Err        error
}

// Error 让 AppError 实现 error 接口。
// 如果内部包了一层原始错误，就优先返回原始错误文本，方便排查。
func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

// Unwrap 允许 errors.Is / errors.As 继续往里解包。
func (e *AppError) Unwrap() error {
	return e.Err
}

// New 创建一个不带底层原始错误的 AppError。
func New(httpStatus, code int, message string) *AppError {
	return &AppError{
		HTTPStatus: httpStatus,
		Code:       code,
		Message:    message,
	}
}

// Wrap 创建一个带原始错误的 AppError。
// 这样既能保留底层错误，又能控制对外响应结构。
func Wrap(err error, httpStatus, code int, message string) *AppError {
	return &AppError{
		HTTPStatus: httpStatus,
		Code:       code,
		Message:    message,
		Err:        err,
	}
}

// Internal 用于快速包装内部错误。
func Internal(err error) *AppError {
	return Wrap(err, http.StatusInternalServerError, CodeInternalError, "internal server error")
}

// ServiceUnavailable 用于包装“依赖不可用”这类错误。
func ServiceUnavailable(message string, err error) *AppError {
	return Wrap(err, http.StatusServiceUnavailable, CodeServiceUnusable, message)
}
