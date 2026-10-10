package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

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

func TestParseToolArgs(t *testing.T) {
	v, errMsg := parseToolArgs[toolSaveFollowupArgs](`{"content":"再想想","difficulty":2,"qtype":"fill"}`)
	if errMsg != "" {
		t.Fatalf("unexpected parse err: %s", errMsg)
	}
	if v.Content != "再想想" || v.Difficulty != 2 || v.QType != "fill" {
		t.Errorf("parse mismatch: %+v", v)
	}
	if _, errMsg := parseToolArgs[toolSaveFollowupArgs](`{bad json`); errMsg == "" {
		t.Error("expected parse error message on bad json")
	}
}

// ---- 工具处理器单测（fake ChatModel，无 DB/网络） ----

// fakeModel 可控地返回自由文本，用于内部子调用（当前工具不再调 LLM，保留用于构造）。
type fakeModel struct {
	gen string
}

func (f *fakeModel) Generate(_ context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.Message, error) {
	return &schema.Message{Role: schema.Assistant, Content: f.gen}, nil
}

func (f *fakeModel) Stream(_ context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{{Role: schema.Assistant, Content: f.gen}}), nil
}

func (f *fakeModel) BindTools(tools []*schema.ToolInfo) error { return nil }

// testAgent 构造 Model 为 fake 的 Agent（repo/emb 为空，避免触碰 DB/网络）。
func testAgent(m *fakeModel) *Agent {
	return &Agent{
		ChatModelAgentConfig: &adk.ChatModelAgentConfig{Model: m},
		tk: &toolkit{
			repo:   repo.NewQuizRepo(nil),
			kbRepo: repo.NewKnowledgeRepo(nil),
			log:    zap.NewNop(),
		},
	}
}

// newCtx 构造一个已通过前置检查的 Ctx（有 Session/KB + 领域依赖）。
func newCtx(msg string) *Ctx {
	qid := uint(1)
	return &Ctx{
		In: ChatInput{UserID: 1, SessionID: 1, Message: msg},
		tk: &toolkit{
			repo:   repo.NewQuizRepo(nil),
			kbRepo: repo.NewKnowledgeRepo(nil),
			log:    zap.NewNop(),
		},
		Session: &practiceSessionView{ID: 1, State: "idle", LastQuestionID: &qid},
		KB:      &kbView{ID: 1, GradeName: "3年级", SubjectName: "数学", SubjectCode: "math"},
	}
}

// TestSaveFollowup 直接测 save_followup 工具：产出 followup 帧并携带难度/题型。
func TestSaveFollowup(t *testing.T) {
	a := testAgent(&fakeModel{})
	c := newCtx("24")
	status := a.toolSaveFollowup(context.Background(), c, `{"content":"答对啦！再试试两位数的乘法？","mode":"extend","difficulty":4,"qtype":"select"}`)

	if strings.Contains(status, "失败") {
		t.Fatalf("save_followup should not fail: %s", status)
	}
	if len(c.Frames) != 1 || c.Frames[0].Kind != FrameFollowup {
		t.Fatalf("expected followup frame, got %+v", c.Frames)
	}
	f := c.Frames[0]
	if f.Difficulty != 4 || f.QType != "select" {
		t.Errorf("followup should carry difficulty/qtype: %+v", f)
	}
}

// TestSaveFollowupEmptyContent 追问缺少内容 → 返回错误串，不产出帧。
func TestSaveFollowupEmptyContent(t *testing.T) {
	a := testAgent(&fakeModel{})
	c := newCtx("不知道")
	status := a.toolSaveFollowup(context.Background(), c, `{}`)
	if !strings.Contains(status, "content") {
		t.Errorf("expected missing-content error, got %q", status)
	}
	if len(c.Frames) != 0 {
		t.Errorf("should not emit frame on missing content, got %+v", c.Frames)
	}
}

