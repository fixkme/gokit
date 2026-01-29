package errs

const (
	ErrCode_OK        = 0
	ErrCode_Unknown   = 1
	ErrCode_Unmarshal = 2
	ErrCode_Marshal   = 3
)

var (
	Unknown   = CreateCodeError(ErrCode_Unknown, "UNKNOWN")
	Unmarshal = CreateCodeError(ErrCode_Unmarshal, "UNMARSHAL")
	Marshal   = CreateCodeError(ErrCode_Marshal, "MARSHAL")
)
