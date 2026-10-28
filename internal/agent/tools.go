package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pgvector/pgvector-go"
	"go.uber.org/zap"

	"gexue/internal/model"
	"gexue/internal/repo"
)

// 工具参数结构（JSON 反序列化；字段缺失为零值，处理器据此兜底）。
// jsonschema 标签由 tool/utils.GoStruct2ToolInfo 推断为 LLM 工具参数 schema：
//   - json 标签无 omitempty → required；有 omitempty → 可选
//   - enum=x 累计枚举；description= 字段说明（描述里不能含逗号）
//
// 工具是**原子能力**（检索/取题/落库展示），内容（题目/判定/讲解/总结）由专家 Agent 自己的
// ReAct 循环产出后传入 save_* 工具——工具内不再偷偷调 LLM 生成。

// llmQuestion 一道题目（专家 Agent 生成，save_questions 落库）。
type llmQuestion struct {
	Stem       string   `json:"stem" jsonschema:"description=the complete question stem"`
	Options    []string `json:"options,omitempty" jsonschema:"description=answer options for multiple-choice; empty array for non-choice questions"`
	Answer     string   `json:"answer" jsonschema:"description=the standard answer"`
	Analysis   string   `json:"analysis,omitempty" jsonschema:"description=brief analysis or explanation"`
	Difficulty int      `json:"difficulty,omitempty" jsonschema:"description=difficulty 1-5"`
}

// toolSearchKbArgs search_kb 参数。
type toolSearchKbArgs struct {
	KnowledgePoint string `json:"knowledge_point" jsonschema:"description=the knowledge point or query to search"`
	Query          string `json:"query,omitempty" jsonschema:"description=检索关键词（knowledge_point 的别名，二选一）"`
	Keyword        string `json:"keyword,omitempty" jsonschema:"description=检索关键词（knowledge_point 的别名，DSML 转换场景下模型可能用 keyword）"`
	TopK           int    `json:"top_k,omitempty" jsonschema:"description=number of chunks to retrieve"`
}

// toolGetQuestionArgs get_question 参数（无）。
type toolGetQuestionArgs struct{}

// toolSaveQuestionsArgs save_questions 参数。
type toolSaveQuestionsArgs struct {
	KnowledgePoint string        `json:"knowledge_point" jsonschema:"description=the knowledge point"`
	Difficulty     int           `json:"difficulty,omitempty" jsonschema:"description=difficulty 1-5"`
	QType          string        `json:"qtype,omitempty" jsonschema:"description=question type,enum=select,enum=fill,enum=apply,enum=mixed"`
	Questions      []llmQuestion `json:"questions" jsonschema:"description=the questions you generated to save and show"`
}

// toolSaveGradeArgs save_grade 参数。
type toolSaveGradeArgs struct {
	IsCorrect     bool   `json:"is_correct" jsonschema:"description=whether the student's answer is correct"`
	Analysis      string `json:"analysis" jsonschema:"description=brief feedback explaining why"`
	StudentAnswer string `json:"student_answer,omitempty" jsonschema:"description=the student's answer; defaults to the current message"`
}

// toolSaveFollowupArgs save_followup 参数。
type toolSaveFollowupArgs struct {
	Content    string `json:"content" jsonschema:"description=the guidance content you wrote"`
	Mode       string `json:"mode,omitempty" jsonschema:"description=guidance mode,enum=hint,enum=extend,enum=drill"`
	Difficulty int    `json:"difficulty,omitempty" jsonschema:"description=target difficulty 1-5"`
	QType      string `json:"qtype,omitempty" jsonschema:"description=target question type,enum=select,enum=fill,enum=apply,enum=mixed"`
}

// toolSaveSolutionArgs save_solution 参数。
type toolSaveSolutionArgs struct {
	Text string `json:"text" jsonschema:"description=the step-by-step solution text you wrote"`
}

// toolGetStatsArgs get_session_stats 参数（无）。
type toolGetStatsArgs struct{}

// toolSaveSummaryArgs save_summary 参数。
type toolSaveSummaryArgs struct {
	Summary      string `json:"summary" jsonschema:"description=the encouraging summary you wrote"`
	RecommendKP  string `json:"recommend_kp,omitempty" jsonschema:"description=the recommended next knowledge point"`
	RecommendWhy string `json:"recommend_why,omitempty" jsonschema:"description=reason for the recommendation"`
}

