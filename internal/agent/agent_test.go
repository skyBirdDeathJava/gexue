package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"gexue/internal/model"
	"gexue/internal/repo"
)

// ---- 纯逻辑单测（无需 DB / 网络） ----

func TestNormalizeAnswer(t *testing.T) {
	cases := []struct{ in, want string }{
		{" B ", "B"},
		{"b", "B"},
		{"答案B", "B"},
		{"2 4", "24"},
		{"24 个", "24"},
	}
	for _, c := range cases {
		if got := normalizeAnswer(c.in); got != c.want {
			t.Errorf("normalizeAnswer(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestRuleCheck(t *testing.T) {
	if !ruleCheck(&model.Question{Answer: "B"}, "b") {
		t.Error("ruleCheck should accept case-insensitive B")
	}
	if ruleCheck(&model.Question{Answer: "B"}, "C") {
		t.Error("ruleCheck should reject C")
	}
	if !ruleCheck(&model.Question{Answer: "24"}, "24 个") {
		t.Error("ruleCheck should tolerate extra non-alnum")
	}
}

func TestTrimJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  {\"a\":1}  ", "{\"a\":1}"},
		{"```json\n{\"a\":1}\n```", "{\"a\":1}"},
		{"前文{\"a\":1}后文", "{\"a\":1}"},
	}
	for _, c := range cases {
		if got := trimJSON(c.in); got != c.want {
			t.Errorf("trimJSON(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseToolArgs(t *testing.T) {
	v, errMsg := parseToolArgs[toolFollowupArgs](`{"content":"再想想","difficulty":2,"qtype":"fill"}`)
	if errMsg != "" {
		t.Fatalf("unexpected parse err: %s", errMsg)
	}
	if v.Content != "再想想" || v.Difficulty != 2 || v.QType != "fill" {
		t.Errorf("parse mismatch: %+v", v)
	}
	if _, errMsg := parseToolArgs[toolFollowupArgs](`{bad json`); errMsg == "" {
		t.Error("expected parse error message on bad json")
	}
}

// ---- Agent 主循环单测（可返回工具调用的 fake ChatModel，无 DB） ----

// fakeModel 可控地返回「自由文本」或「工具调用」，用于驱动主循环。
// turn 依次返回列表中的响应；耗尽后返回最后一个。
type fakeModel struct {
	gen   string            // 自由文本回复（Generate 默认）
	turns []*schema.Message // 按调用顺序返回的响应
	idx   int
	binds []*schema.ToolInfo // 记录 BindTools 收到的工具
}

func (f *fakeModel) Generate(_ context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.Message, error) {
	if len(f.turns) > 0 {
		if f.idx < len(f.turns) {
			m := f.turns[f.idx]
			f.idx++
			return m, nil
		}
		return f.turns[len(f.turns)-1], nil
	}
	return &schema.Message{Role: schema.Assistant, Content: f.gen}, nil
}

func (f *fakeModel) Stream(_ context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{{Role: schema.Assistant, Content: f.gen}}), nil
}

func (f *fakeModel) BindTools(tools []*schema.ToolInfo) error { f.binds = tools; return nil }

// toolMsg 构造一条带工具调用的 assistant 消息。
func toolMsg(name, args string) *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID: "call-1",
			Function: schema.FunctionCall{
				Name:      name,
				Arguments: args,
			},
		}},
	}
}

// testAgent 构造 tools 已注册、llm 为 fake 的 Agent（repo/emb 为空，避免触碰 DB/网络）。
// 注意：fake model 无实际工具执行能力，主循环测试直接断言工具调用被正确执行/产出帧。
func testAgent(m *fakeModel) *Agent {
	a := &Agent{
		repo:   repo.NewQuizRepo(nil),
		kbRepo: repo.NewKnowledgeRepo(nil),
		log:    nil,
		tools:  map[string]toolHandler{},
	}
	a.registerTools()
	a.llm = m
	return a
}

// newLoopCtx 构造一个已通过前置检查的 Ctx（有 Session/KB）。
func newLoopCtx(msg string) *Ctx {
	qid := uint(1)
	return &Ctx{
		In:      ChatInput{UserID: 1, SessionID: 1, Message: msg},
		Session: &practiceSessionView{ID: 1, State: "idle", LastQuestionID: &qid},
		KB:      &kbView{ID: 1, GradeName: "3年级", SubjectName: "数学"},
	}
}

// TestLoopFreeTextReply 模型直接给自由文本（闲聊）→ 产出 chitchat 回复帧。
func TestLoopFreeTextReply(t *testing.T) {
	a := testAgent(&fakeModel{gen: "哈哈，我们继续学习吧～"})
	c := newLoopCtx("今天天气真好")
	a.runLoop(context.Background(), c)

	if len(c.Frames) != 1 || c.Frames[0].Kind != FrameChitChat {
		t.Fatalf("expected one chitchat frame, got %+v", c.Frames)
	}
	if !strings.Contains(c.Frames[0].Text, "继续学习") {
		t.Errorf("unexpected reply: %q", c.Frames[0].Text)
	}
}

