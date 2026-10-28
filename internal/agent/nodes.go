package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	"gexue/internal/model"
	"gexue/internal/repo"
)

// maxTurns 上下文携带的最近对话轮数（出题/判分/解答/追问共用）。
// 20 轮：覆盖"一道题 10+ 轮还没结束"的边界（如一直要求提示），判分/解答不会因历史截断丢上下文。
const maxTurns = 20

// maxSummaryTurns 总结意图时加载的历史轮数（≈全部；上限防超长会话 token 爆炸）。
const maxSummaryTurns = 200

// noLLMFrame 未配置大模型时的友好提示帧（不 panic）。
func (a *Agent) noLLMFrame(in ChatInput) *Frame {
	return &Frame{
		Intent: "chitchat",
		Kind:   FrameNoLLM,
		Text:   "大模型服务尚未配置（缺少 API Key），暂时无法陪你练习。请在 .env 中配置 LLM 密钥后重启服务。",
	}
}

// checkKb 前置检查：会话归属 + 知识库归属（硬前置）。
// 未选/无效知识库 → 置 select_kb 帧并 Done 短路，不进入出题。
func (a *Agent) checkKb(ctx context.Context, c *Ctx) error {
	in := c.In

	sess, err := c.tk.repo.GetSession(ctx, in.UserID, in.SessionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			c.Frames = []*Frame{{
				Intent: "generate", Kind: FrameSelectKB,
				Text: "请先选择一个知识库再开始练习吧～（在知识库列表中选择后，我会按照里面的内容出题）",
			}}
			c.Done = true
			return nil
		}
		return err
	}
	c.Session = &practiceSessionView{ID: sess.ID, State: sess.State, LastQuestionID: sess.LastQuestionID}

	kb, err := c.tk.kbRepo.GetBaseWithMeta(ctx, in.UserID, sess.KbID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			c.Frames = []*Frame{{
				Intent: "generate", Kind: FrameSelectKB,
				Text: "当前会话绑定的知识库不可用，请先重新选择一个知识库再开始练习。",
			}}
			c.Done = true
			return nil
		}
		return err
	}

	// 防御性检查：Grade 和 Subject 可能为 nil
	gradeName := ""
	if kb.Grade != nil {
		gradeName = kb.Grade.Name
	}
	subjectName := ""
	subjectCode := ""
	if kb.Subject != nil {
		subjectName = kb.Subject.Name
		subjectCode = kb.Subject.Code
	}
	c.KB = &kbView{ID: kb.ID, SubjectID: kb.SubjectID, GradeID: kb.GradeID, GradeName: gradeName, SubjectName: subjectName, SubjectCode: subjectCode}
	return nil
}

// buildCtx 读最近 N 轮 ChatTurn，组装 LLM 上下文历史链（不含本轮 user 消息）。
// 本轮消息由 Agent 作为 User 单独拼接；内部子调用与主循环共用同一份 History。
// 过滤脏对话（DSML 泄漏 / 空内容）：避免修复前的失败轮次污染模型上下文。
func (a *Agent) buildCtx(ctx context.Context, c *Ctx) error {
	limit := maxTurns
	if looksLikeSummary(c.In.Message) {
		limit = maxSummaryTurns // 总结要看全程，不截断
	}
	turns, err := c.tk.repo.ListTurns(ctx, c.Session.ID, limit)
	if err != nil {
		return err
	}
	// ListTurns 按 id DESC，需反转回正序
	c.History = make([]*schema.Message, 0, len(turns))
	for i := len(turns) - 1; i >= 0; i-- {
		if isDirtyTurn(turns[i]) {
			continue // 脏对话不进上下文
		}
		c.History = append(c.History, &schema.Message{Role: schema.RoleType(turns[i].Role), Content: turns[i].Content})
	}
	return nil
}

// looksLikeSummary 结束/总结意图关键词检测（扩大历史窗口用；最终意图仍由路由 LLM 判定）。
func looksLikeSummary(msg string) bool {
	s := strings.ToLower(msg)
	for _, k := range []string{"结束", "总结", "不想学", "完成了", "汇总", "回顾", "今天学了"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// isDirtyTurn 脏对话判定：assistant 内容含 DSML 标记（DeepSeek 内部标记语言泄漏）或为空。
// user 消息是真实输入，一律保留。DSML 形态覆盖 ASCII/全角竖线：<dsml、<|dsml、｜｜dsml。
func isDirtyTurn(t model.ChatTurn) bool {
	if t.Role != "assistant" {
		return false
	}
	content := strings.ToLower(strings.TrimSpace(t.Content))
	if content == "" {
		return true
	}
	for _, marker := range []string{"<dsml", "<|dsml", "｜｜dsml"} {
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

// persist 落库：① 会话状态机（state / last_question_id）；② assistant ChatTurn。
func (a *Agent) persist(ctx context.Context, c *Ctx) {
	// ① 状态机变更落库（仅当 state 或 last_question_id 有变化；短路路径 Session 为空则跳过）
	if c.Session == nil {
		return
	}
	sess, err := c.tk.repo.GetSession(ctx, c.In.UserID, c.Session.ID)
	if err != nil {
		c.tk.log.Warn("reload session for persist failed", zap.Error(err))
	} else {
		changed := false
		if c.State != "" && c.State != sess.State {
			sess.State = c.State
			changed = true
		}
		if c.Session.LastQuestionID != nil &&
			(sess.LastQuestionID == nil || *sess.LastQuestionID != *c.Session.LastQuestionID) {
			sess.LastQuestionID = c.Session.LastQuestionID
			changed = true
		}
		if changed {
			if err := c.tk.repo.UpdateSession(ctx, sess); err != nil {
				c.tk.log.Warn("persist session state failed", zap.Error(err))
			}
		}
	}

	// ② assistant 对话轮次落库（包含工具调用日志和生成的帧）
	var texts []string
	for _, f := range c.Frames {
		if f.Text != "" {
			texts = append(texts, f.Text)
		}
	}

	// 即使无文本也要记录工具调用日志
	turn := model.ChatTurn{
		SessionID:   c.Session.ID,
		Role:        "assistant",
		Content:     strings.Join(texts, "\n"),
		Intent:      c.AgentIntent,
		ToolResults: nil,
		Frames:      nil,
	}

	// 记录工具调用信息
	if len(c.ToolCalls) > 0 {
		toolCallsJSON, _ := json.Marshal(c.ToolCalls)
		turn.ToolCalls = toolCallsJSON
	}
	if len(c.ToolResults) > 0 {
		toolResultsJSON, _ := json.Marshal(c.ToolResults)
		turn.ToolResults = toolResultsJSON
	}

	// 记录生成的帧（题目、判分、追问等）
	if len(c.Frames) > 0 {
		framesJSON, _ := json.Marshal(c.Frames)
		turn.Frames = framesJSON
	}

	turns := []model.ChatTurn{turn}
	if err := c.tk.repo.AddTurns(ctx, turns); err != nil {
		c.tk.log.Warn("persist assistant turns failed", zap.Error(err))
	}
}