// parseToolArgs 解析工具参数 JSON；失败返回错误串（供模型看到并自我纠正）。
func parseToolArgs[T any](argsJSON string) (T, string) {
	var v T
	if argsJSON == "" {
		return v, ""
	}
	if err := json.Unmarshal([]byte(argsJSON), &v); err != nil {
		return v, "工具参数解析失败：" + err.Error() + "。请修正参数后重试，或直接回复学生。"
	}
	return v, ""
}

// takeRetrievedChunks 取出并清空检索缓存（消费一次，避免跨题串用）。
func (c *Ctx) takeRetrievedChunks() []chunkView {
	chunks := c.retrievedChunks
	c.retrievedChunks = nil
	return chunks
}

// ---- search_kb：检索知识库（出题/解题前由专家 LLM 自主调用） ----

func (a *Agent) toolSearchKb(ctx context.Context, c *Ctx, argsJSON string) string {
	args, errMsg := parseToolArgs[toolSearchKbArgs](argsJSON)
	if errMsg != "" {
		return errMsg
	}
	kp := strings.TrimSpace(args.KnowledgePoint)
	if kp == "" {
		kp = strings.TrimSpace(args.Query) // query 别名
	}
	if kp == "" {
		kp = strings.TrimSpace(args.Keyword) // keyword 别名（DSML 转换场景模型可能用 keyword）
	}
	if kp == "" {
		return "缺少 knowledge_point（要检索的知识点）。"
	}
	topK := args.TopK
	if topK <= 0 {
		topK = 6
	}
	chunks, err := a.embSearch(ctx, c, kp, topK)
	if err != nil {
		c.tk.log.Warn("search kb failed", zap.Error(err))
		return "检索失败：" + err.Error()
	}
	c.retrievedChunks = chunks
	if len(chunks) == 0 {
		return "知识库中暂无「" + kp + "」相关内容。"
	}
	return "已检索到「" + kp + "」相关内容：\n" + chunkTexts(chunks)
}

// ---- get_question：取当前题目（判分专家判断前先看题） ----

func (a *Agent) toolGetQuestion(ctx context.Context, c *Ctx, _ string) string {
	if c.Session.LastQuestionID == nil {
		return "当前没有正在做的题。"
	}
	q, err := c.tk.repo.GetQuestion(ctx, c.Session.ID, *c.Session.LastQuestionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return "刚才那道题找不到了，请向学生说我们重新来一道。"
		}
		c.tk.log.Warn("get question failed", zap.Error(err))
		return "获取当前题目失败。"
	}
	return fmt.Sprintf("当前题目：\n题干：%s\n题型：%s\n标准答案：%s", q.Stem, q.QType, q.Answer)
}

// ---- save_questions：保存专家生成的题目并展示 ----

func (a *Agent) toolSaveQuestions(ctx context.Context, c *Ctx, argsJSON string) string {
	args, errMsg := parseToolArgs[toolSaveQuestionsArgs](argsJSON)
	if errMsg != "" {
		return errMsg
	}
	kp := strings.TrimSpace(args.KnowledgePoint)
	if kp == "" {
		return "缺少 knowledge_point。请说明题目对应哪个知识点。"
	}
	qtype := args.QType
	if qtype == "" {
		qtype = model.QTypeMixed
	}
	diff := args.Difficulty
	if diff < 1 || diff > 5 {
		diff = 3
	}

	var views []QuestionView
	for _, rq := range args.Questions {
		if strings.TrimSpace(rq.Stem) == "" {
			continue
		}
		optsJSON, _ := json.Marshal(rq.Options)
		q := &model.Question{
			SessionID:      c.Session.ID,
			KbID:           c.KB.ID,
			QType:          normalizeQType(qtype, len(rq.Options)),
			KnowledgePoint: kp,
			Stem:           rq.Stem,
			Options:        optsJSON,
			Answer:         rq.Answer,
			Analysis:       rq.Analysis,
			Difficulty:     rq.Difficulty,
			Source:         "ai",
		}
		if q.Difficulty < 1 {
			q.Difficulty = diff
		}
		if err := c.tk.repo.CreateQuestion(ctx, q); err != nil {
			c.tk.log.Warn("create question failed", zap.Error(err))
			return "题目保存失败，请重试。"
		}
		views = append(views, QuestionView{
			ID: q.ID, QType: normalizeQType(qtype, len(rq.Options)), Stem: q.Stem, Options: rq.Options, Difficulty: q.Difficulty,
		})
		c.Session.LastQuestionID = &q.ID
	}
	if len(views) == 0 {
		return "questions 为空或无效。请生成题目后传入。"
	}
	c.State = model.SessionStateAwaitingAnswer
	c.emit(&Frame{
		Intent: "generate", Kind: FrameQuestionList,
		Text:       fmt.Sprintf("好的！给你出了 %d 道「%s」的题，请作答：", len(views), kp),
		Questions:  views,
		Difficulty: diff,
		QType:      qtype,
	})
	return fmt.Sprintf("已保存并展示 %d 道「%s」（难度%d），等学生作答，不要再重复出题。", len(views), kp, diff)
}

