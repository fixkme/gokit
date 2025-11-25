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

// NewMessageByFullName 根据proto全类名获取Message实例 game.CLoginSLG
func NewMessageByFullName(fullName string, data []byte) (proto.Message, error) {
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
