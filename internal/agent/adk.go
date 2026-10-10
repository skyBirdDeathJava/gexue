package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	extopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// buildChatModel 构建 OpenAI 兼容 ChatModel（DeepSeek V4 / 方舟，均 OpenAI 兼容）。
// ADK 通过 model.WithTools 派生带工具的模型实例，故共享同一 base model 是并发安全的。
func buildChatModel(ctx context.Context, cfg LLMConfig) (model.ChatModel, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	return extopenai.NewChatModel(ctx, &extopenai.ChatModelConfig{
		Model:   cfg.Model,
		APIKey:  cfg.APIKey,
		BaseURL: baseURL,
	})
}

// ctxKeyCtx 用于在 context 中携带本次请求的 *Ctx。
// ADK 工具执行时从中取出 Ctx，产出 Frame / 更新状态 / 落库。
type ctxKeyCtx struct{}

func withCtx(ctx context.Context, c *Ctx) context.Context {
	return context.WithValue(ctx, ctxKeyCtx{}, c)
}

func cFromCtx(ctx context.Context) *Ctx {
	c, _ := ctx.Value(ctxKeyCtx{}).(*Ctx)
	return c
}

// toolDef 工具定义：Info（供大模型）+ handler（现有 5 个工具实现，签名不变）。
type toolDef struct {
	info *schema.ToolInfo
	run  toolHandler
}

// toolAdapter 把现有 toolHandler（argsJSON → 执行 → 产出 Frame → 返回状态串）
// 适配为 tool.BaseTool + tool.InvokableTool，交给 ADK 专家 Agent 使用。
type toolAdapter struct {
	info *schema.ToolInfo
	run  toolHandler
}

func (t *toolAdapter) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

func (t *toolAdapter) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	c := cFromCtx(ctx)
	result := t.run(ctx, c, argsJSON)
	// 工具级 trace：记录专家内部每次原子工具调用（名/参数/结果），随 persist 落库到 tool_calls/tool_results
	if c != nil {
		c.ToolCalls = append(c.ToolCalls, t.info.Name)
		c.ToolResults = append(c.ToolResults, fmt.Sprintf("%s(%s) → %s",
			t.info.Name, truncateTrace(argsJSON, 200), truncateTrace(result, 200)))
	}
	return result, nil
}

// truncateTrace 截断 trace 片段（按 rune，避免切坏多字节），防止检索结果等过长撑爆落库。
func truncateTrace(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// toolDefs 返回 5 个工具的 ToolInfo + 处理器（复用 loop.go 的 registerTools 定义）。
func (a *Agent) toolDefs() []toolDef {
	infos, handlers := a.toolInfosAndHandlers()
	defs := make([]toolDef, 0, len(infos))
	for _, info := range infos {
		defs = append(defs, toolDef{info: info, run: handlers[info.Name]})
	}
	return defs
}

// toolInfos 仅返回 ToolInfo 列表（供序列化校验等）。
func (a *Agent) toolInfos() []*schema.ToolInfo {
	infos, _ := a.toolInfosAndHandlers()
	return infos
}

// subjectRouter 学科路由：subjectCode（math/yuwen/english/default）→ 路由 ChatModelAgent。
// 学科由会话 KB 确定性决定，路由 Agent 只做意图识别（选择调用哪个专家）。
type subjectRouter struct {
	agents map[string]adk.Agent
}

func (r *subjectRouter) resolve(code string) adk.Agent {
	if ag, ok := r.agents[code]; ok {
		return ag
	}
	return r.agents["default"]
}

// subjectNames 学科 code → 中文名（路由 Instruction 用）。
var subjectNames = map[string]string{
	"math":    "数学",
	"yuwen":   "语文",
	"english": "英语",
}

// expertSpec 专家 Agent 规格：一个专家 = 一个职责 + 若干工具。
type expertSpec struct {
	name        string
	desc        string
	instruction string
	tools       []string // 工具名（toolDefs 里注册）
}

// buildExpertConfigs 从 expertSpecs 解析工具，生成 5 个专家配置模板（跨学科共享）。
// 工具 handler 从 Ctx.tk 读取领域依赖，故这里只需登记 handler 引用，不持有基础设施。
func (a *Agent) buildExpertConfigs() error {
	defs := a.toolDefs()
	defByName := make(map[string]toolDef, len(defs))
	for _, d := range defs {
		defByName[d.info.Name] = d
	}

	a.experts = make([]*adk.ChatModelAgentConfig, 0, len(expertSpecs))
	for _, spec := range expertSpecs {
		tools := make([]tool.BaseTool, 0, len(spec.tools))
		for _, tn := range spec.tools {
			d, ok := defByName[tn]
			if !ok {
				return fmt.Errorf("expert %s: unknown tool %s", spec.name, tn)
			}
			tools = append(tools, &toolAdapter{info: d.info, run: d.run})
		}
		a.experts = append(a.experts, &adk.ChatModelAgentConfig{
			Name:        spec.name,
			Description: spec.desc,
			Instruction: spec.instruction,
			Model:       a.Model,
			ToolsConfig: adk.ToolsConfig{
				ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools},
			},
			MaxIterations: maxAgentIterations,
		})
	}
	return nil
}

