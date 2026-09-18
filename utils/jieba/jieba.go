package jieba

import "github.com/yanyiwu/gojieba"

// 进程内只加载一次词典（NewJieba 约需 400ms，之前每次调用都重新加载）。
// gojieba 的 Cut 是只读操作，并发安全；AddWord 等 mutating 方法并发使用需自行加锁。
var x = gojieba.NewJieba()

func JieBa(s string) []string {
	return x.Cut(s, true)
}