// TestSaveSolution 直接测 save_solution 工具：产出 help 帧（内容由专家撰写后传入）。
func TestSaveSolution(t *testing.T) {
	a := testAgent(&fakeModel{})
	c := newCtx("3+5×2=？")
	status := a.toolSaveSolution(context.Background(), c, `{"text":"先算乘法再算加法，答案是 24。"}`)
	if strings.Contains(status, "失败") {
		t.Fatalf("save_solution should not fail: %s", status)
	}
	if len(c.Frames) != 1 || c.Frames[0].Kind != FrameHelp {
		t.Fatalf("expected help frame, got %+v", c.Frames)
	}
}

// TestSaveQuestionsMissingPoint 保存题目缺知识点 → 返回错误串，不产出题帧。
func TestSaveQuestionsMissingPoint(t *testing.T) {
	a := testAgent(&fakeModel{})
	c := newCtx("出几道题")
	status := a.toolSaveQuestions(context.Background(), c, `{"difficulty":3}`)
	if !strings.Contains(status, "knowledge_point") {
		t.Errorf("expected missing knowledge_point error, got %q", status)
	}
	if len(c.Frames) != 0 {
		t.Errorf("should not emit question frame, got %+v", c.Frames)
	}
}

// ---- Agent 构造单测 ----

// TestNewAgentNoKey 无 API Key 时 llm 为 nil、路由未构建，可编译可调用（入口兜底）。
func TestNewAgentNoKey(t *testing.T) {
	a, err := NewAgent(repo.NewQuizRepo(nil), repo.NewKnowledgeRepo(nil), nil, nil, LLMConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAgent err: %v", err)
	}
	if a.Model != nil {
		t.Error("Model should be nil without API key")
	}
	if a.router != nil {
		t.Error("router should be nil without LLM")
	}
	if len(a.toolInfos()) != 8 {
		t.Errorf("expected 8 tool infos, got %d", len(a.toolInfos()))
	}
}

// TestNewAgentWithKey 有 API Key 时构建 ADK 路由（专家 + 每学科路由），无网络调用。
func TestNewAgentWithKey(t *testing.T) {
	a, err := NewAgent(repo.NewQuizRepo(nil), repo.NewKnowledgeRepo(nil), nil, nil,
		LLMConfig{Provider: "deepseek", APIKey: "test-key", Model: "deepseek-chat"}, nil)
	if err != nil {
		t.Fatalf("NewAgent err: %v", err)
	}
	if a.Model == nil {
		t.Error("Model should be built with API key")
	}
	if a.router == nil {
		t.Fatal("router should be built with LLM")
	}
	for _, code := range []string{"math", "yuwen", "english", "default"} {
		if a.router.resolve(code) == nil {
			t.Errorf("router should resolve %s", code)
		}
	}
}

// TestToolInfosSerializable 工具参数 schema 可序列化为 JSON（供大模型解析）。
func TestToolInfosSerializable(t *testing.T) {
	a, _ := NewAgent(repo.NewQuizRepo(nil), repo.NewKnowledgeRepo(nil), nil, nil, LLMConfig{}, nil)
	for _, ti := range a.toolInfos() {
		s, err := ti.ParamsOneOf.ToJSONSchema()
		if err != nil {
			t.Fatalf("tool %s ToJSONSchema err: %v", ti.Name, err)
		}
		if _, err := json.Marshal(s); err != nil {
			t.Fatalf("tool %s schema marshal err: %v", ti.Name, err)
		}
	}
}

// TestCtxEmit 帧在无 sink 时只入列；有 sink 时同时推送通道（流式）。
func TestCtxEmit(t *testing.T) {
	c := &Ctx{}
	c.emit(&Frame{Kind: FrameChitChat, Text: "hi"})
	if len(c.Frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(c.Frames))
	}

	out := make(chan *Frame, 1)
	c2 := &Ctx{sink: out}
	c2.emit(&Frame{Kind: FrameChitChat, Text: "hi"})
	select {
	case f := <-out:
		if f.Text != "hi" {
			t.Errorf("unexpected streamed frame: %+v", f)
		}
	default:
		t.Error("expected frame pushed to sink")
	}
}

