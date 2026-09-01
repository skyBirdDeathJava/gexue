package retrieval

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-ego/gse"
	"github.com/kljensen/snowball"
)

// 领域词表默认路径（相对项目根，go run main.go / 测试均在根目录运行）。
const defaultDomainDictPath = "configs/dict/edu_dict.txt"

// domainDictEnv 可通过环境变量覆盖领域词表路径。
const domainDictEnv = "GEXUE_DICT"

// segmenter 封装 gse 分词器（基于词典、纯 Go、无 CGO）。
// 词典构成：gse 内嵌通用中文词典（NewEmbed 自动加载）+ 领域词表（追加，用户词优先）。
// 注意：gse.Segmenter 是值类型，必须在"所有词典加载完成后"再整体赋值给 s.seg，
// 否则 LoadDictStr 只作用于局部变量，领域词会丢失。
type segmenter struct {
	seg gse.Segmenter
}

// domainDictDefaultFreq 领域词默认词频。
// gse DAG 走最短路：distance(token) = log2(totalFreq) - log2(freq)，
// 词频越高 distance 越小、越优先。领域词若按内嵌词典默认 2.0 词频加入，
// 会在「整词 vs 拆词」的最短路比拼中落败（如"配方法"被拆成"配 方法"）。
// 实测 freq=100 已足以让领域词整词胜出且不干扰内嵌词。
const domainDictDefaultFreq = 100

// loadDomainDict 加载领域词表到 gse 词典：
//   - 逐行解析：跳过空行与 # 注释；每行一词，可带"词 词频"（无词频用 domainDictDefaultFreq）；
//   - 用 AddToken 逐词加入（而非 LoadDictStr）：LoadDictStr 按 \n 分割，词表末尾换行会产生
//     空串行、向 cedar trie 插入空 key 导致整个词典损坏（实测内嵌词全部被拆成单字），
//     且其 Size() 对无词频词的过滤行为依赖 TextFreq/LoadNoFreq，行为不可控；
//   - AddToken 对 trie 中已存在的词（内嵌通用词）自动跳过（不重复、不降频），
//     领域新词（配方法、松下问童子等）按高词频加入，DAG 优先选择整词。
// 注意：gse.Segmenter 是值类型，必须在所有词典加载完成后才整体赋值给 s.seg，
// 否则 AddToken/LoadDictStr 只作用于局部变量，领域词会丢失。
func loadDomainDict(seg *gse.Segmenter, data []byte) error {
	lines := strings.Split(string(data), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		freq := float64(domainDictDefaultFreq)
		if len(fields) > 1 {
			if f, err := strconv.ParseFloat(fields[1], 64); err == nil && f > 0 {
				freq = f
			}
		}
		if err := seg.AddToken(fields[0], freq); err != nil {
			return fmt.Errorf("add domain token %q: %w", fields[0], err)
		}
	}
	// 重新计算 DAG 路径距离（词频变化影响切分偏好）
	seg.CalcToken()
	return nil
}

// newSegmenter 构建分词器：
//   - 通用词典：gse.NewEmbed("zh") 加载内嵌中文通用词典；
//   - 领域词表：domainDictPath 非空且文件存在时追加（AddToken 逐词，用户词优先）；
//   - domainDictPath 为空或文件缺失：仅用通用词典，不报错（优雅降级，检索不中断）。
func newSegmenter(domainDictPath string) (*segmenter, error) {
	seg, err := gse.NewEmbed("zh")
	if err != nil {
		return nil, fmt.Errorf("gse.NewEmbed: %w", err)
	}
	if domainDictPath == "" {
		domainDictPath = os.Getenv(domainDictEnv)
	}
	if domainDictPath != "" {
		data, err := os.ReadFile(domainDictPath)
		if err != nil {
			// 领域词表缺失不致命：仅用通用词典
		} else if err := loadDomainDict(&seg, data); err != nil {
			return nil, fmt.Errorf("load domain dict %s: %w", domainDictPath, err)
		}
	}
	// 关键：所有词典加载完成后才把值写入 s.seg
	return &segmenter{seg: seg}, nil
}