// ---- save_grade：保存判分结果 + 反馈 + 返回推题建议 ----

// questionRecommend 推题算法输出。
type questionRecommend struct {
	KP         string
	QType      string
	Difficulty int
	Reason     string
}

func (a *Agent) toolSaveGrade(ctx context.Context, c *Ctx, argsJSON string) string {
	args, errMsg := parseToolArgs[toolSaveGradeArgs](argsJSON)
	if errMsg != "" {
		return errMsg
	}
	if c.Session.LastQuestionID == nil {
		return "当前没有正在做的题。请向学生说先练一个知识点。"
	}
	q, err := c.tk.repo.GetQuestion(ctx, c.Session.ID, *c.Session.LastQuestionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return "刚才那道题找不到了，请向学生说重新来一道。"
		}
		c.tk.log.Warn("get question for grade failed", zap.Error(err))
		return "获取当前题目失败。"
	}

	studentAnswer := strings.TrimSpace(args.StudentAnswer)
	if studentAnswer == "" {
		studentAnswer = c.In.Message
	}
	isCorrect := args.IsCorrect
	analysis := args.Analysis

	// 客观题（选择/填空）用规则判分覆盖，避免 LLM 误判大小写/空白等
	if q.QType == model.QTypeSelect || q.QType == model.QTypeFill {
		isCorrect = ruleCheck(q, studentAnswer)
		if analysis == "" {
			analysis = q.Analysis
		}
		if !isCorrect && analysis == "" {
			analysis = "答案不太对，再想一想哦～"
		}
	}

	rec := &model.AnswerRecord{
		UserID:     c.In.UserID,
		SessionID:  c.Session.ID,
		QuestionID: q.ID,
		Answer:     studentAnswer,
		IsCorrect:  isCorrect,
	}
	if err := c.tk.repo.CreateAnswer(ctx, rec); err != nil {
		c.tk.log.Warn("create answer record failed", zap.Error(err))
		return "作答记录保存失败，请重试。"
	}

	c.State = model.SessionStateAwaitingAnswer
	c.emit(&Frame{
		Intent: "answer", Kind: FrameFeedback,
		Text:      feedbackText(isCorrect),
		IsCorrect: &isCorrect,
		Analysis:  analysis,
	})

	// 推题建议（确定性算法），返回给专家 LLM，由其决定下一题
	recNext := a.recommendNext(ctx, c, q, isCorrect)
	verdict := "错误"
	if isCorrect {
		verdict = "正确"
	}
	return fmt.Sprintf("已判分（%s）并反馈给学生。建议下一题：%s（知识点「%s」难度%d 题型%s）。请据此调 search_kb 检索该知识点，再调 save_questions 出下一题。",
		verdict, recNext.Reason, recNext.KP, recNext.Difficulty, recNext.QType)
}

