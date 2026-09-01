package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

// DashScope 阿里云 qwen3-rerank 实现（text-rerank 服务）。
// 端点 = DASHSCOPE_BASE + /text-rerank，复用 DASHSCOPE_API_KEY。
// 注意：官方已停服 gte-rerank-v2（2026-05-30），当前统一使用 qwen3-rerank。
type DashScope struct {
	apiKey  string
	model   string
	baseURL string // 完整端点，如 https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank
	cli     *http.Client
}

// DefaultDashScopeBaseURL qwen3-rerank 的 text-rerank 服务端点。
const DefaultDashScopeBaseURL = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"

func NewDashScope(apiKey, model string) *DashScope {
	return NewDashScopeWithURL(apiKey, model, DefaultDashScopeBaseURL)
}

// NewDashScopeWithURL 支持自定义端点（测试注入 httptest server 用）。
func NewDashScopeWithURL(apiKey, model, baseURL string) *DashScope {
	if model == "" {
		model = "qwen3-rerank"
	}
	if baseURL == "" {
		baseURL = DefaultDashScopeBaseURL
	}
	return &DashScope{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		cli:     &http.Client{Timeout: 30 * time.Second},
	}
}

type rerankReq struct {
	Model      string       `json:"model"`
	Input      rerankInput  `json:"input"`
	Parameters rerankParams `json:"parameters"`
}

type rerankInput struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type rerankParams struct {
	TopN            int  `json:"top_n"`
	ReturnDocuments bool `json:"return_documents"`
}

type rerankResp struct {
	Output rerankOutput `json:"output"`
}

type rerankOutput struct {
	Results []rerankItem `json:"results"`
}

type rerankItem struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// Rerank 精排 documents，按 relevance_score 降序返回前 topN。
// 返回的 Index 指向输入 documents 的下标，调用方据此重排原始文档。
func (d *DashScope) Rerank(ctx context.Context, query string, documents []string, topN int) ([]Result, error) {
	if d.apiKey == "" {
		return nil, fmt.Errorf("未配置 DASHSCOPE_API_KEY，无法调用 rerank")
	}
	if len(documents) == 0 {
		return nil, nil
	}
	if topN <= 0 {
		topN = len(documents)
	}
	body, err := json.Marshal(rerankReq{
		Model:      d.model,
		Input:      rerankInput{Query: query, Documents: documents},
		Parameters: rerankParams{TopN: topN, ReturnDocuments: false},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+d.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("dashscope rerank status=%d body=%s", resp.StatusCode, rb)
	}
	var out rerankResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	res := make([]Result, 0, len(out.Output.Results))
	for _, it := range out.Output.Results {
		if it.Index >= 0 && it.Index < len(documents) {
			res = append(res, Result{Index: it.Index, Score: it.RelevanceScore})
		}
	}
	sort.SliceStable(res, func(i, j int) bool { return res[i].Score > res[j].Score })
	return res, nil
}
