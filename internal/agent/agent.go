package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	"gexue/internal/embedding"
	"gexue/internal/repo"
	"gexue/internal/retrieval"
)

// ChatInput 对话入口（一次消息）。多轮对话由前端按会话累积，本 Agent 每次只处理一条。
type ChatInput struct {
	UserID    uint   `json:"user_id"`
	SessionID uint   `json:"session_id"`
	Message   string `json:"message"`
}

// Frame SSE 流式帧。
// kind ∈ select_kb / params_ask / question_list / feedback / followup / help / chitchat / summary / delta
type Frame struct {
	Intent string `json:"intent"`
	// Kind 由 Agent 决策，前端按 kind 渲染不同卡片样式；text/analysis/recommend 仅在特定 kind 下有意义。
	Kind      string         `json:"kind"`
	Text      string         `json:"text,omitempty"`
	Questions []QuestionView `json:"questions,omitempty"`
	IsCorrect *bool          `json:"is_correct,omitempty"`
	Analysis  string         `json:"analysis,omitempty"`
	Recommend *RecommendView `json:"recommend,omitempty"`
	SessionID uint           `json:"session_id,omitempty"`
	State     string         `json:"state,omitempty"`
	// Difficulty/QType 由 Agent 按学生作答自主选择（追问/变式记录用，前端可忽略）。
	Difficulty int    `json:"difficulty,omitempty"`
	QType      string `json:"qtype,omitempty"`
}

// QuestionView 出题结果（前端渲染题目卡片；不下发答案/解析，判分在服务端）。
type QuestionView struct {
	ID         uint     `json:"id"`
	QType      string   `json:"qtype"`
	Stem       string   `json:"stem"`
	Options    []string `json:"options,omitempty"`
	Difficulty int      `json:"difficulty"`
}

// RecommendView 总结帧携带的推荐知识点。
type RecommendView struct {
	KPName string `json:"kp_name"`
	Reason string `json:"reason"`
}

// 帧类型常量。渲染时前端按 kind 区分卡片样式。
const (
	FrameSelectKB     = "select_kb"     // 未选/无效知识库提示
	FrameParamsAsk    = "params_ask"    // 缺参/无可做题目等引导
	FrameQuestionList = "question_list" // 出题结果
	FrameFeedback     = "feedback"      // 判分
	FrameFollowup     = "followup"      // 追问/变式
	FrameHelp         = "help"          // 解题
	FrameChitChat     = "chitchat"      // 一般对话回复（Agent 无工具调用时的自由回答）
	FrameSummary      = "summary"       // 结束总结+推荐
	FrameNoLLM        = "no_llm"        // 未配置大模型提示
	FrameDelta        = "delta"         // 流式自由文本增量（token 级，前端直接 append）
)

// LLMConfig 大模型接入（DeepSeek V4 / 方舟，均 OpenAI 兼容）。
type LLMConfig struct {
	Provider string // deepseek / ark（仅用于日志标识）
	APIKey   string
	BaseURL  string
	Model    string
}

// Agent 对话式辅导 Agent：ADK 多 Agent，按 (学科, 年级) 从 DB 动态加载 agent 树。
// 每个学科+年级 = 1 个路由 Agent + 5 个专家子 agent（generate/answer/followup/help/summarize），
// 提示词（题型/知识点/判分逻辑）按年级差异化，存于 agent_config 表，经 agentRegistry 懒加载 + 缓存。
// 领域依赖（repo/kbRepo/emb/retriever/log）收拢进 toolkit，经每请求的 Ctx.tk 供工具读取。
type Agent struct {
	llm      model.ChatModel   // 全局默认模型（agent 行未指定 model 时用）；nil 时 no_llm 兜底
	registry *agentRegistry    // 按 (学科,年级) 懒加载的 ADK agent 树（路由 + 专家）
	tk       *toolkit          // 领域依赖，用于 seed 每请求 Ctx
}

// toolkit 领域依赖：工具的原子能力 + 会话流（checkKb/buildCtx/persist）所需的数据访问。
// 与 ADK 编排无关；经 Ctx.tk 在请求内读取，避免把基础设施平铺在 Agent 上。
type toolkit struct {
	repo      *repo.QuizRepo
	kbRepo    *repo.KnowledgeRepo
	emb       embedding.Embedder
	retriever *retrieval.Retriever // 双路召回检索编排（nil 时回退旧向量检索）
	log       *zap.Logger
}