// TestLoopToolThenFreeText 模型先调工具（这里工具执行可能因 DB 为空失败），随后给出自由文本。
// 验证循环不因工具失败而崩溃，且自由文本能成为回复。
func TestLoopToolThenFreeText(t *testing.T) {
	m := &fakeModel{turns: []*schema.Message{
		toolMsg("follow_up", `{"content":"先想想 5×2 等于多少？","mode":"hint","difficulty":2,"qtype":"fill"}`),
		{Content: "想好了吗？"},
	}}
	a := testAgent(m)
	c := newLoopCtx("24")
	a.runLoop(context.Background(), c)

	// follow_up 无需 DB，直接产出 followup 帧；后续自由文本因已有帧被丢弃
	if len(c.Frames) != 1 || c.Frames[0].Kind != FrameFollowup {
		t.Fatalf("expected followup frame, got %+v", c.Frames)
	}
	if c.Frames[0].Text != "先想想 5×2 等于多少？" {
		t.Errorf("unexpected followup text: %q", c.Frames[0].Text)
	}
}

// TestToolFollowup 直接测 follow_up 工具：产出 followup 帧并携带难度/题型（由 Agent 按作答选择）。
func TestToolFollowup(t *testing.T) {
	a := testAgent(&fakeModel{})
	c := newLoopCtx("24")
	status := a.toolFollowup(context.Background(), c, `{"content":"答对啦！再试试两位数的乘法？","mode":"extend","difficulty":4,"qtype":"select"}`)

	if strings.Contains(status, "失败") {
		t.Fatalf("followup should not fail: %s", status)
	}
	if len(c.Frames) != 1 || c.Frames[0].Kind != FrameFollowup {
		t.Fatalf("expected followup frame, got %+v", c.Frames)
	}
	f := c.Frames[0]
	if f.Difficulty != 4 || f.QType != "select" {
		t.Errorf("followup should carry difficulty/qtype chosen by agent: %+v", f)
	}
}

// TestToolFollowupEmptyContent 追问缺少内容 → 返回错误串（模型可自我纠正），不产出帧。
func TestToolFollowupEmptyContent(t *testing.T) {
	a := testAgent(&fakeModel{})
	c := newLoopCtx("不知道")
	status := a.toolFollowup(context.Background(), c, `{}`)
	if !strings.Contains(status, "content") {
		t.Errorf("expected missing-content error, got %q", status)
	}
	if len(c.Frames) != 0 {
		t.Errorf("should not emit frame on missing content, got %+v", c.Frames)
	}
}

// TestToolHelp 直接测 help_solve 工具：产出 help 帧。
func TestToolHelp(t *testing.T) {
	a := testAgent(&fakeModel{gen: "先算乘法再算加法，答案是 24。"})
	c := newLoopCtx("3+5×2=？")
	status := a.toolHelp(context.Background(), c, `{"question":"3+5×2=？"}`)
	if strings.Contains(status, "失败") {
		t.Fatalf("help should not fail: %s", status)
	}
	if len(c.Frames) != 1 || c.Frames[0].Kind != FrameHelp {
		t.Fatalf("expected help frame, got %+v", c.Frames)
	}
}

// TestToolGenerateMissingPoint 出题缺知识点 → 返回错误串，不产出题帧。
func TestToolGenerateMissingPoint(t *testing.T) {
	a := testAgent(&fakeModel{})
	c := newLoopCtx("出几道题")
	status := a.toolGenerate(context.Background(), c, `{"difficulty":3}`)
	if !strings.Contains(status, "knowledge_point") {
		t.Errorf("expected missing knowledge_point error, got %q", status)
	}
	if len(c.Frames) != 0 {
		t.Errorf("should not emit question frame, got %+v", c.Frames)
	}
}

// TestNewAgentNoKey 无 API Key 时 llm 为 nil，工具注册好，可编译可调用（入口兜底）。
func TestNewAgentNoKey(t *testing.T) {
	a, err := NewAgent(repo.NewQuizRepo(nil), repo.NewKnowledgeRepo(nil), nil, nil, LLMConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAgent err: %v", err)
	}
	if a.llm != nil {
		t.Error("llm should be nil without API key")
	}
	if len(a.tools) != 5 || len(a.toolInfos) != 5 {
		t.Errorf("expected 5 tools registered, got tools=%d infos=%d", len(a.tools), len(a.toolInfos))
	}
}

// TestToolInfosSerializable 工具参数 schema 可序列化为 JSON（供大模型解析）。
func TestToolInfosSerializable(t *testing.T) {
	a, _ := NewAgent(repo.NewQuizRepo(nil), repo.NewKnowledgeRepo(nil), nil, nil, LLMConfig{}, nil)
	for _, ti := range a.toolInfos {
		s, err := ti.ParamsOneOf.ToJSONSchema()
		if err != nil {
			t.Fatalf("tool %s ToJSONSchema err: %v", ti.Name, err)
		}
		if _, err := json.Marshal(s); err != nil {
			t.Fatalf("tool %s schema marshal err: %v", ti.Name, err)
		}
	}
}
