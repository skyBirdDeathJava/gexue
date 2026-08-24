package service

import (
	"context"
	"fmt"

	"gexue/internal/agent"
	"gexue/internal/model"
	"gexue/internal/pkg/oss"
	"gexue/internal/pkg/volcengine"
	"gexue/internal/repo"
)

// QuizService 模拟测试编排：会话 CRUD + Agent 对话。
type QuizService struct {
	repo      *repo.QuizRepo
	kbRepo    *repo.KnowledgeRepo
	agent     *agent.Agent
	uploader  *oss.Uploader
	ocrClient *volcengine.OCRClient // 火山引擎 OCR：图片答案/题目识别（LLM 纯文本，需先 OCR）
}

func NewQuizService(r *repo.QuizRepo, kr *repo.KnowledgeRepo, ag *agent.Agent, uploader *oss.Uploader) *QuizService {
	return &QuizService{repo: r, kbRepo: kr, agent: ag, uploader: uploader}
}

// SetOCRClient 注入火山引擎 OCR 客户端（可选；未配置时图片识别不可用）。
func (s *QuizService) SetOCRClient(client *volcengine.OCRClient) {
	s.ocrClient = client
}

// CreateSession 新建模拟测试会话。
// kb_id 必填且须属于当前用户；无效 → 返回 ErrNotFound（handler 映射为引导提示"请先选择知识库"）。
func (s *QuizService) CreateSession(ctx context.Context, userID, kbID uint) (*model.PracticeSession, error) {
	if _, err := s.kbRepo.GetBase(ctx, userID, kbID); err != nil {
		return nil, err
	}
	sess := &model.PracticeSession{
		UserID: userID,
		KbID:   kbID,
		State:  model.SessionStateIdle,
	}
	if err := s.repo.CreateSession(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// ListSessions 当前用户的对话历史（带标题 / 消息数 / 知识库名称）。
func (s *QuizService) ListSessions(ctx context.Context, userID uint) ([]model.SessionSummary, error) {
	sessions, err := s.repo.ListSessions(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]model.SessionSummary, 0, len(sessions))
	if len(sessions) == 0 {
		return out, nil
	}

	ids := make([]uint, 0, len(sessions))
	kbIDs := make([]uint, 0, len(sessions))
	seenKB := map[uint]struct{}{}
	for _, sess := range sessions {
		ids = append(ids, sess.ID)
		if _, ok := seenKB[sess.KbID]; !ok {
			seenKB[sess.KbID] = struct{}{}
			kbIDs = append(kbIDs, sess.KbID)
		}
	}

	turns, err := s.repo.ListTurnsBySessions(ctx, ids)
	if err != nil {
		return nil, err
	}
	countBySess := map[uint]int{}
	titleBySess := map[uint]string{}
	for _, t := range turns {
		countBySess[t.SessionID]++
		if t.Role == "user" {
			if _, ok := titleBySess[t.SessionID]; !ok {
				titleBySess[t.SessionID] = t.Content
			}
		}
	}

	kbMeta := map[uint]*model.KnowledgeBase{}
	for _, kbID := range kbIDs {
		kb, err := s.kbRepo.GetBaseWithMeta(ctx, userID, kbID)
		if err != nil {
			continue
		}
		kbMeta[kbID] = kb
	}

	for _, sess := range sessions {
		item := model.SessionSummary{
			ID:           sess.ID,
			KbID:         sess.KbID,
			State:        sess.State,
			Title:        titleBySess[sess.ID],
			MessageCount: countBySess[sess.ID],
			CreatedAt:    sess.CreatedAt,
			UpdatedAt:    sess.UpdatedAt,
		}
		if item.Title == "" {
			item.Title = "新对话"
		}
		if kb := kbMeta[sess.KbID]; kb != nil {
			item.KbName = kb.Name
			if kb.Grade != nil {
				item.GradeName = kb.Grade.Name
			}
			if kb.Subject != nil {
				item.SubjectName = kb.Subject.Name
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// DeleteSession 删除会话及其对话记录。
func (s *QuizService) DeleteSession(ctx context.Context, userID, sessionID uint) error {
	return s.repo.DeleteSession(ctx, userID, sessionID)
}

// GetSession 会话上下文（状态/最近题/历史，恢复用）。
func (s *QuizService) GetSession(ctx context.Context, userID, sessionID uint) (*model.PracticeSession, []model.ChatTurn, error) {
	sess, err := s.repo.GetSession(ctx, userID, sessionID)
	if err != nil {
		return nil, nil, err
	}
	turns, err := s.repo.ListTurns(ctx, sessionID, 50)
	if err != nil {
		return nil, nil, err
	}
	// 反转为正序
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	return sess, turns, nil
}

// Chat 驱动 Agent 对话主图。
// 用户消息先行落库（保证上下文记忆），assistant 帧由 Agent 内落库。
func (s *QuizService) Chat(ctx context.Context, userID, sessionID uint, message string) ([]*agent.Frame, error) {
	if _, err := s.repo.GetSession(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	turn := model.ChatTurn{SessionID: sessionID, Role: "user", Content: message}
	if err := s.repo.AddTurns(ctx, []model.ChatTurn{turn}); err != nil {
		return nil, err
	}
	return s.agent.Chat(ctx, agent.ChatInput{
		UserID:    userID,
		SessionID: sessionID,
		Message:   message,
	})
}

// UploadAnswerImageBytes 上传答题图片字节到 OSS，返回图片 URL。
func (s *QuizService) UploadAnswerImageBytes(ctx context.Context, userID uint, data []byte, filename string) (string, error) {
	if s.uploader == nil {
		return "", fmt.Errorf("OSS uploader not configured")
	}
	// 上传到 OSS：路径格式 /answer-images/{userID}_{filename}
	objectKey := fmt.Sprintf("answer-images/%d_%s", userID, filename)
	return s.uploader.UploadWithContent(objectKey, data)
}

// OCRImageBytes 把图片字节识别为文字。LLM 为纯文本模型无法直接看图，需先 OCR。
func (s *QuizService) OCRImageBytes(ctx context.Context, data []byte) (string, error) {
	if s.ocrClient == nil {
		return "", fmt.Errorf("OCR 客户端未配置")
	}
	return s.ocrClient.RecognizeImageBytes(ctx, data)
}
