package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

const maxAgentIterations = 8

func (a *Agent) agentDefaultReply() *Frame {
	return &Frame{Intent: "chitchat", Kind: FrameChitChat, Text: "嗯，我先帮你梳理一下～想练哪个知识点，或者要不要把刚才的题目再讲一讲?"}
}

func (a *Agent) runLoop(ctx context.Context, c *Ctx) {
	msgs := append(a.agentBaseMessages(c), schema.UserMessage(c.In.Message))

	for i := 0; i < maxAgentIterations; i++ {
		resp, err := a.llm.Generate(ctx, msgs)
		if err != nil {
			a.log.Warn("agent loop generate failed", zap.Error(err))
			if len(c.Frames) == 0 {
				c.Frames = append(c.Frames, a.agentDefaultReply())
			}
			return
		}

		if len(resp.ToolCalls) == 0 {
			text := strings.TrimSpace(resp.Content)

			// DeepSeek fallback: if model says it will generate question but didn't call tool, force it
			if shouldForceGenerate(c.In.Message, text) && len(c.Frames) == 0 {
				a.log.Warn("model didn't call generate_question, forcing it",
					zap.String("user_msg", c.In.Message), zap.String("model_text", text))
				// Extract knowledge point from user message or use default
				kp := extractKnowledgePoint(c.In.Message)
				result := a.toolGenerate(ctx, c, `{"knowledge_point":"`+kp+`","difficulty":2}`)
				a.log.Info("forced generate_question", zap.String("result", result))
				// Append the model's text as context if it exists
				if text != "" {
					c.Frames = append(c.Frames, &Frame{Intent: "context", Kind: "chitchat", Text: text})
				}
				return
			}

			if text != "" && len(c.Frames) == 0 {
				c.Frames = append(c.Frames, &Frame{Intent: "chitchat", Kind: FrameChitChat, Text: text})
			}
			return
		}

		executed := false
		for i := range resp.ToolCalls {
			tc := &resp.ToolCalls[i]
			if tc.Function.Name == "" {
				continue
			}
			c.ToolCalls = append(c.ToolCalls, tc.Function.Name)
			c.AgentIntent = tc.Function.Name

			result := a.executeTool(ctx, c, tc)
			c.ToolResults = append(c.ToolResults, tc.Function.Name+": "+result)

			msgs = append(msgs, schema.AssistantMessage("", []schema.ToolCall{*tc}))
			msgs = append(msgs, schema.ToolMessage(result, tc.ID))
			executed = true
		}
		if !executed {
			return
		}
	}

	if len(c.Frames) == 0 {
		c.Frames = append(c.Frames, a.agentDefaultReply())
	}
}

func (a *Agent) executeTool(ctx context.Context, c *Ctx, tc *schema.ToolCall) (result string) {
	h, ok := a.tools[tc.Function.Name]
	if !ok {
		return "unknown tool: " + tc.Function.Name
	}
	defer func() {
		if r := recover(); r != nil {
			a.log.Warn("tool panic", zap.String("tool", tc.Function.Name), zap.Any("panic", r))
			result = "tool execution error"
		}
	}()
	return h(ctx, c, tc.Function.Arguments)
}

func (a *Agent) registerTools() {
	a.toolInfos = []*schema.ToolInfo{
		{
			Name: "generate_question",
			Desc: "output questions for knowledge base and display to student",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"knowledge_point": {Type: schema.String, Desc: "topic to practice", Required: true},
				"difficulty":      {Type: schema.Integer, Desc: "difficulty 1-5"},
				"qtype":           {Type: schema.String, Enum: []string{"select", "fill", "apply", "mixed"}},
				"count":           {Type: schema.Integer, Desc: "how many questions to generate (1-10); if the student says a number like \"10道\", pass it here"},
			}),
		},
		{
			Name: "answer_question",
			Desc: "student answer submission and grading",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"student_answer": {Type: schema.String, Desc: "student answer", Required: true},
			}),
		},
		{
			Name: "follow_up",
			Desc: "follow-up guidance after grading",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"content":    {Type: schema.String, Desc: "guidance content", Required: true},
				"mode":       {Type: schema.String, Enum: []string{"hint", "extend", "drill"}},
				"difficulty": {Type: schema.Integer, Desc: "target difficulty 1-5"},
				"qtype":      {Type: schema.String, Enum: []string{"select", "fill", "apply", "mixed"}},
			}),
		},
		{
			Name: "help_solve",
			Desc: "help solve a specific problem",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"question": {Type: schema.String, Desc: "problem to solve", Required: true},
			}),
		},
		{
			Name: "summarize",
			Desc: "end session and provide summary",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"reason": {Type: schema.String, Desc: "reason for ending"},
			}),
		},
	}

	a.tools = map[string]toolHandler{
		"generate_question": a.toolGenerate,
		"answer_question":   a.toolAnswer,
		"follow_up":         a.toolFollowup,
		"help_solve":        a.toolHelp,
		"summarize":         a.toolSummarize,
	}
}

// shouldForceGenerate checks if model should have called generate_question but didn't
func shouldForceGenerate(userMsg, modelText string) bool {
	userLower := strings.ToLower(userMsg)
	modelLower := strings.ToLower(modelText)

	// User keywords suggesting they want a question
	wantsQuestion := strings.Contains(userLower, "换") || strings.Contains(userLower, "道") ||
		strings.Contains(userLower, "练") || strings.Contains(userLower, "题") ||
		strings.Contains(userLower, "出题") || strings.Contains(userLower, "考考")

	// Model text suggests it will generate but didn't call tool
	saidWillGenerate := strings.Contains(modelLower, "出") || strings.Contains(modelLower, "给你") ||
		strings.Contains(modelLower, "题") || strings.Contains(modelLower, "道")

	return wantsQuestion && saidWillGenerate
}

// extractKnowledgePoint tries to extract knowledge point from user message
func extractKnowledgePoint(msg string) string {
	// Common patterns like "练XXX" or "出XXX的题"
	patterns := []string{"练", "出", "关于", "练习", "这"}
	for _, p := range patterns {
		if idx := strings.Index(msg, p); idx >= 0 && idx+1 < len(msg) {
			start := idx + len(p)
			// Find next punctuation or end
			for end := start; end < len(msg); end++ {
				if end-start > 20 { // Max 20 chars for knowledge point
					return msg[start:end]
				}
				ch := rune(msg[end])
				if ch == '。' || ch == '，' || ch == '、' || ch == '?' || ch == '？' {
					if end > start {
						return msg[start:end]
					}
				}
			}
			return msg[start:]
		}
	}
	return "练习题"
}