// recommendNext 据本题作答情况与会话表现，决定下一题难度/题型/知识点（确定性推题算法）。
func (a *Agent) recommendNext(ctx context.Context, c *Ctx, q *model.Question, isCorrect bool) questionRecommend {
	baseDiff := q.Difficulty
	if baseDiff < 1 {
		baseDiff = 3
	}
	kp := q.KnowledgePoint
	if kp == "" {
		kp = "本次知识点"
	}
	streak, rate := a.sessionStreakAndRate(ctx, c)

	if isCorrect {
		nextDiff := baseDiff + 1
		if nextDiff > 5 {
			nextDiff = 5
		}
		if streak >= 2 && rate >= 0.7 {
			if nk, ok := a.pickNextKp(ctx, c, kp); ok {
				return questionRecommend{KP: nk, QType: model.QTypeMixed, Difficulty: nextDiff,
					Reason: fmt.Sprintf("连续答对，掌握不错，进阶到「%s」", nk)}
			}
		}
		return questionRecommend{KP: kp, QType: model.QTypeMixed, Difficulty: nextDiff,
			Reason: fmt.Sprintf("答对了，换个题型巩固「%s」", kp)}
	}

	nextDiff := baseDiff - 1
	if nextDiff < 1 {
		nextDiff = 1
	}
	return questionRecommend{KP: kp, QType: model.QTypeMixed, Difficulty: nextDiff,
		Reason: fmt.Sprintf("这题有点难，降一档再练「%s」", kp)}
}

// sessionStreakAndRate 返回当前连击（正=连对，负=连错）与会话正确率。
func (a *Agent) sessionStreakAndRate(ctx context.Context, c *Ctx) (streak int, rate float64) {
	as, err := c.tk.repo.ListAnswers(ctx, c.Session.ID)
	if err != nil || len(as) == 0 {
		return 0, 0
	}
	correct := 0
	for _, r := range as {
		if r.IsCorrect {
			correct++
		}
	}
	rate = float64(correct) / float64(len(as))
	last := as[len(as)-1].IsCorrect
	for i := len(as) - 1; i >= 0; i-- {
		if as[i].IsCorrect != last {
			break
		}
		if last {
			streak++
		} else {
			streak--
		}
	}
	return streak, rate
}

// pickNextKp 从知识库知识点中挑一个与当前不同的（进阶用）。
func (a *Agent) pickNextKp(ctx context.Context, c *Ctx, cur string) (string, bool) {
	points, err := c.tk.kbRepo.ListPoints(ctx, c.KB.ID)
	if err != nil || len(points) == 0 {
		return "", false
	}
	for _, p := range points {
		if p.Name != cur {
			return p.Name, true
		}
	}
	return "", false
}

// ---- save_followup：展示专家撰写的追问/变式 ----

func (a *Agent) toolSaveFollowup(ctx context.Context, c *Ctx, argsJSON string) string {
	args, errMsg := parseToolArgs[toolSaveFollowupArgs](argsJSON)
	if errMsg != "" {
		return errMsg
	}
	content := strings.TrimSpace(args.Content)
	if content == "" {
		return "缺少 content（要展示给学生的追问内容）。"
	}
	c.emit(&Frame{
		Intent:     "answer",
		Kind:       FrameFollowup,
		Text:       content,
		Difficulty: args.Difficulty,
		QType:      args.QType,
	})
	return "已将追问/变式展示给学生。"
}

// ---- save_solution：展示专家撰写的解题讲解 ----

func (a *Agent) toolSaveSolution(ctx context.Context, c *Ctx, argsJSON string) string {
	args, errMsg := parseToolArgs[toolSaveSolutionArgs](argsJSON)
	if errMsg != "" {
		return errMsg
	}
	text := strings.TrimSpace(args.Text)
	if text == "" {
		return "缺少 text（解题内容）。"
	}
	c.emit(&Frame{Intent: "help", Kind: FrameHelp, Text: text})
	return "已向学生展示解题讲解。"
}

// ---- get_session_stats：会话统计（总结专家用） ----

func (a *Agent) toolGetSessionStats(ctx context.Context, c *Ctx, _ string) string {
	answers, err := c.tk.repo.ListAnswers(ctx, c.Session.ID)
	if err != nil {
		c.tk.log.Warn("list answers for stats failed", zap.Error(err))
		return "无法读取作答记录。"
	}
	total := len(answers)
	correct := 0
	for _, ans := range answers {
		if ans.IsCorrect {
			correct++
		}
	}
	rate := 0
	if total > 0 {
		rate = correct * 100 / total
	}
	kpNames, err := a.pointNames(ctx, c)
	if err != nil {
		c.tk.log.Warn("list points for stats failed", zap.Error(err))
	}
	return fmt.Sprintf("本次数据：总题数 %d，答对 %d，正确率 %d%%。知识库知识点：%s。请据此撰写总结并推荐下一知识点。",
		total, correct, rate, strings.Join(kpNames, "、"))
}

// ---- save_summary：展示专家撰写的总结与推荐 ----