// buildRouter 构建每学科一个路由 Agent（transfer 编排）。
// transfer 约束：一个专家只能挂在一个父 agent 下（OnSetAsSubAgent 单父校验），
// 故每个路由用共享的专家配置模板各自 NewChatModelAgent 建专属实例（NewChatModelAgent 会拷贝字段，不改模板）。
// 路由系统提示词里的专家清单（名字+desc）由 ADK 依据子 agent 自动注入。
func (a *Agent) buildRouter(ctx context.Context) error {
	agents := make(map[string]adk.Agent, len(subjectNames)+1)
	for code, name := range subjectNames {
		router, err := a.newRouterAgent(ctx, code, name)
		if err != nil {
			return err
		}
		agents[code] = router
	}
	// default 路由（未知学科兜底）
	defRouter, err := a.newRouterAgent(ctx, "default", "学习")
	if err != nil {
		return err
	}
	agents["default"] = defRouter

	a.router = &subjectRouter{agents: agents}
	return nil
}

// newRouterAgent 构建某学科的路由 Agent：从嵌入的路由配置模板派生（Name/Description/Instruction 按学科覆盖），
// 经 transfer_to_agent 移交给对应专家子 agent。路由自身不带工具。
// 专家经 AgentWithOptions(WithDisallowTransferToParent) 挂为子 agent，禁止回传父路由，保证单向分发。
func (a *Agent) newRouterAgent(ctx context.Context, code, name string) (adk.Agent, error) {
	routerCfg := *a.ChatModelAgentConfig // 拷贝模板，按学科派生
	routerCfg.Name = code + "_router"
	routerCfg.Description = name + "辅导路由：识别学生意图，派发出题/判分/追问/解题/总结专家"
	routerCfg.Instruction = routerInstruction(name)
	router, err := adk.NewChatModelAgent(ctx, &routerCfg)
	if err != nil {
		return nil, err
	}

	// 每个路由自建专属专家实例（transfer 单父约束）；共享配置模板，NewChatModelAgent 会拷贝字段
	subs := make([]adk.Agent, 0, len(a.experts))
	for _, cfg := range a.experts {
		ag, err := adk.NewChatModelAgent(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("build expert %s: %w", cfg.Name, err)
		}
		subs = append(subs, adk.AgentWithOptions(ctx, ag, adk.WithDisallowTransferToParent()))
	}

	fa, err := adk.SetSubAgents(ctx, router, subs)
	if err != nil {
		return nil, fmt.Errorf("set sub-agents for router %s: %w", code, err)
	}
	return fa, nil
}

