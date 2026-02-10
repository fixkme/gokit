package errs

// 系统内部错误码，为了隔离，业务层错误码应该从大数字开始
const (
	ErrCode_OK             = 0
	ErrCode_Unknown        = 1
	ErrCode_Unmarshal      = 2
	ErrCode_Marshal        = 3
	ErrCode_NotFindService = 4
	ErrCode_NotFindMethod  = 5
)

var (
	Unknown        = CreateCodeError(ErrCode_Unknown, "UNKNOWN")
	Unmarshal      = CreateCodeError(ErrCode_Unmarshal, "UNMARSHAL")
	Marshal        = CreateCodeError(ErrCode_Marshal, "MARSHAL")
	NotFindService = CreateCodeError(ErrCode_NotFindService, "NOT_FIND_SERVICE")
	NotFindMethod  = CreateCodeError(ErrCode_NotFindMethod, "NOT_FIND_METHOD")
)
