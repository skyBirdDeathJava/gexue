package agent

import (
	"fmt"
	"strings"

	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

const maxAgentIterations = 8

func (a *Agent) agentDefaultReply() *Frame {
	return &Frame{Intent: "chitchat", Kind: FrameChitChat, Text: "嗯，我先帮你梳理一下～想练哪个知识点，或者要不要把刚才的题目再讲一讲?"}
}

// mustInfo 从结构体标签推断工具参数 schema（tool/utils.GoStruct2ToolInfo）。
// 参数结构仅为 string/int/struct 字段，推断不会失败；失败即 panic 快速暴露（fail-fast）。
func mustInfo[T any](name, desc string) *schema.ToolInfo {
	info, err := toolutils.GoStruct2ToolInfo[T](name, desc)
	if err != nil {
		panic(fmt.Sprintf("build tool info %s: %v", name, err))
	}
	return info
}

// toolInfosAndHandlers 返回工具的 ToolInfo（供大模型）+ 处理器（供专家 Agent 包装）。
// ToolInfo 由 toolXxxArgs 结构体的 jsonschema 标签经 GoStruct2ToolInfo 自动推断，无需手写 ParamsOneOf。
//
// 这里定义的是**原子能力**：检索（search_kb）/ 取题（get_question）/ 落库展示（save_*）/ 统计（get_session_stats）。
// 「出题/判分/解题/追问/总结」的内容由专家 Agent 自己的 ReAct 循环产出后传入 save_* 工具，
// 工具本身不再调用 LLM。见 docs/MULTI-SUBJECT-AGENT-SOLUTION.md §4。
func (a *Agent) toolInfosAndHandlers() ([]*schema.ToolInfo, map[string]toolHandler) {
	infos := []*schema.ToolInfo{
		mustInfo[toolSearchKbArgs]("search_kb", "retrieve relevant knowledge base content by knowledge point or query"),
		mustInfo[toolGetQuestionArgs]("get_question", "get the current question's stem, type and standard answer"),
		mustInfo[toolSaveQuestionsArgs]("save_questions", "save and show the questions you generated to the student"),
		mustInfo[toolSaveGradeArgs]("save_grade", "save the grade result, give feedback, and return a next-question suggestion"),
		mustInfo[toolSaveFollowupArgs]("save_followup", "show the follow-up guidance you wrote to the student"),
		mustInfo[toolSaveSolutionArgs]("save_solution", "show the step-by-step solution you wrote to the student"),
		mustInfo[toolGetStatsArgs]("get_session_stats", "get session statistics (total/correct/rate/knowledge points) for the summary"),
		mustInfo[toolSaveSummaryArgs]("save_summary", "show the summary and next-knowledge-point recommendation you wrote"),
	}

	handlers := map[string]toolHandler{
		"search_kb":         a.toolSearchKb,
		"get_question":      a.toolGetQuestion,
		"save_questions":    a.toolSaveQuestions,
		"save_grade":        a.toolSaveGrade,
		"save_followup":     a.toolSaveFollowup,
		"save_solution":     a.toolSaveSolution,
		"get_session_stats": a.toolGetSessionStats,
		"save_summary":      a.toolSaveSummary,
	}
	return infos, handlers
}

// shouldForceGenerate checks if model should have delegated generate but didn't
func shouldForceGenerate(userMsg, modelText string) bool {
	userLower := strings.ToLower(userMsg)
	modelLower := strings.ToLower(modelText)

	wantsQuestion := strings.Contains(userLower, "换") || strings.Contains(userLower, "道") ||
		strings.Contains(userLower, "练") || strings.Contains(userLower, "题") ||
		strings.Contains(userLower, "出题") || strings.Contains(userLower, "考考")

	saidWillGenerate := strings.Contains(modelLower, "出") || strings.Contains(modelLower, "给你") ||
		strings.Contains(modelLower, "题") || strings.Contains(modelLower, "道")

	return wantsQuestion && saidWillGenerate
}
