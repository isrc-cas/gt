package config

import (
	"flag"
	"fmt"
	"os"
	"reflect"
	"time"

	"gopkg.in/yaml.v3"
)

// ParseFlags parses args and sets the result to config and options.
func ParseFlags(args []string, config, options interface{}) error {
	if len(args) < 2 {
		return nil
	}

	flagSet, n2fi, configPath := registerFlags(args[0], options)
	err := flagSet.Parse(args[1:])
	if err != nil {
		return err
	}

	err = Yaml2Interface(configPath, config)
	if err != nil {
		return err
	}

	return copyFlagsValue(options, flagSet, n2fi)
}

// Yaml2Interface 解析 yaml 配置文件
func Yaml2Interface(path *string, dstInterface interface{}) (err error) {
	// 当参数不合法时，直接返回 nil
	if path == nil || len(*path) == 0 ||
		dstInterface == nil {
		return
	}

	file, err := os.Open(*path)
	if err != nil {
		err = fmt.Errorf("open yaml file %q failed: %v", *path, err)
		return
	}
	defer func() {
		if e := file.Close(); e != nil {
			if err != nil {
				err = fmt.Errorf("%w and %s", err, e.Error())
			} else {
				err = e
			}
		}
	}()

	err = yaml.NewDecoder(file).Decode(dstInterface)
	if err != nil {
		err = fmt.Errorf("decode yaml file %q failed: %v", *path, err)
		return
	}

	return
}

func copyFlagsValue(dst interface{}, src *flag.FlagSet, name2FieldIndex map[string]int) (err error) {
	value := reflect.ValueOf(dst).Elem()
	src.Visit(func(f *flag.Flag) {
		i, ok := name2FieldIndex[f.Name]
		if !ok {
			return
		}
		fv := value.Field(i)
		if fv.Kind() == reflect.Ptr {
			fv = value.Field(i).Elem()
		}
		nv := reflect.ValueOf(f.Value.(flag.Getter).Get())
		fvt := fv.Type()
		if nv.Type().AssignableTo(fvt) {
			fv.Set(nv)
		} else {
			fv.Set(nv.Convert(fvt))
		}
	})
	return
}

func registerFlags(flagSetName string, options interface{}) (flagSet *flag.FlagSet, name2FieldIndex map[string]int, config *string) {
	flagSet = flag.NewFlagSet(flagSetName, flag.ExitOnError)
	name2FieldIndex = make(map[string]int)
	value := reflect.ValueOf(options).Elem()
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := value.Field(i)
		fieldType := typ.Field(i)
		name, ok := fieldType.Tag.Lookup("yaml")
		if !ok || name == "-" {
			name, ok = fieldType.Tag.Lookup("arg")
			if !ok {
				continue
			}
		}
		name2FieldIndex[name] = i
		usage := fieldType.Tag.Get("usage")
		switch v := field.Interface().(type) {
		case time.Duration:
			flagSet.Duration(name, v, usage)
			continue
		case StringSlice:
			ss := field.Interface().(StringSlice)
			flagSet.Var(&ss, name, usage)
			continue
		}
		// 用这种方式可以处理 type xxx string 的情况
		switch fieldType.Type.Kind() {
		case reflect.Uint16, reflect.Uint:
			flagSet.Uint(name, uint(field.Uint()), usage)
		case reflect.Int:
			flagSet.Int(name, int(field.Int()), usage)
		case reflect.Int64:
			flagSet.Int64(name, field.Int(), usage)
		case reflect.Float64:
			flagSet.Float64(name, field.Float(), usage)
		case reflect.String:
			if name != "config" {
				flagSet.String(name, field.String(), usage)
			} else {
				config = flagSet.String(name, field.String(), usage)
			}
		case reflect.Bool:
			flagSet.Bool(name, field.Bool(), usage)
		default:
			panic(fmt.Sprintf("not supported type %s of field %s", fieldType.Type.Kind().String(), fieldType.Name))
		}
	}
	return
}

// ShowUsage generates and prints the usage document of options.
func ShowUsage(options interface{}) {
	if options == nil {
		panic("options can not be nil")
	}
	flagSet, _, _ := registerFlags(os.Args[0], options)
	flagSet.Usage()
}