// TestToolAdapterTrace 工具级 trace：toolAdapter 执行后把工具名/参数/结果记入 Ctx，供 persist 落库。
func TestToolAdapterTrace(t *testing.T) {
	a := testAgent(&fakeModel{})
	infos, handlers := a.toolInfosAndHandlers()

	var fInfo *schema.ToolInfo
	var fHandler toolHandler
	for _, ti := range infos {
		if ti.Name == "save_followup" {
			fInfo = ti
			fHandler = handlers[ti.Name]
		}
	}
	if fInfo == nil {
		t.Fatal("save_followup tool not found")
	}

	adapter := &toolAdapter{info: fInfo, run: fHandler}
	c := newCtx("24")
	ctx := withCtx(context.Background(), c)

	if _, err := adapter.InvokableRun(ctx, `{"content":"再想想","mode":"hint"}`); err != nil {
		t.Fatalf("InvokableRun err: %v", err)
	}
	if len(c.ToolCalls) != 1 || c.ToolCalls[0] != "save_followup" {
		t.Errorf("expected save_followup in ToolCalls, got %v", c.ToolCalls)
	}
	if len(c.ToolResults) != 1 || !strings.Contains(c.ToolResults[0], "save_followup") {
		t.Errorf("expected save_followup trace in ToolResults, got %v", c.ToolResults)
	}
}

// ---- transfer 集成测试：路由 SetSubAgents 移交专家，专家 ReAct 产出帧 ----

// scriptedModel 按调用顺序返回预编排消息（路由→专家），用于验证 transfer 事件流。
// 顺序：路由 1 次（transfer_to_agent 工具调用）→ 专家 2 次（原子工具调用 + 最终文本）。
type scriptedModel struct {
	steps []*schema.Message
	pos   int
}

func (m *scriptedModel) Generate(_ context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.Message, error) {
	return m.next(), nil
}

func (m *scriptedModel) Stream(_ context.Context, _ []*schema.Message, _ ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{m.next()}), nil
}

func (m *scriptedModel) BindTools(_ []*schema.ToolInfo) error { return nil }

func (m *scriptedModel) next() *schema.Message {
	if m.pos >= len(m.steps) {
		return &schema.Message{Role: schema.Assistant, Content: "好的"} // 兜底，防意外多轮挂死
	}
	msg := m.steps[m.pos]
	m.pos++
	return msg
}

// toolCallMsg 构造一次工具调用消息（arguments 为 JSON 字符串）。
func toolCallMsg(name, args string) *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:       "call_0",
			Type:     "function",
			Function: schema.FunctionCall{Name: name, Arguments: args},
		}},
	}
}

// transferTestAgent 构造已 buildRouter 的 Agent（Model 为可编排的 scriptedModel）。
// 复刻 NewAgent：配置模板 + toolkit + buildExpertConfigs + buildRouter。
func transferTestAgent(t *testing.T, m *scriptedModel) *Agent {
	t.Helper()
	a := &Agent{
		ChatModelAgentConfig: &adk.ChatModelAgentConfig{
			Name:          "default_router",
			Description:   "学习辅导路由：识别学生意图，派发出题/判分/追问/解题/总结专家",
			Instruction:   routerInstruction("学习"),
			MaxIterations: maxAgentIterations,
		},
		tk: &toolkit{
			repo:   repo.NewQuizRepo(nil),
			kbRepo: repo.NewKnowledgeRepo(nil),
			log:    zap.NewNop(),
		},
	}
	a.Model = m
	if err := a.buildExpertConfigs(); err != nil {
		t.Fatalf("buildExpertConfigs: %v", err)
	}
	if err := a.buildRouter(context.Background()); err != nil {
		t.Fatalf("buildRouter: %v", err)
	}
	return a
}

