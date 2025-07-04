package rpc

import (
	"encoding/binary"
	"errors"

	"github.com/fixkme/gokit/util/errs"
	"google.golang.org/protobuf/proto"
)

var byteOrder binary.ByteOrder = binary.LittleEndian

// rpc消息的全局解码器，客户端、服务端共用
var defaultUnmarshaler = proto.UnmarshalOptions{
	AllowPartial:   true, //跳过required字段检查，因为根本没有required字段
	DiscardUnknown: true, //跳过未知字段检查, 1、字段是明确的，没有扩展字段，2、即使版本增减导致的unknown字段也不处理
	RecursionLimit: 100,
	NoLazyDecoding: true, //禁用lazy解码，1、没有lazy标志的字段，2、开启lazy字段解码会导致buff延长生命周期
}

// rpc消息的全局编码器，客户端、服务端共用
var defaultMarshaler = proto.MarshalOptions{
	AllowPartial:  true,  //跳过required字段检查，因为根本没有required字段
	Deterministic: false, //是否对map进行排序，以此保证输出的二进制数据是相同的。rpc消息是不需要的
}

func (msg *RpcResponseMessage) ParserError() error {
	if msg.Error != "" {
		if msg.Ecode != 0 {
			return errs.CreateCodeError(msg.Ecode, msg.Error)
		}
		return errors.New(msg.Error)
	}
	return nil
}

func (md *Meta) GetInt(key string) int64 {
	if md == nil {
		return 0
	}
	v, ok := md.Kvs[key]
	if ok && len(v.Ints) != 0 {
		return v.Ints[0]
	}
	return 0
}

func (md *Meta) GetStr(key string) string {
	if md == nil {
		return ""
	}
	v, ok := md.Kvs[key]
	if ok && len(v.Strs) != 0 {
		return v.Strs[0]
	}
	return ""
}

func (md *Meta) SetInt(key string, valInt int64) {
	if md == nil {
		return
	}
	if md.Kvs == nil {
		md.Kvs = make(map[string]*MetaValues)
	}
	md.Kvs[key] = &MetaValues{Ints: []int64{valInt}}
}

func (md *Meta) SetStr(key string, valStr string) {
	if md == nil {
		return
	}
	if md.Kvs == nil {
		md.Kvs = make(map[string]*MetaValues)
	}
	md.Kvs[key] = &MetaValues{Strs: []string{valStr}}
}

func (md *Meta) AddInt(key string, valInt int64) {
	if md == nil {
		return
	}
	if md.Kvs == nil {
		md.Kvs = make(map[string]*MetaValues)
		md.Kvs[key] = &MetaValues{Ints: []int64{valInt}}
		return
	}
	v, ok := md.Kvs[key]
	if ok {
		v.Ints = append(v.Ints, valInt)
	} else {
		md.Kvs[key] = &MetaValues{Ints: []int64{valInt}}
	}
}

func (md *Meta) AddStr(key string, valStr string) {
	if md == nil {
		return
	}
	if md.Kvs == nil {
		md.Kvs = make(map[string]*MetaValues)
		md.Kvs[key] = &MetaValues{Strs: []string{valStr}}
		return
	}
	v, ok := md.Kvs[key]
	if ok {
		v.Strs = append(v.Strs, valStr)
	} else {
		md.Kvs[key] = &MetaValues{Strs: []string{valStr}}
	}
}

func (md *Meta) IntValues(key string) []int64 {
	if md == nil {
		return nil
	}
	v, ok := md.Kvs[key]
	if ok {
		return v.Ints
	}
	return nil
}

func (md *Meta) StrValues(key string) []string {
	if md == nil {
		return nil
	}
	v, ok := md.Kvs[key]
	if ok {
		return v.Strs
	}
	return nil
}

func (md *Meta) Del(key string) {
	if md == nil {
		return
	}
	delete(md.Kvs, key)
}
