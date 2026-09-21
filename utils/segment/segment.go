// Package segment 是中文分词的唯一入口：BM25 的查询词和文档都用它切。
// 底层换成纯 Go 的 gse，词表内置在二进制里（gse.NewEmbed），
// 既不需要词典文件、也不再引入 C++/CGO 依赖。
package segment

import (
	"strings"

	"github.com/go-ego/gse"
)

// 词表只需加载一次（解析内置词表，几十毫秒）。
// 想更省内存可以改成 gse.NewEmbed("zh_s")，只装简体词表。
var seg = newSegmenter()

func newSegmenter() *gse.Segmenter {
	s, err := gse.NewEmbed()
	if err != nil {
		panic("加载 gse 内置词表失败: " + err.Error())
	}
	return &s
}

// Cut 按精确模式切词，打开 HMM 处理未登录词（与原 jieba Cut(s, true) 对应）。
// 英文会自动转小写，查询和文档两边一致；
// gse 会把空白本身也当成一个词返回（" "、"  "），对 BM25 没有意义，过滤掉。
func Cut(s string) []string {
	words := seg.Cut(s, true)
	cut := make([]string, 0, len(words))
	for _, w := range words {
		if strings.TrimSpace(w) == "" {
			continue
		}
		cut = append(cut, w)
	}
	return cut
}