// TestParseTransferTarget 直接测 transfer 意图解析（参数格式由库固定为 {"agent_name": "..."}）。
func TestParseTransferTarget(t *testing.T) {
	// 命中：transfer_to_agent 参数取 agent_name
	mv := &adk.MessageVariant{Message: &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID: "c1", Type: "function",
			Function: schema.FunctionCall{Name: adk.TransferToAgentToolName, Arguments: `{"agent_name":"generate"}`},
		}},
	}}
	if got := parseTransferTarget(mv); got != "generate" {
		t.Errorf("parseTransferTarget = %q, want generate", got)
	}
	// 未命中：普通工具调用 → 空串
	mv2 := &adk.MessageVariant{Message: &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID: "c2",
			Function: schema.FunctionCall{Name: "search_kb", Arguments: `{"q":"加法"}`},
		}},
	}}
	if got := parseTransferTarget(mv2); got != "" {
		t.Errorf("parseTransferTarget = %q, want empty for non-transfer", got)
	}
	// 无工具调用 → 空串
	mv3 := &adk.MessageVariant{Message: &schema.Message{Role: schema.Assistant, Content: "你好"}}
	if got := parseTransferTarget(mv3); got != "" {
		t.Errorf("parseTransferTarget = %q, want empty for plain text", got)
	}
}

// TestRouterTransferFollowup 集成测试（流式）：路由 transfer_to_agent 移交 followup 专家，
// 验证 ① 意图 = transfer 目标专家 ② 专家帧经 sink 实时推送 ③ 工具日志去重 ④ 最终文本 = 专家末尾文本。
func TestRouterTransferFollowup(t *testing.T) {
	a := transferTestAgent(t, &scriptedModel{steps: []*schema.Message{
		toolCallMsg(adk.TransferToAgentToolName, `{"agent_name":"followup"}`),
		toolCallMsg("save_followup", `{"content":"再想想，5 可以分成几和几？","mode":"hint","difficulty":2,"qtype":"fill"}`),
		{Role: schema.Assistant, Content: "再想想，5 可以分成几和几？"},
	}})

	router := a.router.resolve("math")
	if router == nil {
		t.Fatal("math router should resolve")
	}

	c := newCtx("不知道怎么做")
	msgs := a.runtimeMessages(c)
	out := make(chan *Frame, 64)
	c.sink = out // 与真实 ChatStream 一致：结构性帧经 sink 实时推送
	runCtx := withCtx(context.Background(), c)

	finalText := a.streamRouter(runCtx, c, router, msgs, out)
	close(out)

	// ① 意图 = transfer 目标专家
	if c.AgentIntent != "followup" {
		t.Errorf("AgentIntent = %q, want followup", c.AgentIntent)
	}
	// ② 专家移交后产出 followup 帧（sink 收到 + c.Frames 记到）
	sawFollowup := false
	for f := range out {
		if f.Kind == FrameFollowup {
			sawFollowup = true
		}
	}
	if !sawFollowup {
		t.Error("expected followup frame streamed to sink")
	}
	if len(c.Frames) != 1 || c.Frames[0].Kind != FrameFollowup {
		t.Fatalf("expected 1 followup frame in c.Frames, got %+v", c.Frames)
	}
	if !strings.Contains(c.Frames[0].Text, "5 可以分成几和几") {
		t.Errorf("followup frame text mismatch: %q", c.Frames[0].Text)
	}
	// ③ 工具日志去重：路由 transfer 一次 + 专家原子工具一次（原子工具由 toolAdapter 内部记录）
	want := []string{adk.TransferToAgentToolName, "save_followup"}
	if len(c.ToolCalls) != len(want) {
		t.Fatalf("ToolCalls = %v, want %v", c.ToolCalls, want)
	}
	for i, w := range want {
		if c.ToolCalls[i] != w {
			t.Errorf("ToolCalls[%d] = %q, want %q", i, c.ToolCalls[i], w)
		}
	}
	if len(c.ToolResults) != 2 || !strings.Contains(c.ToolResults[0], adk.TransferToAgentToolName) {
		t.Errorf("ToolResults should have transfer trace first, got %v", c.ToolResults)
	}
	// ④ 最终自由文本 = 专家 ReAct 末尾文本
	if got := strings.TrimSpace(finalText); got != "再想想，5 可以分成几和几？" {
		t.Errorf("finalText = %q, want expert final text", got)
	}
}
