package httpapi

import (
	"net/http"

	"github.com/fixkme/gokit/errs"
	"github.com/gin-gonic/gin"
)

// 返回格式
type ResponseResult struct {
	Ecode int         `json:"ecode"` // 业务状态码，0为成功，其他表示失败
	Error string      `json:"error"` // 业务消息提示，不参与逻辑，仅用于ui展示，默认为空字符串
	Data  interface{} `json:"data"`  // 业务数据，如果不设置时默认为json空对象{}
}

func Response(c *gin.Context, httpStatus int, response *ResponseResult) {
	data := response.Data
	if response.Data == nil {
		data = gin.H{}
	}
	c.JSON(httpStatus, gin.H{
		"status": response.Ecode,
		"error":  response.Error,
		"data":   data,
		"_links": gin.H{
			"self": gin.H{
				"href": c.Request.RequestURI,
			},
		},
	})
}

func ResponseError(c *gin.Context, httpStatus int, err error) {
	errCode, errDesc := parserError(err)
	c.JSON(httpStatus, gin.H{
		"status": errCode,
		"error":  errDesc,
		"data":   gin.H{},
		"_links": gin.H{
			"self": gin.H{
				"href": c.Request.RequestURI,
			},
		},
	})
}

func ResponseSuccess(c *gin.Context, data any) {
	if data == nil {
		data = gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{
		"status": 0,
		"error":  "",
		"data":   data,
		"_links": gin.H{
			"self": gin.H{
				"href": c.Request.RequestURI,
			},
		},
	})
}

func parserError(err error) (errCode int, errDesc string) {
	codeErr, ok := err.(errs.CodeError)
	if ok {
		errCode, errDesc = int(codeErr.Code()), codeErr.Error()
	} else {
		errCode, errDesc = errs.ErrCode_Unknown, err.Error()
	}
	return
}