// asciiWordRe 匹配英文/数字整词（撇号并入词内）。
// 用正则从原始文本提取英文词，而不是依赖 gse 切分——gse 会把撇号当分隔符
// （"don't" 切成 don / ' / t），导致缩略语后半（t）被单字符规则丢弃。
// 含撇号整词（don't、children's、o'clock）一次匹配，词干还原时再处理撇号形态。
var asciiWordRe = regexp.MustCompile(`[a-zA-Z0-9']+`)

// tokenize 对文本分词：
//   - 英文/数字：正则整词提取（撇号并入）→ 小写 → 去首尾撇号 → snowball 词干还原 → 长度>1 保留。
//     词干还原让 running/runs/run 归并为 run；children's 的 's 在还原中去掉。
//   - 中文段：交给 gse 词典切分（DAG 模式），词典词输出整词（如"一元二次方程""配方法"），
//     未登录词切成单字；
//   - bigram 回退：连续两个 CJK 单字组成二字词（未登录词碎片），覆盖未登录多字词；
//   - 标点/空白为分隔符丢弃。
func (s *segmenter) tokenize(text string) []string {
	var out []string
	last := 0
	for _, loc := range asciiWordRe.FindAllStringIndex(text, -1) {
		// 英文词之间的文本段（中文/标点）交给 gse 切分
		if loc[0] > last {
			out = s.cutCJK(out, text[last:loc[0]])
		}
		word := strings.ToLower(strings.Trim(text[loc[0]:loc[1]], "'"))
		if len(word) > 1 { // 单字母/数字丢弃（弱信号）
			out = append(out, stemEnglish(word))
		}
		last = loc[1]
	}
	if last < len(text) {
		out = s.cutCJK(out, text[last:])
	}
	// bigram 回退：仅连续两个 CJK 单字（未登录词碎片）组合，避免对词典词/英文词跨界造噪声
	for i := 0; i < len(out)-1; i++ {
		if len(out[i]) != utf8.RuneLen('一') || len(out[i+1]) != utf8.RuneLen('一') {
			continue // 非 CJK 单字（3 字节）跳过
		}
		r1, _ := utf8.DecodeRuneInString(out[i])
		r2, _ := utf8.DecodeRuneInString(out[i+1])
		if isCJK(r1) && isCJK(r2) {
			out = append(out, out[i]+out[i+1])
		}
	}
	return out
}

// cutCJK 将一段文本（中文+标点）交给 gse 切分，仅保留 CJK 词。
func (s *segmenter) cutCJK(out []string, segText string) []string {
	for _, w := range s.seg.Cut(segText, false) { // DAG 精确模式
		if w == "" {
			continue
		}
		first, _ := utf8.DecodeRuneInString(w)
		if isCJK(first) {
			out = append(out, w)
		}
	}
	return out
}

// stemEnglish 英文词干还原（snowball English / Porter 算法）。
//   - 规则形态合并：running/runs/run → run；studies/studying → studi；children's → children；
//   - 撇号缩略词（don't、can't、o'clock）保持原样，不强行还原；
//   - 纯数字（1234）经还原后不变；还原出错时原样返回（检索不中断）。
func stemEnglish(word string) string {
	stemmed, err := snowball.Stem(word, "english", false)
	if err != nil || stemmed == "" {
		return word
	}
	return stemmed
}

var (
	segOnce  sync.Once
	segInst  *segmenter
	segError error
)

// defaultSegmenter 包级共享分词器（懒加载单例）。
// 首次调用时加载通用词典 + 领域词表；后续复用。并发安全。
func defaultSegmenter() *segmenter {
	segOnce.Do(func() {
		segInst, segError = newSegmenter(defaultDomainDictPath)
	})
	if segError != nil {
		// 仅当 NewEmbed 内嵌词典不可用才可能走到（构建期即应失败）；
		// 兜底退化为仅通用词典，避免检索中断。
		seg, _ := gse.NewEmbed("zh")
		return &segmenter{seg: seg}
	}
	return segInst
}
