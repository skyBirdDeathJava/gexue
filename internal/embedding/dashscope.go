package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DashScope 阿里云 text-embedding-v3 实现（OpenAI 兼容）。
// 端点 = DASHSCOPE_BASE_URL + /embeddings，与 ~/ww/mianba/backend/config.py 对齐。
type DashScope struct {
	apiKey string
	model  string
	dim    int
	cli    *http.Client
}

func NewDashScope(apiKey, model string, dim int) *DashScope {
	return &DashScope{
		apiKey: apiKey,
		model:  model,
		dim:    dim,
		cli:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (d *DashScope) Dim() int { return d.dim }

type dashScopeEmbedReq struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions *int     `json:"dimensions,omitempty"`
}

type dashScopeEmbedResp struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed 批量向量化，返回按 index 排序的向量列表。
func (d *DashScope) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if d.apiKey == "" {
		return nil, fmt.Errorf("未配置 DASHSCOPE_API_KEY，请在 .env 中填入后重启服务")
	}
	dim := d.dim
	body, err := json.Marshal(dashScopeEmbedReq{Model: d.model, Input: texts, Dimensions: &dim})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.embedURL(), bytes.NewReader(body))
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
		return nil, fmt.Errorf("dashscope embed status=%d body=%s", resp.StatusCode, rb)
	}
	var out dashScopeEmbedResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	res := make([][]float32, len(out.Data))
	for _, it := range out.Data {
		if it.Index >= 0 && it.Index < len(res) {
			res[it.Index] = it.Embedding
		}
	}
	return res, nil
}

// embedURL 默认对齐 mianba DASHSCOPE_BASE_URL。
func (d *DashScope) embedURL() string {
	return "https://dashscope.aliyuncs.com/compatible-mode/v1/embeddings"
}
