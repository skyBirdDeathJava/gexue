package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/pgvector/pgvector-go"
	"go.uber.org/zap"

	"gexue/internal/model"
	"gexue/internal/repo"
)

// 工具参数结构（JSON 反序列化；字段缺失为零值，处理器据此兜底）。

// toolGenerateArgs generate_question 参数。
type toolGenerateArgs struct {
	KnowledgePoint string `json:"knowledge_point"`
	Difficulty     int    `json:"difficulty"`
	QType          string `json:"qtype"`
	Count          int    `json:"count"` // 出题数量，0=默认1道
}

// toolAnswerArgs answer_question 参数。
type toolAnswerArgs struct {
	StudentAnswer string `json:"student_answer"`
}

// toolFollowupArgs follow_up 参数。
type toolFollowupArgs struct {
	Content    string `json:"content"`
	Mode       string `json:"mode"`
	Difficulty int    `json:"difficulty"`
	QType      string `json:"qtype"`
}

// toolHelpArgs help_solve 参数。
type toolHelpArgs struct {
	Question string `json:"question"`
}

// toolSummarizeArgs summarize 参数。
type toolSummarizeArgs struct {
	Reason string `json:"reason"`
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

// ---- generate_question：出题并展示 ----

func (a *Agent) toolGenerate(ctx context.Context, c *Ctx, argsJSON string) string {
	args, errMsg := parseToolArgs[toolGenerateArgs](argsJSON)
	if errMsg != "" {
		return errMsg
	}

	kp := strings.TrimSpace(args.KnowledgePoint)
	if kp == "" {
		return "缺少 knowledge_point（要练的知识点）。请先在回复里向学生询问要练哪个知识点，不要直接出题。"
	}
	if a.llm == nil {
		c.Frames = []*Frame{a.noLLMFrame(c.In)}
		return "大模型未配置，无法出题。"
	}

	qtype := args.QType
	if qtype == "" {
		qtype = model.QTypeMixed
	}
	diff := args.Difficulty
	if diff < 1 || diff > 5 {
		diff = 3
	}
	count := args.Count
	if count < 1 {
		count = 1 // 默认1道
	}

	frame, err := a.makeQuestionFrame(ctx, c, kp, qtype, diff, count, "")
	if err != nil {
		a.log.Warn("generate questions failed", zap.Error(err))
		return "出题失败了。请向学生说：这道知识点我暂时还没准备好题目，换个说法或知识点再试试。"
	}
	frame.Text = fmt.Sprintf("好的！给你出了 %d 道「%s」的题，请作答：", len(frame.Questions), kp)
	c.Frames = append(c.Frames, frame)
	return fmt.Sprintf("已出 %d 道「%s」（难度%d）并展示给学生等待作答，不要再重复出题。", len(frame.Questions), kp, diff)
}

// makeQuestionFrame 出题并落库，返回 question_list 帧（Text 留给调用方设置）。
// 检索→生成→建题→更新 LastQuestionID；成功后 c.Session.LastQuestionID 指向最后一题。
func (a *Agent) makeQuestionFrame(ctx context.Context, c *Ctx, kp, qtype string, diff, count int, prevStem string) (*Frame, error) {
	chunks, err := a.embSearch(ctx, c.In.UserID, c.KB.ID, kp, 6)
	if err != nil {
		a.log.Warn("retrieve for generate failed", zap.Error(err))
	}
	reqs, err := a.generateQuestions(ctx, c, kp, qtype, diff, count, chunks, prevStem)
	if err != nil {
		return nil, err
	}
	var views []QuestionView
	for _, rq := range reqs {
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
		if err := a.repo.CreateQuestion(ctx, q); err != nil {
			return nil, err
		}
		views = append(views, QuestionView{
			ID: q.ID, QType: normalizeQType(qtype, len(rq.Options)), Stem: q.Stem, Options: rq.Options, Difficulty: q.Difficulty,
		})
		c.Session.LastQuestionID = &q.ID
	}
	c.State = model.SessionStateAwaitingAnswer
	return &Frame{
		Intent: "generate", Kind: FrameQuestionList,
		Questions:  views,
		Difficulty: diff,
		QType:      qtype,
	}, nil
}

// ---- answer_question：判分 + 反馈 ----

func (a *Agent) toolAnswer(ctx context.Context, c *Ctx, argsJSON string) string {
	args, errMsg := parseToolArgs[toolAnswerArgs](argsJSON)
	if errMsg != "" {
		return errMsg
	}

	if c.Session.LastQuestionID == nil {
		return "当前没有正在做的题目。请向学生说：先告诉我你想练哪个知识点，我给你出道题～（不要调用 answer_question）"
	}
	q, err := a.repo.GetQuestion(ctx, c.Session.ID, *c.Session.LastQuestionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return "刚才那道题找不到了。请向学生说：我们重新来一道吧，先告诉我知识点～（不要调用 answer_question）"
		}
		a.log.Warn("get question for answer failed", zap.Error(err))
		return "获取当前题目失败，请换个方式引导学生。"
	}

	studentAnswer := strings.TrimSpace(args.StudentAnswer)
	if studentAnswer == "" {
		studentAnswer = c.In.Message
	}

	var (
		isCorrect bool
		analysis  string
	)

	switch q.QType {
	case model.QTypeSelect, model.QTypeFill:
		// 选择题和填空题：规则判分（图片答案已在入口 OCR 为文字，此处一律按文本比对）
		isCorrect = ruleCheck(q, studentAnswer)
		analysis = q.Analysis
		if !isCorrect && q.Analysis == "" {
			analysis = "答案不太对，再想一想哦～"
		}
	default:
		// 应用题/主观题：LLM 判分
		judge, err := a.llmJudge(ctx, c, q.Stem, q.Answer, studentAnswer)
		if err != nil {
			a.log.Warn("llm judge failed, fallback conservative", zap.Error(err))
			isCorrect = false
			analysis = "这道题我没法立刻判对错，先看看我的讲解吧～"
		} else {
			isCorrect = judge.IsCorrect
			analysis = judge.Analysis
		}
	}

	rec := &model.AnswerRecord{
		UserID:     c.In.UserID,
		SessionID:  c.Session.ID,
		QuestionID: q.ID,
		Answer:     studentAnswer,
		IsCorrect:  isCorrect,
	}
	if err := a.repo.CreateAnswer(ctx, rec); err != nil {
		a.log.Warn("create answer record failed", zap.Error(err))
		return "作答记录保存失败，请重试。"
	}

	c.State = model.SessionStateAwaitingAnswer
	c.Frames = append(c.Frames, &Frame{
		Intent: "answer", Kind: FrameFeedback,
		Text:      feedbackText(isCorrect),
		IsCorrect: &isCorrect,
		Analysis:  analysis,
	})
	// 推题：判分后据作答情况自动推出下一道（不依赖模型决策，确保"答完即有下一题"）
	pushed := a.pushNextQuestion(ctx, c, q, isCorrect)
	verdict := "错误"
	if isCorrect {
		verdict = "正确"
	}
	if pushed {
		return fmt.Sprintf("已判分（%s）并据作答自动推出下一题，等学生作答，不要再重复出题。", verdict)
	}
	return fmt.Sprintf("已判分（%s）。下一题暂未推出，可由你按作答质量出题或用 follow_up 引导。", verdict)
}

