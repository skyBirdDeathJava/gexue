package agent

import (
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// dsmlAwareModel 包装底层 ChatModel：把模型输出的 DSML 工具调用文本转成结构化 ToolCalls。
// 背景：deepseek-v4-flash 等模型在收到工具定义后，默认用 DSML 标记表达工具调用
// （<dsml_tool_call>{"name","arguments"}</dsml_tool_call> 或 <|DSML|invoke name="X">...），
// 而 ADK 只认结构化 tool_calls —— DSML 会被当纯文本输出、工具不执行。这里统一转换。
// 代价：流式路径需缓冲整段判断（自由文本由"逐 token"退化为"一次整段"；工具/结构性帧仍实时）。
type dsmlAwareModel struct {
	inner einoModel.ChatModel
	log   *zap.Logger // 诊断：记录 DSML→tool_calls 转换（定位转换链路问题）
}

func (m *dsmlAwareModel) Generate(ctx context.Context, msgs []*schema.Message, opts ...einoModel.Option) (*schema.Message, error) {
	logRequestMsgs(m.log, msgs) // 诊断：dump 发给模型的完整请求历史，排查 tool_calls/tool_result 是否配对
	msg, err := m.inner.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return msg, nil
	}
	if m.log != nil {
		m.log.Warn("model generate raw",
			zap.String("role", string(msg.Role)),
			zap.Int("toolcalls", len(msg.ToolCalls)),
			zap.String("content", truncateTrace(msg.Content, 200)))
	}
	if name, args, ok := parseDSMLToolCall(msg.Content); ok {
		m.logDsmlConverted("generate", name, args)
		msg.Content = ""
		msg.ToolCalls = []schema.ToolCall{dsmlToolCall(name, args)}
		markAsToolCall(msg) // ADK 靠 finish_reason 判断工具调用，必须把 "stop" 改成 "tool_calls"
	}
	if m.log != nil {
		dump, _ := json.Marshal(msg)
		m.log.Warn("model generate after",
			zap.Int("toolcalls", len(msg.ToolCalls)),
			zap.Int("content_len", len(msg.Content)),
			zap.String("msg_json", string(dump)))
	}
	return msg, nil
}

func (m *dsmlAwareModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	reader, err := m.inner.Stream(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}

	// 首块探测：只有开头是 DSML 才缓冲转换；否则原样透传，保持 token 级流式
	first, err := reader.Recv()
	if err != nil {
		reader.Close()
		if err == io.EOF {
			return schema.StreamReaderFromArray[*schema.Message](nil), nil
		}
		return nil, err
	}
	if isDSMLStart(first.Content) {
		return m.bufferConvertStream(reader, first), nil
	}

	// 正常输出：透传 first + 后续（goroutine 喂 Pipe，保留流式）
	sr, sw := schema.Pipe[*schema.Message](16)
	go func() {
		defer sw.Close()
		defer reader.Close()
		if closed := sw.Send(first, nil); closed {
			return
		}
		for {
			chunk, err := reader.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				sw.Send(nil, err)
				return
			}
			if closed := sw.Send(chunk, nil); closed {
				return
			}
		}
	}()
	return sr, nil
}

// isDSMLStart 判断首个 chunk 是否以 DSML 标记开头（归一化全角分隔符 + 忽略大小写）。
// 容忍 <|dsml / <||dsml（全角 ｜｜ 归一化为 ||）/ <dsml 三种开头。
func isDSMLStart(text string) bool {
	text = strings.ToLower(strings.TrimSpace(normalizeDSMLDelim(text)))
	if !strings.HasPrefix(text, "<") {
		return false
	}
	rest := text[1:]
	i := 0
	for i < len(rest) && rest[i] == '|' {
		i++
	}
	return strings.HasPrefix(rest[i:], "dsml")
}

// bufferConvertStream DSML 开头：缓冲全部并转成结构化 tool_calls（罕见兜底）。
func (m *dsmlAwareModel) bufferConvertStream(reader *schema.StreamReader[*schema.Message], first *schema.Message) *schema.StreamReader[*schema.Message] {
	defer reader.Close()
	chunks := []*schema.Message{first}
	var sb strings.Builder
	sb.WriteString(first.Content)
	for {
		chunk, err := reader.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if chunk != nil {
			chunks = append(chunks, chunk)
			sb.WriteString(chunk.Content)
		}
	}
	if name, args, ok := parseDSMLToolCall(sb.String()); ok {
		m.logDsmlConverted("stream", name, args)
		converted := &schema.Message{
			Role:      schema.Assistant,
			ToolCalls: []schema.ToolCall{dsmlToolCall(name, args)},
		}
		markAsToolCall(converted)
		return schema.StreamReaderFromArray[*schema.Message]([]*schema.Message{converted})
	}
	return schema.StreamReaderFromArray[*schema.Message](chunks)
}

func (m *dsmlAwareModel) logDsmlConverted(path, name, args string) {
	if m.log != nil {
		m.log.Warn("dsml converted to tool_calls", zap.String("path", path), zap.String("tool", name), zap.String("args", args))
	}
}