// toolHandler 工具处理器：解析参数 JSON，执行并产出 Frames/状态，返回给 LLM 的内部状态串。
// 返回串仅作 LLM 继续决策的上下文，不直接展示给学生（学生看到的是 Frame）。
type toolHandler func(ctx context.Context, c *Ctx, argsJSON string) string

// NewAgent 构造 Agent；llmCfg.APIKey 为空时 llm 置 nil（无 key 可编译可测试，入口兜底）。
// retriever 可为 nil（未注入时 embSearchRaw 回退旧向量检索）。
// agentRepo 提供 agent_config 表查询（动态加载各学科+年级的 agent 树）。
func NewAgent(qr *repo.QuizRepo, kr *repo.KnowledgeRepo, emb embedding.Embedder, retriever *retrieval.Retriever, agentRepo *repo.AgentRepo, llmCfg LLMConfig, log *zap.Logger) (*Agent, error) {
	a := &Agent{
		tk: &toolkit{repo: qr, kbRepo: kr, emb: emb, retriever: retriever, log: log},
	}

	if llmCfg.APIKey != "" {
		cm, err := buildChatModel(context.Background(), llmCfg)
		if err != nil {
			return nil, fmt.Errorf("init chat model: %w", err)
		}
		// 包一层 DSML 转换器：deepseek 等模型默认用 DSML 标记表达工具调用，ADK 只认结构化 tool_calls。
		a.llm = &dsmlAwareModel{inner: cm, log: log}
	}

	// 动态 agent 注册表（按 (学科,年级) 懒加载 + 缓存）；无 LLM 时跳过，入口走 no_llm 兜底。
	if a.llm != nil {
		a.registry = &agentRegistry{
			lister:    agentRepo,
			llm:       a.llm,
			defByName: a.toolDefsByName(),
			cache:     make(map[agentScope]adk.Agent),
			log:       log,
		}
	}
	return a, nil
}

// ChatStream 流式处理一次用户消息，返回帧通道（实时推送结构性帧 + 自由文本 delta）。
// 前置检查失败时错误返回；会话/知识库短路帧会先推入通道再关闭。
func (a *Agent) ChatStream(ctx context.Context, in ChatInput) (<-chan *Frame, error) {
	out := make(chan *Frame, 64)
	// c.sink == out
	c := &Ctx{In: in, tk: a.tk, sink: out, runCtx: ctx}

	// ① 前置硬检查
	if err := a.checkKb(ctx, c); err != nil {
		close(out)
		return nil, err
	}
	if c.Done {
		a.persist(ctx, c)
		close(out)
		return out, nil
	}

	// ② 上下文
	if err := a.buildCtx(ctx, c); err != nil {
		close(out)
		return nil, err
	}

	// ③ 无 LLM 兜底
	if a.llm == nil {
		c.emit(a.noLLMFrame(in))
		a.persist(ctx, c)
		close(out)
		return out, nil
	}

	// ④ 动态加载该 (学科,年级) 的路由 agent（懒构建 + 缓存）；未配置时给友好提示
	router, err := a.registry.resolve(ctx, c.KB.SubjectID, c.KB.GradeID)
	if err != nil {
		c.emit(&Frame{Intent: "chitchat", Kind: FrameChitChat, Text: "辅导配置加载失败，请稍后再试。"})
		a.persist(ctx, c)
		close(out)
		return out, nil
	}
	if router == nil {
		c.emit(&Frame{Intent: "chitchat", Kind: FrameChitChat, Text: "这个年级的语文辅导还没准备好，换个年级试试吧～"})
		a.persist(ctx, c)
		close(out)
		return out, nil
	}

	msgs := a.runtimeMessages(c)
	runCtx := withCtx(ctx, c)

	go func() {
		defer close(out)
		finalText := a.streamRouter(runCtx, c, router, msgs, out)
		a.finishTurn(c, finalText)
		a.persist(runCtx, c)
	}()
	return out, nil
}

