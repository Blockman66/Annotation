package rlp

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

var (
	typeCacheMutex sync.RWMutex                  //读写锁，保护多线程时的map
	typeCache      = make(map[typekey]*typeinfo) //主结构，保存了类型到codex的映射
)

type typeinfo struct {
	decoder    decoder
	decoderErr error
	writer     writer
	writerErr  error
}

type tags struct {
	nilOK   bool //rlp:"nil" controls whether empty input results in a nil pointer.
	tail    bool //只存在于lastfield,且field的kind()必须是slice
	ignored bool //
}

type typekey struct {
	reflect.Type
	tags
}

type decoder func(*Stream, reflect.Value) error

type writer func(reflect.Value, *encbuf) error

func cachedDecoder(typ reflect.Type) (decoder, error) { //返回解码方法
	info := cachedTypeInfo(typ, tags{})
	return info.decoder, info.writerErr
}

func cachedWriter(typ reflect.Type) (writer, error) { //返回编码方法
	info := cachedTypeInfo(typ, tags{})
	return info.writer, info.writerErr
}

func cachedTypeInfo(typ reflect.Type, tags tags) *typeinfo { //上读锁
	typeCacheMutex.RLock()
	info := typeCache[typekey{typ, tags}]
	typeCacheMutex.RUnlock()

	if info != nil {
		return info
	}
	//若缓存中没有，则需要加入，上写锁
	typeCacheMutex.Lock()
	defer typeCacheMutex.Unlock()
	return cachedTypedInfo1(typ, tags)
}

func cachedTypedInfo1(typ reflect.Type, tags tags) *typeinfo {
	key := typekey{typ, tags}
	info := typeCache[key]
	//等待写锁过程中，info可能已经被其他线程写入，所以重读一遍
	if info != nil {
		return info
	}
	info = new(typeinfo) //防止递归结构出现？？？？
	typeCache[key] = info
	info.generate(typ, tags) //实现info
	return info
}

type field struct {
	//定分别处理结构体内每个字段
	index int
	info  *typeinfo
}

func structFields(typ reflect.Type) (fields []field, err error) { //逐个解析struct内字段的编解码方式
	lastpublic := lastPublicField(typ)
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); f.PkgPath == "" { //exported only
			tags, err := parseStructTag(typ, i, lastpublic)
			if err != nil {
				return nil, err //tail有问题
			}
			if tags.ignored { //忽略此field
				continue
			}
			info := cachedTypedInfo1(f.Type, tags) //加入map
			fields = append(fields, field{i, info})
		}
	}
	return fields, nil
}

func parseStructTag(typ reflect.Type, fi, lastPublic int) (tags, error) { //解析某个structfield的keytag
	f := typ.Field(fi)
	var ts tags
	for _, t := range strings.Split(f.Tag.Get("rlp"), ",") { //找到field fi 的 tag`rlp` 中的每一个词
		switch t = strings.TrimSpace(t); t { //去前后空格
		case "":
		case "-":
			ts.ignored = true
		case "nil":
			ts.nilOK = true
		case "tail":
			ts.tail = true
			//后续处理为：直接报错跳出
			if fi != lastPublic {
				return ts, fmt.Errorf(`rlp: invalid struct tag "tail" for %v.%s (must be on last field)`, typ, f.Name)
			}
			if f.Type.Kind() != reflect.Slice {
				return ts, fmt.Errorf(`rlp: invalid struct tag "tail" for %v.%s (field type is not slice)`, typ, f.Name)
			}
		default:
			return ts, fmt.Errorf(`rlp: unknown struct tag %q on %v.%s`, t, typ, f.Name)
		}
	}
	return ts, nil
}

func lastPublicField(typ reflect.Type) int { //找到结构体内最后一个exported字段的位置
	last := 0
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).PkgPath == "" {
			last = i
		}
	}
	return last
}

func (i *typeinfo) generate(typ reflect.Type, tags tags) {
	//生成typeinfo
	i.decoder, i.decoderErr = makeDecoder(typ, tags)
	i.writer, i.writerErr = makeWriter(typ, tags)
}

func isUnit(k reflect.Kind) bool { //
	return k >= reflect.Uint && k <= reflect.Uintptr
}
