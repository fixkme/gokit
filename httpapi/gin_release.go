//go:build !debug
// +build !debug

package httpapi

import "github.com/gin-gonic/gin"

func setMode() {
	gin.SetMode(gin.ReleaseMode)
}
