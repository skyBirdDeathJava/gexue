package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	extopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	imodel "gexue/internal/model"
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
	log  *zap.Logger // 诊断：无条件记录工具是否被 ADK 调用（不受 Ctx 是否有 nil 影响）
}

func (t *toolAdapter) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

func (t *toolAdapter) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	c := cFromCtx(ctx)
	// 诊断：无条件记录 ADK 是否真的调到了工具（不受 Ctx 是否为 nil 影响）
	if t.log != nil {
		t.log.Warn("tool invoked", zap.String("tool", t.info.Name), zap.String("args", truncateTrace(argsJSON, 120)))
	}
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

// toolDefsByName 工具名 → toolDef 映射（agentRegistry 按 bindtools 字符串解析工具用）。
func (a *Agent) toolDefsByName() map[string]toolDef {
	defs := a.toolDefs()
	m := make(map[string]toolDef, len(defs))
	for _, d := range defs {
		m[d.info.Name] = d
	}
	return m
}

// toolInfos 仅返回 ToolInfo 列表（供序列化校验等）。
func (a *Agent) toolInfos() []*schema.ToolInfo {
	infos, _ := a.toolInfosAndHandlers()
	return infos
}

// agentScope 动态 agent 的加载键：学科 + 年级。
type agentScope struct {
	subjectID uint
	gradeID   uint
}

// agentRegistry 按 (学科, 年级) 从 DB 懒加载并缓存构建的 ADK agent 树（1 路由 + 专家子 agent）。
// 年级差异（题型/知识点/判分逻辑）落在 agent 行的 prompt 上，故每个 scope 是独立的树。
// transfer 单父约束：每个 scope 的专家子 agent 均为独立实例（NewChatModelAgent 拷贝字段），
// 缓存键 = scope，命中直接复用已构建的路由。
// agentLister agent_config 表查询（*repo.AgentRepo 实现；测试可注入假实现）。
type agentLister interface {
	ListByScope(ctx context.Context, subjectID, gradeID uint) ([]*imodel.Agent, error)
}

type agentRegistry struct {
	lister    agentLister
	llm       model.ChatModel    // 全局默认模型（agent 行未指定 model 时用）
	defByName map[string]toolDef // 工具注册表（bindtools 字符串 → toolAdapter）
	cache     map[agentScope]adk.Agent
	mu        sync.Mutex
	log       *zap.Logger // 诊断
}

// resolve 返回某学科+年级的路由 agent（懒构建 + 缓存）；scope 未配置（无路由行）时返回 (nil, nil)。
func (r *agentRegistry) resolve(ctx context.Context, subjectID, gradeID uint) (adk.Agent, error) {
	key := agentScope{subjectID: subjectID, gradeID: gradeID}
	r.mu.Lock()
	ag, ok := r.cache[key]
	r.mu.Unlock()
	if ok {
		if r.log != nil {
			r.log.Warn("registry cache hit", zap.Uint64("subject", uint64(subjectID)), zap.Uint64("grade", uint64(gradeID)))
		}
		return ag, nil
	}

	rows, err := r.lister.ListByScope(ctx, subjectID, gradeID)
	if err != nil {
		return nil, fmt.Errorf("list agents scope(%d,%d): %w", subjectID, gradeID, err)
	}
	if r.log != nil {
		r.log.Warn("registry building", zap.Uint64("subject", uint64(subjectID)), zap.Uint64("grade", uint64(gradeID)), zap.Int("rows", len(rows)))
	}

	fa, err := r.build(ctx, rows)
	if err != nil {
		return nil, err
	}
	if fa == nil {
		return nil, nil // scope 未配置
	}

	r.mu.Lock()
	r.cache[key] = fa
	r.mu.Unlock()
	return fa, nil
}

