package config

import "strings"

// StringSlice 字符串切片，用于命令行数组解析
type StringSlice []string

// String flag.value 接口
func (ss *StringSlice) String() string {
	var builder strings.Builder
	for _, str := range *ss {
		builder.WriteString(str)
		builder.WriteByte('\n')
	}
	return builder.String()
}

// Set flag.value 接口
func (ss *StringSlice) Set(value string) error {
	*ss = append(*ss, value)
	return nil
}

// Get flag.Getter 接口
func (ss *StringSlice) Get() interface{} {
	return []string(*ss)
}
