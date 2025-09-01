package httpapi

import "github.com/gin-gonic/gin"

// 返回格式
type ResponseResult struct {
	Ecode int         `json:"ecode"` // 业务状态码，0为成功，其他表示失败
	Error string      `json:"error"` // 业务消息提示，不参与逻辑，仅用于ui展示，默认为空字符串
	Data  interface{} `json:"data"`  // 业务数据，如果不设置时默认为json空对象{}
}

// ResponseResult 遵循公司web规范的http返回
// c http请求的上下文
// httpStatus http状态码，由于业务完全使用返回的status判断是否成功，禁止返回204/205这一类没有任何返回的状态码（否则会收不到json返回）
// response 返回的json，格式为 { "status": xxx,"data": xxx }
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