// ---- 推题算法：判分后决定下一题的难度/题型/知识点 ----

// questionRecommend 推题算法输出。
type questionRecommend struct {
	KP         string
	QType      string
	Difficulty int
	Reason     string
}

// recommendNext 据本题作答情况与会话表现，决定下一题难度/题型/知识点。
// 答对：难度+1、轮换题型；连对≥2 且正确率≥70% 则换知识点进阶，否则同知识点变式。
// 答错：难度-1、偏简单题型、同知识点巩固。
func (a *Agent) recommendNext(ctx context.Context, c *Ctx, q *model.Question, isCorrect bool) questionRecommend {
	baseDiff := q.Difficulty
	if baseDiff < 1 {
		baseDiff = 3
	}
	kp := q.KnowledgePoint
	if kp == "" {
		kp = "本次知识点"
	}
	streak, rate := a.sessionStreakAndRate(ctx, c.Session.ID)

	if isCorrect {
		nextDiff := baseDiff + 1
		if nextDiff > 5 {
			nextDiff = 5
		}
		// 题型交给出题老师按知识点自选（mixed），由"避开上一题"提示保证换题型
		if streak >= 2 && rate >= 0.7 {
			if nk, ok := a.pickNextKp(ctx, c.KB.ID, kp); ok {
				return questionRecommend{KP: nk, QType: model.QTypeMixed, Difficulty: nextDiff,
					Reason: fmt.Sprintf("连续答对，掌握不错，进阶到「%s」", nk)}
			}
		}
		return questionRecommend{KP: kp, QType: model.QTypeMixed, Difficulty: nextDiff,
			Reason: fmt.Sprintf("答对了，换个题型巩固「%s」", kp)}
	}

	// 答错：降难度、同知识点巩固（题型同样交给老师自选并避开上一题）
	nextDiff := baseDiff - 1
	if nextDiff < 1 {
		nextDiff = 1
	}
	return questionRecommend{KP: kp, QType: model.QTypeMixed, Difficulty: nextDiff,
		Reason: fmt.Sprintf("这题有点难，降一档再练「%s」", kp)}
}