// runtimeMessages 组装 ADK 路由的输入消息：[历史] + [本轮 user]。
// System Prompt 由 ADK 的 GenModelInput 自动把路由 Instruction 拼在前面。
func (a *Agent) runtimeMessages(c *Ctx) []*schema.Message {
	msgs := make([]*schema.Message, 0, len(c.History)+1)
	msgs = append(msgs, c.History...)
	msgs = append(msgs, schema.UserMessage(c.In.Message))
	return msgs
}

// finishTurn 收尾：自由文本兜底 + DeepSeek「说出题却没派发 generate」兜底。
// finalText 为事件流末尾文本（专家或路由的，可能为空）。结构性帧已由工具写入 c.Frames。
// 自由文本已由 streamRouter 以 delta 帧流式发出，故此处 chitchat 仅落库供历史恢复、不再推送。
func (a *Agent) finishTurn(c *Ctx, finalText string) {
	finalText = strings.TrimSpace(finalText)

	// DeepSeek 兜底：模型嘴上说出题却没派发 generate 专家 → 引导补知识点（下一轮正常出题）用户意图是出题时
	// c.frames 存储的是完整的结构化帧，调用工具 handler 时会显式 c.emit存储到c.frames
	// 没有调用任何工具 且 用户意图是出题时，说明模型没有调用出题工具，兜底引导用户补充知识点
	if len(c.Frames) == 0 && shouldForceGenerate(c.In.Message, finalText) {
		c.tk.log.Warn("model didn't delegate generate", zap.String("user_msg", c.In.Message), zap.String("model_text", finalText))
		c.emit(&Frame{Intent: "generate", Kind: FrameChitChat, Text: "好的！你想练哪个知识点？告诉我，我马上给你出题～"})
		return
	}

	if len(c.Frames) == 0 { //表示是纯对话，不是出题/判分/追问/解题/总结等专家调用的结构化帧
		// 将大模型输出的自由文本作为 chitchat 帧落库（前端已流式渲染过了）
		if finalText != "" {
			c.Frames = append(c.Frames, &Frame{Intent: "chitchat", Kind: FrameChitChat, Text: finalText})
			// 大模型没有输出任何内容，兜底回复默认文本
		} else {
			c.emit(a.agentDefaultReply())
		}
	}
}

// Ctx 一次消息流转上下文（各工具共享的中间状态）。
type Ctx struct {
	In              ChatInput
	tk              *toolkit // 领域依赖（repo/kbRepo/emb/retriever/log），工具经此读取
	Session         *practiceSessionView
	KB              *kbView
	History         []*schema.Message // 最近 N 轮历史对话（不含本轮 user 消息），Agent/内部调用共用上下文
	retrievedChunks []chunkView       // search_kb 检索结果缓存，供出题/解题复用（消费即清空）
	Frames          []*Frame
	sink            chan<- *Frame   // 流式输出通道（ChatStream 推帧用）
	runCtx          context.Context // 请求 ctx，用于流式推送时感知客户端断开
	State           string          // 本回合结束后应落库的会话状态（空=不变）
	Done            bool            // 前置检查失败等短路标记
	ToolCalls       []string        // 本轮调用的工具列表（用于日志记录）
	ToolResults     []string        // 各工具的执行摘要
	AgentIntent     string          // Agent 本轮主要意图（generate/answer/followup 等）
}

// emit 追加帧到 c.Frames，并在流式模式下实时推送到通道。
// 流式推送感知 ctx 取消（客户端断开），避免 goroutine 阻塞泄漏。
func (c *Ctx) emit(f *Frame) {
	if f == nil {
		return
	}
	c.Frames = append(c.Frames, f)
	if c.sink == nil {
		return
	}
	if c.runCtx != nil {
		select {
		case c.sink <- f:
		case <-c.runCtx.Done():
		}
		return
	}
	c.sink <- f
}

// kbView 知识库元信息（含年级/学科名/学科码，供 prompt 注入与路由选中）。
type kbView struct {
	ID          uint
	SubjectID   uint
	GradeID     uint
	GradeName   string
	SubjectName string
	SubjectCode string
}

// practiceSessionView 会话视图（不含敏感字段）。
type practiceSessionView struct {
	ID             uint
	State          string
	LastQuestionID *uint
}
