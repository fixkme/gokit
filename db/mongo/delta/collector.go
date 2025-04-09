package delta

import (
	"reflect"
	"strings"

	"github.com/armon/go-radix"
	"go.mongodb.org/mongo-driver/bson"
)

type idType interface {
	int64 | string
}

type IDeltaCollector[ID idType] interface {
	// 收集变化的数据。由于兼容生成的.pc代码，加入ntf参数
	Collect(key string, val any, ntf bool)
	// 重置收集器
	Reset()
	CollectSize() int
	// 绑定监视器
	BindMonitor(IDeltaMonitor[ID])
	// 获取变化数据
	CatchDeltaData() bson.D
	// 获取所属对象
	GetEntityId() ID
	IsDataChange() bool
	SetDataChange(bool)
	IsDelete() bool
	SetDelete(bool)
}

type DeltaPair struct {
	Key string
	Val any
}

type DeltaCollector[ID idType] struct {
	id           ID
	array        []*DeltaPair
	isDataChange bool
	isDelete     bool //从mongo里删除
}

func NewDeltaCollector[ID idType](id ID) *DeltaCollector[ID] {
	return &DeltaCollector[ID]{
		id:    id,
		array: make([]*DeltaPair, 0, 8),
	}
}

func (c *DeltaCollector[ID]) Reset() {
	if c.array != nil {
		c.array = c.array[:0]
	} else {
		c.array = make([]*DeltaPair, 0)
	}
}

func (c *DeltaCollector[ID]) CollectSize() int {
	return len(c.array)
}

func (c *DeltaCollector[ID]) Collect(key string, val any, _ bool) {
	c.array = append(c.array, &DeltaPair{Key: key, Val: val})
}

func (c *DeltaCollector[ID]) CatchDeltaData() (result bson.D) {
	tree := radix.New()
	prefix := new(strings.Builder)
	prefix.Grow(64)
outer:
	for _, pair := range c.array {
		k := pair.Key
		v := pair.Val
		parts := strings.Split(k, ".")
		prefix.Reset()
		// 检查是否已经存在父级节点，如果有，直接跳过当前key
		// 需要注意的是，父级节点需要是最新的引用值，否则会有问题
		for i := 0; i < len(parts)-1; i++ {
			prefix.WriteString(parts[i])
			_, ok := tree.Get(prefix.String())
			if ok {
				continue outer
			}
			prefix.WriteString(".")
		}
		// 删除当前节点的所有子节点
		tree.DeletePrefix(k + ".")
		// 更新当前节点
		tree.Insert(k, v)
	}
	if tree.Len() == 0 {
		return nil
	}

	update := make(map[string]interface{})
	unset := make(map[string]interface{})
	tree.Walk(func(key string, v interface{}) bool {
		st := reflect.TypeOf(v)
		_, ok := st.MethodByName("ToDB")
		if ok {
			outputs := reflect.ValueOf(v).MethodByName("ToDB").Call([]reflect.Value{})
			update[key] = outputs[0].Interface()
		} else {
			vv, okk := v.(string)
			if okk && vv == "__DELETE__" {
				unset[key] = ""
			} else {
				update[key] = v
			}
		}
		// 返回false表示继续walk
		return false
	})
	if len(update) > 0 {
		result = append(result, bson.E{
			Key:   "$set",
			Value: update,
		})
	}
	if len(unset) > 0 {
		result = append(result, bson.E{
			Key:   "$unset",
			Value: unset,
		})
	}
	return
}

func (c *DeltaCollector[ID]) BindMonitor(IDeltaMonitor[ID]) {
	panic("not implement BindMonitor")
}

func (c *DeltaCollector[ID]) GetEntityId() ID {
	return c.id
}

func (c *DeltaCollector[ID]) IsDataChange() bool {
	return c.isDataChange
}

func (c *DeltaCollector[ID]) SetDataChange(isDataChange bool) {
	c.isDataChange = isDataChange
}

func (c *DeltaCollector[ID]) IsDelete() bool {
	return c.isDelete
}

func (c *DeltaCollector[ID]) SetDelete(isDelete bool) {
	c.isDelete = isDelete
}