// logRequestMsgs 诊断：逐条 dump 发给模型的请求消息，检查 tool_calls 消息与 tool_result 消息是否配对。
// 排查方向：历史含工具调用但当前 tools 缺 / tool_calls 缺对应 tool_result → DeepSeek 可能回退 DSML。
func logRequestMsgs(log *zap.Logger, msgs []*schema.Message) {
	if log == nil {
		return
	}
	for i, m := range msgs {
		if m == nil {
			continue
		}
		log.Warn("req msg",
			zap.Int("i", i),
			zap.String("role", string(m.Role)),
			zap.Int("tool_calls", len(m.ToolCalls)),
			zap.String("tool_call_id", m.ToolCallID),
			zap.String("content", truncateTrace(m.Content, 60)))
	}
}

// markAsToolCall 设置消息的 finish_reason=tool_calls。
// ADK react 的 toolCallCheck 依赖 finish_reason 决定是否路由到工具节点；
// 转换 DSML 得到的消息其 finish_reason 仍是模型原生的 "stop"，会被当作最终答案而不执行工具。
func markAsToolCall(msg *schema.Message) {
	if msg.ResponseMeta == nil {
		msg.ResponseMeta = &schema.ResponseMeta{}
	}
	msg.ResponseMeta.FinishReason = "tool_calls"
}

func (m *dsmlAwareModel) BindTools(tools []*schema.ToolInfo) error {
	return m.inner.BindTools(tools)
}

func dsmlToolCall(name, args string) schema.ToolCall {
	idx := 0
	return schema.ToolCall{
		Index:    &idx, // 与真实模型原生 tool_calls 一致（带 index:0）
		ID:       "dsml_tool_call",
		Type:     "function",
		Function: schema.FunctionCall{Name: name, Arguments: args},
	}
}

// dsmlToolCallRe 等：分隔符统一按 ASCII | 匹配（parseDSMLToolCall 先归一化全角 ｜/‖ → |）。
// 实际形态可能是 <|DSML|invoke>（单个 |）或 <||DSML|| invoke>（双 | + 空格），故用 \|+ 与 \s* 容忍。
var (
	dsmlToolCallRe = regexp.MustCompile(`(?is)<dsml_tool_call>\s*(.*?)\s*</dsml_tool_call>`)
	dsmlInvokeRe   = regexp.MustCompile(`(?is)<\|+\s*dsml\s*\|+\s*invoke name="([^"]+)"(.*?)(?:</\|+\s*dsml\s*\|+\s*invoke>|$)`)
	dsmlParamRe    = regexp.MustCompile(`(?is)<\|+\s*dsml\s*\|+\s*parameter name="([^"]+)"[^>]*>(.*?)</\|+\s*dsml\s*\|+\s*parameter>`)
	dsmlCallRe     = regexp.MustCompile(`(?is)<dsml>call\s+([a-z_]+)\s+with\s+(.*?)</dsml>`)
)

// normalizeDSMLDelim 把模型输出的全角 ｜（U+FF5C）、双竖线 ‖（U+2016）等分隔符统一成 ASCII |。
// 实际观测：deepseek-v4-flash 输出的是全角双竖线 ｜｜（如 <｜｜DSML｜｜ invoke>）。
// 用 \u 转义（Go 字符串语义）保证匹配到正确码点，避免字面量错成视觉相似字符。
func normalizeDSMLDelim(text string) string {
	return strings.NewReplacer("｜", "|", "‖", "|").Replace(text)
}

// parseDSMLToolCall 识别模型输出里的 DSML 工具调用块，返回 (工具名, 参数JSON, 是否命中)。
// 支持两种形态：
//   ① <dsml_tool_call>{"name":"search_kb","arguments":{"query":"..."}}</dsml_tool_call>
//   ② <|DSML|calls>...<|DSML|invoke name="X"><|DSML|parameter name="p">v</|DSML|parameter>...</|DSML|invoke>
func parseDSMLToolCall(text string) (name, argsJSON string, ok bool) {
	text = normalizeDSMLDelim(text) // 全角 ｜/‖ → |，统一走 ASCII 正则
	if m := dsmlToolCallRe.FindStringSubmatch(text); len(m) == 2 {
		var payload struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal([]byte(m[1]), &payload) == nil && payload.Name != "" {
			if len(payload.Arguments) > 0 {
				return payload.Name, string(payload.Arguments), true
			}
			return payload.Name, "{}", true
		}
	}

	if m := dsmlInvokeRe.FindStringSubmatch(text); len(m) >= 2 {
		name = m[1]
		params := map[string]json.RawMessage{}
		for _, pm := range dsmlParamRe.FindAllStringSubmatch(m[2], -1) {
			params[pm[1]] = json.RawMessage(strconv.Quote(pm[2]))
		}
		args, _ := json.Marshal(params)
		return name, string(args), true
	}

	if m := dsmlCallRe.FindStringSubmatch(text); len(m) == 3 {
		return m[1], m[2], true // <dsml>call search_kb with {...}</dsml>
	}
	return "", "", false
}
