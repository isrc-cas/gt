package util

import (
	"math/rand"
	"strconv"
	"time"
)

// RandomString 随机字符串
func RandomString(n int) string {
	letters := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	r := rand.New(rand.NewSource(time.Now().Unix()))

	s := make([]rune, n)
	for i := range s {
		s[i] = letters[r.Intn(len(letters))]
	}
	return string(s)
}

// RandomPort 从 [10000,65535] 中随机返回一个数字
func RandomPort() string {
	rand.Seed(time.Now().UnixNano())
	min := 10000
	max := 65535
	n := rand.Intn(max - min)
	n += min
	return strconv.FormatInt(int64(n), 10)
}