func (a *Agent) toolSaveSummary(ctx context.Context, c *Ctx, argsJSON string) string {
	args, errMsg := parseToolArgs[toolSaveSummaryArgs](argsJSON)
	if errMsg != "" {
		return errMsg
	}
	summary := strings.TrimSpace(args.Summary)
	if summary == "" {
		summary = "本次练习结束啦，继续加油！💪"
	}
	frame := &Frame{Intent: "end", Kind: FrameSummary, Text: summary}
	if args.RecommendKP != "" {
		frame.Recommend = &RecommendView{KPName: args.RecommendKP, Reason: args.RecommendWhy}
	}
	c.emit(frame)
	c.State = model.SessionStateFinished
	return "已展示总结与推荐，不要重复总结。"
}

// ---- 判分辅助（确定性） ----

// ruleCheck 客观题规则判分：归一化后与标准答案比对。
func ruleCheck(q *model.Question, answer string) bool {
	return normalizeAnswer(answer) == normalizeAnswer(q.Answer)
}

// normalizeAnswer 归一化：去空白/全半角/大小写，仅保留字母数字。
func normalizeAnswer(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// feedbackText 判分反馈语（简短鼓励）。
func feedbackText(isCorrect bool) string {
	if isCorrect {
		return "答对啦，真棒！🎉"
	}
	return "这道题答错了，没关系，我们一起看看～"
}

// normalizeQType 依据选项数量判定题型（选择题需≥2选项）。
func normalizeQType(qtype string, optionCount int) string {
	if qtype == model.QTypeMixed && optionCount >= 2 {
		return model.QTypeSelect
	}
	return qtype
}

// ---- 检索 ----

// chunkView 检索召回片段（出题语料）。
type chunkView struct {
	Content string
}

// embSearchRaw 库内检索：已注入 retriever 时走双路召回（向量 + BM25）→ RRF → rerank；
// 未注入则回退为旧单路向量检索（HNSW + 余弦）。领域依赖从 c.tk 读取。
func (a *Agent) embSearchRaw(ctx context.Context, c *Ctx, query string, topK int) ([]model.KnowledgeChunk, error) {
	if c.tk.retriever != nil {
		return c.tk.retriever.Search(ctx, c.In.UserID, c.KB.ID, query, topK)
	}
	if c.tk.emb == nil {
		return nil, fmt.Errorf("embedder not configured")
	}
	if _, err := c.tk.kbRepo.GetBase(ctx, c.In.UserID, c.KB.ID); err != nil {
		return nil, err
	}
	vecs, err := c.tk.emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	return c.tk.kbRepo.SearchInKb(ctx, c.KB.ID, pgvector.NewVector(vecs[0]), topK)
}

// embSearch 库内语义检索出题语料（KnowledgeChunk → chunkView）。
func (a *Agent) embSearch(ctx context.Context, c *Ctx, query string, topK int) ([]chunkView, error) {
	chunks, err := a.embSearchRaw(ctx, c, query, topK)
	if err != nil {
		return nil, err
	}
	views := make([]chunkView, 0, len(chunks))
	for _, ch := range chunks {
		views = append(views, chunkView{Content: ch.ContentText})
	}
	return views, nil
}

// chunkTexts 拼接召回片段文本。
func chunkTexts(chunks []chunkView) string {
	var b strings.Builder
	for i, ch := range chunks {
		if ch.Content == "" {
			continue
		}
		if i > 0 {
			b.WriteString("\n---\n")
		}
		b.WriteString(ch.Content)
	}
	if b.Len() == 0 {
		return "（知识库中暂无相关内容）"
	}
	return b.String()
}

// pointNames 知识库下知识点名称列表（供推荐用，含父子层级用「父/子」表示）。
func (a *Agent) pointNames(ctx context.Context, c *Ctx) ([]string, error) {
	points, err := c.tk.kbRepo.ListPoints(ctx, c.KB.ID)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint]model.KnowledgePoint, len(points))
	for _, p := range points {
		byID[p.ID] = p
	}
	names := make([]string, 0, len(points))
	for _, p := range points {
		if p.ParentID != nil {
			if parent, ok := byID[*p.ParentID]; ok {
				names = append(names, parent.Name+"/"+p.Name)
				continue
			}
		}
		names = append(names, p.Name)
	}
	return names, nil
}
