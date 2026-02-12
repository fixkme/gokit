package util

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// GetMessageName  从  game.CLoginSLG  中获取 CLoginSLG
func GetMessageName(msg proto.Message) string {
	return string(msg.ProtoReflect().Descriptor().Name())
}

// GetMessageFullName 返回 game.CLoginSLG
func GetMessageFullName(msg proto.Message) string {
	return string(msg.ProtoReflect().Descriptor().FullName())
}

// MakeMessageByFullName 根据proto全类名获取Message实例 game.CLoginSLG
func MakeMessageByFullName(fullName string) (proto.Message, error) {
	// 要从 GlobalTypes 找到的前提是某个地方import过这个message的包， 比如 import _ "github.com/fixkme/protoc-gen-gom/example/pbout/go/datas"
	// 因为是通过 *.pb.go 文件的init函数注册到 GlobalTypes 的
	msgType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(fullName))
	if err != nil {
		return nil, err
	}
	msg := msgType.New().Interface()
	return msg, nil
}

// NewMessageByFullName 根据proto全类名获取Message实例 game.CLoginSLG
func NewMessageByFullName(fullName string, data []byte) (proto.Message, error) {
	// 要从 GlobalTypes 找到的前提是某个地方import过这个message的包， 比如 import _ "github.com/fixkme/protoc-gen-gom/example/pbout/go/datas"
	// 因为是通过 *.pb.go 文件的init函数注册到 GlobalTypes 的
	msgType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(fullName))
	if err != nil {
		return nil, err
	}
	msg := msgType.New().Interface()
	if len(data) > 0 {
		err = proto.Unmarshal(data, msg)
		if err != nil {
			return nil, err
		}
	}
	return msg, nil
}
