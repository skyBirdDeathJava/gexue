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

// maxTurns 上下文携带的最近对话轮数。
const maxTurns = 10

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

	sess, err := a.repo.GetSession(ctx, in.UserID, in.SessionID)
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

	kb, err := a.kbRepo.GetBaseWithMeta(ctx, in.UserID, sess.KbID)
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
	if kb.Subject != nil {
		subjectName = kb.Subject.Name
	}
	c.KB = &kbView{ID: kb.ID, GradeName: gradeName, SubjectName: subjectName}
	return nil
}

// buildCtx 读最近 N 轮 ChatTurn，组装 LLM 上下文历史链（不含本轮 user 消息）。
// 本轮消息由 Agent 作为 User 单独拼接；内部子调用与主循环共用同一份 History。
func (a *Agent) buildCtx(ctx context.Context, c *Ctx) error {
	turns, err := a.repo.ListTurns(ctx, c.Session.ID, maxTurns)
	if err != nil {
		return err
	}
	// ListTurns 按 id DESC，需反转回正序
	c.History = make([]*schema.Message, 0, len(turns))
	for i := len(turns) - 1; i >= 0; i-- {
		c.History = append(c.History, &schema.Message{Role: schema.RoleType(turns[i].Role), Content: turns[i].Content})
	}
	return nil
}

// persist 落库：① 会话状态机（state / last_question_id）；② assistant ChatTurn。
func (a *Agent) persist(ctx context.Context, c *Ctx) {
	// ① 状态机变更落库（仅当 state 或 last_question_id 有变化；短路路径 Session 为空则跳过）
	if c.Session == nil {
		return
	}
	sess, err := a.repo.GetSession(ctx, c.In.UserID, c.Session.ID)
	if err != nil {
		a.log.Warn("reload session for persist failed", zap.Error(err))
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
			if err := a.repo.UpdateSession(ctx, sess); err != nil {
				a.log.Warn("persist session state failed", zap.Error(err))
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
	if err := a.repo.AddTurns(ctx, turns); err != nil {
		a.log.Warn("persist assistant turns failed", zap.Error(err))
	}
}

// gsTag 年级-学科标签（prompt 注入）。
func (a *Agent) gsTag(kb *kbView) string {
	return gradeSubject(kb.GradeName, kb.SubjectName)
}