// build 由 DB 行构建一棵 ADK agent 树：路由行 + 其下的专家子 agent（按 parent_id 归属；当前每 scope 一树，按 agent_type 分组即可）。
func (r *agentRegistry) build(ctx context.Context, rows []*imodel.Agent) (adk.Agent, error) {
	var routerRow *imodel.Agent
	expertRows := make([]*imodel.Agent, 0, 5)
	for _, row := range rows {
		switch row.AgentType {
		case "router":
			routerRow = row
		case "expert":
			expertRows = append(expertRows, row)
		}
	}
	if routerRow == nil {
		return nil, nil
	}

	routerCfg, err := r.agentConfig(routerRow)
	if err != nil {
		return nil, fmt.Errorf("router %s: %w", routerRow.Name, err)
	}
	router, err := adk.NewChatModelAgent(ctx, routerCfg)
	if err != nil {
		return nil, fmt.Errorf("build router %s: %w", routerRow.Name, err)
	}

	subs := make([]adk.Agent, 0, len(expertRows))
	for _, row := range expertRows {
		cfg, err := r.agentConfig(row)
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", row.Name, err)
		}
		ag, err := adk.NewChatModelAgent(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("build expert %s: %w", row.Name, err)
		}
		subs = append(subs, adk.AgentWithOptions(ctx, ag, adk.WithDisallowTransferToParent()))
	}

	fa, err := adk.SetSubAgents(ctx, router, subs)
	if err != nil {
		return nil, fmt.Errorf("set sub-agents for router %s: %w", routerRow.Name, err)
	}
	return fa, nil
}

// toolCallInstruction 追加到每个 agent 的 Instruction 末尾：约束模型用结构化 tool_calls 调用工具。
// 部分 DeepSeek 模型（如 deepseek-v4-flash）默认用 DSML 标记（<|DSML|invoke...>）表达工具调用，
// 而 ADK 只认结构化 tool_calls —— DSML 会被当纯文本输出，工具不执行。统一在此压制。
const toolCallInstruction = "\n\n【工具调用】调用工具必须走 function calling（结构化 tool_calls，返回函数名与 JSON 参数），严禁输出 <|DSML|...|> 或任何尖括号标记语言；需要调用工具时直接以函数调用形式返回，不要用标记文本表达。"

// agentConfig 把 DB 行转成 adk.ChatModelAgentConfig。
// bindtools 为逗号分隔的工具名，解析成 toolAdapter（工具 handler 从 Ctx.tk 读领域依赖）。
func (r *agentRegistry) agentConfig(row *imodel.Agent) (*adk.ChatModelAgentConfig, error) {
	cfg := &adk.ChatModelAgentConfig{
		Name:          row.Name,
		Description:   row.Description,
		Instruction:   row.Prompt + toolCallInstruction,
		Model:         r.llm,
		MaxIterations: maxAgentIterations,
	}
	if row.MaxIterations > 0 {
		cfg.MaxIterations = row.MaxIterations
	}
	if row.BindTools != "" {
		tools, err := r.buildTools(row)
		if err != nil {
			return nil, err
		}
		cfg.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools},
		}
	}
	return cfg, nil
}

// buildTools 由 bindtools 解析出 toolAdapter 列表（诊断：记录每个专家的工具注册数）。
func (r *agentRegistry) buildTools(row *imodel.Agent) ([]tool.BaseTool, error) {
	if row.BindTools == "" {
		return nil, nil
	}
	tools := make([]tool.BaseTool, 0, 3)
	for _, tn := range strings.Split(row.BindTools, ",") {
		tn = strings.TrimSpace(tn)
		d, ok := r.defByName[tn]
		if !ok {
			return nil, fmt.Errorf("agent %s: unknown tool %s", row.Name, tn)
		}
		tools = append(tools, &toolAdapter{info: d.info, run: d.run, log: r.log})
	}
	if r.log != nil {
		r.log.Warn("expert tools registered", zap.String("agent", row.Name), zap.Int("count", len(tools)))
	}
	return tools, nil
}

// streamRouter 运行路由 Agent（流式）。路由/专家的自由文本以 delta 帧实时推送到 out；
// 结构性帧（question_list/feedback/...）由工具经 Ctx.emit（frame sink）实时推送。
// 返回最终完整自由文本（供收尾与持久化）。
// 流式开启：dsmlAwareModel 只在 DSML 开头才缓冲转换，正常文本原样透传，保留 token 级流式。
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
			// 诊断：记录专家子 agent 的工具事件（确认工具是否真的执行 + 结果）
			if ev.AgentName != routerName && c.tk != nil && c.tk.log != nil {
				if content, _ := mv.GetMessage(); content != nil {
					c.tk.log.Warn("expert tool event",
						zap.String("agent", ev.AgentName),
						zap.String("tool", mv.ToolName),
						zap.String("result", truncateTrace(content.Content, 200)))
				}
			}
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
