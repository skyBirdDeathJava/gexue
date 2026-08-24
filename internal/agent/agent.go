package agent

import (
	"context"
	"fmt"

	extopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	"gexue/internal/embedding"
	"gexue/internal/repo"
)

// SessionLogGenerator 会话过程日志生成器
type SessionLogData struct {
	TotalQuestions  int            `json:"total_questions"`
	CorrectAnswers  int            `json:"correct_answers"`
	CorrectRate     float32        `json:"correct_rate"`
	KnowledgePoints []string       `json:"knowledge_points"`
	QuestionTypes   map[string]int `json:"question_types"`
	DifficultyStats map[int]int    `json:"difficulty_stats"`
	Duration        int            `json:"duration_sec"`
	Summary         string         `json:"summary"`
	Recommendation  string         `json:"recommendation"`
}

// ChatInput 对话入口（一次消息）。多轮对话由前端按会话累积，本 Agent 每次只处理一条。
type ChatInput struct {
	UserID    uint   `json:"user_id"`
	SessionID uint   `json:"session_id"`
	Message   string `json:"message"`
}

// Frame SSE 流式帧。
// kind ∈ select_kb / params_ask / question_list / feedback / followup / help / chitchat / summary
type Frame struct {
	Intent    string         `json:"intent"`
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

// 帧类型常量。
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
)

// LLMConfig 大模型接入（DeepSeek V4 / 方舟，均 OpenAI 兼容）。
type LLMConfig struct {
	Provider string // deepseek / ark（仅用于日志标识）
	APIKey   string
	BaseURL  string
	Model    string
}

// Agent 对话式辅导 Agent：LLM 自主决策（工具调用循环）+ 兜底。
// 与旧版“意图识别→固定流程分派”不同：Agent 读整段对话后自主决定
// 要不要出题 / 判分 / 追问 / 解题 / 总结，追问的难度与题型也由它按学生作答判断。
type Agent struct {
	repo   *repo.QuizRepo
	kbRepo *repo.KnowledgeRepo
	emb    embedding.Embedder
	llm    model.ChatModel // 未配置时为 nil（各工具/入口兜底返回友好提示）
	log    *zap.Logger

	// tools 工具名 → 处理器；在 NewAgent 时注册，LLM 自主调用。
	tools map[string]toolHandler
	// toolInfos 描述工具参数，绑定到大模型（BindTools 一次）。
	toolInfos []*schema.ToolInfo
}

// toolHandler 工具处理器：解析参数 JSON，执行并产出 Frames/状态，返回给 LLM 的内部状态串。
// 返回串仅作 LLM 继续决策的上下文，不直接展示给学生（学生看到的是 Frame）。
type toolHandler func(ctx context.Context, c *Ctx, argsJSON string) string

// NewAgent 构造 Agent；llmCfg.APIKey 为空时 llm 置 nil（无 key 可编译可测试，入口兜底）。
func NewAgent(qr *repo.QuizRepo, kr *repo.KnowledgeRepo, emb embedding.Embedder, llmCfg LLMConfig, log *zap.Logger) (*Agent, error) {
	a := &Agent{repo: qr, kbRepo: kr, emb: emb, log: log, tools: map[string]toolHandler{}}

	if llmCfg.APIKey != "" {
		baseURL := llmCfg.BaseURL
		if baseURL == "" {
			baseURL = "https://api.deepseek.com"
		}
		cm, err := extopenai.NewChatModel(context.Background(), &extopenai.ChatModelConfig{
			Model:   llmCfg.Model,
			APIKey:  llmCfg.APIKey,
			BaseURL: baseURL,
		})
		if err != nil {
			return nil, fmt.Errorf("init chat model: %w", err)
		}
		a.llm = cm
	}

	a.registerTools()
	if a.llm != nil {
		if err := a.llm.BindTools(a.toolInfos); err != nil {
			return nil, fmt.Errorf("bind tools: %w", err)
		}
	}
	return a, nil
}

// Chat 处理一次用户消息，返回 SSE 帧列表；用户消息落库由 handler 在调用前完成。
// 流程：前置检查（会话/知识库）→ 组装上下文 → LLM 自主工具循环 → 落库。
func (a *Agent) Chat(ctx context.Context, in ChatInput) ([]*Frame, error) {
	c := &Ctx{In: in}

	// ① 前置硬检查：会话归属 + 知识库归属（失败 → select_kb 帧并短路）
	if err := a.checkKb(ctx, c); err != nil {
		return nil, err
	}
	if c.Done {
		a.persist(ctx, c)
		return c.Frames, nil
	}

	// ② 组装最近对话上下文（历史链供 Agent 感知整段对话）
	if err := a.buildCtx(ctx, c); err != nil {
		return nil, err
	}

	// ③ 无 LLM 兜底
	if a.llm == nil {
		c.Frames = []*Frame{a.noLLMFrame(in)}
		a.persist(ctx, c)
		return c.Frames, nil
	}

	// ④ LLM 自主决策循环（工具调用，流程不固定）
	a.runLoop(ctx, c)

	// ⑤ 落库（状态机 + assistant 帧文本）
	a.persist(ctx, c)
	return c.Frames, nil
}

// Ctx 一次消息流转上下文（各工具共享的中间状态）。
type Ctx struct {
	In          ChatInput
	Session     *practiceSessionView
	KB          *kbView
	History     []*schema.Message // 最近 N 轮历史对话（不含本轮 user 消息），Agent/内部调用共用上下文
	Frames      []*Frame
	State       string   // 本回合结束后应落库的会话状态（空=不变）
	Done        bool     // 前置检查失败等短路标记
	ToolCalls   []string // 本轮调用的工具列表（用于日志记录）
	ToolResults []string // 各工具的执行摘要
	AgentIntent string   // Agent 本轮主要意图（generate/answer/followup 等）
}

// kbView 知识库元信息（含年级/学科名，供 prompt 注入）。
type kbView struct {
	ID          uint
	GradeName   string
	SubjectName string
}

// practiceSessionView 会话视图（不含敏感字段）。
type practiceSessionView struct {
	ID             uint
	State          string
	LastQuestionID *uint
}

// agentBaseMessages 组装 Agent 主循环消息：[System] + 最近历史（不含本轮 user）。
// 新建底层数组，避免 append 复用 History 底层数组导致历史被覆盖。
func (a *Agent) agentBaseMessages(c *Ctx) []*schema.Message {
	msgs := make([]*schema.Message, 0, len(c.History)+1)
	msgs = append(msgs, schema.SystemMessage(a.agentSystemPrompt(c)))
	msgs = append(msgs, c.History...)
	return msgs
}

// chatMessages 组装内部子调用消息（判分/出题等）：[System] + 最近历史 + [User]。
// 内部调用走 Generate 且期望 JSON，故默认禁用工具选择，避免模型在这类调用里又发起工具。
func (a *Agent) chatMessages(sys string, c *Ctx, user string) []*schema.Message {
	msgs := make([]*schema.Message, 0, len(c.History)+2)
	msgs = append(msgs, schema.SystemMessage(sys))
	msgs = append(msgs, c.History...)
	msgs = append(msgs, schema.UserMessage(user))
	return msgs
}

// noTools 内部子调用禁用工具（工具已绑定，需显式禁止以免子调用误发工具调用）。
func noTools() model.Option {
	return model.WithToolChoice(schema.ToolChoiceForbidden)
}