// streamRouter 运行路由 Agent（流式）。路由/专家的自由文本以 delta 帧实时推送到 out；
// 结构性帧（question_list/feedback/...）由工具经 Ctx.emit（frame sink）实时推送。
// 返回最终完整自由文本（供收尾与持久化）。
func (a *Agent) streamRouter(ctx context.Context, c *Ctx, router adk.Agent, msgs []*schema.Message, out chan<- *Frame) string {
	routerName := router.Name(ctx)
	iter := router.Run(ctx, &adk.AgentInput{Messages: msgs, EnableStreaming: true})
	var finalText strings.Builder
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			c.tk.log.Warn("router stream event error", zap.Error(ev.Err))
			continue
		}
		if ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}
		mv := ev.Output.MessageOutput
		if mv.Role == schema.Tool {
			if ev.AgentName == routerName {
				a.recordToolEvent(c, mv)
			} else if c.AgentIntent == "" && ev.AgentName != "" {
				c.AgentIntent = ev.AgentName
			}
			continue
		}
		// 不是大模型输出的信息
		if mv.Role != schema.Assistant {
			continue
		}
		// 路由意图识别：transfer_to_agent 工具调用的参数里有目标专家名，供后续专家子 agent 处理。
		// 大模型输出的工具调用delta中content为空，所以是非流式输出时才解析 transfer_to_agent 目标专家名。
		if !mv.IsStreaming {
			if target := parseTransferTarget(mv); target != "" && c.AgentIntent == "" {
				c.AgentIntent = target
			}
		}
		// 流式输出自由文本（delta 帧），供前端实时渲染；工具调用/结构化输出由工具经 Ctx.emit 直接推送。
		if mv.IsStreaming && mv.MessageStream != nil {
			for {
				chunk, err := mv.MessageStream.Recv()
				if err != nil {
					break // io.EOF
				}
				if chunk == nil || chunk.Content == "" {
					continue
				}
				finalText.WriteString(chunk.Content)
				//将流式文本信息写到out channel中，供前端实时渲染
				if !a.pushDelta(ctx, out, chunk.Content) {
					return finalText.String()
				}
			}
			//因为 ADK 的 react 流式组装对某些输出（工具调用、或一次性返回的文本）产出的就是完整 MessageVariant，不带 MessageStream。这个分支就是给它们兜底的 ——
			//保证所有 assistant 文本（无论流式/完整）都被 finalText 累积 + 以 delta 推给前端。
		} else if msg, err := mv.GetMessage(); err == nil && msg != nil {
			if chunk := strings.TrimSpace(msg.Content); chunk != "" {
				finalText.WriteString(chunk)
				if !a.pushDelta(ctx, out, chunk) {
					return finalText.String()
				}
			}
		}
	}
	//流式分支是"一个字推一次"，这个是"整段推一次"，两者目标一样 —— 把模型说的话流式给用户并记进 finalText，同时靠 pushDelta 的返回值在客户端断开时提前收工。
	return finalText.String()
}

// pushDelta 推送 delta 帧；客户端断开（ctx 取消）时返回 false，调用方据此提前结束。
func (a *Agent) pushDelta(ctx context.Context, out chan<- *Frame, text string) bool {
	select {
	case out <- &Frame{Intent: "chitchat", Kind: FrameDelta, Text: text}:
		return true
	case <-ctx.Done():
		return false
	}
}

// recordToolEvent 记录路由自身的工具事件（transfer_to_agent）到 Ctx，供 persist 落库回看。
// 专家子 agent 的原子工具由 toolAdapter 在 InvokableRun 内部记录，这里不重复记录；
// 本轮主意图由 parseTransferTarget 依据 transfer 目标专家名确定。
func (a *Agent) recordToolEvent(c *Ctx, mv *adk.MessageVariant) {
	if mv.ToolName == "" {
		return
	}
	c.ToolCalls = append(c.ToolCalls, mv.ToolName)
	if msg, err := mv.GetMessage(); err == nil && msg != nil {
		c.ToolResults = append(c.ToolResults, mv.ToolName+": "+strings.TrimSpace(msg.Content))
	}
}

// parseTransferTarget 解析路由 assistant 消息里 transfer_to_agent 工具调用的参数，
// 返回目标专家名；未发起移交时返回空串。transfer 参数格式固定为 {"agent_name":"<专家名>"}。
func parseTransferTarget(mv *adk.MessageVariant) string {
	msg, err := mv.GetMessage()
	if err != nil || msg == nil || len(msg.ToolCalls) == 0 {
		return ""
	}
	for _, tc := range msg.ToolCalls {
		if tc.Function.Name != adk.TransferToAgentToolName {
			continue
		}
		var p struct {
			AgentName string `json:"agent_name"`
		}
		if json.Unmarshal([]byte(tc.Function.Arguments), &p) != nil {
			return ""
		}
		return p.AgentName
	}
	return ""
}