// sessionStreakAndRate 返回当前连击（正=连对，负=连错）与会话正确率。
func (a *Agent) sessionStreakAndRate(ctx context.Context, sessionID uint) (streak int, rate float64) {
	as, err := a.repo.ListAnswers(ctx, sessionID)
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

// pushNextQuestion 判分后据推题算法出下一道并追加帧；返回是否成功推出。
func (a *Agent) pushNextQuestion(ctx context.Context, c *Ctx, q *model.Question, isCorrect bool) bool {
	if a.llm == nil {
		return false
	}
	rec := a.recommendNext(ctx, c, q, isCorrect)
	// 传入上一题题干，让出题老师换题型、不重复
	frame, err := a.makeQuestionFrame(ctx, c, rec.KP, rec.QType, rec.Difficulty, 1, q.Stem)
	if err != nil || frame == nil || len(frame.Questions) == 0 {
		a.log.Warn("push next question failed", zap.Error(err))
		return false
	}
	frame.Text = fmt.Sprintf("%s\n给你出了一道「%s」（难度%d），请作答：", rec.Reason, rec.KP, rec.Difficulty)
	c.Frames = append(c.Frames, frame)
	return true
}

// pickNextKp 从知识库知识点中挑一个与当前不同的（进阶用）。
func (a *Agent) pickNextKp(ctx context.Context, kbID uint, cur string) (string, bool) {
	points, err := a.kbRepo.ListPoints(ctx, kbID)
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

// ---- follow_up：针对性追问/变式 ----

func (a *Agent) toolFollowup(ctx context.Context, c *Ctx, argsJSON string) string {
	args, errMsg := parseToolArgs[toolFollowupArgs](argsJSON)
	if errMsg != "" {
		return errMsg
	}
	content := strings.TrimSpace(args.Content)
	if content == "" {
		return "缺少 content（要展示给学生的追问内容）。请补充后重试。"
	}

	c.Frames = append(c.Frames, &Frame{
		Intent:     "answer",
		Kind:       FrameFollowup,
		Text:       content,
		Difficulty: args.Difficulty,
		QType:      args.QType,
	})
	return fmt.Sprintf("已将追问/变式展示给学生（mode=%s, 难度%d, 题型%s）。", args.Mode, args.Difficulty, args.QType)
}

// ---- help_solve：解一道具体题 ----

func (a *Agent) toolHelp(ctx context.Context, c *Ctx, argsJSON string) string {
	args, errMsg := parseToolArgs[toolHelpArgs](argsJSON)
	if errMsg != "" {
		return errMsg
	}
	if a.llm == nil {
		c.Frames = []*Frame{a.noLLMFrame(c.In)}
		return "大模型未配置，无法解题。"
	}

	question := strings.TrimSpace(args.Question)
	if question == "" {
		question = c.In.Message
	}

	sys := strings.ReplaceAll(helpSystemPrompt, "【年级】【学科】", a.gsTag(c.KB))
	user := fmt.Sprintf("请帮我解这道题：%s", question)
	if chunks, err := a.embSearch(ctx, c.In.UserID, c.KB.ID, question, 4); err == nil && len(chunks) > 0 {
		user += "\n\n（知识库相关参考：\n" + chunkTexts(chunks) + "\n）"
	}

	respMsg, err := a.llm.Generate(ctx, a.chatMessages(sys, c, user), noTools())
	if err != nil {
		a.log.Warn("help solve failed", zap.Error(err))
		return "解题失败。请向学生说：这道题我暂时解答不了，我们回到刚才的练习吧～"
	}

	text := strings.TrimSpace(respMsg.Content)
	if text == "" {
		return "解题无输出，请换个方式。"
	}
	c.Frames = append(c.Frames, &Frame{Intent: "help", Kind: FrameHelp, Text: text})
	return "已向学生给出解题讲解，无需再重复。"
}

// ---- summarize：结束并总结推荐 ----

func (a *Agent) toolSummarize(ctx context.Context, c *Ctx, argsJSON string) string {
	// reason 仅记录，不强校验
	answers, err := a.repo.ListAnswers(ctx, c.Session.ID)
	if err != nil {
		a.log.Warn("list answers for summarize failed", zap.Error(err))
		return "无法读取作答记录，请稍后再试或继续练习。"
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

	if total == 0 {
		c.Frames = append(c.Frames, &Frame{
			Intent: "end", Kind: FrameSummary,
			Text: "还没开始做题就结束啦～下次记得告诉我知识点，我陪你多练几道哦！💪",
		})
		c.State = model.SessionStateFinished
		return "已结束会话并总结。"
	}

	weak := "（本次错题的知识点，可回看题目）"
	kpNames, err := a.pointNames(ctx, c.KB.ID)
	if err != nil {
		a.log.Warn("list points for summary failed", zap.Error(err))
	}

	frame := &Frame{Intent: "end", Kind: FrameSummary}
	if a.llm != nil {
		sys := strings.ReplaceAll(summarizeSystemPrompt, "【年级】【学科】", a.gsTag(c.KB))
		user := fmt.Sprintf(
			"本次数据：总题数 %d，答对 %d，正确率 %d%%，薄弱知识点 %s\n知识库知识点：%s",
			total, correct, rate, weak, strings.Join(kpNames, "、"),
		)
		if respMsg, err := a.llm.Generate(ctx, []*schema.Message{
			schema.SystemMessage(sys),
			schema.UserMessage(user),
		}, noTools()); err == nil {
			var sr SummaryRecommend
			if err := json.Unmarshal([]byte(trimJSON(respMsg.Content)), &sr); err == nil {
				frame.Text = sr.Summary
				if sr.Recommend != nil {
					frame.Recommend = &RecommendView{KPName: sr.Recommend.KPName, Reason: sr.Recommend.Reason}
				}
			}
		} else {
			a.log.Warn("summarize llm failed", zap.Error(err))
		}
	}

	// LLM 失败/未配置 → 本地兜底总结
	if frame.Text == "" {
		frame.Text = fmt.Sprintf(
			"本次练习你做了 %d 道题，答对 %d 道，正确率 %d%%。继续加油，明天我们再练一练薄弱的知识点！💪",
			total, correct, rate,
		)
		frame.Recommend = &RecommendView{KPName: "薄弱知识点巩固", Reason: "建议优先复习本次答错的题目对应知识点。"}
	}

	c.Frames = append(c.Frames, frame)
	c.State = model.SessionStateFinished
	return "已结束会话并给出总结与推荐，不要重复总结。"
}

// ---- 内部子调用（判分 / 出题），沿用原逻辑 ----

// judgeResult 主观题 LLM 判分输出。
type judgeResult struct {
	IsCorrect bool   `json:"is_correct"`
	Analysis  string `json:"analysis"`
	Score     int    `json:"score"`
}

// llmJudge 主观题 LLM 判分（内部调用，禁用工具）。
func (a *Agent) llmJudge(ctx context.Context, c *Ctx, stem, standard, answer string) (*judgeResult, error) {
	if a.llm == nil {
		return nil, fmt.Errorf("llm not configured")
	}
	sys := strings.ReplaceAll(judgeSystemPrompt, "【年级】【学科】", a.gsTag(c.KB))
	user := fmt.Sprintf("题目：%s\n标准答案：%s\n学生答案：%s", stem, standard, answer)
	respMsg, err := a.llm.Generate(ctx, a.chatMessages(sys, c, user), noTools())
	if err != nil {
		return nil, err
	}
	var out judgeResult
	if err := json.Unmarshal([]byte(trimJSON(respMsg.Content)), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

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

// ---- 出题 ----

// llmQuestion LLM 出题单条。
type llmQuestion struct {
	Stem       string   `json:"stem"`
	Options    []string `json:"options"`
	Answer     string   `json:"answer"`
	Analysis   string   `json:"analysis"`
	Difficulty int      `json:"difficulty"`
}

// questionResp LLM 出题输出。
type questionResp struct {
	Questions []llmQuestion `json:"questions"`
}

// generateQuestions 调用 ChatModel 出题，解析失败重试一次后放弃（内部调用，禁用工具）。
func (a *Agent) generateQuestions(ctx context.Context, c *Ctx, kp, qtype string, diff, count int, chunks []chunkView, prevStem string) ([]llmQuestion, error) {
	userPrompt := fmt.Sprintf("知识库相关知识点：\n%s\n\n请按主题「%s」出 %d 道题。", chunkTexts(chunks), kp, count)
	sys := strings.NewReplacer(
		"{knowledge_point}", kp,
		"{qtype}", qtype,
		"{difficulty}", fmt.Sprint(diff),
		"{question_types}", questionTypesGuide(c.KB.SubjectName),
		"{variation_hint}", variationHint(prevStem),
		"【年级】【学科】", a.gsTag(c.KB),
	).Replace(generateSystemPrompt)

	for attempt := 0; attempt < 2; attempt++ {
		respMsg, err := a.llm.Generate(ctx, a.chatMessages(sys, c, userPrompt), noTools())
		if err != nil {
			return nil, err
		}
		var out questionResp
		if err := json.Unmarshal([]byte(trimJSON(respMsg.Content)), &out); err != nil || len(out.Questions) == 0 {
			a.log.Warn("parse questions failed, retry", zap.Int("attempt", attempt))
			continue
		}
		valid := out.Questions[:0]
		for _, q := range out.Questions {
			if strings.TrimSpace(q.Stem) != "" {
				valid = append(valid, q)
			}
		}
		if len(valid) == 0 {
			continue
		}
		return valid, nil
	}
	return nil, fmt.Errorf("generate questions: invalid llm output")
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
// 未注入则回退为旧单路向量检索（HNSW + 余弦）。
func (a *Agent) embSearchRaw(ctx context.Context, userID, kbID uint, query string, topK int) ([]model.KnowledgeChunk, error) {
	if a.retriever != nil {
		return a.retriever.Search(ctx, userID, kbID, query, topK)
	}
	if a.emb == nil {
		return nil, fmt.Errorf("embedder not configured")
	}
	if _, err := a.kbRepo.GetBase(ctx, userID, kbID); err != nil {
		return nil, err
	}
	vecs, err := a.emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	return a.kbRepo.SearchInKb(ctx, kbID, pgvector.NewVector(vecs[0]), topK)
}

// embSearch 库内语义检索出题语料（KnowledgeChunk → chunkView）。
func (a *Agent) embSearch(ctx context.Context, userID, kbID uint, query string, topK int) ([]chunkView, error) {
	chunks, err := a.embSearchRaw(ctx, userID, kbID, query, topK)
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
func (a *Agent) pointNames(ctx context.Context, kbID uint) ([]string, error) {
	points, err := a.kbRepo.ListPoints(ctx, kbID)
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

// trimJSON 剥离可能的 ```json 代码块与首尾空白。
func trimJSON(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, "```"); idx >= 0 {
		rest := s[idx+3:]
		if j := strings.Index(rest, "\n"); j >= 0 {
			rest = rest[j+1:]
		}
		if j := strings.LastIndex(rest, "```"); j >= 0 {
			rest = rest[:j]
		}
		s = strings.TrimSpace(rest)
	}
	if start, end := strings.Index(s, "{"), strings.LastIndex(s, "}"); start >= 0 && end > start {
		s = s[start : end+1]
	}
	return s
}
