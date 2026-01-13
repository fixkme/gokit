package httpapi

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"

	"github.com/fixkme/gokit/errs"
	"github.com/fixkme/gokit/mlog"
	"github.com/fixkme/gokit/rpc"
	"github.com/fixkme/gokit/util"
	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

type Server struct {
	rpcClient rpc.RpcClient
	Addr      string
	Ln        net.Listener
	Router    *gin.Engine
}

func NewWeb(network, addr string, rpcClient rpc.RpcClient, middleware ...gin.HandlerFunc) (*Server, error) {
	ln, err := net.Listen(network, addr)
	if err != nil {
		return nil, err
	}

	setMode()
	engine := gin.New()
	if len(middleware) > 0 {
		engine.Use(middleware...)
	}

	return &Server{
		rpcClient: rpcClient,
		Addr:      addr,
		Ln:        ln,
		Router:    engine,
	}, nil
}

func (s *Server) Start() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				mlog.Warnf("web recover error: %v.", r)
			}
		}()
		if err := s.Run(); err != nil {
			mlog.Warnf("web run error: %v", err)
		}
	}()
}

func (s *Server) Run() (err error) {
	s.regWebRouter()
	return s.Router.RunListener(s.Ln)
}

func (s *Server) Stop() {
	if err := s.Ln.Close(); err != nil {
		mlog.Warnf("web stop error %v", err)
	}
}

func (s *Server) regWebRouter() {
	v0 := s.Router.Group("/v0")
	v0.GET("/myip", s.myIPHandler)
	v0.POST("/myip", s.myIPHandler)

	v1 := s.Router.Group("/api")
	v1.Any("/:service/:pathname", s.httpHandler)
}

// http handler
func (s *Server) httpHandler(c *gin.Context) {
	serviceName := c.Param("service")
	methodName := c.Param("pathname")
	httpMethod := c.Request.Method
	serviceName = strings.ToLower(serviceName)
	methodName = strings.ToLower(methodName)
	httpMethod = strings.ToLower(httpMethod)
	mlog.Debugf("httpHandler HttpMethod:%s Content-Type:%s URL:%s, serviceName:%s, methodName:%s", httpMethod, c.ContentType(), c.Request.URL, serviceName, methodName)

	req, resp, err := s.makeMsg(serviceName, methodName)
	if err != nil {
		Response(c, http.StatusBadRequest, &ResponseResult{Ecode: 2301, Error: err.Error()})
		return
	}

	// HTTP Content-Type标准
	// 默认：application/json
	// GET：query string
	// 上传文件：POST multipart/form-data
	// 上传文件情况：提取文件数据到proto.Message
	if c.ContentType() == "multipart/form-data" {
		if err := fillReqUsingFormData(c, req); err != nil {
			Response(c, http.StatusBadRequest, &ResponseResult{Ecode: 2302, Error: err.Error()})
			return
		}
	} else {
		if err := c.Bind(req); nil != err {
			Response(c, http.StatusBadRequest, &ResponseResult{Ecode: 2302})
			mlog.Warnf("HTTP request bind json error: %s", err)
			return
		}
	}

	_, err = s.rpcClient.Call(serviceName, func(ctx context.Context, cc *rpc.ClientConn) (proto.Message, error) {
		_, _, _err := cc.Invoke(ctx, serviceName, methodName, req, resp)
		return resp, _err
	})
	if err != nil {
		errCode, errDesc := parserError(err)
		mlog.Debugf("HTTP(%s %s/%s) call RPC error: %s %d|%s", serviceName, methodName, httpMethod, err, errCode, errDesc)
		Response(c, http.StatusInternalServerError, &ResponseResult{Ecode: errCode, Data: resp, Error: errDesc})
		return
	}
	Response(c, http.StatusOK, &ResponseResult{Ecode: 0, Data: resp})
}

func (s *Server) makeMsg(service, method string) (req, resp proto.Message, err error) {
	method = util.UpperFirst(method)
	reqFullName := service + ".C" + method
	respFullName := service + ".S" + method
	reqType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(reqFullName))
	if err != nil {
		return
	}
	respType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(respFullName))
	if err != nil {
		return
	}
	req = reqType.New().(proto.Message)
	resp = respType.New().(proto.Message)
	return
}

type myIP struct {
	// IP 客户端连接IP
	IP string `json:"ip"`
}

// 回复客户端使用的IP
func (s *Server) myIPHandler(c *gin.Context) {
	c.JSON(http.StatusOK, myIP{IP: c.ClientIP()})
}

// 利用form-data填充req结构
func fillReqUsingFormData(c *gin.Context, req proto.Message) error {
	if form, err := c.MultipartForm(); err != nil {
		mlog.Errorf("解析multipart-form失败.%s   url.%s", err, c.Request.URL.Path)
		return errors.New("parse multipart form failed")
	} else {
		refValue := reflect.ValueOf(req).Elem() // 拿到值反射 无论是值还是类型均是指针类型
		refType := reflect.TypeOf(req).Elem()   // 拿到类型反射
		// 解析key-value
		for k, v := range form.Value {
			node := refValue.FieldByName(strings.Title(k))
			if !node.IsValid() {
				continue
			}
			node.SetString(v[0])
		}

		// 解析upload的文件
		for k, v := range form.File {
			node := refValue.FieldByName(strings.Title(k))
			if !node.IsValid() {
				continue
			}
			if !node.CanAddr() {
				mlog.Warnf("pb中feild.%s 接受文件但未非指针类型，意料之外 path.%s", k, c.Request.URL.Path)
				continue
			}
			field, ok := refType.FieldByName(strings.Title(k)) // 拿到类型域
			if !ok || field.Type.Kind() != reflect.Ptr {
				mlog.Warnf("pb中feild.%s ok.%v 接受文件但获得类型失败，意料之外 path.%s", k, ok, c.Request.URL.Path)
				continue
			}

			val := reflect.New(field.Type.Elem()) // 根据类型new一个元素出来，注意去掉指针
			file := val.Elem()                    // 拿到文件
			content := reflect.Indirect(file).FieldByName("Content")
			if !content.IsValid() {
				mlog.Warnf("pb中feild.%s 接受文件但未包含Content域 path.%s", k, c.Request.URL.Path)
				continue
			}
			name := reflect.Indirect(file).FieldByName("Name")
			if !name.IsValid() {
				mlog.Warnf("pb中feild.%s 接受文件但未包含Name域 path.%s", k, c.Request.URL.Path)
				continue
			}
			if file, err := v[0].Open(); err != nil {
				mlog.Errorf("打开.%s 文件失败.%s path.%s", k, err, c.Request.URL.Path)
				continue
			} else if byts, err := io.ReadAll(file); err != nil {
				mlog.Errorf("读取.%s 文件数据失败.%s path.%s", k, err, c.Request.URL.Path)
				continue
			} else {
				content.SetBytes(byts)
				name.SetString(v[0].Filename)
				node.Set(val) // 将new出来的元素设置到节点上去
			}
		}
	}

	return nil
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
